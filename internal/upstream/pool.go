package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	DefaultIdleTTL     = 5 * time.Minute
	DefaultMaxSessions = 256
	minJanitorInterval = 50 * time.Millisecond
)

var (
	// ErrPoolExhausted is returned when every pooled session is busy and the cap is reached.
	ErrPoolExhausted = errors.New("upstream session pool exhausted; retry later")
	// ErrClosed is returned after Client.Close.
	ErrClosed = errors.New("upstream client closed")
)

type ownerKey struct{}

// WithOwner tags ctx with the identity (a user id) that owns the upstream session used for the
// calls made with it. Sessions are never shared across owners, nor across credentials, so a
// stateful upstream (a stdio process, a legacy HTTP session) never mixes two users' state.
func WithOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ownerKey{}, owner)
}

func ownerFrom(ctx context.Context) string {
	owner, _ := ctx.Value(ownerKey{}).(string)
	return owner
}

type sessionKey struct{ endpoint, owner, credential string }

// credentialID fingerprints the headers (or environment variable) a credential sets; equal
// credentials share a key.
func credentialID(cred Credential) string {
	if cred == nil {
		return ""
	}
	if ec, ok := cred.(EnvCredential); ok {
		h := sha256.New()
		h.Write([]byte("env\x00" + ec.Name + "\x00" + ec.Value))
		for _, k := range slices.Sorted(maps.Keys(ec.Env)) {
			h.Write([]byte("\x01" + k + "\x00" + ec.Env[k]))
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	probe, _ := http.NewRequest(http.MethodGet, "http://probe.invalid/", nil)
	cred.Apply(probe)
	names := make([]string, 0, len(probe.Header))
	for name := range probe.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		for _, v := range probe.Header[name] {
			h.Write([]byte{0})
			h.Write([]byte(v))
		}
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// dialed is an open session plus the error scrubbing that belongs to its transport.
// overflowed reports that an inbound message broke the size bound, which leaves the stream
// unusable (and would mislabel later errors), so such a session is discarded.
type dialed struct {
	cs         *mcp.ClientSession
	sanitize   func(error) error
	overflowed func() bool
	// secrets are the values (stdio env, HTTP credential) scrubbed from tool results and errors.
	secrets []string
}

type dialFunc func(ctx context.Context, key sessionKey, cred Credential) (dialed, error)

// session is one pooled upstream session. ready is closed once the connect attempt finished.
type session struct {
	key    sessionKey
	ready  chan struct{}
	closed chan struct{} // closed when the session ended (peer gone or Close)
	cancel context.CancelFunc
	dialed
	err error

	refs     int
	lastUsed time.Time

	// toolsListed loads the server's tool list into the SDK cache once per session: the SDK reads
	// x-mcp-header annotations from it to send the Mcp-Param-* headers a tools/call needs.
	toolsListed sync.Once
}

func (s *session) isDead() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// pool keeps one live session per (endpoint, owner, credential) with an idle TTL and a cap.
// Concurrent callers of the same key share the session (MCP multiplexes requests).
type pool struct {
	dial           dialFunc
	onLost         func(endpoint string)
	idleTTL        time.Duration
	max            int
	connectTimeout time.Duration
	base           context.Context
	stop           context.CancelFunc

	mu       sync.Mutex
	sessions map[sessionKey]*session
	closed   bool
	wg       sync.WaitGroup
}

func newPool(dial dialFunc, onLost func(string), idleTTL time.Duration, max int, connectTimeout time.Duration) *pool {
	base, stop := context.WithCancel(context.Background())
	p := &pool{
		dial: dial, onLost: onLost, idleTTL: idleTTL, max: max, connectTimeout: connectTimeout,
		base: base, stop: stop, sessions: map[sessionKey]*session{},
	}
	p.wg.Add(1)
	go p.janitor()
	return p
}

// acquire returns a ready session for key, connecting if needed; release it with p.release.
func (p *pool) acquire(ctx context.Context, key sessionKey, cred Credential) (*session, error) {
	s, err := p.reserve(key, cred)
	if err != nil {
		return nil, err
	}
	select {
	case <-s.ready:
	case <-ctx.Done():
		p.release(s)
		return nil, ctx.Err()
	}
	if s.err != nil {
		p.release(s)
		return nil, s.err
	}
	return s, nil
}

func (p *pool) reserve(key sessionKey, cred Credential) (*session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	s := p.sessions[key]
	if s != nil && s.isDead() {
		p.dropLocked(s)
		if s.err == nil { // an established session ended, as opposed to a failed connect
			p.onLost(key.endpoint)
		}
		s = nil
	}
	if s == nil {
		if len(p.sessions) >= p.max && !p.evictIdleLocked() {
			return nil, ErrPoolExhausted
		}
		s = p.startLocked(key, cred)
	}
	s.refs++
	return s, nil
}

func (p *pool) startLocked(key sessionKey, cred Credential) *session {
	ctx, cancel := context.WithCancel(p.base)
	s := &session{key: key, ready: make(chan struct{}), closed: make(chan struct{}), cancel: cancel, lastUsed: time.Now()}
	p.sessions[key] = s
	p.wg.Add(1)
	go p.connect(ctx, s, cred)
	return s
}

// connect dials with the session's long-lived ctx (legacy SSE binds its stream to it), bounded
// by connectTimeout through cancellation rather than a deadline on that ctx.
func (p *pool) connect(ctx context.Context, s *session, cred Credential) {
	defer p.wg.Done()
	timer := time.AfterFunc(p.connectTimeout, s.cancel)
	d, err := p.dial(ctx, s.key, cred)
	if !timer.Stop() && err == nil {
		_ = d.cs.Close()
		err = context.DeadlineExceeded
	}
	if err != nil {
		s.err = err
		close(s.closed)
		close(s.ready)
		s.cancel()
		p.forget(s)
		return
	}
	s.dialed = d
	close(s.ready)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		_ = d.cs.Wait()
		close(s.closed)
	}()
}

func (p *pool) release(s *session) {
	p.mu.Lock()
	s.refs--
	s.lastUsed = time.Now()
	p.mu.Unlock()
}

// invalidate drops s so the next acquire reconnects.
func (p *pool) invalidate(s *session) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dropLocked(s)
}

func (p *pool) forget(s *session) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions[s.key] == s {
		delete(p.sessions, s.key)
	}
}

// dropLocked removes s from the map and closes it in the background. After close, every
// session is already being closed by close itself.
func (p *pool) dropLocked(s *session) {
	if p.sessions[s.key] == s {
		delete(p.sessions, s.key)
	}
	if p.closed {
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		closeSession(s)
	}()
}

func closeSession(s *session) {
	<-s.ready
	if s.cs != nil {
		_ = s.cs.Close()
	}
	s.cancel()
}

// evictIdleLocked closes the least recently used idle session; false if none is idle.
func (p *pool) evictIdleLocked() bool {
	var victim *session
	for _, s := range p.sessions {
		if s.refs == 0 && (victim == nil || s.lastUsed.Before(victim.lastUsed)) {
			victim = s
		}
	}
	if victim == nil {
		return false
	}
	p.dropLocked(victim)
	return true
}

func (p *pool) janitor() {
	defer p.wg.Done()
	t := time.NewTicker(max(p.idleTTL/4, minJanitorInterval))
	defer t.Stop()
	for {
		select {
		case <-p.base.Done():
			return
		case <-t.C:
			p.sweep()
		}
	}
}

func (p *pool) sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, s := range p.sessions {
		if s.refs == 0 && (s.isDead() || now.Sub(s.lastUsed) > p.idleTTL) {
			p.dropLocked(s)
		}
	}
}

// dropMatching drops every session whose endpoint satisfies match, in-flight calls included.
func (p *pool) dropMatching(match func(endpoint string) bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sessions {
		if match(s.key.endpoint) {
			p.dropLocked(s)
		}
	}
}

// size reports the number of pooled sessions (tests and metrics).
func (p *pool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sessions)
}

// close shuts every session down and waits for them; later acquires fail with ErrClosed.
func (p *pool) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	all := slices.Collect(maps.Values(p.sessions))
	p.sessions = map[sessionKey]*session{}
	p.mu.Unlock()

	p.stop() // cancels pending connects and the session contexts
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Go(func() { closeSession(s) })
	}
	wg.Wait()
	p.wg.Wait()
}
