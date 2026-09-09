package arca

import (
	"context"
	"encoding/json"
)

// GetOperationOrder reads fresh execution evidence for an existing operation.
// This method never places an order. The caller must use the original account;
// missing receipts remain unresolved, including for terminal operations.
func (a *Arca) GetOperationOrder(ctx context.Context, objectID string, operation Operation) (SimOrderWithFills, error) {
	outcome := operation.ParsedOutcome
	if operation.Outcome != nil && *operation.Outcome != "" {
		var decoded map[string]any
		if json.Unmarshal([]byte(*operation.Outcome), &decoded) == nil {
			outcome = decoded
		}
	}
	id, _ := outcome["orderId"].(string)
	if id == "" {
		id = operation.ID
	}
	if id == "" {
		return SimOrderWithFills{}, newArcaError("ORDER_UNRESOLVED", "Order execution identity is not available", "")
	}
	out, err := a.GetOrder(ctx, objectID, id)
	if err != nil {
		return out, err
	}
	// An operation id may resolve to a venue order id. If an actual order id
	// was already supplied, a different id cannot be evidence for this order.
	expected, _ := outcome["orderId"].(string)
	if expected != "" && out.Order.ID != expected {
		return SimOrderWithFills{}, newArcaError("ORDER_IDENTITY_MISMATCH", "Order execution identity does not match", "")
	}
	return out, nil
}

// operationExecution accepts an authoritative terminal aggregate, never mere settlement.
// Aggregate execution does not imply completeness of the individual fill history.
func operationExecution(operation Operation, objectID string) (SimOrderWithFills, bool) {
	if operation.State != OpCompleted {
		return SimOrderWithFills{}, false
	}
	raw, err := json.Marshal(operation.ParsedOutcome)
	if operation.Outcome != nil && *operation.Outcome != "" {
		raw = []byte(*operation.Outcome)
	}
	if err != nil {
		return SimOrderWithFills{}, false
	}
	var value struct {
		OrderID       string    `json:"orderId"`
		Status        string    `json:"status"`
		FilledSize    string    `json:"filledSize"`
		AvgFillPrice  *string   `json:"avgFillPrice"`
		Fills         []SimFill `json:"fills"`
		FillsComplete *bool     `json:"fillsComplete"`
	}
	if json.Unmarshal(raw, &value) != nil || value.OrderID == "" {
		return SimOrderWithFills{}, false
	}
	status := normalizeExecutionStatus(OrderStatus(value.Status))
	order := SimOrder{ID: value.OrderID, AccountID: "", Status: status, FilledSize: value.FilledSize, AvgFillPrice: value.AvgFillPrice}
	// Input is the immutable original intent, not a reconstructed size from
	// the current balance. It supplies requested quantity for partial execution.
	if operation.Input != nil {
		var intent struct {
			ObjectID    string    `json:"exchangeObjectId"`
			Market      string    `json:"market"`
			Side        OrderSide `json:"side"`
			OrderType   string    `json:"orderType"`
			Size        string    `json:"size"`
			TimeInForce string    `json:"timeInForce"`
			GLLPrepared *struct {
				Request struct {
					Effect int `json:"Effect"`
				} `json:"request"`
			} `json:"gllPrepared"`
		}
		if json.Unmarshal([]byte(*operation.Input), &intent) == nil {
			if intent.ObjectID != "" && intent.ObjectID != objectID {
				return SimOrderWithFills{}, false
			}
			order.Market = intent.Market
			order.Side = intent.Side
			order.OrderType = intent.OrderType
			order.Size = intent.Size
			order.TimeInForce = intent.TimeInForce
			// The signed venue request records the real time-in-force; GLL market
			// orders are sent as a marketable IOC limit even if generic input says GTC.
			if intent.GLLPrepared != nil && intent.GLLPrepared.Request.Effect == 1 {
				order.TimeInForce = "IOC"
			}
		}
	}
	disposition, _ := executionDisposition(order)
	if !isTerminalOrderStatus(status) || disposition == "unknown" {
		return SimOrderWithFills{}, false
	}
	complete := false
	if value.FillsComplete != nil {
		complete = *value.FillsComplete
	}
	return SimOrderWithFills{Order: order, Fills: value.Fills, FillsComplete: &complete}, true
}

// executionUpdate joins a correlated observation to immutable original intent.
// Venue history may rewrite size to executed size, so never copy its size here.
func executionUpdate(operation Operation, objectID string, order SimOrder) (SimOrderWithFills, bool) {
	raw, err := json.Marshal(map[string]any{"orderId": order.ID, "status": order.Status, "filledSize": order.FilledSize, "avgFillPrice": order.AvgFillPrice})
	if err != nil {
		return SimOrderWithFills{}, false
	}
	outcome := string(raw)
	operation.Outcome = &outcome
	operation.State = OpCompleted
	return operationExecution(operation, objectID)
}
