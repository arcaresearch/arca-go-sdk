package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Windowed projection figures and the projection stream's recovery paths:
// re-marking moves Equity, UnrealizedValue and Pnl by the position P&L change
// only (unsettled funding and the baseline stay as delivered, Volume never
// moves), rows decode one by one, a replaced account at the same path
// survives its frame, a delivery gap or server resync re-reads the scope
// under the same watch, and a failed re-create is retried.

const projFrom = "2026-10-01T00:00:00Z"

func decodeRow(t *testing.T, raw string) ProjectedValuation {
	t.Helper()
	var v ProjectedValuation
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	return v
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// ---- Re-marking ----

func TestRevalueProjectedValuation_KeepsUnsettledFunding(t *testing.T) {
	// equity 1047.5 = realized 1000 + position P&L 50 - unsettled funding 2.5
	v := decodeRow(t, `{"objectId":"obj_a","path":"/users/alice","type":"exchange",
		"equity":"1047.5","realizedValue":"1000","unrealizedValue":"50",
		"positions":[{"market":"hl:0:BTC","side":"long","size":"1","entryPrice":"100",
		              "markPrice":"150","unrealizedPnl":"50","valueUsd":"50"}]}`)
	out := RevalueProjectedValuation(v, map[string]string{"hl:0:BTC": "160"})
	if deref(out.Equity) != "1057.5" {
		t.Fatalf("equity = %s, want 1057.5: funding owed stays owed when the mark moves", deref(out.Equity))
	}
	if deref(out.UnrealizedValue) != "60" || deref(out.RealizedValue) != "1000" {
		t.Fatalf("unrealized %s realized %s", deref(out.UnrealizedValue), deref(out.RealizedValue))
	}
}

func TestRevalueProjectedValuation_ShiftsWindowedPnlButNeverVolume(t *testing.T) {
	v := decodeRow(t, `{"objectId":"obj_a","path":"/users/alice","type":"exchange","equity":"1000",
		"positions":[{"market":"hl:0:ETH","side":"long","size":"2","entryPrice":"100",
		              "markPrice":"100","unrealizedPnl":"0","valueUsd":"0"}],
		"pnl":{"window":"30d","from":"`+projFrom+`","status":"complete","pnlUsd":"12.5"},
		"volume":{"window":"30d","from":"`+projFrom+`","status":"complete","volumeUsd":"3000"}}`)
	out := RevalueProjectedValuation(v, map[string]string{"hl:0:ETH": "110"})
	if deref(out.Pnl.PnlUsd) != "32.5" || out.Pnl.From != projFrom {
		t.Fatalf("pnl = %s from %s", deref(out.Pnl.PnlUsd), out.Pnl.From)
	}
	if deref(out.Volume.VolumeUsd) != "3000" {
		t.Fatalf("volume = %s: a price move is not volume", deref(out.Volume.VolumeUsd))
	}
	if deref(out.Equity) != "1020" {
		t.Fatalf("equity = %s", deref(out.Equity))
	}
	if deref(v.Pnl.PnlUsd) != "12.5" {
		t.Fatal("re-marking must not modify the delivered row")
	}
}

func TestRevalueProjectedValuation_IsExactDecimal(t *testing.T) {
	v := decodeRow(t, `{"objectId":"obj_a","path":"/users/alice","type":"exchange","equity":"100.1","unrealizedValue":"0",
		"positions":[{"market":"hl:0:XRP","side":"long","size":"3","entryPrice":"0.1",
		              "markPrice":"0.1","unrealizedPnl":"0","valueUsd":"0"}]}`)
	out := RevalueProjectedValuation(v, map[string]string{"hl:0:XRP": "0.3"})
	if deref(out.UnrealizedValue) != "0.6" || deref(out.Equity) != "100.7" {
		t.Fatalf("unrealized %s equity %s", deref(out.UnrealizedValue), deref(out.Equity))
	}
}

func TestRevalueProjectedValuation_PendingPnlIsNotGivenANumber(t *testing.T) {
	v := decodeRow(t, `{"objectId":"obj_a","path":"/users/alice","type":"exchange","equity":"1000",
		"positions":[{"market":"hl:0:BTC","side":"short","size":"1","entryPrice":"100",
		              "markPrice":"100","unrealizedPnl":"0","valueUsd":"0"}],
		"pnl":{"window":"30d","from":"`+projFrom+`","status":"pending"},
		"volume":{"window":"30d","from":"`+projFrom+`","status":"pending"}}`)
	out := RevalueProjectedValuation(v, map[string]string{"hl:0:BTC": "90"})
	if out.Pnl.PnlUsd != nil || out.Pnl.Status != WindowedPending {
		t.Fatalf("pending pnl must not read as a number: %+v", out.Pnl)
	}
	if out.Volume.VolumeUsd != nil {
		t.Fatal("pending volume must stay absent")
	}
	if deref(out.Equity) != "1010" {
		t.Fatalf("short 1 from 100 to 90: equity = %s, want 1010", deref(out.Equity))
	}
}

func TestRevalueProjectedValuation_ServerPricedRowIsNotRemarked(t *testing.T) {
	v := decodeRow(t, `{"objectId":"obj_a","path":"/users/alice","type":"exchange","equity":"1000","pricingMode":"server",
		"positions":[{"market":"hl:0:BTC","side":"long","size":"1","entryPrice":"100",
		              "markPrice":"100","unrealizedPnl":"0","valueUsd":"0"}]}`)
	out := RevalueProjectedValuation(v, map[string]string{"hl:0:BTC": "200"})
	if deref(out.Equity) != "1000" {
		t.Fatalf("equity = %s", deref(out.Equity))
	}
}

// ---- Decoding ----

func TestDecodeProjectedValuations_SkipsAnUnreadableRow(t *testing.T) {
	var page ProjectionValuationsResponse
	if err := json.Unmarshal([]byte(`{"projection":"board","fields":["equity"],"window":"30d","valuations":[
		{"objectId":"obj_a","path":"/users/alice","type":"exchange","equity":"1"},
		{"objectId":"obj_b","type":"exchange","equity":"2"},
		{"objectId":"obj_c","path":"/users/carol","type":"exchange","equity":3},
		{"objectId":"obj_d","path":"/users/dan","type":"exchange","equity":"4",
		 "pnl":{"window":"30d"},
		 "volume":{"window":"30d","from":"`+projFrom+`","status":"complete","volumeUsd":"5"}}]}`), &page); err != nil {
		t.Fatal(err)
	}
	rows := page.Rows()
	if len(rows) != 2 || rows[0].Path != "/users/alice" || rows[1].Path != "/users/dan" {
		t.Fatalf("rows = %+v: unreadable rows are skipped, not the page", rows)
	}
	if rows[1].Pnl != nil || deref(rows[1].Volume.VolumeUsd) != "5" {
		t.Fatal("an unreadable figure drops only that figure")
	}
	if page.Window != ProjectionWindow30d || len(page.Valuations) != 4 {
		t.Fatal("the raw rows and window stay available")
	}
}

func TestGetProjectionValuations_SendsTheWindow(t *testing.T) {
	var gotWindow string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWindow = r.URL.Query().Get("window")
		writeEnvelope(w, http.StatusOK, json.RawMessage(`{"projection":"board","fields":["pnl"],"window":"30d","valuations":[
			{"objectId":"obj_a","path":"/users/alice","type":"exchange",
			 "pnl":{"window":"30d","from":"`+projFrom+`","status":"complete","pnlUsd":"0.32"}}]}`))
	}))
	defer srv.Close()
	a := newTestArca(t, srv.URL)
	page, err := a.GetProjectionValuations(context.Background(), "board", ProjectionValuationsOptions{Paths: []string{"/users/alice"}, Window: ProjectionWindow30d})
	if err != nil {
		t.Fatal(err)
	}
	if gotWindow != "30d" {
		t.Fatalf("window query = %q", gotWindow)
	}
	if rows := page.Rows(); len(rows) != 1 || deref(rows[0].Pnl.PnlUsd) != "0.32" {
		t.Fatalf("rows = %+v", rows)
	}
}

// ---- Stream ----

func projRow(path, objectID, pnl, volume string) map[string]any {
	return map[string]any{
		"objectId": objectID, "path": path, "type": "exchange", "equity": "1000", "positions": []any{},
		"pnl":    map[string]any{"window": "30d", "from": projFrom, "status": "complete", "pnlUsd": pnl},
		"volume": map[string]any{"window": "30d", "from": projFrom, "status": "complete", "volumeUsd": volume},
	}
}

func answerProjection(c *wsTestConn, action, replyType, watchID string, rows ...map[string]any) map[string]any {
	req := c.waitFor(action)
	c.send(map[string]any{
		"type": replyType, "requestId": req["requestId"], "watchId": watchID, "projection": "board",
		"fields": []string{"equity", "positions", "pnl", "volume"}, "window": "30d", "valuations": rows,
	})
	return req
}

func openProjectionWatch(t *testing.T, s *wsTestServer, a *Arca) (*ProjectionWatchStream, *wsTestConn, map[string]any) {
	t.Helper()
	type result struct {
		s   *ProjectionWatchStream
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := a.WatchProjection(context.Background(), "board", WatchProjectionOptions{Paths: []string{"/users/alice"}, Window: ProjectionWindow30d})
		done <- result{st, err}
	}()
	c := s.accept()
	c.handshake(0)
	req := answerProjection(c, "watch_projection", "projection_watch_created", "w1", projRow("/users/alice", "obj_a", "1", "10"))
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("WatchProjection: %v", r.err)
		}
		t.Cleanup(r.s.Close)
		return r.s, c, req
	case <-time.After(wsTestWait):
		t.Fatal("WatchProjection never returned")
		return nil, nil, nil
	}
}

func rowOf(s *ProjectionWatchStream, path string) (ProjectedValuation, bool) {
	v, ok := s.Value()
	if !ok {
		return ProjectedValuation{}, false
	}
	r, ok := v[path]
	return r, ok
}

func TestWatchProjection_SendsTheWindowAndRowsCarryFigures(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)
	st, _, req := openProjectionWatch(t, s, a)
	if req["window"] != "30d" {
		t.Fatalf("watch_projection window = %v", req["window"])
	}
	if st.Window() != ProjectionWindow30d || st.WatchID() != "w1" {
		t.Fatalf("window %q watch %q", st.Window(), st.WatchID())
	}
	row, ok := rowOf(st, "/users/alice")
	if !ok || deref(row.Pnl.PnlUsd) != "1" || deref(row.Volume.VolumeUsd) != "10" {
		t.Fatalf("row = %+v", row)
	}
	if st.IsStale() {
		t.Fatal("a fresh stream is not stale")
	}
}

func TestWatchProjection_ReplacedAccountAtTheSamePathIsKept(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)
	st, c, _ := openProjectionWatch(t, s, a)
	c.send(map[string]any{
		"type": "object.valuation", "watchId": "w1", "projection": "board", "deliverySeq": 1,
		"removed":    []string{"/users/alice"},
		"valuations": map[string]any{"/users/alice": projRow("/users/alice", "obj_new", "0", "0")},
	})
	waitFor(t, "the replacement row", func() bool {
		r, ok := rowOf(st, "/users/alice")
		return ok && r.ObjectID == "obj_new"
	})
}

func TestWatchProjection_DeliveryGapReReadsUnderTheSameWatch(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)
	st, c, _ := openProjectionWatch(t, s, a)

	frame := func(seq int, pnl string) map[string]any {
		return map[string]any{
			"type": "object.valuation", "watchId": "w1", "projection": "board", "deliverySeq": seq,
			"valuations": map[string]any{"/users/alice": projRow("/users/alice", "obj_a", pnl, "10")},
		}
	}
	c.send(frame(1, "2"))
	waitFor(t, "the first frame", func() bool {
		r, _ := rowOf(st, "/users/alice")
		return deref(r.Pnl.PnlUsd) == "2"
	})
	c.send(frame(4, "3"))

	req := answerProjection(c, "update_projection_watch", "projection_watch_updated", "w1", projRow("/users/alice", "obj_a", "7", "70"))
	if req["watchId"] != "w1" {
		t.Fatalf("a gap re-reads under the same watch, got %v", req["watchId"])
	}
	waitFor(t, "the re-read snapshot", func() bool {
		r, _ := rowOf(st, "/users/alice")
		return deref(r.Pnl.PnlUsd) == "7" && !st.IsStale()
	})
	c.expectNoAction("watch_projection", 100*time.Millisecond)
}

func TestWatchProjection_ServerResyncReReadsTheScope(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)
	st, c, _ := openProjectionWatch(t, s, a)

	c.send(map[string]any{"type": "stream.resync", "deliverySeq": 1})
	answerProjection(c, "update_projection_watch", "projection_watch_updated", "w1", projRow("/users/alice", "obj_a", "9", "90"))
	waitFor(t, "the re-read snapshot", func() bool {
		r, _ := rowOf(st, "/users/alice")
		return deref(r.Volume.VolumeUsd) == "90"
	})
}

func TestWatchProjection_FailedRecreateIsRetriedWithTheLastRowsKept(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)
	st, c, _ := openProjectionWatch(t, s, a)

	c.close()
	next := s.accept()
	next.handshake(0)
	first := next.waitFor("watch_projection")
	if first["window"] != "30d" {
		t.Fatalf("the re-create keeps the window, got %v", first["window"])
	}
	next.send(map[string]any{"type": "error", "requestId": first["requestId"], "message": "busy"})

	waitFor(t, "the stream to read as stale", st.IsStale)
	if r, _ := rowOf(st, "/users/alice"); deref(r.Pnl.PnlUsd) != "1" {
		t.Fatalf("the last rows stay while retrying, got %s", deref(r.Pnl.PnlUsd))
	}

	answerProjection(next, "watch_projection", "projection_watch_created", "w2", projRow("/users/alice", "obj_a", "5", "50"))
	waitFor(t, "the retried re-create", func() bool {
		r, _ := rowOf(st, "/users/alice")
		return st.WatchID() == "w2" && deref(r.Pnl.PnlUsd) == "5" && !st.IsStale()
	})
}

func TestWatchProjection_SetPathsKeepsTheWatch(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)
	st, c, _ := openProjectionWatch(t, s, a)

	errCh := make(chan error, 1)
	go func() { errCh <- st.SetPaths(context.Background(), []string{"/users/carol"}) }()
	req := answerProjection(c, "update_projection_watch", "projection_watch_updated", "w1", projRow("/users/carol", "obj_c", "3", "30"))
	if req["watchId"] != "w1" || req["window"] != nil {
		t.Fatalf("update = %v: same watch, window kept server-side", req)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new scope's rows", func() bool {
		v, _ := st.Value()
		_, carol := v["/users/carol"]
		_, alice := v["/users/alice"]
		return carol && !alice
	})
	if paths, _ := st.Scope(); len(paths) != 1 || paths[0] != "/users/carol" {
		t.Fatalf("scope = %v", paths)
	}
}

func TestWatchAggregation_SendsFlowsSinceOnCreateAndRecreate(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)
	done := make(chan *AggregationWatchStream, 1)
	go func() {
		st, err := a.WatchAggregation(context.Background(), []AggregationSource{NewPrefixSource("/users")},
			WatchAggregationOptions{FlowsSince: projFrom})
		if err != nil {
			t.Errorf("WatchAggregation: %v", err)
		}
		done <- st
	}()
	c := s.accept()
	c.handshake(0)
	req := c.waitFor("create_aggregation_watch")
	if req["flowsSince"] != projFrom {
		t.Fatalf("flowsSince = %v", req["flowsSince"])
	}
	c.send(map[string]any{
		"type": "aggregation_watch_created", "requestId": req["requestId"], "watchId": "watch-1",
		"aggregation": map[string]any{"prefix": "/", "totalEquityUsd": "0", "departingUsd": "0", "breakdown": []any{}},
	})
	st := <-done
	if st == nil {
		return
	}
	t.Cleanup(st.Close)

	c.close()
	next := s.accept()
	next.handshake(0)
	if again := next.waitFor("create_aggregation_watch"); again["flowsSince"] != projFrom {
		t.Fatalf("re-create flowsSince = %v", again["flowsSince"])
	}
}
