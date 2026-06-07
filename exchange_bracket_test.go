package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// bracketMockState records the single batch POST openWithBracket issues so the
// test can assert on the exact body shape.
type bracketMockState struct {
	mu    sync.Mutex
	posts []map[string]any
}

// newBracketTestServer answers the atomic batch endpoint with a bracket
// operation whose outcome lists one order summary per leg (each with its own
// orderId), mirroring what PlaceExchangeOrderBatch returns.
func newBracketTestServer(m *bracketMockState) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exchange/orders/batch") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.mu.Lock()
			m.posts = append(m.posts, body)
			m.mu.Unlock()
			outcome := `{"grouping":"normalTpsl","orders":[` +
				`{"orderId":"ord_entry"},` +
				`{"orderId":"ord_tp","tpsl":"tp"},` +
				`{"orderId":"ord_sl","tpsl":"sl"}]}`
			writeEnvelope(w, 200, OrderOperationResponse{
				Operation: Operation{ID: "op_bracket", State: OpCompleted, Outcome: &outcome},
			})
			return
		}
		writeEnvelope(w, 200, map[string]any{})
	}))
}

func legOrderID(t *testing.T, h *OrderHandle) string {
	t.Helper()
	resp, err := h.Submitted(context.Background())
	if err != nil {
		t.Fatalf("Submitted: %v", err)
	}
	if resp.Operation.Outcome == nil {
		t.Fatalf("leg outcome is nil")
	}
	var parsed struct {
		OrderID string `json:"orderId"`
	}
	if err := json.Unmarshal([]byte(*resp.Operation.Outcome), &parsed); err != nil {
		t.Fatalf("unmarshal leg outcome: %v", err)
	}
	return parsed.OrderID
}

// TestOpenWithBracket_OneCallThreeHandles pins the SDK's atomic-bracket
// contract: a single batch POST carrying [entry, tp, sl] under one grouping,
// and one OrderHandle per leg, each resolving to its OWN orderId even though
// all three share the single bracket operation.
func TestOpenWithBracket_OneCallThreeHandles(t *testing.T) {
	m := &bracketMockState{}
	srv := newBracketTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	res, err := a.OpenWithBracket(context.Background(), OpenBracketOptions{
		Path: "/op/bracket/1", ObjectID: "obj_1", Market: "hl:0:BTC",
		Side: Buy, Size: "0.01", TakeProfitPx: "72000", StopLossPx: "58000",
	})
	if err != nil {
		t.Fatalf("OpenWithBracket: %v", err)
	}

	if len(m.posts) != 1 {
		t.Fatalf("expected exactly 1 batch POST, got %d", len(m.posts))
	}
	body := m.posts[0]
	if body["grouping"] != "normalTpsl" {
		t.Errorf("grouping = %v, want normalTpsl", body["grouping"])
	}
	orders, _ := body["orders"].([]any)
	if len(orders) != 3 {
		t.Fatalf("expected 3 legs, got %d", len(orders))
	}

	entry, _ := orders[0].(map[string]any)
	if entry["side"] != "buy" || entry["orderType"] != "MARKET" || entry["size"] != "0.01" {
		t.Errorf("entry leg wrong: %+v", entry)
	}
	if _, ok := entry["reduceOnly"]; ok {
		t.Errorf("entry must not be reduceOnly: %+v", entry)
	}

	findLeg := func(tpsl string) map[string]any {
		for _, o := range orders {
			mo, _ := o.(map[string]any)
			if mo["tpsl"] == tpsl {
				return mo
			}
		}
		return nil
	}
	tp := findLeg("tp")
	if tp == nil || tp["side"] != "sell" || tp["reduceOnly"] != true || tp["sizeToMax"] != true ||
		tp["isTrigger"] != true || tp["triggerPx"] != "72000" {
		t.Errorf("tp leg wrong: %+v", tp)
	}
	sl := findLeg("sl")
	if sl == nil || sl["side"] != "sell" || sl["triggerPx"] != "58000" {
		t.Errorf("sl leg wrong: %+v", sl)
	}

	// Three handles, each resolving to its own leg's orderId.
	if res.Entry == nil || res.TakeProfit == nil || res.StopLoss == nil {
		t.Fatalf("expected all three handles, got entry=%v tp=%v sl=%v", res.Entry, res.TakeProfit, res.StopLoss)
	}
	if id := legOrderID(t, res.Entry); id != "ord_entry" {
		t.Errorf("entry handle orderId = %q, want ord_entry", id)
	}
	if id := legOrderID(t, res.TakeProfit); id != "ord_tp" {
		t.Errorf("take-profit handle orderId = %q, want ord_tp", id)
	}
	if id := legOrderID(t, res.StopLoss); id != "ord_sl" {
		t.Errorf("stop-loss handle orderId = %q, want ord_sl", id)
	}
}

// TestOpenWithBracket_OnlyStopLoss verifies a single-trigger bracket omits the
// TP leg and returns a nil TakeProfit handle.
func TestOpenWithBracket_OnlyStopLoss(t *testing.T) {
	m := &bracketMockState{}
	srv := newBracketTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	res, err := a.OpenWithBracket(context.Background(), OpenBracketOptions{
		Path: "/op/bracket/2", ObjectID: "obj_1", Market: "hl:0:BTC",
		Side: Buy, Size: "0.01", StopLossPx: "58000",
	})
	if err != nil {
		t.Fatalf("OpenWithBracket: %v", err)
	}
	orders, _ := m.posts[0]["orders"].([]any)
	if len(orders) != 2 {
		t.Fatalf("expected 2 legs (entry + sl), got %d", len(orders))
	}
	if res.TakeProfit != nil {
		t.Errorf("TakeProfit should be nil when no TakeProfitPx given")
	}
	if res.StopLoss == nil {
		t.Errorf("StopLoss handle should be present")
	}
}

// TestOpenWithBracket_SizedTakeProfit pins the sized-partial-leg contract: a
// non-empty TakeProfitSz makes the TP leg a sized reduce-only close (carries
// its base-unit Size, NO sizeToMax), while a leg with no size stays unsized
// (size "0" + sizeToMax true). This is what enables "scale out half" through
// the typed bracket surface.
func TestOpenWithBracket_SizedTakeProfit(t *testing.T) {
	m := &bracketMockState{}
	srv := newBracketTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	_, err := a.OpenWithBracket(context.Background(), OpenBracketOptions{
		Path: "/op/bracket/sized", ObjectID: "obj_1", Market: "hl:0:BTC",
		Side: Buy, Size: "0.02",
		TakeProfitPx: "72000", TakeProfitSz: "0.01", // sized: scale out half
		StopLossPx: "58000", // unsized: protect the whole position
	})
	if err != nil {
		t.Fatalf("OpenWithBracket: %v", err)
	}
	orders, _ := m.posts[0]["orders"].([]any)
	findLeg := func(tpsl string) map[string]any {
		for _, o := range orders {
			mo, _ := o.(map[string]any)
			if mo["tpsl"] == tpsl {
				return mo
			}
		}
		return nil
	}
	tp := findLeg("tp")
	if tp["size"] != "0.01" {
		t.Errorf("sized TP size = %v, want 0.01", tp["size"])
	}
	if _, ok := tp["sizeToMax"]; ok {
		t.Errorf("sized TP must NOT carry sizeToMax, got %+v", tp)
	}
	if tp["reduceOnly"] != true {
		t.Errorf("sized TP reduceOnly = %v, want true", tp["reduceOnly"])
	}
	sl := findLeg("sl")
	if sl["size"] != "0" || sl["sizeToMax"] != true {
		t.Errorf("unsized SL wrong: size=%v sizeToMax=%v", sl["size"], sl["sizeToMax"])
	}
}

// TestOpenWithBracket_RequiresATrigger rejects a bracket with neither TP nor SL
// before any network call.
func TestOpenWithBracket_RequiresATrigger(t *testing.T) {
	m := &bracketMockState{}
	srv := newBracketTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	_, err := a.OpenWithBracket(context.Background(), OpenBracketOptions{
		Path: "/op/bracket/3", ObjectID: "obj_1", Market: "hl:0:BTC", Side: Buy, Size: "0.01",
	})
	if err == nil {
		t.Fatalf("expected a validation error with no TP/SL")
	}
	if len(m.posts) != 0 {
		t.Errorf("no network call expected, got %d POSTs", len(m.posts))
	}
}
