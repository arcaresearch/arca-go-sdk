package arca

import (
	"context"
	"encoding/json"
	"math/big"
)

// OrderExecutionReceipt is terminal execution evidence, not a complete order
// record or ledger history. RequestedSize is the original intent, which can
// differ from the venue's terminal history size. AveragePriceFinal is false
// for a venue aggregate whose precision is not established by complete fills.
type OrderExecutionReceipt struct {
	ObjectID             string  `json:"objectId"`
	OperationID          string  `json:"operationId"`
	Market               string  `json:"market,omitempty"`
	FulfillmentState     string  `json:"fulfillmentState"`
	AveragePriceSource   string  `json:"averagePriceSource"`
	OrderID              string  `json:"orderId"`
	Status               string  `json:"status"`
	FilledSize           string  `json:"filledSize"`
	RequestedSize        *string `json:"requestedSize,omitempty"`
	RemainingSize        *string `json:"remainingSize,omitempty"`
	ExecutionState       string  `json:"executionState"`
	RemainingDisposition string  `json:"remainingDisposition"`
	AvgFillPrice         *string `json:"avgFillPrice,omitempty"`
	AveragePriceFinal    bool    `json:"averagePriceFinal"`
	FillsComplete        bool    `json:"fillsComplete"`
}

// ExecutionReceipt resolves from an authoritative terminal response or correlated
// execution push. It never waits for every individual fill to be journaled.
func (h *OrderHandle) ExecutionReceipt(ctx context.Context) (OrderExecutionReceipt, error) {
	evidence, err := h.waitExecutionEvidence(ctx)
	if err != nil {
		return OrderExecutionReceipt{}, err
	}
	if h.deps.releaseExecution != nil {
		h.deps.releaseExecution()
	}
	submitted, err := h.Submitted(ctx)
	if err != nil {
		return OrderExecutionReceipt{}, err
	}
	return executionReceipt(evidence, submitted.Operation.ID, h.objectID)
}

func executionReceipt(evidence SimOrderWithFills, operationID, objectID string) (OrderExecutionReceipt, error) {
	body, err := json.Marshal(evidence.ExecutionOutcome())
	if err != nil {
		return OrderExecutionReceipt{}, err
	}
	var receipt OrderExecutionReceipt
	err = json.Unmarshal(body, &receipt)
	receipt.ObjectID, receipt.OperationID, receipt.Market = objectID, operationID, evidence.Order.Market
	return receipt, err
}

// GetOperationExecutionReceipt recovers terminal execution from the original
// operation before reading venue history. It never submits a replacement order.
func (a *Arca) GetOperationExecutionReceipt(ctx context.Context, objectID string, operation Operation) (OrderExecutionReceipt, error) {
	if operation.Input != nil {
		var intent struct {
			ObjectID string `json:"exchangeObjectId"`
		}
		if json.Unmarshal([]byte(*operation.Input), &intent) == nil && intent.ObjectID != "" && intent.ObjectID != objectID {
			return OrderExecutionReceipt{}, newArcaError("ORDER_IDENTITY_MISMATCH", "Operation belongs to a different exchange account", "")
		}
	}
	if operation.State == OpFailed || operation.State == OpExpired {
		return OrderExecutionReceipt{}, newOperationFailedError(operation.snapshot())
	}
	if evidence, valid := operationExecution(operation, objectID); valid {
		return executionReceipt(evidence, operation.ID, objectID)
	}
	detail, err := a.GetOperationOrder(ctx, objectID, operation)
	if err != nil {
		return OrderExecutionReceipt{}, err
	}
	evidence, valid := executionUpdate(operation, objectID, detail.Order)
	if !valid {
		return OrderExecutionReceipt{}, newArcaError("ORDER_UNRESOLVED", "Terminal execution evidence is not available", "")
	}
	return executionReceipt(evidence, operation.ID, objectID)
}

func (r OrderExecutionReceipt) Outcome() map[string]any {
	encoded, _ := json.Marshal(r)
	var out map[string]any
	_ = json.Unmarshal(encoded, &out)
	return out
}

// Filled retains its full-order return contract. ExecutionReceipt is the prompt
// confirmation API. This method materializes the full order once after terminal
// proof; unavailable details remain an error, never fabricated metadata.
func (h *OrderHandle) Filled(ctx context.Context) (SimOrderWithFills, error) {
	receipt, err := h.ExecutionReceipt(ctx)
	if err != nil {
		return SimOrderWithFills{}, err
	}
	detail, err := h.deps.getOrder(ctx, h.objectID, receipt.OrderID)
	if err != nil {
		return detail, err
	}
	if detail.Order.ID != receipt.OrderID {
		return SimOrderWithFills{}, newArcaError("ORDER_IDENTITY_MISMATCH", "Order details do not match the execution receipt", "")
	}
	executed, validExecuted := new(big.Rat).SetString(receipt.FilledSize)
	materialized, validMaterialized := new(big.Rat).SetString(detail.Order.FilledSize)
	if !isTerminalOrderStatus(detail.Order.Status) || !executionDecimal.MatchString(detail.Order.FilledSize) || !validExecuted || !validMaterialized || executed.Cmp(materialized) != 0 {
		return SimOrderWithFills{}, newArcaError("ORDER_DETAILS_PENDING", "Execution completed; matching terminal order details are not available yet", "")
	}
	return detail, nil
}
