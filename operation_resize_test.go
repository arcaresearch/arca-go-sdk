package arca

import (
	"context"
	"sync"
	"testing"
	"time"
)

// newSettledOrderHandle builds an OrderHandle whose placement has already
// "settled" (non-pending, so settle() never touches the network) and whose
// outcome carries an orderId. This lets us exercise Cancel/Resize path
// derivation without a live server.
func newSettledOrderHandle(placementPath, orderID string, deps orderHandleDeps) *OrderHandle {
	outcome := `{"orderId":"` + orderID + `"}`
	call := func() (OrderOperationResponse, error) {
		return OrderOperationResponse{
			Operation: Operation{ID: "op_place", State: OpCompleted, Outcome: &outcome},
		}, nil
	}
	base := newOperationHandle(call, OrderOperationResponse.op, (*OrderOperationResponse).setOp,
		func(context.Context, string, time.Duration) (*Operation, error) { return nil, nil }, nil, 0)
	return newOrderHandle(base, "obj_exchange", placementPath, deps)
}

// newSettledOrderHandleWithOutcome is like newSettledOrderHandle but lets the
// test supply the full placement outcome JSON (so a pending bracket child with
// a cloid and no venue orderId can be modelled).
func newSettledOrderHandleWithOutcome(placementPath, outcome string, deps orderHandleDeps) *OrderHandle {
	call := func() (OrderOperationResponse, error) {
		return OrderOperationResponse{
			Operation: Operation{ID: "op_place", State: OpCompleted, Outcome: &outcome},
		}, nil
	}
	base := newOperationHandle(call, OrderOperationResponse.op, (*OrderOperationResponse).setOp,
		func(context.Context, string, time.Duration) (*Operation, error) { return nil, nil }, nil, 0)
	return newOrderHandle(base, "obj_exchange", placementPath, deps)
}

// TestOrderHandle_OnFill_MatchesPendingChildByCloid pins the cloid-identity
// half of the bracket fix: a normalTpsl child before activation has NO venue
// order id (resolveOrderID falls back to the operation id), so a fill for it —
// which arrives carrying the cloid once the venue arms the child — can only be
// correlated by cloid. OnFill must match on the cloid, not just the oid.
func TestOrderHandle_OnFill_MatchesPendingChildByCloid(t *testing.T) {
	var handler func(RealmEvent)
	deps := orderHandleDeps{
		onFillEvent: func(h func(RealmEvent)) func() { handler = h; return func() {} },
	}
	// Pending child: cloid present, no venue orderId.
	h := newSettledOrderHandleWithOutcome("/op/order/bracket-1",
		`{"orderId":"","cloid":"0xdeadbeefdeadbeefdeadbeefdeadbeef","tpsl":"tp"}`, deps)

	var got []SimFill
	var mu sync.Mutex
	unsub := h.OnFill(context.Background(), func(f SimFill) {
		mu.Lock()
		got = append(got, f)
		mu.Unlock()
	})
	defer unsub()

	// A fill whose venue OrderID does NOT equal the operation id, but whose
	// Cloid matches — only cloid identity can correlate it.
	handler(RealmEvent{Fill: &SimFill{
		ID: "fill-1", OrderID: "venue-oid-999", Cloid: "0xdeadbeefdeadbeefdeadbeefdeadbeef",
		Market: "hl:0:BTC", Side: Sell, Size: "0.01", Price: "72000",
	}})
	// A fill for a different order+cloid must NOT match.
	handler(RealmEvent{Fill: &SimFill{
		ID: "fill-2", OrderID: "someone-else", Cloid: "0xffff",
		Market: "hl:0:BTC", Side: Buy, Size: "1", Price: "1",
	}})

	// OnFill resolves the id/cloid lazily on the first event; give it a beat.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 matched fill, got %d", len(got))
	}
	if got[0].Cloid != "0xdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Fatalf("matched wrong fill: %+v", got[0])
	}
}

func TestOrderHandle_Resize_ForwardsNewSizeAndAutoPath(t *testing.T) {
	var got ModifyOrderOptions
	deps := orderHandleDeps{
		modifyOrder: func(ctx context.Context, opts ModifyOrderOptions) *OperationHandle[OrderOperationResponse] {
			got = opts
			return newOperationHandle(
				func() (OrderOperationResponse, error) {
					return OrderOperationResponse{Operation: Operation{ID: "op_modify", State: OpCompleted}}, nil
				},
				OrderOperationResponse.op, (*OrderOperationResponse).setOp,
				func(context.Context, string, time.Duration) (*Operation, error) { return nil, nil }, nil, 0)
		},
	}
	h := newSettledOrderHandle("/op/order/btc-buy-1", "ord_abc", deps)

	if _, err := h.Resize(context.Background(), "0.75", ""); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if got.Path != "/op/modify/btc-buy-1-0.75" {
		t.Fatalf("auto path: got %q want %q", got.Path, "/op/modify/btc-buy-1-0.75")
	}
	if got.ObjectID != "obj_exchange" {
		t.Fatalf("objectID: got %q", got.ObjectID)
	}
	if got.OrderID != "ord_abc" {
		t.Fatalf("orderID: got %q", got.OrderID)
	}
	if got.NewSize != "0.75" {
		t.Fatalf("newSize: got %q", got.NewSize)
	}
}

func TestOrderHandle_Resize_HonorsExplicitPath(t *testing.T) {
	var got ModifyOrderOptions
	deps := orderHandleDeps{
		modifyOrder: func(ctx context.Context, opts ModifyOrderOptions) *OperationHandle[OrderOperationResponse] {
			got = opts
			return newOperationHandle(
				func() (OrderOperationResponse, error) {
					return OrderOperationResponse{Operation: Operation{ID: "op_modify", State: OpCompleted}}, nil
				},
				OrderOperationResponse.op, (*OrderOperationResponse).setOp,
				func(context.Context, string, time.Duration) (*Operation, error) { return nil, nil }, nil, 0)
		},
	}
	h := newSettledOrderHandle("/op/order/btc-buy-1", "ord_abc", deps)

	if _, err := h.Resize(context.Background(), "2", "/op/modify/custom"); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got.Path != "/op/modify/custom" {
		t.Fatalf("explicit path: got %q want %q", got.Path, "/op/modify/custom")
	}
	if got.NewSize != "2" {
		t.Fatalf("newSize: got %q", got.NewSize)
	}
}
