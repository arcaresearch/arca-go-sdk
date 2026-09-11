package arca

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// OrderHandleFor attaches to an order this client did not place.
//
// Every other order factory is a mutation: it submits an order and hands back
// a handle to it. An integration whose orders are submitted by its own backend
// holds an operation id and nothing else, and calling PlaceOrder again to
// obtain a handle would be a second submission, not a read. This performs one
// GetOperation read and returns a handle bound to that existing order, so
// Accounted, ExecutionReceipt, Filled, Fills and OnFill work on it exactly as
// they do on a placed order.
//
// Attaching is read-only: it places nothing, cancels nothing and resizes
// nothing. Cancel and Resize remain on the returned handle and are, as always,
// explicit mutations the caller opts into.
//
// An order that already completed its accounting resolves immediately; one
// still in flight converges on the same pushes a placed handle sees, because
// execution observers are installed before the read. As with PlaceOrder, ctx
// governs the handle's event capture — pass one that outlives the wait, not a
// per-request context.
//
// Returns ORDER_IDENTITY_MISMATCH when the operation is not an order, or
// records a different exchange account — an id from another account must never
// resolve into a handle on this one.
func (a *Arca) OrderHandleFor(ctx context.Context, objectID, operationID string) (*OrderHandle, error) {
	stream := a.newOrderStream(ctx, objectID)
	deps := a.orderHandleDeps()
	deps.releaseExecution = stream.stop
	deps.onExecutionEvent = stream.subscribe
	liveFills := deps.onFillEvent
	deps.onFillEvent = func(handler func(RealmEvent)) func() {
		live := liveFills(handler)
		replay := stream.subscribe(handler)
		return func() { live(); replay() }
	}
	deps.awaitExecutionReady = stream.awaitReady

	resp, err := a.readOrderOperation(ctx, objectID, operationID)
	if err != nil {
		stream.stop()
		return nil, err
	}
	stream.submitted(resp.Operation)

	base := newOperationHandle(
		func() (OrderOperationResponse, error) { return resp, nil },
		OrderOperationResponse.op, (*OrderOperationResponse).setOp,
		func(c context.Context, id string, t time.Duration) (*Operation, error) {
			return a.waitForOperation(c, id, t)
		},
		nil, 0)
	handle := newOrderHandle(base, objectID, resp.Operation.Path, deps)
	handle.immediate = recordedImmediateIntent(resp.Operation)
	return handle, nil
}

// readOrderOperation reads an existing order operation and refuses anything
// that is not this account's order.
func (a *Arca) readOrderOperation(ctx context.Context, objectID, operationID string) (OrderOperationResponse, error) {
	var zero OrderOperationResponse
	detail, err := a.GetOperation(ctx, operationID, nil)
	if err != nil {
		return zero, err
	}
	operation := detail.Operation
	if operation.Type != OpOrder {
		return zero, newArcaError("ORDER_IDENTITY_MISMATCH",
			"Operation "+operationID+" is a "+string(operation.Type)+" operation, not an order", "")
	}
	// Absent input is not a mismatch — older operations may not carry it — but
	// a present and different account is.
	if account := recordedOrderIntent(operation).ObjectID; account != "" && account != objectID {
		return zero, newArcaError("ORDER_IDENTITY_MISMATCH",
			"Operation "+operationID+" belongs to a different exchange account", "")
	}
	return OrderOperationResponse{Operation: operation}, nil
}

// orderIntent is the subset of an order operation's immutable recorded input
// that attach needs: which account placed it, and whether it was an immediate
// order (which decides what Confirmed waits for).
type orderIntent struct {
	ObjectID    string `json:"exchangeObjectId"`
	OrderType   string `json:"orderType"`
	TimeInForce string `json:"timeInForce"`
	IsTrigger   bool   `json:"isTrigger"`
}

func recordedOrderIntent(operation Operation) orderIntent {
	var intent orderIntent
	if operation.Input == nil || *operation.Input == "" {
		return intent
	}
	if json.Unmarshal([]byte(*operation.Input), &intent) != nil {
		return orderIntent{}
	}
	return intent
}

// recordedImmediateIntent reconstructs PlaceOrder's `immediate` classification
// from what the operation recorded, so Confirmed waits for execution on an
// attached market/IOC order and for placement on an attached resting one.
func recordedImmediateIntent(operation Operation) bool {
	intent := recordedOrderIntent(operation)
	if intent.IsTrigger {
		return false
	}
	return intent.OrderType == "" ||
		strings.EqualFold(intent.OrderType, "MARKET") ||
		strings.EqualFold(intent.TimeInForce, "IOC") ||
		strings.EqualFold(intent.TimeInForce, "FOK")
}
