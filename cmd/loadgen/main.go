// Command loadgen drives concurrent users, each sending `execute` calls at a fixed rate, against a
// running gateway and reports latency percentiles, an error breakdown and the gateway RSS.
//
//	loadgen -url http://127.0.0.1:8787/mcp -tokens-file tokens.txt -users 50 -rps 5 -duration 30s \
//	        -script 'async () => await tools.acme.get_item({id: "1"})' -pid 12345
//
// Each user needs its own token: the gateway rate-limits and caps concurrency per user, so one
// token shared by 50 simulated users would measure those limits, not the gateway. Tokens come from
// -token (one, reused), -tokens-file (one per line) or -mint (a dev-only helper that runs the real
// OAuth flow for existing users, see mint.go).
//
// Load is open-loop: requests are started on schedule whether or not earlier ones finished, so a
// slow gateway shows up as latency, not as a lower request rate.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	protocolVersion = "2026-07-28"
	// busyMessagePrefix starts the gateway's explicit "too many scripts" tool error
	// (mcpserver busyToolError). It is the only error the budget tolerates.
	busyMessagePrefix = "Too many scripts are running"
)

type config struct {
	url      string
	tokens   []string
	users    int
	rps      float64
	duration time.Duration
	script   string
	pid      int
	timeout  time.Duration
	mintOut  string

	seed       bool
	seedServer string
}

// outcome classes, in report order.
const (
	classOK        = "ok"
	classBusy      = "busy (explicit)"
	classRateLimit = "http 429 rate limited"
	classToolError = "tool error"
	classHTTPError = "http error"
	classTransport = "transport error"
)

type sample struct {
	latency time.Duration
	class   string
	detail  string
}

func main() {
	cfg, mint, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(2)
	}
	if cfg.seed {
		sd := seeder{server: cfg.seedServer, level: "read"}
		if cfg.tokens, err = sd.tokens(cfg.users); err != nil {
			fmt.Fprintln(os.Stderr, "loadgen: seed:", err)
			os.Exit(2)
		}
	}
	if mint != nil || cfg.seed {
		if mint != nil {
			if cfg.tokens, err = mint.tokens(cfg.url, cfg.users); err != nil {
				fmt.Fprintln(os.Stderr, "loadgen: mint:", err)
				os.Exit(2)
			}
		}
		if cfg.mintOut != "" {
			if err := os.WriteFile(cfg.mintOut, []byte(strings.Join(cfg.tokens, "\n")+"\n"), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "loadgen:", err)
				os.Exit(2)
			}
		}
	}
	if len(cfg.tokens) == 0 {
		fmt.Fprintln(os.Stderr, "loadgen: no token: use -token, -tokens-file or -mint-email-fmt")
		os.Exit(2)
	}
	os.Exit(run(cfg, os.Stdout))
}

func parseFlags(args []string) (config, *minter, error) {
	var (
		cfg        config
		token      string
		tokensFile string
		m          minter
	)
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.StringVar(&cfg.url, "url", "http://127.0.0.1:8787/mcp", "gateway MCP endpoint")
	fs.StringVar(&token, "token", "", "bearer token shared by all users")
	fs.StringVar(&tokensFile, "tokens-file", "", "file with one bearer token per line; user i uses line i mod n")
	fs.IntVar(&cfg.users, "users", 10, "concurrent simulated users")
	fs.Float64Var(&cfg.rps, "rps", 1, "execute requests per second per user")
	fs.DurationVar(&cfg.duration, "duration", 10*time.Second, "how long to generate load")
	fs.StringVar(&cfg.script, "script", `async () => 1`, "script sent to execute")
	fs.IntVar(&cfg.pid, "pid", 0, "gateway process id; RSS is sampled with ps when set")
	fs.DurationVar(&cfg.timeout, "timeout", 60*time.Second, "per-request timeout")
	fs.StringVar(&m.emailFmt, "mint-email-fmt", "", "dev only: mint one token per user for existing users, e.g. load%d@example.test (%d is 1..users)")
	fs.StringVar(&m.password, "mint-password", "", "password of the minted users (or env LOADGEN_PASSWORD)")
	fs.StringVar(&cfg.mintOut, "mint-out", "", "write the minted tokens to this file (valid 10 min), for -tokens-file on later runs")
	fs.StringVar(&m.level, "mint-level", "read", "consent level granted to the minted clients")
	fs.BoolVar(&cfg.seed, "seed", false, "dev only: bulk-create -users users in the database of MCPGW_DATABASE_URL and sign their tokens (see seed.go)")
	fs.StringVar(&cfg.seedServer, "seed-server", "acme", "server slug the seeded users get a ceiling and consent on")
	if err := fs.Parse(args); err != nil {
		return cfg, nil, err
	}
	if cfg.users < 1 || cfg.rps <= 0 || cfg.duration <= 0 {
		return cfg, nil, fmt.Errorf("users, rps and duration must be positive")
	}
	if token != "" {
		cfg.tokens = []string{token}
	}
	if tokensFile != "" {
		raw, err := os.ReadFile(tokensFile)
		if err != nil {
			return cfg, nil, err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				cfg.tokens = append(cfg.tokens, line)
			}
		}
	}
	if m.emailFmt == "" {
		return cfg, nil, nil
	}
	if m.password == "" {
		m.password = os.Getenv("LOADGEN_PASSWORD")
	}
	return cfg, &m, nil
}

func run(cfg config, out io.Writer) int {
	client := &http.Client{Timeout: cfg.timeout, Transport: &http.Transport{
		MaxIdleConnsPerHost: cfg.users * 4, MaxConnsPerHost: 0,
	}}
	body := requestBody(cfg.script)

	rss := startRSSSampler(cfg.pid)
	var (
		mu      sync.Mutex
		samples []sample
		wg      sync.WaitGroup
	)
	record := func(s sample) {
		mu.Lock()
		samples = append(samples, s)
		mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.duration)
	defer cancel()
	started := time.Now()
	interval := time.Duration(float64(time.Second) / cfg.rps)
	for u := 0; u < cfg.users; u++ {
		wg.Add(1)
		go func(token string, offset time.Duration) {
			defer wg.Done()
			drive(ctx, client, cfg.url, token, body, interval, offset, record, &wg)
		}(cfg.tokens[u%len(cfg.tokens)], time.Duration(u)*interval/time.Duration(cfg.users))
	}
	wg.Wait()
	elapsed := time.Since(started)

	rssStats := rss.stop()
	return report(out, cfg, samples, elapsed, rssStats)
}

// drive starts one request per interval until ctx ends. Each request runs in its own goroutine so
// the schedule never waits for the gateway.
func drive(ctx context.Context, client *http.Client, url, token string, body []byte,
	interval, offset time.Duration, record func(sample), wg *sync.WaitGroup) {
	select {
	case <-time.After(offset):
	case <-ctx.Done():
		return
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		wg.Add(1)
		go func() {
			defer wg.Done()
			record(send(client, url, token, body))
		}()
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}

func requestBody(script string) []byte {
	args, _ := json.Marshal(map[string]string{"code": script})
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"` + protocolVersion +
		`","io.modelcontextprotocol/clientInfo":{"name":"loadgen","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}`
	return []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":` +
		string(args) + `,` + meta + `}}`)
}

func send(client *http.Client, url, token string, body []byte) sample {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return sample{class: classTransport, detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", protocolVersion)
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "execute")
	req.Header.Set("Authorization", "Bearer "+token)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return sample{latency: time.Since(start), class: classTransport, detail: shorten(err.Error())}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	latency := time.Since(start)
	if err != nil {
		return sample{latency: latency, class: classTransport, detail: shorten(err.Error())}
	}
	class, detail := classify(resp.StatusCode, raw)
	return sample{latency: latency, class: class, detail: detail}
}

// classify maps a response to an outcome class. The busy error is an ordinary tool result with
// isError set, recognized by its message prefix.
func classify(status int, raw []byte) (class, detail string) {
	switch {
	case status == http.StatusTooManyRequests:
		return classRateLimit, ""
	case status != http.StatusOK:
		return classHTTPError, fmt.Sprintf("%d %s", status, shorten(string(raw)))
	}
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return classHTTPError, "invalid JSON-RPC body: " + shorten(string(raw))
	}
	if rpc.Error != nil {
		return classToolError, "rpc error: " + shorten(rpc.Error.Message)
	}
	if !rpc.Result.IsError {
		return classOK, ""
	}
	text := ""
	if len(rpc.Result.Content) > 0 {
		text = rpc.Result.Content[0].Text
	}
	if strings.HasPrefix(text, busyMessagePrefix) {
		return classBusy, ""
	}
	return classToolError, shorten(text)
}

func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func report(out io.Writer, cfg config, samples []sample, elapsed time.Duration, rss rssStats) int {
	counts := map[string]int{}
	details := map[string]int{}
	var okLat, allLat []time.Duration
	for _, s := range samples {
		counts[s.class]++
		allLat = append(allLat, s.latency)
		if s.class == classOK {
			okLat = append(okLat, s.latency)
		}
		if s.detail != "" {
			details[s.class+": "+s.detail]++
		}
	}
	fmt.Fprintf(out, "users=%d rps/user=%.1f duration=%s requests=%d achieved=%.1f req/s\n",
		cfg.users, cfg.rps, elapsed.Round(time.Millisecond), len(samples), float64(len(samples))/elapsed.Seconds())
	printLatency(out, "latency (ok only)", okLat)
	printLatency(out, "latency (all)", allLat)

	fmt.Fprintln(out, "outcomes:")
	realErrors := 0
	for _, c := range []string{classOK, classBusy, classRateLimit, classToolError, classHTTPError, classTransport} {
		if counts[c] == 0 {
			continue
		}
		fmt.Fprintf(out, "  %-24s %d\n", c, counts[c])
		if c != classOK && c != classBusy {
			realErrors += counts[c]
		}
	}
	for _, line := range sortedKeys(details) {
		fmt.Fprintf(out, "    %dx %s\n", details[line], line)
	}
	if rss.samples > 0 {
		fmt.Fprintf(out, "gateway rss: start=%.0f MB peak=%.0f MB end=%.0f MB (%d samples)\n",
			rss.startMB, rss.peakMB, rss.endMB, rss.samples)
	}
	if realErrors > 0 {
		fmt.Fprintf(out, "RESULT: %d real errors (busy is tolerated)\n", realErrors)
		return 1
	}
	fmt.Fprintln(out, "RESULT: no errors except explicit busy")
	return 0
}

func printLatency(out io.Writer, label string, lat []time.Duration) {
	if len(lat) == 0 {
		fmt.Fprintf(out, "%s: no samples\n", label)
		return
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pick := func(p float64) time.Duration { return lat[min(len(lat)-1, int(float64(len(lat))*p))] }
	fmt.Fprintf(out, "%s: p50=%s p95=%s p99=%s max=%s\n", label,
		pick(0.50).Round(10*time.Microsecond), pick(0.95).Round(10*time.Microsecond),
		pick(0.99).Round(10*time.Microsecond), lat[len(lat)-1].Round(10*time.Microsecond))
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
