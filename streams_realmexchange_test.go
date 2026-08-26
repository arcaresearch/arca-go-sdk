package arca

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// WatchRealmExchange is the realm-wide live-tail of exchange.updated. These
// tests pin its contract against a scripted WS server:
//
//   - the subscribe is type-routed (subscribe_events), never a path watch;
//   - an inlined exchangeState is delivered as State, including liquidationPrice;
//   - a name-only event is delivered with State=nil and does not GET;
//   - a gap / reconnect after start emits Resync and does not GET;
//   - events before the first consumer attach are dropped.

type realmExchangeServer struct {
	*wsTestServer
	mu      sync.Mutex
	gets    int
	lastGet string
}

func newRealmExchangeServer(t *testing.T) *realmExchangeServer {
	t.Helper()
	s := &realmExchangeServer{}
	inner := &wsTestServer{t: t, conns: make(chan *wsTestConn, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(wsReadLimit)
		tc := &wsTestConn{t: t, c: c, in: make(chan map[string]any, 256), gone: make(chan struct{})}
		inner.conns <- tc
		go tc.readLoop()
		<-tc.gone
	})
	mux.HandleFunc("/api/v1/objects/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.gets++
		s.lastGet = r.URL.Path
		s.mu.Unlock()
		http.NotFound(w, r)
	})
	inner.srv = httptest.NewServer(mux)
	t.Cleanup(inner.srv.Close)
	s.wsTestServer = inner
	return s
}

func (s *realmExchangeServer) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func newRealmExchangeArca(t *testing.T, s *realmExchangeServer) *Arca {
	t.Helper()
	a, err := New(Config{
		APIKey:  "arca_test_key",
		Realm:   "rlm_01h2xcejqtf2nbrexx3vqjhp41",
		BaseURL: s.rootURL(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(a.Dispose)
	return a
}

type realmExchangeCollector struct {
	mu      sync.Mutex
	updates []RealmExchangeUpdate
}

func (c *realmExchangeCollector) add(u RealmExchangeUpdate) {
	c.mu.Lock()
	c.updates = append(c.updates, u)
	c.mu.Unlock()
}

func (c *realmExchangeCollector) snapshot() []RealmExchangeUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RealmExchangeUpdate, len(c.updates))
	copy(out, c.updates)
	return out
}

func openRealmExchangeStream(t *testing.T, s *realmExchangeServer, a *Arca) (*RealmExchangeWatchStream, *realmExchangeCollector, *wsTestConn) {
	t.Helper()
	col := &realmExchangeCollector{}
	type result struct {
		st  *RealmExchangeWatchStream
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := a.WatchRealmExchange(t.Context())
		done <- result{st, err}
	}()
	c := s.accept()
	c.handshake(0)
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("WatchRealmExchange: %v", r.err)
		}
		t.Cleanup(r.st.Close)
		r.st.OnUpdate(col.add)
		return r.st, col, c
	case <-time.After(wsTestWait):
		t.Fatal("WatchRealmExchange never returned")
		return nil, nil, nil
	}
}

func waitSubscribeEvents(t *testing.T, c *wsTestConn) map[string]any {
	t.Helper()
	return c.waitFor("subscribe_events")
}

func exchangeUpdatedFrame(objectID, path string, state map[string]any, seq int64) map[string]any {
	msg := map[string]any{
		"type":       string(EventExchangeUpdated),
		"entityId":   objectID,
		"entityPath": path,
	}
	if state != nil {
		msg["exchangeState"] = state
	}
	if seq > 0 {
		msg["deliverySeq"] = seq
	}
	return msg
}

func TestWatchRealmExchange_SubscribesByTypeNotPath(t *testing.T) {
	s := newRealmExchangeServer(t)
	a := newRealmExchangeArca(t, s)
	_, _, c := openRealmExchangeStream(t, s, a)

	sub := waitSubscribeEvents(t, c)
	types, _ := sub["types"].([]any)
	if len(types) != 1 || types[0] != string(EventExchangeUpdated) {
		t.Fatalf("subscribe_events types = %#v, want [exchange.updated]", sub["types"])
	}
	c.expectNoAction("watch", 150*time.Millisecond)
}

func TestWatchRealmExchange_DeliversInlineBook(t *testing.T) {
	s := newRealmExchangeServer(t)
	a := newRealmExchangeArca(t, s)
	_, col, c := openRealmExchangeStream(t, s, a)
	_ = waitSubscribeEvents(t, c)

	c.send(exchangeUpdatedFrame("obj_a", "/users/a/ex", map[string]any{
		"positions": []map[string]any{{
			"market": "hl:0:BTC", "side": "long", "size": "0.1",
			"liquidationPrice": "91234.5",
		}},
	}, 1))

	waitFor(t, "inline book delivery", func() bool { return len(col.snapshot()) == 1 })
	u := col.snapshot()[0]
	if u.Resync {
		t.Fatal("live event must not be marked Resync")
	}
	if u.ObjectID != "obj_a" || u.ObjectPath != "/users/a/ex" {
		t.Fatalf("ids = (%q, %q)", u.ObjectID, u.ObjectPath)
	}
	if u.State == nil || len(u.State.Positions) != 1 {
		t.Fatalf("State = %+v, want one position", u.State)
	}
	if u.State.Positions[0].LiquidationPrice == nil || *u.State.Positions[0].LiquidationPrice != "91234.5" {
		t.Fatalf("liquidationPrice = %v, want serve-path 91234.5", u.State.Positions[0].LiquidationPrice)
	}
	if s.getCount() != 0 {
		t.Fatalf("GetExchangeState was called %d times; inline book must not refetch", s.getCount())
	}
}

func TestWatchRealmExchange_NameOnlyDoesNotRefetch(t *testing.T) {
	s := newRealmExchangeServer(t)
	a := newRealmExchangeArca(t, s)
	_, col, c := openRealmExchangeStream(t, s, a)
	_ = waitSubscribeEvents(t, c)

	c.send(exchangeUpdatedFrame("obj_a", "/users/a/ex", nil, 1))

	waitFor(t, "name-only delivery", func() bool { return len(col.snapshot()) == 1 })
	u := col.snapshot()[0]
	if u.State != nil || u.Resync || u.ObjectID != "obj_a" {
		t.Fatalf("update = %+v, want name-only obj_a", u)
	}
	if s.getCount() != 0 {
		t.Fatalf("GetExchangeState was called %d times; nil State must not fan out GETs", s.getCount())
	}
}

func TestWatchRealmExchange_GapEmitsResync(t *testing.T) {
	s := newRealmExchangeServer(t)
	a := newRealmExchangeArca(t, s)
	_, col, c := openRealmExchangeStream(t, s, a)
	_ = waitSubscribeEvents(t, c)

	c.send(exchangeUpdatedFrame("obj_a", "/users/a/ex", map[string]any{}, 1))
	waitFor(t, "first live event", func() bool { return len(col.snapshot()) == 1 })

	c.send(resyncMarker(2))
	waitFor(t, "resync after gap", func() bool {
		ups := col.snapshot()
		return len(ups) == 2 && ups[1].Resync
	})
	if col.snapshot()[1].State != nil {
		t.Fatal("resync marker must not carry State")
	}
	if s.getCount() != 0 {
		t.Fatalf("resync must not GET, got %d", s.getCount())
	}
}

func TestWatchRealmExchange_ReconnectEmitsResync(t *testing.T) {
	s := newRealmExchangeServer(t)
	a := newRealmExchangeArca(t, s)
	_, col, c := openRealmExchangeStream(t, s, a)
	_ = waitSubscribeEvents(t, c)

	// First authenticated already happened at handshake and must not
	// resync. A later authenticated is a reconnect.
	c.sendAuthenticated(0)
	waitFor(t, "resync after reconnect auth", func() bool {
		ups := col.snapshot()
		return len(ups) == 1 && ups[0].Resync
	})
	if s.getCount() != 0 {
		t.Fatalf("reconnect resync must not GET, got %d", s.getCount())
	}
}

func TestWatchRealmExchange_DropsEventsBeforeAttach(t *testing.T) {
	s := newRealmExchangeServer(t)
	a := newRealmExchangeArca(t, s)

	type result struct {
		st  *RealmExchangeWatchStream
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := a.WatchRealmExchange(t.Context())
		done <- result{st, err}
	}()
	c := s.accept()
	c.handshake(0)
	var stream *RealmExchangeWatchStream
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("WatchRealmExchange: %v", r.err)
		}
		stream = r.st
		t.Cleanup(stream.Close)
	case <-time.After(wsTestWait):
		t.Fatal("WatchRealmExchange never returned")
	}
	_ = waitSubscribeEvents(t, c)

	c.send(exchangeUpdatedFrame("obj_early", "/users/e/ex", map[string]any{}, 1))
	time.Sleep(80 * time.Millisecond)

	col := &realmExchangeCollector{}
	stream.OnUpdate(col.add)
	c.send(exchangeUpdatedFrame("obj_late", "/users/l/ex", map[string]any{}, 2))
	waitFor(t, "post-attach event", func() bool { return len(col.snapshot()) == 1 })
	if col.snapshot()[0].ObjectID != "obj_late" {
		t.Fatalf("got %+v, want only the post-attach event", col.snapshot())
	}
}
