package arca

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetOperationExecutionReceiptUsesOriginalAccountAndInlineProof(t *testing.T) {
	// A zero client has no HTTP transport: either path must finish before any GET.
	client := &Arca{}
	input := `{"exchangeObjectId":"original","size":"10","timeInForce":"IOC"}`
	outcome := `{"orderId":"venue-order","status":"filled","filledSize":"2"}`
	op := Operation{ID: "original-operation", State: OpCompleted, Input: &input, Outcome: &outcome}
	receipt, err := client.GetOperationExecutionReceipt(context.Background(), "original", op)
	if err != nil || receipt.ObjectID != "original" || receipt.OperationID != op.ID || receipt.FulfillmentState != "partial" || receipt.RemainingSize == nil || *receipt.RemainingSize != "8" {
		t.Fatalf("unexpected receipt: %+v, %v", receipt, err)
	}
	if _, err := client.GetOperationExecutionReceipt(context.Background(), "other", op); err == nil {
		t.Fatal("cross-account receipt accepted")
	}
	op.State = OpPending
	if _, err := client.GetOperationExecutionReceipt(context.Background(), "other", op); err == nil {
		t.Fatal("cross-account pending operation attempted recovery")
	}
}

func TestExecutionRecoveryRequiresGapAndFreshReadiness(t *testing.T) {
	var gap func()
	var reads, barriers atomic.Int32
	seeded := make(chan struct{})
	h := newSettledOrderHandle("/original", "exact", orderHandleDeps{
		onExecutionGap:        func(f func()) func() { gap = f; return func() {} },
		recoverExecutionReady: func(context.Context) error { barriers.Add(1); return nil },
		getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
			if reads.Add(1) == 1 {
				close(seeded)
				return SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderOpen}}, nil
			}
			return SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderFilled, FilledSize: "1"}}, nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.ExecutionReceipt(ctx); done <- err }()
	<-seeded
	time.Sleep(30 * time.Millisecond)
	if reads.Load() != 1 {
		t.Fatal("healthy pending snapshot was polled")
	}
	gap()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 || barriers.Load() != 1 {
		t.Fatalf("reads=%d barriers=%d", reads.Load(), barriers.Load())
	}
}

func TestFailedExecutionRecoveryHasFiniteReadBudget(t *testing.T) {
	var reads atomic.Int32
	h := newSettledOrderHandle("/original", "exact", orderHandleDeps{getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
		reads.Add(1)
		return SimOrderWithFills{}, errors.New("history missing")
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 1100*time.Millisecond)
	defer cancel()
	if _, err := h.ExecutionReceipt(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing history claimed execution: %v", err)
	}
	if reads.Load() != 3 {
		t.Fatalf("unbounded or missing recovery: %d", reads.Load())
	}
}

func TestExecutionFailurePushDoesNotWaitForHistoryOrDeadline(t *testing.T) {
	h := newSettledOrderHandle("/original", "exact", orderHandleDeps{
		onExecutionEvent: func(f func(RealmEvent)) func() {
			f(RealmEvent{Operation: &Operation{ID: "op_place", State: OpFailed}})
			return func() {}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := h.ExecutionReceipt(ctx)
	var failure *OperationFailedError
	if !errors.As(err, &failure) {
		t.Fatalf("failure became unresolved: %v", err)
	}
}

func TestOrderCaptureStopsOnTerminalOperationUpdate(t *testing.T) {
	for _, state := range []OperationState{OpCompleted, OpFailed, OpExpired} {
		ready := make(chan struct{})
		close(ready)
		stops := 0
		s := &orderStream{operationID: "op", objectID: "account", ready: ready, cancel: func() {}, listeners: map[int]func(RealmEvent){}, onStop: func() { stops++ }}
		body := `{"orderId":"exact","status":"FILLED","filledSize":"1"}`
		s.receive(RealmEvent{Operation: &Operation{ID: "op", State: state, Outcome: &body}})
		if !s.stopped || stops != 1 {
			t.Fatalf("terminal %s retained capture", state)
		}
		s.stop()
		if stops != 1 {
			t.Fatal("capture released twice")
		}
	}
}

func TestExecutionLearnsVenueIdentityAndRechecksEarlyEvidence(t *testing.T) {
	for _, early := range []bool{true, false} {
		t.Run(map[bool]string{true: "before identity", false: "after open snapshot"}[early], func(t *testing.T) {
			release := make(chan struct{})
			subscribed := make(chan struct{})
			seeded := make(chan struct{})
			var receive func(RealmEvent)
			base := newOperationHandle(func() (OrderOperationResponse, error) {
				return OrderOperationResponse{Operation: Operation{ID: "operation", State: OpPending}}, nil
			}, OrderOperationResponse.op, (*OrderOperationResponse).setOp,
				func(context.Context, string, time.Duration) (*Operation, error) {
					<-release
					return &Operation{ID: "operation", State: OpCompleted, ParsedOutcome: map[string]any{"orderId": "venue-order"}}, nil
				}, nil, 0)
			h := newOrderHandle(base, "account", "/original", orderHandleDeps{
				onExecutionEvent: func(f func(RealmEvent)) func() { receive = f; close(subscribed); return func() {} },
				getOrder: func(_ context.Context, account, id string) (SimOrderWithFills, error) {
					if account != "account" || id != "venue-order" {
						t.Errorf("wrong lookup %s/%s", account, id)
					}
					close(seeded)
					return SimOrderWithFills{Order: SimOrder{ID: id, Status: OrderOpen}}, nil
				},
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan OrderExecutionReceipt, 1)
			errs := make(chan error, 1)
			go func() { r, e := h.ExecutionReceipt(ctx); done <- r; errs <- e }()
			<-subscribed
			if !early {
				close(release)
				<-seeded
			}
			receive(RealmEvent{EntityID: "other", Order: &SimOrderWithFills{Order: SimOrder{ID: "venue-order", Status: OrderFilled, FilledSize: "99"}}})
			receive(RealmEvent{EntityID: "account", Order: &SimOrderWithFills{Order: SimOrder{ID: "different-order", Status: OrderFilled, FilledSize: "88"}}})
			receive(RealmEvent{EntityID: "account", Order: &SimOrderWithFills{Order: SimOrder{ID: "venue-order", Status: OrderFilled, FilledSize: "1"}}})
			if early {
				close(release)
			}
			result := <-done
			if err := <-errs; err != nil || result.OrderID != "venue-order" || result.FilledSize != "1" {
				t.Fatalf("lost scoped execution: %+v %v", result, err)
			}
		})
	}
}

func TestRejectedAndExpiredExecutionUpdatesAreTerminal(t *testing.T) {
	for _, tc := range []struct {
		status OrderStatus
		state  string
	}{{"REJECTED", "rejected"}, {"EXPIRED", "no_fill"}} {
		h := newSettledOrderHandle("/original", "exact", orderHandleDeps{onExecutionEvent: func(f func(RealmEvent)) func() {
			f(RealmEvent{EntityID: "obj_exchange", Order: &SimOrderWithFills{Order: SimOrder{ID: "exact", Status: tc.status, FilledSize: "0.0"}}})
			return func() {}
		}})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := h.ExecutionReceipt(ctx)
		cancel()
		if err != nil || result.ExecutionState != tc.state || result.RemainingDisposition != "cancelled" {
			t.Fatalf("%s: %+v %v", tc.status, result, err)
		}
	}
}

func TestCaptureSurvivesFailedACKAndRecoveredOpenSnapshot(t *testing.T) {
	server := newWSTestServer(t)
	a := newTestArca(t, server.rootURL())
	defer a.Dispose()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	stream := a.newOrderStream(ctx, "obj_exchange")
	defer stream.stop()
	body := `{"orderId":"exact","status":"OPEN","filledSize":"0"}`
	stream.submitted(Operation{ID: "op_place", State: OpCompleted, Outcome: &body})
	seeded := make(chan struct{})
	deps := a.orderHandleDeps()
	deps.onExecutionEvent = stream.subscribe
	deps.awaitExecutionReady = stream.awaitReady
	deps.getOrder = func(context.Context, string, string) (SimOrderWithFills, error) {
		select {
		case <-seeded:
		default:
			close(seeded)
		}
		return SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderOpen}}, nil
	}
	h := newSettledOrderHandle("/original", "exact", deps)
	done := make(chan error, 1)
	go func() { _, err := h.ExecutionReceipt(ctx); done <- err }()
	first := server.accept()
	first.handshake(0)
	first.waitFor("subscribe_events")
	first.close()
	<-stream.ready
	stream.mu.Lock()
	stopped := stream.stopped
	stream.mu.Unlock()
	if stopped {
		t.Fatal("ACK failure permanently removed capture")
	}
	second := server.accept()
	second.handshake(0)
acknowledge:
	for {
		select {
		case message := <-second.in:
			if message["action"] == "watch" {
				t.Fatal("order capture watched a path; it subscribes to its event types")
			}
			if message["action"] == "subscribe_events" && message["requestId"] != nil {
				second.send(map[string]any{"type": "events_subscribed", "requestId": message["requestId"], "types": message["types"]})
			}
		case <-seeded:
			break acknowledge
		case <-ctx.Done():
			t.Fatal("recovery snapshot not reached")
		}
	}
	second.send(map[string]any{"type": "order.updated", "entityId": "obj_exchange", "order": map[string]any{"order": map[string]any{"id": "exact", "status": "FILLED", "filledSize": "1"}}})
	if err := <-done; err != nil {
		t.Fatalf("terminal event lost after recovery: %v", err)
	}
}

func TestConfirmedTerminalResponseDoesNotReadOrder(t *testing.T) {
	for _, outcome := range []string{
		`{"orderId":"exact","status":"filled","filledSize":"1.000000000000000001","avgFillPrice":"100"}`,
		`{"orderId":"exact","status":"cancelled","filledSize":"0.4","avgFillPrice":"100"}`,
		`{"orderId":"exact","status":"cancelled","filledSize":"0"}`,
	} {
		h := newSettledOrderHandleWithOutcome("/original", outcome, orderHandleDeps{
			getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
				t.Fatal("terminal response must not read order")
				return SimOrderWithFills{}, nil
			},
		})
		h.immediate = true
		r, err := h.Confirmed(context.Background())
		if err != nil || r.Operation.ParsedOutcome["orderId"] != "exact" {
			t.Fatalf("%+v %v", r, err)
		}
		if r.Operation.ParsedOutcome["fillsComplete"] != false {
			t.Fatal("aggregate receipt invented complete fill history")
		}
	}
}
func TestSettlementAloneDoesNotProveExecution(t *testing.T) {
	for _, outcome := range []string{`{"orderId":"exact","status":"filled","filledSize":"1/2"}`, `{"orderId":"exact","status":"filled","filledSize":"0x10"}`, `{"orderId":"exact"}`, `{"orderId":"exact","status":"accepted","filledSize":"1"}`, `{"orderId":"exact","status":"filled","filledSize":"invalid"}`, `{"orderId":"exact","status":"filled","filledSize":"0"}`} {
		o := Operation{State: OpCompleted, Outcome: &outcome}
		if _, known := operationExecution(o, "account"); known {
			t.Fatalf("accepted %s", outcome)
		}
	}
}

func TestOnFillCannotDeadlockInlineSubmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := make(chan struct{})
	var handler func(RealmEvent)
	delivered := make(chan SimFill, 1)
	body := `{"orderId":"exact","status":"filled","filledSize":"1","avgFillPrice":"100"}`
	base := newOperationHandle(func() (OrderOperationResponse, error) {
		<-start
		handler(RealmEvent{Fill: &SimFill{ID: "fill", OrderID: "exact", Size: "1"}})
		return OrderOperationResponse{Operation: Operation{ID: "op", State: OpCompleted, Outcome: &body}}, nil
	}, OrderOperationResponse.op, (*OrderOperationResponse).setOp, nil, nil, 0)
	h := newOrderHandle(base, "account", "/original", orderHandleDeps{onFillEvent: func(f func(RealmEvent)) func() { handler = f; return func() {} }})
	stop := h.OnFill(ctx, func(f SimFill) { delivered <- f })
	defer stop()
	close(start)
	if _, err := h.Submitted(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
	case <-ctx.Done():
		t.Fatal("inline fill blocked submission")
	}
}

func TestFilledConsumesCorrelatedPushWithoutRefetch(t *testing.T) {
	var deliver func(RealmEvent)
	reads := 0
	h := newSettledOrderHandle("/original", "exact", orderHandleDeps{
		onExecutionEvent: func(f func(RealmEvent)) func() { deliver = f; return func() {} },
		getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
			reads++
			deliver(RealmEvent{EntityID: "other-account", Order: &SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderFilled, FilledSize: "99"}}})
			deliver(RealmEvent{EntityID: "obj_exchange", Order: &SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderCancelled, FilledSize: "0.4"}}})
			return SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderOpen}}, nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := h.waitExecutionEvidence(ctx)
	if err != nil || reads != 1 || result.Order.FilledSize != "0.4" {
		t.Fatalf("%+v %v reads=%d", result, err, reads)
	}
}

func TestOnFillFailedSubmissionReleasesListener(t *testing.T) {
	start := make(chan struct{})
	var handler func(RealmEvent)
	var releases atomic.Int32
	released := make(chan struct{})
	base := newOperationHandle(func() (OrderOperationResponse, error) {
		<-start
		return OrderOperationResponse{}, errors.New("submission failed")
	}, OrderOperationResponse.op, (*OrderOperationResponse).setOp, nil, nil, 0)
	h := newOrderHandle(base, "account", "/original", orderHandleDeps{onFillEvent: func(f func(RealmEvent)) func() {
		handler = f
		return func() {
			if releases.Add(1) == 1 {
				close(released)
			}
		}
	}})
	stop := h.OnFill(context.Background(), func(SimFill) { t.Error("callback after failure") })
	close(start)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("listener leaked after submission failure")
	}
	for i := 0; i < 1000; i++ {
		handler(RealmEvent{EntityID: "fill-id", Fill: &SimFill{OrderID: "unrelated"}})
	}
	stop()
	stop()
	if releases.Load() != 1 {
		t.Fatalf("unsubscribed %d times", releases.Load())
	}
}

func TestTerminalPushDoesNotWaitForSubscriptionAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var reads atomic.Int32
	h := newSettledOrderHandle("/original", "exact", orderHandleDeps{
		onExecutionEvent: func(f func(RealmEvent)) func() {
			f(RealmEvent{EntityID: "obj_exchange", Order: &SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderFilled, FilledSize: "1"}}})
			return func() {}
		},
		awaitExecutionReady: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
			reads.Add(1)
			return SimOrderWithFills{}, nil
		},
	})
	result, err := h.waitExecutionEvidence(ctx)
	if err != nil || result.Order.ID != "exact" || reads.Load() != 0 {
		t.Fatalf("result=%+v err=%v reads=%d", result, err, reads.Load())
	}
}

func TestTerminalPushWinsWhileHistoryReadIsBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var send func(RealmEvent)
	h := newSettledOrderHandle("/original", "exact", orderHandleDeps{
		onExecutionEvent: func(f func(RealmEvent)) func() { send = f; return func() {} },
		getOrder: func(ctx context.Context, _ string, _ string) (SimOrderWithFills, error) {
			send(RealmEvent{EntityID: "obj_exchange", Order: &SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderCancelled, FilledSize: "0.4"}}})
			<-ctx.Done()
			return SimOrderWithFills{}, ctx.Err()
		},
	})
	result, err := h.waitExecutionEvidence(ctx)
	if err != nil || result.Order.FilledSize != "0.4" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestTerminalIOCReceiptReportsPartialOriginalQuantity(t *testing.T) {
	for _, tc := range []struct{ requested, executed, remaining string }{
		{"40.110692", "4.041", "36.069692"},
		{"286.522911", "1.809", "284.713911"},
		{"47.700441", "3.825", "43.875441"},
		{"40.110692", "0.0", "40.110692"},
	} {
		input := `{"exchangeObjectId":"account","size":"` + tc.requested + `","timeInForce":"GTC","gllPrepared":{"request":{"Effect":1}}}`
		body := `{"orderId":"exact","status":"filled","filledSize":"` + tc.executed + `"}`
		receipt, ok := operationExecution(Operation{State: OpCompleted, Input: &input, Outcome: &body}, "account")
		if !ok {
			t.Fatal("terminal IOC did not resolve")
		}
		outcome := receipt.ExecutionOutcome()
		state := "partial"
		if tc.executed == "0.0" {
			state = "no_fill"
		}
		if outcome["executionState"] != state || outcome["remainingDisposition"] != "cancelled" || outcome["remainingSize"] != tc.remaining {
			t.Fatalf("incorrect partial receipt: %+v", outcome)
		}
	}
}

func TestExecutionUpdatePreservesOriginalRequestedQuantity(t *testing.T) {
	input := `{"exchangeObjectId":"account","size":"40.110692","gllPrepared":{"request":{"Effect":1}}}`
	op := Operation{ID: "op", State: OpPending, Input: &input}
	evidence, ok := executionUpdate(op, "account", SimOrder{ID: "exact", Status: OrderFilled, Size: "4.041", FilledSize: "4.041"})
	if !ok {
		t.Fatal("terminal observation rejected")
	}
	outcome := evidence.ExecutionOutcome()
	if outcome["requestedSize"] != "40.110692" || outcome["remainingSize"] != "36.069692" || outcome["fulfillmentState"] != "partial" || outcome["remainingDisposition"] != "cancelled" {
		t.Fatalf("%+v", outcome)
	}
	op.Input = nil
	evidence, ok = executionUpdate(op, "account", SimOrder{ID: "exact", Status: OrderFilled, Size: "4.041", FilledSize: "4.041"})
	if !ok || evidence.ExecutionOutcome()["fulfillmentState"] != "unknown" {
		t.Fatalf("venue rewritten size became original intent: %+v", evidence)
	}
}

func TestExecutionReceiptDoesNotWaitForPendingOperationSettlement(t *testing.T) {
	outcome := `{"orderId":"exact"}`
	op := Operation{ID: "pending-op", State: OpPending, Outcome: &outcome}
	base := newOperationHandle(func() (OrderOperationResponse, error) { return OrderOperationResponse{Operation: op}, nil }, OrderOperationResponse.op, (*OrderOperationResponse).setOp, nil, nil, 0)
	h := newOrderHandle(base, "account", "/original", orderHandleDeps{
		onExecutionEvent: func(deliver func(RealmEvent)) func() {
			deliver(RealmEvent{EntityID: "account", Order: &SimOrderWithFills{Order: SimOrder{ID: "exact", Status: OrderFilled, FilledSize: "3"}}})
			return func() {}
		},
		awaitExecutionReady: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	receipt, err := h.ExecutionReceipt(ctx)
	if err != nil || receipt.OrderID != "exact" || receipt.FilledSize != "3" {
		t.Fatalf("%+v %v", receipt, err)
	}
}

func TestExecutionFailureSnapshotWithoutPushIsPromptAndScoped(t *testing.T) {
	for _, state := range []OperationState{OpFailed, OpExpired} {
		for _, matches := range []bool{true, false} {
			t.Run(string(state)+map[bool]string{true: "/original", false: "/other"}[matches], func(t *testing.T) {
				var posts, reads, releases atomic.Int32
				base := newOperationHandle(func() (OrderOperationResponse, error) {
					posts.Add(1)
					return OrderOperationResponse{Operation: Operation{ID: "original-operation", State: OpPending}}, nil
				}, OrderOperationResponse.op, (*OrderOperationResponse).setOp,
					func(_ context.Context, id string, _ time.Duration) (*Operation, error) {
						reads.Add(1)
						if id != "original-operation" {
							t.Errorf("snapshot lookup changed identity: %s", id)
						}
						if !matches {
							id = "other-operation"
						}
						return nil, newOperationFailedError((&Operation{ID: id, State: state}).snapshot())
					}, nil, 0)
				h := newOrderHandle(base, "account", "/original", orderHandleDeps{
					onExecutionEvent: func(func(RealmEvent)) func() { return func() {} },
					releaseExecution: func() { releases.Add(1) },
					getOrder: func(context.Context, string, string) (SimOrderWithFills, error) {
						t.Error("operation failure must not read order history")
						return SimOrderWithFills{}, errors.New("unexpected history")
					},
				})
				ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
				defer cancel()
				_, err := h.ExecutionReceipt(ctx)
				var failure *OperationFailedError
				if matches {
					if !errors.As(err, &failure) || failure.Operation.ID != "original-operation" || failure.Operation.State != string(state) || ctx.Err() != nil {
						t.Fatalf("authoritative snapshot did not promptly fail with original identity: %v", err)
					}
					if releases.Load() != 1 {
						t.Fatalf("capture releases=%d", releases.Load())
					}
				} else if !errors.Is(err, context.DeadlineExceeded) || releases.Load() != 0 {
					t.Fatalf("unrelated operation failure treated as terminal: %v releases=%d", err, releases.Load())
				}
				if posts.Load() != 1 || reads.Load() != 1 {
					t.Fatalf("posts=%d snapshot reads=%d", posts.Load(), reads.Load())
				}
			})
		}
	}
}
