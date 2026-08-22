package arca

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// WatchRealmFills is the at-least-once realm-wide fill stream. These tests
// pin its delivery contract end-to-end against a scripted server that
// serves both halves of the protocol — the durable log (REST
// /exchange/fills) and the live tail (WebSocket fill.recorded):
//
//   - catch-up from a persisted cursor replays exactly the missed fills,
//     then live fills flow with the cursor advancing;
//   - an empty FromCursor starts at the head of the log (no history replay);
//   - a reconnect and a server resync marker both re-run catch-up from the
//     last durable position, recovering fills recorded during the outage;
//   - the catch-up/tail overlap window dedupes by fill id;
//   - a live fill without createdAt is delivered but never advances the
//     cursor (duplicates over loss).

// realmFillsServer serves the REST fills log and accepts WS connections.
type realmFillsServer struct {
	t     *testing.T
	srv   *httptest.Server
	conns chan *wsTestConn

	mu    sync.Mutex
	fills []Fill // ascending (createdAt, id) order
	gets  int    // REST hits, for asserting replay actually ran
}

func newRealmFillsServer(t *testing.T) *realmFillsServer {
	t.Helper()
	s := &realmFillsServer{t: t, conns: make(chan *wsTestConn, 16)}
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
	mux.HandleFunc("/api/v1/exchange/fills", s.handleListFills)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// addFill appends a fill to the durable log (must be appended in ascending
// (createdAt, id) order, like Spanner commit order).
func (s *realmFillsServer) addFill(f Fill) {
	s.mu.Lock()
	s.fills = append(s.fills, f)
	s.mu.Unlock()
}

func (s *realmFillsServer) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

// handleListFills implements the server's realm-wide listing contract: keyset
// cursor on (createdAt, id), asc replay (cursor always returned; echoed on an
// empty page) and desc head reads.
func (s *realmFillsServer) handleListFills(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++

	q := r.URL.Query()
	desc := q.Get("order") == "desc"
	limit := 500
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	cursor := q.Get("cursor")
	afterCursor := func(f Fill) bool {
		if cursor == "" {
			return true
		}
		parts := strings.SplitN(cursor, "|", 2)
		cTime, cID := parts[0], parts[1]
		if f.CreatedAt != cTime {
			return f.CreatedAt > cTime
		}
		return f.ID > cID
	}

	var page []Fill
	if desc {
		for i := len(s.fills) - 1; i >= 0 && len(page) < limit; i-- {
			page = append(page, s.fills[i])
		}
	} else {
		for _, f := range s.fills {
			if len(page) == limit {
				break
			}
			if afterCursor(f) {
				page = append(page, f)
			}
		}
	}

	out := FillListResponse{Fills: page, Total: len(page)}
	if !desc {
		if len(page) > 0 {
			last := page[len(page)-1]
			out.Cursor = last.CreatedAt + "|" + last.ID
		} else {
			out.Cursor = cursor
		}
	}
	w.Header().Set("Content-Type", "application/json")
	raw, _ := json.Marshal(out)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": json.RawMessage(raw)})
}

// waitQuiescent waits until the stream's replay machinery has gone idle (no
// REST hits for a settle window). Tests use it before injecting live frames
// so cursor-advance assertions are not racing a queued replay.
func (s *realmFillsServer) waitQuiescent(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(wsTestWait)
	for time.Now().Before(deadline) {
		before := s.getCount()
		time.Sleep(120 * time.Millisecond)
		if s.getCount() == before {
			return
		}
	}
	t.Fatal("replay machinery never went quiescent")
}

func (s *realmFillsServer) accept() *wsTestConn {
	s.t.Helper()
	select {
	case c := <-s.conns:
		return c
	case <-time.After(wsTestWait):
		s.t.Fatal("timed out waiting for a WS connection")
		return nil
	}
}

// mkFill builds a durable-log fill at a given (createdAt, id) position.
func mkFill(id, createdAt, objectID string) Fill {
	return Fill{
		ID: id, ObjectID: objectID, Market: "hl:0:BTC", Side: "buy",
		Size: "0.1", Price: "50000", CreatedAt: createdAt,
	}
}

// fillRecordedFrame is the WS frame for a live fill.recorded event carrying
// the enriched payload (createdAt for cursor handoff).
func fillRecordedFrame(id, createdAt, objectID string, seq int64) map[string]any {
	msg := map[string]any{
		"type":     string(EventFillRecorded),
		"entityId": objectID,
		"fill": map[string]any{
			"id": id, "market": "hl:0:BTC", "side": "buy",
			"size": "0.1", "price": "50000", "createdAt": createdAt,
			"realizedPnl": nil,
		},
	}
	if seq > 0 {
		msg["deliverySeq"] = seq
	}
	return msg
}

// updateCollector accumulates stream deliveries for assertions.
type updateCollector struct {
	mu      sync.Mutex
	updates []RealmFillUpdate
}

func (c *updateCollector) add(u RealmFillUpdate) {
	c.mu.Lock()
	c.updates = append(c.updates, u)
	c.mu.Unlock()
}

func (c *updateCollector) snapshot() []RealmFillUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]RealmFillUpdate(nil), c.updates...)
}

func (c *updateCollector) ids() []string {
	out := []string{}
	for _, u := range c.snapshot() {
		out = append(out, u.Fill.ID)
	}
	return out
}

func (c *updateCollector) hasID(id string) bool {
	for _, u := range c.snapshot() {
		if u.Fill.ID == id {
			return true
		}
	}
	return false
}

func newRealmFillsArca(t *testing.T, s *realmFillsServer) *Arca {
	t.Helper()
	a, err := New(Config{
		APIKey:  "arca_test_key",
		Realm:   "rlm_01h2xcejqtf2nbrexx3vqjhp41",
		BaseURL: s.srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(a.Dispose)
	return a
}

func openRealmFillStream(t *testing.T, s *realmFillsServer, a *Arca, fromCursor string) (*RealmFillWatchStream, *updateCollector, *wsTestConn) {
	t.Helper()
	col := &updateCollector{}
	type result struct {
		st  *RealmFillWatchStream
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := a.WatchRealmFills(t.Context(), &WatchRealmFillsOptions{FromCursor: fromCursor})
		done <- result{st, err}
	}()
	c := s.accept()
	c.handshake(0)
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("WatchRealmFills: %v", r.err)
		}
		t.Cleanup(r.st.Close)
		r.st.OnUpdate(col.add)
		return r.st, col, c
	case <-time.After(wsTestWait):
		t.Fatal("WatchRealmFills never returned")
		return nil, nil, nil
	}
}

const (
	ts1 = "2026-08-22T10:00:00.000000Z"
	ts2 = "2026-08-22T10:00:01.000000Z"
	ts3 = "2026-08-22T10:00:02.000000Z"
	ts4 = "2026-08-22T10:00:03.000000Z"
	ts5 = "2026-08-22T10:00:04.000000Z"
)

// Catch-up from a persisted cursor replays the missed fills in order, then
// the live tail takes over and advances the cursor.
func TestWatchRealmFills_CatchUpThenTail(t *testing.T) {
	s := newRealmFillsServer(t)
	s.addFill(mkFill("f1", ts1, "obj-a"))
	s.addFill(mkFill("f2", ts2, "obj-a"))
	s.addFill(mkFill("f3", ts3, "obj-b"))
	a := newRealmFillsArca(t, s)

	// Resume from after f1 — the persisted position of a consumer that
	// processed f1 and then went down.
	stream, col, c := openRealmFillStream(t, s, a, ts1+"|f1")

	waitFor(t, "catch-up to replay f2 and f3", func() bool {
		return col.hasID("f2") && col.hasID("f3")
	})
	ups := col.snapshot()
	if len(ups) != 2 || ups[0].Fill.ID != "f2" || ups[1].Fill.ID != "f3" {
		t.Fatalf("catch-up ids = %v, want exactly [f2 f3] in order", col.ids())
	}
	for _, u := range ups {
		if !u.Replayed {
			t.Errorf("catch-up delivery %s must be marked Replayed", u.Fill.ID)
		}
	}
	if ups[1].Cursor != ts3+"|f3" {
		t.Errorf("cursor after catch-up = %q, want %q", ups[1].Cursor, ts3+"|f3")
	}
	if got := ups[0].Fill.ObjectID; got != "obj-a" {
		t.Errorf("replayed fill ObjectID = %q, want obj-a", got)
	}

	// Live tail: a new fill arrives over the WS (and, as in production, is
	// also in the durable log by the time its event is emitted).
	s.waitQuiescent(t)
	s.addFill(mkFill("f4", ts4, "obj-b"))
	c.send(fillRecordedFrame("f4", ts4, "obj-b", 1))
	waitFor(t, "live f4 delivery", func() bool { return col.hasID("f4") })

	last := col.snapshot()[len(col.snapshot())-1]
	if last.Replayed {
		t.Error("live tail delivery must not be marked Replayed")
	}
	if last.Fill.ObjectID != "obj-b" {
		t.Errorf("live fill ObjectID = %q, want obj-b (from event entityId)", last.Fill.ObjectID)
	}
	waitFor(t, "cursor advance to f4", func() bool { return stream.Cursor() == ts4+"|f4" })
}

// An empty FromCursor means "start from now": history is not replayed, and
// the first live fill flows with the cursor seeded at the head of the log.
func TestWatchRealmFills_StartFromNowSkipsHistory(t *testing.T) {
	s := newRealmFillsServer(t)
	s.addFill(mkFill("f1", ts1, "obj-a"))
	s.addFill(mkFill("f2", ts2, "obj-a"))
	a := newRealmFillsArca(t, s)

	stream, col, c := openRealmFillStream(t, s, a, "")

	// The head seed + initial replay both run; neither may emit history.
	waitFor(t, "initial replay to finish", func() bool { return stream.Cursor() == ts2+"|f2" })
	s.waitQuiescent(t)
	if n := len(col.snapshot()); n != 0 {
		t.Fatalf("start-from-now delivered %d historical fills (%v), want none", n, col.ids())
	}

	s.addFill(mkFill("f3", ts3, "obj-a"))
	c.send(fillRecordedFrame("f3", ts3, "obj-a", 1))
	waitFor(t, "live f3 delivery", func() bool { return col.hasID("f3") })
	waitFor(t, "cursor advance to f3", func() bool { return stream.Cursor() == ts3+"|f3" })
}

// A reconnect re-runs catch-up from the last durable position: fills
// recorded while the connection was down are recovered from the log. This is
// the at-least-once pin — downtime loses nothing.
func TestWatchRealmFills_ReconnectReplaysMissedFills(t *testing.T) {
	s := newRealmFillsServer(t)
	s.addFill(mkFill("f1", ts1, "obj-a"))
	a := newRealmFillsArca(t, s)

	stream, col, first := openRealmFillStream(t, s, a, ts1+"|f1")
	waitFor(t, "initial replay to settle", func() bool { return stream.Cursor() == ts1+"|f1" })
	s.waitQuiescent(t)

	// Fills land in the durable log while the connection is down — no live
	// events, no gap to observe.
	s.addFill(mkFill("f2", ts2, "obj-a"))
	s.addFill(mkFill("f3", ts3, "obj-b"))
	first.close()

	second := s.accept()
	second.handshake(0)

	waitFor(t, "reconnect replay to recover f2 and f3", func() bool {
		return col.hasID("f2") && col.hasID("f3")
	})
	for _, u := range col.snapshot() {
		if !u.Replayed {
			t.Errorf("recovered fill %s must be marked Replayed", u.Fill.ID)
		}
	}
	waitFor(t, "cursor advance to f3", func() bool { return stream.Cursor() == ts3+"|f3" })
}

// A server resync marker (events dropped before sequencing — no observable
// deliverySeq hole) triggers the same replay-from-cursor recovery.
func TestWatchRealmFills_ResyncMarkerTriggersReplay(t *testing.T) {
	s := newRealmFillsServer(t)
	a := newRealmFillsArca(t, s)

	stream, col, c := openRealmFillStream(t, s, a, "")
	s.waitQuiescent(t)
	gets := s.getCount()

	// The lost fill is only in the durable log; the resync marker is the
	// only signal it was ever missed.
	s.addFill(mkFill("f1", ts1, "obj-a"))
	c.send(map[string]any{"type": "stream.resync", "deliverySeq": float64(7)})

	waitFor(t, "resync replay to recover f1", func() bool { return col.hasID("f1") })
	if !col.snapshot()[0].Replayed {
		t.Error("resync-recovered fill must be marked Replayed")
	}
	if s.getCount() <= gets {
		t.Error("resync must hit the durable log")
	}
	waitFor(t, "cursor advance to f1", func() bool { return stream.Cursor() == ts1+"|f1" })
}

// The catch-up/tail overlap window dedupes by fill id: a fill delivered by
// replay and then again by the live tail (or twice by the tail) emits once.
func TestWatchRealmFills_OverlapDedupe(t *testing.T) {
	s := newRealmFillsServer(t)
	s.addFill(mkFill("f1", ts1, "obj-a"))
	a := newRealmFillsArca(t, s)

	stream, col, c := openRealmFillStream(t, s, a, ts1+"|f0")
	waitFor(t, "replay of f1", func() bool { return col.hasID("f1") })
	s.waitQuiescent(t)

	// The same fill arrives on the live tail (overlap window), twice.
	c.send(fillRecordedFrame("f1", ts1, "obj-a", 1))
	c.send(fillRecordedFrame("f1", ts1, "obj-a", 2))
	// And then a genuinely new fill, which must still come through.
	s.addFill(mkFill("f2", ts2, "obj-a"))
	c.send(fillRecordedFrame("f2", ts2, "obj-a", 3))
	waitFor(t, "live f2 delivery", func() bool { return col.hasID("f2") })

	count := 0
	for _, id := range col.ids() {
		if id == "f1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("f1 delivered %d times, want exactly 1 (overlap dedupe)", count)
	}
	waitFor(t, "cursor advance to f2", func() bool { return stream.Cursor() == ts2+"|f2" })
}

// Deliveries begin at the first consumer attach: a fill recorded between
// stream open and OnUpdate registration must not vanish into a
// callback-less emit (which would advance the cursor past it forever). The
// pre-attach live event is ignored and the attach-time catch-up recovers
// the fill from the durable log.
func TestWatchRealmFills_FillBeforeAttachIsNotLost(t *testing.T) {
	s := newRealmFillsServer(t)
	a := newRealmFillsArca(t, s)

	type result struct {
		st  *RealmFillWatchStream
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := a.WatchRealmFills(t.Context(), &WatchRealmFillsOptions{})
		done <- result{st, err}
	}()
	c := s.accept()
	c.handshake(0)
	var stream *RealmFillWatchStream
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("WatchRealmFills: %v", r.err)
		}
		stream = r.st
		t.Cleanup(stream.Close)
	case <-time.After(wsTestWait):
		t.Fatal("WatchRealmFills never returned")
	}

	// A fill lands (log + live event) before any consumer attaches.
	s.addFill(mkFill("f1", ts1, "obj-a"))
	c.send(fillRecordedFrame("f1", ts1, "obj-a", 1))
	time.Sleep(150 * time.Millisecond) // give the ignored event time to arrive

	if got := stream.Cursor(); got != "" {
		t.Fatalf("cursor = %q before any consumer attached, want untouched", got)
	}

	col := &updateCollector{}
	stream.OnUpdate(col.add)
	waitFor(t, "attach-time catch-up to recover f1", func() bool { return col.hasID("f1") })
	if !col.snapshot()[0].Replayed {
		t.Error("the recovered fill must come from replay, not a stale live emit")
	}
}

// A live fill from a worker predating the createdAt enrichment is still
// delivered, but must NOT advance the cursor — the next replay re-covers its
// position (duplicates over loss).
func TestWatchRealmFills_LiveFillWithoutCreatedAtKeepsCursor(t *testing.T) {
	s := newRealmFillsServer(t)
	s.addFill(mkFill("f1", ts1, "obj-a"))
	a := newRealmFillsArca(t, s)

	stream, col, c := openRealmFillStream(t, s, a, ts1+"|f1")
	waitFor(t, "initial replay to settle", func() bool { return stream.Cursor() == ts1+"|f1" })
	s.waitQuiescent(t)

	c.send(fillRecordedFrame("f2", "", "obj-a", 1))
	waitFor(t, "legacy fill delivery", func() bool { return col.hasID("f2") })

	if got := stream.Cursor(); got != ts1+"|f1" {
		t.Errorf("cursor = %q after a createdAt-less fill, want unchanged %q", got, ts1+"|f1")
	}
}
