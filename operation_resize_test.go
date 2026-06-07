package arca

import (
	"context"
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
