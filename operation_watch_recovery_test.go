package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type operationRecoveryServer struct {
	*wsTestServer
	reads     atomic.Int32
	failures  atomic.Int32
	completed atomic.Bool
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
	mux.HandleFunc("/api/v1/operations/op", func(w http.ResponseWriter, r *http.Request) {
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
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": OperationDetailResponse{Operation: Operation{ID: "op", State: state}}})
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

func operationWatchRequest(c *wsTestConn) map[string]any {
	for {
		request := c.waitFor("watch")
		if id, ok := request["requestId"].(string); ok && strings.HasPrefix(id, "watch-") {
			return request
		}
	}
}

func operationWatchAck(c *wsTestConn, request map[string]any) {
	c.send(map[string]any{"type": "watch_snapshot", "requestId": request["requestId"], "path": "/"})
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

func TestOperationWatchFreshAckQuietPendingAndGap(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	first := operationWatchRequest(c)
	time.Sleep(30 * time.Millisecond)
	if s.reads.Load() != 0 {
		t.Fatal("snapshot read before correlated ACK")
	}
	operationWatchAck(c, first)
	waitOperationReads(t, s, 1)
	time.Sleep(2100 * time.Millisecond)
	if s.reads.Load() != 1 {
		t.Fatal("healthy pending operation was polled")
	}
	s.completed.Store(true)
	c.send(map[string]any{"type": "stream.resync"})
	next := operationWatchRequest(c)
	operationWatchAck(c, first)
	time.Sleep(20 * time.Millisecond)
	if s.reads.Load() != 1 {
		t.Fatal("stale ACK accepted")
	}
	operationWatchAck(c, next)
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
		operationWatchAck(c, operationWatchRequest(c))
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
	operationWatchAck(c, operationWatchRequest(c))
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
	operationWatchAck(c, operationWatchRequest(c))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOperationWatchTerminalPushBeatsMissingAck(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	done := beginOperationWait(t, a)
	c := s.accept()
	c.handshake(0)
	operationWatchRequest(c)
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
	refs := a.ws.pathRefs["/"]
	pending := len(a.ws.pending)
	a.ws.mu.Unlock()
	if refs != 0 || pending != 0 {
		t.Fatalf("leaked refs=%d pending=%d", refs, pending)
	}
}

func TestOperationWatchServerErrorIsNotAnAcknowledgement(t *testing.T) {
	s, a := newOperationRecoveryServer(t)
	done := make(chan error, 1)
	go func() { _, err := a.ws.watchPath(context.Background(), "/"); done <- err }()
	c := s.accept()
	c.handshake(0)
	request := operationWatchRequest(c)
	c.send(map[string]any{"type": "error", "requestId": request["requestId"], "message": "not authorized"})
	if err := <-done; err == nil {
		t.Fatal("server rejected watch was treated as an ACK")
	}
	a.ws.unwatchPath("/")
}
