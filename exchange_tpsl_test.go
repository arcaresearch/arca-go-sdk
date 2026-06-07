package arca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// tpslMockState backs newTpslTestServer; it records the order POSTs and order
// cancellations the TP/SL helpers issue so tests can assert on the exact bodies.
type tpslMockState struct {
	mu          sync.Mutex
	positions   []SimPosition
	meta        []Market
	openTrigger []SimOrder
	posts       []map[string]any
	deletes     []string
}

func newTpslTestServer(m *tpslMockState) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/exchange/positions"):
			writeEnvelope(w, 200, PositionListResponse{Positions: m.positions, Total: len(m.positions)})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/exchange/market/meta"):
			writeEnvelope(w, 200, SimMetaResponse{Universe: m.meta})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/exchange/orders"):
			writeEnvelope(w, 200, OrderListResponse{Orders: m.openTrigger, Total: len(m.openTrigger)})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/exchange/orders/"):
			parts := strings.Split(r.URL.Path, "/exchange/orders/")
			id := parts[len(parts)-1]
			m.mu.Lock()
			m.deletes = append(m.deletes, id)
			m.mu.Unlock()
			writeEnvelope(w, 200, OrderOperationResponse{Operation: Operation{ID: "op_cancel", State: OpCompleted}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exchange/orders"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.mu.Lock()
			m.posts = append(m.posts, body)
			m.mu.Unlock()
			writeEnvelope(w, 200, OrderOperationResponse{Operation: Operation{ID: "op_place", State: OpCompleted}})
		default:
			writeEnvelope(w, 200, map[string]any{})
		}
	}))
}

func tpBoolPtr(b bool) *bool { return &b }

func longBTC() SimPosition {
	return SimPosition{ID: "pos_1", Market: "hl:0:BTC", Side: Long, Size: "0.5", Leverage: 5}
}

func TestSetStopLoss_LongPlacesSellUnsized(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetStopLoss(context.Background(), SetPositionTriggerOptions{
		Path: "/op/sl/1", ObjectID: "obj_1", Market: "hl:0:BTC",
		TriggerPx: "55000", Isolated: tpBoolPtr(false),
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("SetStopLoss: %v", err)
	}
	if len(m.posts) != 1 {
		t.Fatalf("expected 1 order POST, got %d", len(m.posts))
	}
	b := m.posts[0]
	if b["side"] != "sell" {
		t.Errorf("side = %v, want sell (close of long)", b["side"])
	}
	if b["tpsl"] != "sl" {
		t.Errorf("tpsl = %v, want sl", b["tpsl"])
	}
	if b["sizeToMax"] != true {
		t.Errorf("sizeToMax = %v, want true", b["sizeToMax"])
	}
	if b["reduceOnly"] != true {
		t.Errorf("reduceOnly = %v, want true", b["reduceOnly"])
	}
	if b["size"] != "0" {
		t.Errorf("size = %v, want \"0\" (unsized: closes whole position)", b["size"])
	}
	if b["isTrigger"] != true {
		t.Errorf("isTrigger = %v, want true", b["isTrigger"])
	}
	if b["isMarket"] != true {
		t.Errorf("isMarket = %v, want true (default)", b["isMarket"])
	}
	if b["orderType"] != "MARKET" {
		t.Errorf("orderType = %v, want MARKET", b["orderType"])
	}
	if b["triggerPx"] != "55000" {
		t.Errorf("triggerPx = %v", b["triggerPx"])
	}
	if lev, _ := b["leverage"].(float64); lev != 5 {
		t.Errorf("leverage = %v, want 5 (from position)", b["leverage"])
	}
	if len(m.deletes) != 0 {
		t.Errorf("no existing triggers, expected 0 cancels, got %d", len(m.deletes))
	}
}

// TestSetTakeProfit_SizedPartial pins the sized position-trigger path: a
// non-empty Size makes the leg a sized reduce-only close (carries Size, NO
// sizeToMax) so a builder can scale out a fixed quantity of a position.
func TestSetTakeProfit_SizedPartial(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetTakeProfit(context.Background(), SetPositionTriggerOptions{
		Path: "/op/tp/sized", ObjectID: "obj_1", Market: "hl:0:BTC",
		TriggerPx: "70000", Size: "0.25", Isolated: tpBoolPtr(false),
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("SetTakeProfit: %v", err)
	}
	b := m.posts[0]
	if b["size"] != "0.25" {
		t.Errorf("size = %v, want 0.25 (sized partial)", b["size"])
	}
	if _, ok := b["sizeToMax"]; ok {
		t.Errorf("sized trigger must NOT carry sizeToMax, got %+v", b)
	}
	if b["reduceOnly"] != true {
		t.Errorf("reduceOnly = %v, want true", b["reduceOnly"])
	}
}

func TestSetTakeProfit_ShortPlacesBuyUnsized(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{{ID: "pos_2", Market: "hl:0:ETH", Side: Short, Size: "2", Leverage: 3}}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetTakeProfit(context.Background(), SetPositionTriggerOptions{
		Path: "/op/tp/1", ObjectID: "obj_1", Market: "hl:0:ETH",
		TriggerPx: "2000", Isolated: tpBoolPtr(false),
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("SetTakeProfit: %v", err)
	}
	b := m.posts[0]
	if b["side"] != "buy" {
		t.Errorf("side = %v, want buy (close of short)", b["side"])
	}
	if b["tpsl"] != "tp" {
		t.Errorf("tpsl = %v, want tp", b["tpsl"])
	}
}

func TestSetStopLoss_NoPositionReturnsNotFound(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetStopLoss(context.Background(), SetPositionTriggerOptions{
		Path: "/op/sl/2", ObjectID: "obj_1", Market: "hl:0:BTC", TriggerPx: "55000", Isolated: tpBoolPtr(false),
	})
	_, err := h.Submitted(context.Background())
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
	if len(m.posts) != 0 {
		t.Errorf("no position: expected 0 POSTs, got %d", len(m.posts))
	}
}

func TestSetStopLoss_ReplaceCancelsExisting(t *testing.T) {
	m := &tpslMockState{
		positions: []SimPosition{longBTC()},
		openTrigger: []SimOrder{
			{ID: "ord_old_sl", Market: "hl:0:BTC", Tpsl: "sl", SizeToMax: true, Status: OrderWaitingTrigger},
		},
	}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetStopLoss(context.Background(), SetPositionTriggerOptions{
		Path: "/op/sl/3", ObjectID: "obj_1", Market: "hl:0:BTC", TriggerPx: "54000", Isolated: tpBoolPtr(false),
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("SetStopLoss: %v", err)
	}
	if len(m.deletes) != 1 || m.deletes[0] != "ord_old_sl" {
		t.Errorf("expected cancel of ord_old_sl, got %v", m.deletes)
	}
	if len(m.posts) != 1 {
		t.Errorf("expected 1 replacement POST, got %d", len(m.posts))
	}
}

func TestSetStopLoss_NoReplaceSkipsCancel(t *testing.T) {
	m := &tpslMockState{
		positions: []SimPosition{longBTC()},
		openTrigger: []SimOrder{
			{ID: "ord_old_sl", Market: "hl:0:BTC", Tpsl: "sl", SizeToMax: true, Status: OrderWaitingTrigger},
		},
	}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetStopLoss(context.Background(), SetPositionTriggerOptions{
		Path: "/op/sl/4", ObjectID: "obj_1", Market: "hl:0:BTC", TriggerPx: "54000",
		Replace: tpBoolPtr(false), Isolated: tpBoolPtr(false),
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("SetStopLoss: %v", err)
	}
	if len(m.deletes) != 0 {
		t.Errorf("Replace=false: expected 0 cancels, got %v", m.deletes)
	}
	if len(m.posts) != 1 {
		t.Errorf("expected 1 POST, got %d", len(m.posts))
	}
}

func TestSetStopLoss_TriggerLimitRequiresLimitPrice(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetStopLoss(context.Background(), SetPositionTriggerOptions{
		Path: "/op/sl/5", ObjectID: "obj_1", Market: "hl:0:BTC", TriggerPx: "54000",
		IsMarket: tpBoolPtr(false), // limit trigger but no LimitPrice
	})
	_, err := h.Submitted(context.Background())
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(m.posts) != 0 {
		t.Errorf("validation should short-circuit before POST, got %d", len(m.posts))
	}
}

func TestSetStopLoss_TriggerLimitUsesLimitPrice(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetStopLoss(context.Background(), SetPositionTriggerOptions{
		Path: "/op/sl/6", ObjectID: "obj_1", Market: "hl:0:BTC", TriggerPx: "54000",
		IsMarket: tpBoolPtr(false), LimitPrice: "53900", Isolated: tpBoolPtr(false),
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("SetStopLoss: %v", err)
	}
	b := m.posts[0]
	if b["orderType"] != "LIMIT" {
		t.Errorf("orderType = %v, want LIMIT", b["orderType"])
	}
	if b["price"] != "53900" {
		t.Errorf("price = %v, want 53900", b["price"])
	}
	if b["isMarket"] != false {
		t.Errorf("isMarket = %v, want false", b["isMarket"])
	}
}

func TestSetStopLoss_InfersIsolatedFromMeta(t *testing.T) {
	m := &tpslMockState{
		positions: []SimPosition{{ID: "pos_cl", Market: "hl:1:CL", Side: Long, Size: "1", Leverage: 2}},
		meta:      []Market{{Name: "hl:1:CL", OnlyIsolated: true}},
	}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.SetStopLoss(context.Background(), SetPositionTriggerOptions{
		Path: "/op/sl/7", ObjectID: "obj_1", Market: "hl:1:CL", TriggerPx: "60",
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("SetStopLoss: %v", err)
	}
	if m.posts[0]["isolated"] != true {
		t.Errorf("isolated = %v, want true (onlyIsolated market)", m.posts[0]["isolated"])
	}
}

func TestSetPositionTpsl_PlacesBothLegs(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	res, err := a.SetPositionTpsl(context.Background(), SetPositionTpslOptions{
		Path: "/op/tpsl/1", ObjectID: "obj_1", Market: "hl:0:BTC",
		StopLossPx: "54000", TakeProfitPx: "70000",
	})
	if err != nil {
		t.Fatalf("SetPositionTpsl: %v", err)
	}
	if res.StopLoss == nil || res.TakeProfit == nil {
		t.Fatalf("expected both leg handles, got sl=%v tp=%v", res.StopLoss, res.TakeProfit)
	}
	if len(m.posts) != 2 {
		t.Fatalf("expected 2 POSTs, got %d", len(m.posts))
	}
	if m.posts[0]["tpsl"] != "sl" || m.posts[0]["path"] != "/op/tpsl/1/sl" {
		t.Errorf("first leg = %+v, want sl @ /op/tpsl/1/sl", m.posts[0])
	}
	if m.posts[1]["tpsl"] != "tp" || m.posts[1]["path"] != "/op/tpsl/1/tp" {
		t.Errorf("second leg = %+v, want tp @ /op/tpsl/1/tp", m.posts[1])
	}
}

// TestSetPositionTpsl_SharesOcoGroupID pins the true one-cancels-the-other
// linkage: SetPositionTpsl must stamp BOTH legs with the same non-empty
// ocoGroupId so a fill (even partial) on one leg cancels the sibling. Without a
// shared id the bracket falls back to position-state reconcile only.
func TestSetPositionTpsl_SharesOcoGroupID(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	if _, err := a.SetPositionTpsl(context.Background(), SetPositionTpslOptions{
		Path: "/op/tpsl/oco", ObjectID: "obj_1", Market: "hl:0:BTC",
		StopLossPx: "54000", TakeProfitPx: "70000",
	}); err != nil {
		t.Fatalf("SetPositionTpsl: %v", err)
	}
	if len(m.posts) != 2 {
		t.Fatalf("expected 2 POSTs, got %d", len(m.posts))
	}
	slGroup, _ := m.posts[0]["ocoGroupId"].(string)
	tpGroup, _ := m.posts[1]["ocoGroupId"].(string)
	if slGroup == "" {
		t.Errorf("SL leg missing ocoGroupId: %+v", m.posts[0])
	}
	if slGroup != tpGroup {
		t.Errorf("legs must share one ocoGroupId: sl=%q tp=%q", slGroup, tpGroup)
	}
}

// TestSetPositionTpsl_ExplicitOcoGroupID pins that an explicit OcoGroupID
// overrides the auto-minted one and is applied to both legs verbatim.
func TestSetPositionTpsl_ExplicitOcoGroupID(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	if _, err := a.SetPositionTpsl(context.Background(), SetPositionTpslOptions{
		Path: "/op/tpsl/oco2", ObjectID: "obj_1", Market: "hl:0:BTC",
		StopLossPx: "54000", TakeProfitPx: "70000", OcoGroupID: "oco_explicit",
	}); err != nil {
		t.Fatalf("SetPositionTpsl: %v", err)
	}
	if m.posts[0]["ocoGroupId"] != "oco_explicit" || m.posts[1]["ocoGroupId"] != "oco_explicit" {
		t.Errorf("explicit group id not applied to both legs: sl=%v tp=%v",
			m.posts[0]["ocoGroupId"], m.posts[1]["ocoGroupId"])
	}
}

// TestSetPositionTpsl_SizedSkipsAutoOco pins the OCO footgun guard: when either
// leg is sized, SetPositionTpsl must NOT auto-link the legs with a shared
// ocoGroupId — otherwise a partial fill of the sized TP would cancel the SL
// protecting the remainder ("scale out half, keep the stop"). The sizes are
// still threaded to each leg.
func TestSetPositionTpsl_SizedSkipsAutoOco(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	if _, err := a.SetPositionTpsl(context.Background(), SetPositionTpslOptions{
		Path: "/op/tpsl/sized", ObjectID: "obj_1", Market: "hl:0:BTC",
		StopLossPx: "54000", TakeProfitPx: "70000",
		TakeProfitSz: "0.25", // scale out a quarter at the target
	}); err != nil {
		t.Fatalf("SetPositionTpsl: %v", err)
	}
	if len(m.posts) != 2 {
		t.Fatalf("expected 2 POSTs, got %d", len(m.posts))
	}
	for _, p := range m.posts {
		if _, ok := p["ocoGroupId"]; ok {
			t.Errorf("sized legs must NOT be auto-OCO-linked, got %+v", p)
		}
	}
	// SL leg unsized (whole position), TP leg sized (the scale-out quarter).
	sl, tp := m.posts[0], m.posts[1]
	if sl["size"] != "0" || sl["sizeToMax"] != true {
		t.Errorf("SL should stay unsized: %+v", sl)
	}
	if tp["size"] != "0.25" {
		t.Errorf("TP size = %v, want 0.25", tp["size"])
	}
	if _, ok := tp["sizeToMax"]; ok {
		t.Errorf("sized TP must NOT carry sizeToMax: %+v", tp)
	}
}

// TestSetPositionTpsl_SizedRespectsExplicitOco pins that an explicit OcoGroupID
// still force-links sized legs (the caller deliberately opts in), overriding the
// auto-skip in TestSetPositionTpsl_SizedSkipsAutoOco.
func TestSetPositionTpsl_SizedRespectsExplicitOco(t *testing.T) {
	m := &tpslMockState{positions: []SimPosition{longBTC()}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	if _, err := a.SetPositionTpsl(context.Background(), SetPositionTpslOptions{
		Path: "/op/tpsl/sizedoco", ObjectID: "obj_1", Market: "hl:0:BTC",
		StopLossPx: "54000", TakeProfitPx: "70000",
		TakeProfitSz: "0.25", StopLossSz: "0.25", OcoGroupID: "oco_forced",
	}); err != nil {
		t.Fatalf("SetPositionTpsl: %v", err)
	}
	if m.posts[0]["ocoGroupId"] != "oco_forced" || m.posts[1]["ocoGroupId"] != "oco_forced" {
		t.Errorf("explicit group id should link sized legs: sl=%v tp=%v",
			m.posts[0]["ocoGroupId"], m.posts[1]["ocoGroupId"])
	}
}

// TestPlaceOrder_ForwardsOcoGroupID pins the advisory passthrough on the
// general order path: an OcoGroupID on PlaceOrderOptions reaches the request
// body (it is forwarded to the venue but never enters the signed digest).
func TestPlaceOrder_ForwardsOcoGroupID(t *testing.T) {
	m := &tpslMockState{}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	h := a.PlaceOrder(context.Background(), PlaceOrderOptions{
		Path: "/op/place/oco", ObjectID: "obj_1", Market: "hl:0:BTC",
		Side: Sell, OrderType: "MARKET", Size: "0", OcoGroupID: "oco_grp_99",
	})
	if _, err := h.Submitted(context.Background()); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if len(m.posts) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(m.posts))
	}
	if m.posts[0]["ocoGroupId"] != "oco_grp_99" {
		t.Errorf("ocoGroupId = %v, want oco_grp_99", m.posts[0]["ocoGroupId"])
	}
}

// TestSimOrder_DecodesOcoAndCancelReason pins the read-on-demand surface: a
// CANCELLED order returned by the venue exposes its ocoGroupId and cancelReason
// through getOrder/listOrders.
func TestSimOrder_DecodesOcoAndCancelReason(t *testing.T) {
	var o SimOrder
	if err := json.Unmarshal([]byte(`{
		"id":"ord_1","status":"CANCELLED",
		"ocoGroupId":"oco_abc","cancelReason":"sibling_filled"
	}`), &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if o.OcoGroupID != "oco_abc" {
		t.Errorf("OcoGroupID = %q, want oco_abc", o.OcoGroupID)
	}
	if o.CancelReason != "sibling_filled" {
		t.Errorf("CancelReason = %q, want sibling_filled", o.CancelReason)
	}
}

func TestSetPositionTpsl_RequiresOnePrice(t *testing.T) {
	a := newTestArca(t, "http://127.0.0.1:0")
	_, err := a.SetPositionTpsl(context.Background(), SetPositionTpslOptions{
		Path: "/op/tpsl/2", ObjectID: "obj_1", Market: "hl:0:BTC",
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError when no prices given, got %v", err)
	}
}

func TestClearPositionTpsl_CancelsBothLegs(t *testing.T) {
	m := &tpslMockState{openTrigger: []SimOrder{
		{ID: "ord_sl", Market: "hl:0:BTC", Tpsl: "sl", SizeToMax: true, Status: OrderWaitingTrigger},
		{ID: "ord_tp", Market: "hl:0:BTC", Tpsl: "tp", SizeToMax: true, Status: OrderWaitingTrigger},
		{ID: "ord_other", Market: "hl:0:BTC", Tpsl: "sl", SizeToMax: false, Status: OrderWaitingTrigger},
		{ID: "ord_eth", Market: "hl:0:ETH", Tpsl: "sl", SizeToMax: true, Status: OrderWaitingTrigger},
	}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	cleared, err := a.ClearPositionTpsl(context.Background(), ClearPositionTpslOptions{
		Path: "/op/clear/1", ObjectID: "obj_1", Market: "hl:0:BTC",
	})
	if err != nil {
		t.Fatalf("ClearPositionTpsl: %v", err)
	}
	if len(cleared) != 2 {
		t.Fatalf("expected 2 unsized orders for hl:0:BTC, got %d", len(cleared))
	}
	if len(m.deletes) != 2 {
		t.Errorf("expected 2 cancels, got %v", m.deletes)
	}
}

func TestClearPositionTpsl_FilterByLeg(t *testing.T) {
	m := &tpslMockState{openTrigger: []SimOrder{
		{ID: "ord_sl", Market: "hl:0:BTC", Tpsl: "sl", SizeToMax: true, Status: OrderWaitingTrigger},
		{ID: "ord_tp", Market: "hl:0:BTC", Tpsl: "tp", SizeToMax: true, Status: OrderWaitingTrigger},
	}}
	srv := newTpslTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	cleared, err := a.ClearPositionTpsl(context.Background(), ClearPositionTpslOptions{
		Path: "/op/clear/2", ObjectID: "obj_1", Market: "hl:0:BTC", Tpsl: "sl",
	})
	if err != nil {
		t.Fatalf("ClearPositionTpsl: %v", err)
	}
	if len(cleared) != 1 || cleared[0].ID != "ord_sl" {
		t.Errorf("expected only ord_sl, got %+v", cleared)
	}
	if len(m.deletes) != 1 || m.deletes[0] != "ord_sl" {
		t.Errorf("expected cancel of ord_sl only, got %v", m.deletes)
	}
}
