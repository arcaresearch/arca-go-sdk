package arca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Operation waits used to hold a realm-root watch: every wait assembled a
// full-realm snapshot (a realm-wide operations scan) and put every realm
// event on the socket. They now subscribe to operation events by type and
// use the server's acknowledgement of that subscription as the barrier
// before their read.

type operationRecoveryServer struct {
	*wsTestServer
	reads     atomic.Int32
	failures  atomic.Int32
	completed atomic.Bool
	terminal  atomic.Value // OperationState the next reads return, when set
}

func newOperationRecoveryServer(t *testing.T) (*operationRecoveryServer, *Arca) {
	t.Helper()
	s := &operationRecoveryServer{wsTestServer: &wsTestServer{t: t, conns: make(chan *wsTestConn, 16)}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		tc := &wsTestConn{t: t, c: c, in: make(chan map[string]any, 256), gone: make(chan struct{})}
		s.conns <- tc
		go tc.readLoop()
		<-tc.gone
	})
	mux.HandleFunc("/api/v1/operations/", func(w http.ResponseWriter, r *http.Request) {
		s.reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if s.failures.Add(-1) >= 0 {
			w.WriteHeader(422)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]any{"code": "VALIDATION", "message": "fixture failure"}})
			return
		}
		state := OpPending
		if s.completed.Load() {
			state = OpCompleted
		}
		if terminal, ok := s.terminal.Load().(OperationState); ok {
			state = terminal
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": OperationDetailResponse{Operation: Operation{ID: strings.TrimPrefix(r.URL.Path, "/api/v1/operations/"), State: state}}})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	a, err := New(Config{APIKey: "test", Realm: "00000000-1111-2222-3333-444444444444", BaseURL: s.rootURL()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Dispose)
	return s, a
}

// operationConfirmRequest returns the next acknowledged-subscription request.
func operationConfirmRequest(c *wsTestConn) map[string]any {
	for {
		request := c.waitFor("subscribe_events")
		if id, ok := request["requestId"].(string); ok && strings.HasPrefix(id, "events-") {
			return request
		}
	}
}

func operationConfirmAck(c *wsTestConn, request map[string]any) {
	c.send(map[string]any{"type": "events_subscribed", "requestId": request["requestId"], "types": request["types"]})
}

func waitOperationReads(t *testing.T, s *operationRecoveryServer, count int32) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for s.reads.Load() < count && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if s.reads.Load() != count {
		t.Fatalf("reads=%d, want %d", s.reads.Load(), count)
	}
}

func beginOperationWait(t *testing.T, a *Arca) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		op, err := a.WaitForOperation(context.Background(), "op", 8*time.Second)
		if err == nil && (op.ID != "op" || op.State != OpCompleted) {
			t.Errorf("wrong terminal operation %+v", op)
		}
		done <- err
	}()
	return done
}

func eventTypeOwners(a *Arca, eventType string) int {
	a.ws.mu.Lock()
	defer a.ws.mu.Unlock()
	return a.ws.eventTypeSubs[eventType]
}

func TestOperationWatchSubscribesByTypeNotRoot(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	s.completed.Store(true)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	request := operationConfirmRequest(c)
	types, _ := request["types"].([]any)
	if len(types) != 2 || types[0] != EventOperationCreated || types[1] != EventOperationUpdated {
		t.Fatalf("confirmed types = %v", request["types"])
	}
	operationConfirmAck(c, request)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	a.ws.mu.Lock()
	roots := a.ws.pathRefs["/"]
	a.ws.mu.Unlock()
	if roots != 0 {
		t.Fatalf("operation wait took a realm-root watch (%d)", roots)
	}
}

func TestOperationWatchStaleAckCannotAcknowledge(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	request := operationConfirmRequest(c)
	c.send(map[string]any{"type": "events_subscribed", "requestId": "stale-request"})
	time.Sleep(50 * time.Millisecond)
	if s.reads.Load() != 0 {
		t.Fatal("stale acknowledgement released the read")
	}
	operationConfirmAck(c, request)
	waitOperationReads(t, s, 1)
	c.send(map[string]any{"type": EventOperationUpdated, "entityId": "op", "operation": Operation{ID: "op", State: OpCompleted}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.reads.Load() != 1 {
		t.Fatal("unexpected extra recovery read")
	}
}

func TestOperationWatchRotationRecoversTerminalAfterHealthyPending(t *testing.T) {
	for _, state := range []OperationState{OpCompleted, OpFailed, OpExpired} {
		t.Run(string(state), func(t *testing.T) {
			s, a := newOperationRecoveryServer(t)
			done := make(chan error, 1)
			go func() {
				op, err := a.WaitForOperation(context.Background(), "op", 8*time.Second)
				if err == nil && op.State != state {
					t.Errorf("wrong operation: %+v", op)
				}
				done <- err
			}()
			first := s.accept()
			first.handshake(0)
			operationConfirmAck(first, operationConfirmRequest(first))
			waitOperationReads(t, s, 1)
			s.terminal.Store(state)
			if !a.ws.RotateConnection() {
				t.Fatal("rotation refused")
			}
			replacement := s.accept()
			reissued := replacement.warmup(0)
			if !containsAction(reissued, "subscribe_events") {
				t.Fatalf("replacement did not re-subscribe operation events: %v", reissued)
			}
			replacement.promote()
			operationConfirmAck(replacement, operationConfirmRequest(replacement))
			err := <-done
			if state == OpCompleted {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var failed *OperationFailedError
				if !errors.As(err, &failed) || failed.Operation.State != string(state) {
					t.Fatalf("expected typed %s, got %v", state, err)
				}
			}
			if s.reads.Load() != 2 {
				t.Fatalf("reads=%d, want the pending read and one recovery", s.reads.Load())
			}
		})
	}
}

func containsAction(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// Another owner's realm-root watch snapshot still carries terminal
// evidence the wait accepts without a read.
func TestOperationWatchAcceptsAnotherRootOwnersSnapshot(t *testing.T) {
	for _, state := range []OperationState{OpCompleted, OpFailed, OpExpired} {
		t.Run(string(state), func(t *testing.T) {
			s, a := newOperationRecoveryServer(t)
			rootCtx, cancelRoot := context.WithCancel(context.Background())
			defer cancelRoot()
			go func() { _, _ = a.ws.watchPath(rootCtx, "/") }()
			c := s.accept()
			c.handshake(0)
			var root map[string]any
			for {
				root = c.waitFor("watch")
				if id, _ := root["requestId"].(string); strings.HasPrefix(id, "watch-") {
					break
				}
			}
			done := make(chan error, 1)
			go func() {
				op, err := a.WaitForOperation(context.Background(), "op", 3*time.Second)
				if err == nil && op.State != state {
					t.Errorf("wrong operation: %+v", op)
				}
				done <- err
			}()
			operationConfirmRequest(c)
			c.send(map[string]any{"type": "watch_snapshot", "path": "/", "requestId": root["requestId"],
				"operations":         []Operation{{ID: "foreign", State: OpCompleted}, {ID: "op", State: OpPending}},
				"bufferedOperations": []Operation{{ID: "op", State: state}}})
			err := <-done
			if state == OpCompleted {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var failed *OperationFailedError
				if !errors.As(err, &failed) || failed.Operation.State != string(state) {
					t.Fatalf("expected typed %s, got %v", state, err)
				}
			}
			if s.reads.Load() != 0 {
				t.Fatalf("shared terminal evidence performed %d reads", s.reads.Load())
			}
			a.ws.unwatchPath("/")
		})
	}
}

func TestOperationWatchFreshAckQuietPendingAndGap(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	first := operationConfirmRequest(c)
	time.Sleep(30 * time.Millisecond)
	if s.reads.Load() != 0 {
		t.Fatal("read before the subscription was acknowledged")
	}
	operationConfirmAck(c, first)
	waitOperationReads(t, s, 1)
	time.Sleep(2100 * time.Millisecond)
	if s.reads.Load() != 1 {
		t.Fatal("healthy pending operation was polled")
	}
	s.completed.Store(true)
	c.send(map[string]any{"type": "stream.resync"})
	next := operationConfirmRequest(c)
	operationConfirmAck(c, first)
	time.Sleep(20 * time.Millisecond)
	if s.reads.Load() != 1 {
		t.Fatal("stale ACK accepted")
	}
	operationConfirmAck(c, next)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.reads.Load() != 2 {
		t.Fatal("gap did not perform exactly one recovery")
	}
}

func TestOperationWatchFiniteFailuresAndTerminalPush(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	s.failures.Store(9)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	for i := 0; i < 3; i++ {
		operationConfirmAck(c, operationConfirmRequest(c))
	}
	waitOperationReads(t, s, 3)
	time.Sleep(2100 * time.Millisecond)
	if s.reads.Load() != 3 {
		t.Fatal("failed recovery exceeded its read budget")
	}
	c.send(map[string]any{"type": EventOperationUpdated, "entityId": "op", "operation": Operation{ID: "op", State: OpCompleted}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOperationWatchForeignPayloadIgnoredSparseOwnEventRecovers(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	operationConfirmAck(c, operationConfirmRequest(c))
	waitOperationReads(t, s, 1)
	c.send(map[string]any{"type": EventOperationUpdated, "entityId": "op", "operation": Operation{ID: "foreign", State: OpCompleted}})
	time.Sleep(30 * time.Millisecond)
	if s.reads.Load() != 1 {
		t.Fatal("foreign payload triggered recovery")
	}
	select {
	case <-done:
		t.Fatal("foreign payload completed operation")
	default:
	}
	s.completed.Store(true)
	c.send(map[string]any{"type": EventOperationUpdated, "entityId": "op"})
	operationConfirmAck(c, operationConfirmRequest(c))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOperationWatchTerminalPushBeatsMissingAck(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	operationConfirmRequest(c)
	c.send(map[string]any{"type": EventOperationUpdated, "entityId": "op", "operation": Operation{ID: "op", State: OpCompleted}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("terminal push waited for ACK")
	}
	if s.reads.Load() != 0 {
		t.Fatal("terminal push performed history read")
	}
	a.ws.mu.Lock()
	pending := len(a.ws.pending)
	a.ws.mu.Unlock()
	if owners := eventTypeOwners(a, EventOperationUpdated); owners != 0 || pending != 0 {
		t.Fatalf("leaked owners=%d pending=%d", owners, pending)
	}
}

func TestOperationWatchConcurrentWaitsShareTypeInterest(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	first := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	operationConfirmAck(c, operationConfirmRequest(c))
	waitOperationReads(t, s, 1)
	second := make(chan error, 1)
	go func() { _, err := a.WaitForOperation(context.Background(), "op-b", 8*time.Second); second <- err }()
	operationConfirmAck(c, operationConfirmRequest(c))
	waitOperationReads(t, s, 2)
	c.send(map[string]any{"type": EventOperationUpdated, "entityId": "op", "operation": Operation{ID: "op", State: OpCompleted}})
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if owners := eventTypeOwners(a, EventOperationUpdated); owners != 1 {
		t.Fatalf("finishing one wait left %d owners, want the other wait's 1", owners)
	}
	c.send(map[string]any{"type": EventOperationUpdated, "entityId": "op-b", "operation": Operation{ID: "op-b", State: OpCompleted}})
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if owners := eventTypeOwners(a, EventOperationUpdated); owners != 0 {
		t.Fatalf("owners=%d after both waits", owners)
	}
}

func TestOperationWatchServerErrorIsNotAnAcknowledgement(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	types := []string{EventOperationUpdated}
	a.ws.subscribeEvents(types)
	defer a.ws.unsubscribeEvents(types)
	done := make(chan error, 1)
	go func() { done <- a.ws.confirmEventTypes(context.Background(), types) }()
	c := s.accept()
	c.handshake(0)
	request := operationConfirmRequest(c)
	c.send(map[string]any{"type": "error", "requestId": request["requestId"], "message": "not authorized"})
	if err := <-done; err == nil {
		t.Fatal("server-rejected subscription was treated as an ACK")
	}
}

func TestConfirmEventTypesRequiresOwnership(t *testing.T) {
	_, a := newOperationRecoveryServer(t)
	if err := a.ws.confirmEventTypes(context.Background(), []string{EventOperationUpdated}); err == nil {
		t.Fatal("confirmed a type nobody owns")
	}
}
