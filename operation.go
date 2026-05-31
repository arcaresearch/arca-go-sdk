package arca

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// waitFunc waits for an operation to reach a terminal state. A timeout of 0
// means "use the SDK default".
type waitFunc func(ctx context.Context, operationID string, timeout time.Duration) (*Operation, error)

// OperationHandle is returned synchronously from mutation methods. The HTTP
// request is fired immediately in the background.
//
//   - Submitted(ctx) returns the HTTP response before settlement.
//   - Wait(ctx) waits for both HTTP submission AND operation settlement,
//     returning the response with Operation updated to its terminal state.
//
// Predicted, when present, describes the operation's predicted effects
// synchronously (no network).
type OperationHandle[T any] struct {
	Predicted *PredictedEffect

	opOf           func(T) *Operation
	setOp          func(*T, *Operation)
	waitFn         waitFunc
	defaultTimeout time.Duration

	done      chan struct{}
	resp      T
	submitErr error

	settleMu   sync.Mutex
	settleDone bool
	settled    T
	settleErr  error
}

func newOperationHandle[T any](
	call func() (T, error),
	opOf func(T) *Operation,
	setOp func(*T, *Operation),
	waitFn waitFunc,
	predicted *PredictedEffect,
	defaultTimeout time.Duration,
) *OperationHandle[T] {
	h := &OperationHandle[T]{
		Predicted:      predicted,
		opOf:           opOf,
		setOp:          setOp,
		waitFn:         waitFn,
		defaultTimeout: defaultTimeout,
		done:           make(chan struct{}),
	}
	go func() {
		h.resp, h.submitErr = call()
		close(h.done)
	}()
	return h
}

// Submitted blocks until the HTTP submission completes (before settlement) and
// returns the raw response.
func (h *OperationHandle[T]) Submitted(ctx context.Context) (T, error) {
	select {
	case <-h.done:
		return h.resp, h.submitErr
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Wait blocks until the operation reaches a terminal state, returning the
// response with its Operation updated. It returns *OperationFailedError if the
// operation failed and *OperationStalledError on timeout.
func (h *OperationHandle[T]) Wait(ctx context.Context) (T, error) {
	h.settleMu.Lock()
	if h.settleDone {
		r, e := h.settled, h.settleErr
		h.settleMu.Unlock()
		return r, e
	}
	h.settleMu.Unlock()

	r, e := h.settle(ctx, h.defaultTimeout)

	h.settleMu.Lock()
	h.settleDone = true
	h.settled = r
	h.settleErr = e
	h.settleMu.Unlock()
	return r, e
}

// WaitTimeout is Wait with an explicit settlement timeout. It is not cached.
func (h *OperationHandle[T]) WaitTimeout(ctx context.Context, timeout time.Duration) (T, error) {
	return h.settle(ctx, timeout)
}

func (h *OperationHandle[T]) settle(ctx context.Context, timeout time.Duration) (T, error) {
	resp, err := h.Submitted(ctx)
	if err != nil {
		return resp, err
	}
	op := h.opOf(resp)
	if op == nil || op.State != OpPending {
		return resp, nil
	}
	completed, err := h.waitFn(ctx, op.ID, timeout)
	if err != nil {
		return resp, err
	}
	out := resp
	h.setOp(&out, completed)
	return out, nil
}

// ---- OrderHandle ----

type orderHandleDeps struct {
	getOrder    func(ctx context.Context, objectID, orderID string) (SimOrderWithFills, error)
	onFillEvent func(handler func(RealmEvent)) func()
	cancelOrder func(ctx context.Context, opts CancelOrderOptions) *OperationHandle[OrderOperationResponse]
	listFills   func(ctx context.Context, objectID string) (FillListResponse, error)
}

// OrderHandle extends OperationHandle for the order lifecycle. Wait means "the
// order was placed". Fills and cancellation are separate concerns accessed via
// Filled, OnFill, Fills, FillSummary, and Cancel.
type OrderHandle struct {
	*OperationHandle[OrderOperationResponse]

	objectID      string
	placementPath string
	deps          orderHandleDeps
}

func newOrderHandle(
	base *OperationHandle[OrderOperationResponse],
	objectID, placementPath string,
	deps orderHandleDeps,
) *OrderHandle {
	return &OrderHandle{OperationHandle: base, objectID: objectID, placementPath: placementPath, deps: deps}
}

func (h *OrderHandle) resolveOrderID(ctx context.Context) (string, error) {
	resp, err := h.Submitted(ctx)
	if err != nil {
		return "", err
	}
	if resp.Operation.Outcome != nil && *resp.Operation.Outcome != "" {
		var parsed struct {
			OrderID string `json:"orderId"`
		}
		if json.Unmarshal([]byte(*resp.Operation.Outcome), &parsed) == nil && parsed.OrderID != "" {
			return parsed.OrderID, nil
		}
	}
	return resp.Operation.ID, nil
}

func isTerminalOrderStatus(s OrderStatus) bool {
	return s == OrderFilled || s == OrderCancelled || s == OrderFailed
}

// Filled waits for the order to reach a terminal status (FILLED/CANCELLED/
// FAILED) and returns the order with all its fills. It honors ctx for
// cancellation; LIMIT orders may never fill, so pass a deadline-bound ctx.
func (h *OrderHandle) Filled(ctx context.Context) (SimOrderWithFills, error) {
	var zero SimOrderWithFills
	if _, err := h.Wait(ctx); err != nil {
		return zero, err
	}
	orderID, err := h.resolveOrderID(ctx)
	if err != nil {
		return zero, err
	}

	check := func() (SimOrderWithFills, bool) {
		res, err := h.deps.getOrder(ctx, h.objectID, orderID)
		if err != nil {
			return zero, false
		}
		if isTerminalOrderStatus(res.Order.Status) {
			return res, true
		}
		return zero, false
	}

	if res, ok := check(); ok {
		return res, nil
	}

	fillCh := make(chan struct{}, 16)
	unsub := h.deps.onFillEvent(func(ev RealmEvent) {
		select {
		case fillCh <- struct{}{}:
		default:
		}
	})
	defer unsub()

	// Poll on each fill event plus a periodic safety net.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if res, ok := check(); ok {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-fillCh:
		case <-ticker.C:
		}
	}
}

// OnFill registers a callback for each fill on this order. It returns an
// unsubscribe function.
func (h *OrderHandle) OnFill(ctx context.Context, callback func(SimFill)) func() {
	var orderID string
	var once sync.Once
	active := true
	var mu sync.Mutex
	unsub := h.deps.onFillEvent(func(ev RealmEvent) {
		if ev.Fill == nil {
			return
		}
		once.Do(func() {
			if id, err := h.resolveOrderID(ctx); err == nil {
				mu.Lock()
				orderID = id
				mu.Unlock()
			}
		})
		mu.Lock()
		oid := orderID
		a := active
		mu.Unlock()
		if a && ev.Fill.OrderID == oid {
			callback(*ev.Fill)
		}
	})
	return func() {
		mu.Lock()
		active = false
		mu.Unlock()
		unsub()
	}
}

// FillSummary returns the platform-side Fill record (P&L, fee breakdown,
// direction, resulting position) for this order, waiting for it to fill first.
func (h *OrderHandle) FillSummary(ctx context.Context) (*Fill, error) {
	result, err := h.Filled(ctx)
	if err != nil {
		return nil, err
	}
	submitted, err := h.Submitted(ctx)
	if err != nil {
		return nil, err
	}
	opID := submitted.Operation.ID
	fills, err := h.deps.listFills(ctx, h.objectID)
	if err != nil {
		return nil, err
	}
	for i := range fills.Fills {
		f := &fills.Fills[i]
		if f.OperationID == opID || f.OrderID == result.Order.ID {
			return f, nil
		}
	}
	return nil, nil
}

// Cancel cancels the order. It auto-generates a cancel path from the placement
// path; pass a non-empty path to control idempotency explicitly.
func (h *OrderHandle) Cancel(ctx context.Context, path string) (*OperationHandle[OrderOperationResponse], error) {
	orderID, err := h.resolveOrderID(ctx)
	if err != nil {
		return nil, err
	}
	cancelPath := path
	if cancelPath == "" {
		cancelPath = replaceFirst(h.placementPath, "/op/order/", "/op/cancel/")
	}
	return h.deps.cancelOrder(ctx, CancelOrderOptions{
		Path:     cancelPath,
		ObjectID: h.objectID,
		OrderID:  orderID,
	}), nil
}

func replaceFirst(s, old, new string) string {
	if i := indexOf(s, old); i >= 0 {
		return s[:i] + new + s[i+len(old):]
	}
	return s
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// AsOperationFailed is a convenience for errors.As on *OperationFailedError.
func AsOperationFailed(err error) (*OperationFailedError, bool) {
	var e *OperationFailedError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
