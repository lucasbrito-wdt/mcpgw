package oauth

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A client that sends the headers and then trickles (here: stalls) the body must be cut off
// by the per-request read deadline, on every unauthenticated POST route.
func TestSlowBodyIsCutOff(t *testing.T) {
	e := newTestEnv(t)
	e.srv.bodyTimeout = 300 * time.Millisecond
	ts := httptest.NewServer(e.mux)
	t.Cleanup(ts.Close)

	for _, path := range []string{"/token", "/register", "/authorize"} {
		t.Run(path, func(t *testing.T) {
			conn, err := net.Dial("tcp", ts.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ct := "application/x-www-form-urlencoded"
			if path == "/register" {
				ct = "application/json"
			}
			req := "POST " + path + " HTTP/1.1\r\nHost: x\r\nContent-Type: " + ct + "\r\nContent-Length: 1000\r\n\r\ngrant_type="
			if _, err := conn.Write([]byte(req)); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			_ = conn.SetReadDeadline(start.Add(5 * time.Second))
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			// Either an error response or a closed connection is fine; hanging is not.
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 400 {
					t.Fatalf("status %d for truncated body", resp.StatusCode)
				}
			}
			if took := time.Since(start); took > 3*time.Second {
				t.Fatalf("server held the slow connection for %v", took)
			}
		})
	}
}

func TestBodyDeadlineDoesNotBreakNormalPosts(t *testing.T) {
	e := newTestEnv(t)
	ts := httptest.NewServer(e.mux)
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader("grant_type=bogus"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}
