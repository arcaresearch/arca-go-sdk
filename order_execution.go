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
