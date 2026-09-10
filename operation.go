package arca

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
	// A caller deadline or temporary read failure is not cached as settlement.
	// The same handle can resume confirmation without resubmitting its mutation.
	h.settleDone = e == nil
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
	if op == nil {
		return resp, nil
	}
	if op.State == OpFailed || op.State == OpExpired {
		return resp, newOperationFailedError(op.snapshot())
	}
	if op.State != OpPending {
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
	watchLifecycle        func(context.Context, OriginalOrderReference) (*OrderLifecycleWatch, error)
	lifecycleLeg          int
	releaseExecution      func()
	awaitExecutionReady   func(context.Context) error
	recoverExecutionReady func(context.Context) error
	onExecutionGap        func(func()) func()
	getOrder              func(ctx context.Context, objectID, orderID string) (SimOrderWithFills, error)
	onExecutionEvent      func(handler func(RealmEvent)) func()
	onFillEvent           func(handler func(RealmEvent)) func()
	cancelOrder           func(ctx context.Context, opts CancelOrderOptions) *OperationHandle[OrderOperationResponse]
	modifyOrder           func(ctx context.Context, opts ModifyOrderOptions) *OperationHandle[OrderOperationResponse]
	listFills             func(ctx context.Context, objectID string) (FillListResponse, error)
}

// OrderHandle extends OperationHandle for the order lifecycle. Wait means "the
// order was placed". Fills and cancellation are separate concerns accessed via
// Filled, OnFill, Fills, FillSummary, and Cancel.
type OrderHandle struct {
	*OperationHandle[OrderOperationResponse]

	objectID      string
	placementPath string
	immediate     bool
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
	resp, err := h.Wait(ctx)
	if err != nil {
		return "", err
	}
	if id, ok := resp.Operation.ParsedOutcome["orderId"].(string); ok && id != "" {
		return id, nil
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

// Confirmed waits for execution of immediate orders and placement of intentional
// resting orders. The account's venue is irrelevant to this contract. A timeout
// returns the submission response plus an error; it never re-submits the trade.
func (h *OrderHandle) Confirmed(ctx context.Context) (OrderOperationResponse, error) {
	resp, err := h.Submitted(ctx)
	if err != nil {
		return resp, err
	}
	if !h.immediate {
		return h.Wait(ctx)
	}
	filled, err := h.ExecutionReceipt(ctx)
	if err != nil {
		return resp, err
	}
	out := filled.Outcome()
	resp.Operation.ParsedOutcome = out
	raw, _ := json.Marshal(out)
	text := string(raw)
	resp.Operation.Outcome = &text
	return resp, nil
}

// resolveCloid returns the order's client id from the placement outcome. A
// normalTpsl bracket child is not a live venue order until the entry fills and
// the venue arms it — until then it has NO venue order id and is addressable
// only by its cloid. resolveOrderID falls back to the operation id (never a
// real venue oid) for such a child, so fill matching must also key on the
// cloid. Returns "" when the outcome carries no cloid (e.g. sim orders).
func (h *OrderHandle) resolveCloid(ctx context.Context) string {
	resp, err := h.Submitted(ctx)
	if err != nil {
		return ""
	}
	if resp.Operation.Outcome != nil && *resp.Operation.Outcome != "" {
		var parsed struct {
			Cloid string `json:"cloid"`
		}
		if json.Unmarshal([]byte(*resp.Operation.Outcome), &parsed) == nil {
			return parsed.Cloid
		}
	}
	return ""
}

// fillMatches reports whether a fill belongs to this order. It matches on the
// venue order id when the order is live, OR on the cloid — the latter is the
// only handle a still-pending bracket child has before the venue assigns it an
// oid.
func fillMatches(f *SimFill, orderID, cloid string) bool {
	if f.OrderID != "" && f.OrderID == orderID {
		return true
	}
	if cloid != "" && f.Cloid != "" && f.Cloid == cloid {
		return true
	}
	return false
}

func isTerminalOrderStatus(s OrderStatus) bool {
	s = normalizeExecutionStatus(s)
	return s == OrderFilled || s == OrderCancelled || s == OrderFailed
}

func normalizeExecutionStatus(s OrderStatus) OrderStatus {
	switch strings.ToUpper(string(s)) {
	case "CANCELED", "EXPIRED":
		return OrderCancelled
	case "REJECTED":
		return OrderFailed
	default:
		return OrderStatus(strings.ToUpper(string(s)))
	}
}

// Filled waits for the order to reach a terminal status (FILLED/CANCELLED/
// FAILED) and returns the order with all its fills. It honors ctx for
// cancellation; LIMIT orders may never fill, so pass a deadline-bound ctx.
func (h *OrderHandle) waitExecutionEvidence(ctx context.Context) (SimOrderWithFills, error) {
	var zero SimOrderWithFills
	var mu sync.Mutex
	var events []RealmEvent
	wake := make(chan struct{}, 1)
	recovery := make(chan struct{}, 1)
	if h.deps.onExecutionGap != nil {
		off := h.deps.onExecutionGap(func() {
			select {
			case recovery <- struct{}{}:
			default:
			}
		})
		defer off()
	}
	listen := h.deps.onExecutionEvent
	if listen == nil {
		listen = h.deps.onFillEvent
	}
	if listen != nil {
		unsub := listen(func(ev RealmEvent) {
			if ev.Order == nil && ev.Operation == nil {
				return
			}
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
			select {
			case wake <- struct{}{}:
			default:
			}
		})
		defer unsub()
	}
	settled, err := h.Submitted(ctx)
	if err != nil {
		return zero, err
	}
	if settled.Operation.State == OpFailed || settled.Operation.State == OpExpired {
		return zero, newOperationFailedError(settled.Operation.snapshot())
	}
	if execution, known := operationExecution(settled.Operation, h.objectID); known {
		return execution, nil
	}
	orderID, _ := settled.Operation.ParsedOutcome["orderId"].(string)
	if settled.Operation.Outcome != nil {
		var identity struct {
			OrderID string `json:"orderId"`
		}
		if json.Unmarshal([]byte(*settled.Operation.Outcome), &identity) == nil && identity.OrderID != "" {
			orderID = identity.OrderID
		}
	}
	knownOrderID := orderID != ""
	var resolvedIdentity atomic.Value
	resolvedIdentity.Store(orderID)
	if !knownOrderID {
		orderID = settled.Operation.ID
	}
	// A terminal push already captured before readiness must not wait behind
	// an ACK or history read. Snapshot recovery races the authoritative stream.
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	type snapshotResult struct {
		value SimOrderWithFills
		err   error
	}
	snapshot := make(chan snapshotResult, 1)
	go func() {
		lookupID := orderID
		// Resolve a missing venue identity concurrently. An already-correlated
		// execution update can still win while placement settlement is pending.
		if !knownOrderID {
			placed, err := h.Wait(readCtx)
			if err != nil {
				snapshot <- snapshotResult{err: err}
				return
			}
			if evidence, valid := operationExecution(placed.Operation, h.objectID); valid {
				snapshot <- snapshotResult{value: evidence}
				return
			}
			lookupID, err = h.resolveOrderID(readCtx)
			if err != nil {
				snapshot <- snapshotResult{err: err}
				return
			}
			if lookupID != settled.Operation.ID {
				resolvedIdentity.Store(lookupID)
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
		// One acknowledged bootstrap read. Later reads require a real stream
		// gap/re-authentication or a failed read, capped at three per invocation.
		// A healthy pending snapshot never schedules another GET by time alone.
		var retry <-chan time.Time
		for attempt := 0; attempt < 3; attempt++ {
			ready := h.deps.awaitExecutionReady
			if attempt > 0 {
				select {
				case <-readCtx.Done():
					return
				case <-recovery:
				case <-retry:
				}
				ready = h.deps.recoverExecutionReady
			}
			var result snapshotResult
			if ready != nil {
				result.err = ready(readCtx)
			}
			if attempt == 0 {
				select {
				case <-recovery:
				default:
				}
			}
			if result.err == nil && h.deps.getOrder != nil {
				result.value, result.err = h.deps.getOrder(readCtx, h.objectID, lookupID)
				if result.err == nil && result.value.Order.ID != "" && resolvedIdentity.Load().(string) == "" {
					resolvedIdentity.Store(result.value.Order.ID)
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			}
			select {
			case snapshot <- result:
			case <-readCtx.Done():
				return
			}
			retry = nil
			if result.err != nil {
				retry = time.After(time.Duration(attempt+1) * 250 * time.Millisecond)
			}
		}
	}()
	for {
		mu.Lock()
		batch := events
		events = nil
		mu.Unlock()
		for _, ev := range batch {
			resolvedOrderID := resolvedIdentity.Load().(string)
			if ev.Operation != nil && ev.Operation.ID == settled.Operation.ID {
				if ev.Operation.State == OpFailed || ev.Operation.State == OpExpired {
					return zero, newOperationFailedError(ev.Operation.snapshot())
				}
				if result, known := operationExecution(*ev.Operation, h.objectID); known && (resolvedOrderID == "" || result.Order.ID == resolvedOrderID) {
					return result, nil
				}
			}
			if ev.Order != nil && ev.EntityID == h.objectID && resolvedOrderID == "" {
				// Preserve early account-scoped evidence until settlement supplies
				// this order's venue identity; never match an arbitrary account order.
				mu.Lock()
				if len(events) < 256 {
					events = append(events, ev)
				}
				mu.Unlock()
				continue
			}
			if ev.Order != nil && ev.EntityID == h.objectID && ev.Order.Order.ID == resolvedOrderID && isTerminalOrderStatus(ev.Order.Order.Status) {
				if evidence, valid := executionUpdate(settled.Operation, h.objectID, ev.Order.Order); valid {
					return evidence, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-wake:
		case result := <-snapshot:
			var failure *OperationFailedError
			if errors.As(result.err, &failure) && failure.Operation.ID == settled.Operation.ID &&
				(failure.Operation.State == string(OpFailed) || failure.Operation.State == string(OpExpired)) {
				if h.deps.releaseExecution != nil {
					h.deps.releaseExecution()
				}
				return zero, failure
			}
			resolvedOrderID := resolvedIdentity.Load().(string)
			if result.err == nil && (resolvedOrderID == "" || result.value.Order.ID == resolvedOrderID) && isTerminalOrderStatus(result.value.Order.Status) {
				if evidence, valid := executionUpdate(settled.Operation, h.objectID, result.value.Order); valid {
					return evidence, nil
				}
			}
			// A missing/stale historical receipt is not proof of failure. The stream
			// can still establish execution; the caller deadline bounds uncertainty.
		}
	}

}

// OnFill registers a callback for each fill on this order. It returns an
// unsubscribe function.
func (h *OrderHandle) OnFill(ctx context.Context, callback func(SimFill)) func() {
	if h.deps.watchLifecycle != nil {
		child, cancel := context.WithCancel(ctx)
		go func() { defer cancel(); _ = h.streamLifecycleFills(child, callback) }()
		return cancel
	}
	ctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	var queued []SimFill
	wake := make(chan struct{}, 1)
	// The dispatcher must never wait for submission: an optimistic fill can
	// arrive synchronously inside the very HTTP call whose completion we await.
	unsub := h.deps.onFillEvent(func(ev RealmEvent) {
		if ev.Fill == nil || ev.Fill.IsOptimistic || ctx.Err() != nil {
			return
		}
		mu.Lock()
		queued = append(queued, *ev.Fill)
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { cancel(); unsub() }) }
	go func() {
		defer stop()
		id, err := h.resolveOrderID(ctx)
		if err != nil {
			return
		}
		cloid := h.resolveCloid(ctx)
		seen := map[string]bool{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			mu.Lock()
			batch := queued
			queued = nil
			mu.Unlock()
			for _, fill := range batch {
				if ctx.Err() != nil {
					return
				}
				if fillMatches(&fill, id, cloid) {
					key := fill.FillID
					if key == "" {
						key = fill.ID
					}
					if key == "" || seen[key] {
						continue
					}
					seen[key] = true
					callback(fill)
				}
			}
		}
	}()
	return stop
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
		// A bracket child's fills carry the placement operation id, so the
		// OperationID match already correlates a still-pending child correctly
		// (the platform Fill record has no cloid to key on).
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

// Resize changes the order's total size to newSize. Only sized orders can be
// resized (resting limit orders and sized TP/SL triggers); unsized TP/SL
// triggers are rejected by the venue. newSize must exceed the already-filled
// quantity. It auto-generates a per-resize idempotency path from the placement
// path; pass a non-empty path to control idempotency explicitly.
func (h *OrderHandle) Resize(ctx context.Context, newSize, path string) (*OperationHandle[OrderOperationResponse], error) {
	orderID, err := h.resolveOrderID(ctx)
	if err != nil {
		return nil, err
	}
	modifyPath := path
	if modifyPath == "" {
		modifyPath = replaceFirst(h.placementPath, "/op/order/", "/op/modify/") + "-" + newSize
	}
	return h.deps.modifyOrder(ctx, ModifyOrderOptions{
		Path:     modifyPath,
		ObjectID: h.objectID,
		OrderID:  orderID,
		NewSize:  newSize,
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

// executionDisposition describes what is known without treating acceptance or
// a working partial fill as terminal. Amounts are decimal strings, never float64.
var executionDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

func executionDisposition(order SimOrder) (string, string) {
	if !executionDecimal.MatchString(order.FilledSize) {
		return "unknown", "unknown"
	}
	filled, ok := new(big.Rat).SetString(order.FilledSize)
	if !ok || filled.Sign() < 0 {
		return "unknown", "unknown"
	}
	switch normalizeExecutionStatus(order.Status) {
	case OrderFilled:
		if executionDecimal.MatchString(order.Size) {
			requested, _ := new(big.Rat).SetString(order.Size)
			if requested.Sign() > 0 && filled.Cmp(requested) < 0 {
				remainder := "unknown"
				if order.TimeInForce == "IOC" {
					remainder = "cancelled"
				}
				if filled.Sign() > 0 {
					return "partial", remainder
				}
				if remainder == "cancelled" {
					return "no_fill", remainder
				}
				return "unknown", "unknown"
			}
		}
		if filled.Sign() > 0 {
			return "filled", "filled"
		}
		return "unknown", "unknown"
	case OrderCancelled:
		if filled.Sign() > 0 {
			return "partial", "cancelled"
		}
		return "no_fill", "cancelled"
	case OrderFailed:
		if filled.Sign() > 0 {
			return "partial", "cancelled"
		}
		return "rejected", "cancelled"
	default:
		return "pending", "working"
	}
}

// ExecutionOutcome normalizes exact order evidence for confirmation and recovery.
// Unknown or malformed quantities cannot become a definitive execution verdict.
func (filled SimOrderWithFills) ExecutionOutcome() map[string]any {
	out := make(map[string]any)
	out["orderId"] = filled.Order.ID
	out["status"] = string(filled.Order.Status)
	out["filledSize"] = filled.Order.FilledSize
	out["fulfillmentState"] = "unknown"
	out["averagePriceFinal"] = false
	out["averagePriceSource"] = "venue_aggregate"
	if executionDecimal.MatchString(filled.Order.Size) && executionDecimal.MatchString(filled.Order.FilledSize) {
		requested, _ := new(big.Rat).SetString(filled.Order.Size)
		executed, _ := new(big.Rat).SetString(filled.Order.FilledSize)
		if requested.Cmp(executed) >= 0 {
			out["requestedSize"] = filled.Order.Size
			if executed.Sign() == 0 {
				out["fulfillmentState"] = "none"
			} else if requested.Cmp(executed) > 0 {
				out["fulfillmentState"] = "partial"
			} else {
				out["fulfillmentState"] = "full"
			}
			scale := 0
			for _, v := range []string{filled.Order.Size, filled.Order.FilledSize} {
				if dot := strings.IndexByte(v, '.'); dot >= 0 && len(v)-dot-1 > scale {
					scale = len(v) - dot - 1
				}
			}
			out["remainingSize"] = new(big.Rat).Sub(requested, executed).FloatString(scale)
		}
	}

	out["executionState"], out["remainingDisposition"] = executionDisposition(filled.Order)
	if out["executionState"] == "unknown" {
		out["status"] = "UNKNOWN"
	}
	if filled.FillsComplete != nil {
		out["fillsComplete"] = *filled.FillsComplete
	}
	if len(filled.Fills) > 0 {
		out["fills"] = filled.Fills
	}
	if filled.Order.AvgFillPrice != nil {
		out["avgFillPrice"] = *filled.Order.AvgFillPrice
	}
	return out
}
