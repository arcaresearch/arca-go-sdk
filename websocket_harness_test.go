package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The rotation tests drive two live connections at once, so they run against a
// real server rather than a mocked dialer: an injected transport would let the
// production code get the two-socket bookkeeping wrong in ways only a real
// upgrade would expose.

const wsTestWait = 3 * time.Second

// wsTestServer accepts WebSocket upgrades and hands each connection to the test
// to script individually.
type wsTestServer struct {
	t     *testing.T
	srv   *httptest.Server
	conns chan *wsTestConn
}

// wsTestConn is one server-side connection. Inbound client messages are decoded
// onto a channel; outbound frames are written by the test.
type wsTestConn struct {
	t    *testing.T
	c    *websocket.Conn
	in   chan map[string]any
	gone chan struct{} // closed when the client hangs up
}

func newWSTestServer(t *testing.T) *wsTestServer {
	t.Helper()
	s := &wsTestServer{t: t, conns: make(chan *wsTestConn, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(wsReadLimit)
		tc := &wsTestConn{t: t, c: c, in: make(chan map[string]any, 256), gone: make(chan struct{})}
		s.conns <- tc
		go tc.readLoop()
		<-tc.gone
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// rootURL is what Config.BaseURL takes; New appends the /api/v1 prefix itself.
func (s *wsTestServer) rootURL() string { return s.srv.URL }

// baseURL is the already-prefixed form the manager is configured with directly;
// wsURL derives the socket URL from it.
func (s *wsTestServer) baseURL() string { return s.srv.URL + "/api/v1" }

// accept waits for the next client connection.
func (s *wsTestServer) accept() *wsTestConn {
	s.t.Helper()
	select {
	case c := <-s.conns:
		return c
	case <-time.After(wsTestWait):
		s.t.Fatal("timed out waiting for a connection")
		return nil
	}
}

// expectNoConn asserts no further connection arrives within d.
func (s *wsTestServer) expectNoConn(d time.Duration, what string) {
	s.t.Helper()
	select {
	case <-s.conns:
		s.t.Fatalf("unexpected connection: %s", what)
	case <-time.After(d):
	}
}

func (c *wsTestConn) readLoop() {
	defer close(c.gone)
	for {
		_, data, err := c.c.Read(context.Background())
		if err != nil {
			return
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		select {
		case c.in <- msg:
		default:
		}
	}
}

func (c *wsTestConn) send(msg map[string]any) {
	c.t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsTestWait)
	defer cancel()
	_ = c.c.Write(ctx, websocket.MessageText, raw)
}

// sendAuthenticated replies to the client's auth frame. lifetimeSec > 0 is
// reported as the server's connection-lifetime cap.
func (c *wsTestConn) sendAuthenticated(lifetimeSec float64) {
	msg := map[string]any{"type": "authenticated"}
	if lifetimeSec > 0 {
		msg["maxConnectionLifetimeSec"] = lifetimeSec
	}
	c.send(msg)
}

// waitFor returns the next inbound message whose action matches, discarding
// anything before it.
func (c *wsTestConn) waitFor(action string) map[string]any {
	c.t.Helper()
	deadline := time.After(wsTestWait)
	for {
		select {
		case msg := <-c.in:
			if msg["action"] == action {
				return msg
			}
		case <-deadline:
			c.t.Fatalf("timed out waiting for action %q", action)
			return nil
		}
	}
}

// drainActions collects every action seen within d.
func (c *wsTestConn) drainActions(d time.Duration) []string {
	var out []string
	deadline := time.After(d)
	for {
		select {
		case msg := <-c.in:
			if a, _ := msg["action"].(string); a != "" {
				out = append(out, a)
			}
		case <-deadline:
			return out
		}
	}
}

// expectNoAction asserts the client sends no such action within d.
func (c *wsTestConn) expectNoAction(action string, d time.Duration) {
	c.t.Helper()
	for _, a := range c.drainActions(d) {
		if a == action {
			c.t.Fatalf("unexpected action %q", action)
		}
	}
}

// handshake performs the standard auth exchange a fresh connection expects.
func (c *wsTestConn) handshake(lifetimeSec float64) {
	c.t.Helper()
	c.waitFor("auth")
	c.sendAuthenticated(lifetimeSec)
}

// warmup performs the exchange a warming connection expects: auth, then the
// ping that fences the resubscribe batch. It returns the actions the client
// re-issued so a test can assert on them.
func (c *wsTestConn) warmup(lifetimeSec float64) []string {
	c.t.Helper()
	c.waitFor("auth")
	c.sendAuthenticated(lifetimeSec)
	var reissued []string
	deadline := time.After(wsTestWait)
	for {
		select {
		case msg := <-c.in:
			a, _ := msg["action"].(string)
			if a == "ping" {
				return reissued
			}
			if a != "" {
				reissued = append(reissued, a)
			}
		case <-deadline:
			c.t.Fatal("timed out waiting for the handoff ping")
			return nil
		}
	}
}

// promote completes the handoff barrier.
func (c *wsTestConn) promote() { c.send(map[string]any{"type": "pong"}) }

func (c *wsTestConn) close() { _ = c.c.Close(websocket.StatusNormalClosure, "test") }

// waitGone asserts the client closed this connection.
func (c *wsTestConn) waitGone(what string) {
	c.t.Helper()
	select {
	case <-c.gone:
	case <-time.After(wsTestWait):
		c.t.Fatalf("connection was not closed: %s", what)
	}
}

// stillOpen asserts the client has NOT closed this connection.
func (c *wsTestConn) stillOpen(what string) {
	c.t.Helper()
	select {
	case <-c.gone:
		c.t.Fatalf("connection was closed unexpectedly: %s", what)
	default:
	}
}

// ---- Manager under test ----

// newTestWSManager builds a manager pointed at the test server. lifetime 0
// disables auto-rotation, which is what most tests want so they can drive
// RotateConnection explicitly.
func newTestWSManager(t *testing.T, s *wsTestServer, lifetime time.Duration) *WebSocketManager {
	t.Helper()
	m := newWebSocketManager(wsConfig{
		baseURL:    s.baseURL(),
		credential: "arca_test_key",
		credType:   credAPIKey,
		getRealmID: func() string { return "rlm_01h2xcejqtf2nbrexx3vqjhp41" },
		lifetime:   lifetime,
	})
	t.Cleanup(m.Disconnect)
	return m
}

// compressRotationTimings shrinks the handoff timers so a test does not wait
// out production budgets. Restored on cleanup.
func compressRotationTimings(t *testing.T, timeout, retry, promote time.Duration) {
	t.Helper()
	oT, oR, oP := wsHandoffTimeout, wsHandoffRetry, wsPromoteRetry
	wsHandoffTimeout, wsHandoffRetry, wsPromoteRetry = timeout, retry, promote
	t.Cleanup(func() { wsHandoffTimeout, wsHandoffRetry, wsPromoteRetry = oT, oR, oP })
}

// waitFor polls until cond holds, failing the test if it never does.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wsTestWait)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// eventCollector records dispatched events by type.
type eventCollector struct {
	mu     sync.Mutex
	events []RealmEvent
}

func (e *eventCollector) add(ev RealmEvent) {
	e.mu.Lock()
	e.events = append(e.events, ev)
	e.mu.Unlock()
}

func (e *eventCollector) paths() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.events))
	for _, ev := range e.events {
		out = append(out, ev.Path)
	}
	return out
}

func (e *eventCollector) len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

// statusRecorder captures the status transitions a test observes.
type statusRecorder struct {
	mu  sync.Mutex
	all []ConnectionStatus
}

func (s *statusRecorder) add(st ConnectionStatus) {
	s.mu.Lock()
	s.all = append(s.all, st)
	s.mu.Unlock()
}

func (s *statusRecorder) snapshot() []ConnectionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ConnectionStatus(nil), s.all...)
}

func (s *statusRecorder) contains(want ConnectionStatus) bool {
	for _, st := range s.snapshot() {
		if st == want {
			return true
		}
	}
	return false
}

func (s *statusRecorder) String() string {
	parts := make([]string, 0)
	for _, st := range s.snapshot() {
		parts = append(parts, string(st))
	}
	return strings.Join(parts, ",")
}

// connectManager brings a manager up and returns the authenticated connection.
func connectManager(t *testing.T, s *wsTestServer, m *WebSocketManager, lifetimeSec float64) *wsTestConn {
	t.Helper()
	m.EnsureConnected()
	c := s.accept()
	c.handshake(lifetimeSec)
	waitFor(t, "connected status", func() bool { return m.Status() == StatusConnected })
	return c
}

// pathEvent is a minimal server event carrying an identifiable marker and,
// optionally, a delivery sequence.
func pathEvent(marker string, seq int64) map[string]any {
	msg := map[string]any{"type": string(EventObjectUpdated), "path": marker}
	if seq > 0 {
		msg["deliverySeq"] = seq
	}
	return msg
}
