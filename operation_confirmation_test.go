package arca

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConfirmedUsesSettledOrderIdentityAndTerminalReceipt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status OrderStatus
		filled string
	}{
		{"full", OrderFilled, "1"}, {"partial IOC", OrderCancelled, "0.4"}, {"no fill", OrderCancelled, "0"}, {"failed", OrderFailed, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, reads := 0, 0
			call := func() (OrderOperationResponse, error) {
				calls++
				return OrderOperationResponse{Operation: Operation{ID: "operation", State: OpPending}}, nil
			}
			wait := func(context.Context, string, time.Duration) (*Operation, error) {
				return &Operation{ID: "operation", State: OpCompleted, ParsedOutcome: map[string]any{"orderId": "actual-order"}}, nil
			}
			base := newOperationHandle(call, OrderOperationResponse.op, (*OrderOperationResponse).setOp, wait, nil, 0)
			price := "100"
			h := newOrderHandle(base, "account", "/frozen", orderHandleDeps{
				getOrder: func(_ context.Context, account, id string) (SimOrderWithFills, error) {
					reads++
					if account != "account" || id != "actual-order" {
						t.Fatalf("wrong identity %s/%s", account, id)
					}
					status := tc.status

					return SimOrderWithFills{Order: SimOrder{ID: id, Status: status, FilledSize: tc.filled, AvgFillPrice: &price}}, nil
				}, onFillEvent: func(func(RealmEvent)) func() { return func() {} },
			})
			h.immediate = true
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			out, err := h.Confirmed(ctx)
			if err != nil || calls != 1 || reads != 1 || out.Operation.ParsedOutcome["status"] != string(tc.status) || out.Operation.ParsedOutcome["filledSize"] != tc.filled {
				t.Fatalf("%+v err=%v calls=%d reads=%d", out, err, calls, reads)
			}
		})
	}
}

func TestConfirmedRestingOrderDoesNotWaitForFill(t *testing.T) {
	h := newSettledOrderHandle("/frozen", "resting", orderHandleDeps{getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
		t.Fatal("resting order awaited fill")
		return SimOrderWithFills{}, nil
	}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := h.Confirmed(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmedDeadlineDoesNotResubmitOrInventOutcome(t *testing.T) {
	h := newSettledOrderHandle("/frozen", "original", orderHandleDeps{
		getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
			return SimOrderWithFills{Order: SimOrder{ID: "original", Status: OrderOpen}}, nil
		},
		onFillEvent: func(func(RealmEvent)) func() { return func() {} },
	})
	h.immediate = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	out, err := h.Confirmed(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || out.Operation.ID != "op_place" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestConfirmationWaitCanResumeAfterDeadlineWithoutResubmission(t *testing.T) {
	calls, waits := 0, 0
	call := func() (OrderOperationResponse, error) {
		calls++
		return OrderOperationResponse{Operation: Operation{ID: "op", State: OpPending}}, nil
	}
	wait := func(ctx context.Context, _ string, _ time.Duration) (*Operation, error) {
		waits++
		if waits == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &Operation{ID: "op", State: OpCompleted}, nil
	}
	base := newOperationHandle(call, OrderOperationResponse.op, (*OrderOperationResponse).setOp, wait, nil, 0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := base.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first wait %v", err)
	}
	out, err := base.Wait(context.Background())
	if err != nil || out.Operation.State != OpCompleted || calls != 1 || waits != 2 {
		t.Fatalf("out=%+v err=%v calls=%d waits=%d", out, err, calls, waits)
	}
}
