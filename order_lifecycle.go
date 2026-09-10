package arca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// OriginalOrderReference addresses retained intent, including when the original
// placement response was lost. Exactly one of OperationID/OperationPath is set.
type OriginalOrderReference struct {
	ObjectID, OperationID, OperationPath string
	Leg                                  int
}

type OrderLifecycleKey struct {
	RealmID     string `json:"realmId"`
	ObjectID    string `json:"objectId"`
	OperationID string `json:"operationId"`
	Leg         string `json:"leg"`
}

type OrderLifecycleIntent struct {
	OrderLifecycleKey
	Venue                string `json:"venue"`
	VenueAccountID       string `json:"venueAccountId"`
	Market               string `json:"market"`
	RequestedSize        string `json:"requestedSize"`
	OrderType            string `json:"orderType"`
	Side                 string `json:"side"`
	TimeInForce          string `json:"timeInForce"`
	ExecutionTimeInForce string `json:"executionTimeInForce,omitempty"`
	ClientOrderID        string `json:"clientOrderId,omitempty"`
	RequestRef           string `json:"requestRef,omitempty"`
	Price                string `json:"price,omitempty"`
	TriggerKind          string `json:"triggerKind,omitempty"`
	TriggerPrice         string `json:"triggerPrice,omitempty"`
	OCOGroupID           string `json:"ocoGroupId,omitempty"`
	IsTrigger            bool   `json:"isTrigger"`
	IsMarketTrigger      bool   `json:"isMarketTrigger"`
	SizeToMax            bool   `json:"sizeToMax"`
	ReduceOnly           bool   `json:"reduceOnly"`
}

// OrderLifecycle is the server's projection. The SDK preserves exact quantities
// and finality; it never recomputes execution or accounting from client events.
type OrderLifecycleFill struct {
	SimFill
	ObjectID            string `json:"objectId"`
	OriginalOperationID string `json:"operationId"`
	Leg                 string `json:"leg"`
}

type OrderLifecycle struct {
	CommittedFills         []OrderLifecycleFill   `json:"committedFills,omitempty"`
	Intent                 OrderLifecycleIntent   `json:"intent"`
	VenueOrderID           string                 `json:"venueOrderId,omitempty"`
	Submission             string                 `json:"submission"`
	Working                bool                   `json:"working"`
	Execution              string                 `json:"execution"`
	Terminal               bool                   `json:"terminal"`
	ExecutedSize           string                 `json:"executedSize"`
	ExecutionQuantityFinal bool                   `json:"executionQuantityFinal"`
	RequestedSizeKnown     bool                   `json:"requestedSizeKnown"`
	RemainingSize          string                 `json:"remainingSize,omitempty"`
	RemainingDisposition   string                 `json:"remainingDisposition"`
	AccountedSize          string                 `json:"accountedSize"`
	AccountingComplete     bool                   `json:"accountingComplete"`
	AveragePrice           string                 `json:"averagePrice,omitempty"`
	AveragePriceFinal      bool                   `json:"averagePriceFinal"`
	RecoveryRequired       bool                   `json:"recoveryRequired"`
	ExecutionReceipt       *OrderExecutionReceipt `json:"executionReceipt,omitempty"`
}

var lifecycleQuantity = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

func (v *OrderLifecycle) UnmarshalJSON(data []byte) error {
	type wire OrderLifecycle
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var intent map[string]json.RawMessage
	if err := json.Unmarshal(fields["intent"], &intent); err != nil {
		return err
	}
	for _, group := range []struct {
		values map[string]json.RawMessage
		keys   []string
	}{
		{fields, []string{"working", "terminal", "executionQuantityFinal", "requestedSizeKnown", "accountingComplete", "averagePriceFinal", "recoveryRequired"}},
		{intent, []string{"isTrigger", "isMarketTrigger", "sizeToMax", "reduceOnly"}},
	} {
		for _, key := range group.keys {
			value := string(group.values[key])
			if value != "true" && value != "false" {
				return fmt.Errorf("missing or invalid lifecycle boolean %s", key)
			}
		}
	}
	var parsed wire
	if raw := fields["executionReceipt"]; len(raw) != 0 && string(raw) != "null" {
		var receipt map[string]json.RawMessage
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		for _, key := range []string{"averagePriceFinal", "fillsComplete"} {
			if value := string(receipt[key]); value != "true" && value != "false" {
				return fmt.Errorf("missing lifecycle receipt boolean %s", key)
			}
		}
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*v = OrderLifecycle(parsed)
	return nil
}

func validateLifecycle(view OrderLifecycle, key OrderLifecycleKey) error {
	for _, f := range view.CommittedFills {
		if f.ID == "" || f.IsOptimistic || f.ObjectID != key.ObjectID || f.OriginalOperationID != key.OperationID || f.Leg != key.Leg || f.RealmID != key.RealmID || f.AccountID != view.Intent.VenueAccountID || f.OrderID == "" || f.OrderID != view.VenueOrderID || f.Market != view.Intent.Market || string(f.Side) != view.Intent.Side || !lifecycleQuantity.MatchString(f.Size) || !lifecycleQuantity.MatchString(f.Price) {
			return fmt.Errorf("committed fill does not match original order")
		}
	}
	if r := view.ExecutionReceipt; r != nil && (r.ObjectID != key.ObjectID || r.OperationID != key.OperationID || r.Leg != key.Leg || r.OrderID != view.VenueOrderID || r.FilledSize != view.ExecutedSize || r.Market != view.Intent.Market || r.FillsComplete != view.AccountingComplete || r.AveragePriceFinal != view.AveragePriceFinal) {
		return fmt.Errorf("original order receipt identity does not match its server view")
	}
	if view.Intent.OrderLifecycleKey != key || key.OperationID == "" || len(key.OperationID) > 128 || view.Intent.Venue == "" || view.Intent.VenueAccountID == "" || view.Intent.Market == "" {
		return fmt.Errorf("original order evidence does not match account and operation")
	}
	if (view.Intent.OrderType != "MARKET" && view.Intent.OrderType != "LIMIT") || (view.Intent.Side != "buy" && view.Intent.Side != "sell") {
		return fmt.Errorf("original order intent is invalid")
	}
	for _, q := range []string{view.Intent.RequestedSize, view.ExecutedSize, view.AccountedSize} {
		if !lifecycleQuantity.MatchString(q) {
			return fmt.Errorf("original order quantity is invalid")
		}
	}
	for _, q := range []string{view.RemainingSize, view.AveragePrice} {
		if q != "" && !lifecycleQuantity.MatchString(q) {
			return fmt.Errorf("original order quantity is invalid")
		}
	}
	return nil
}

// GetOrderLifecycle reads an existing original operation; it cannot place one.
func (a *Arca) GetOrderLifecycle(ctx context.Context, ref OriginalOrderReference) (OrderLifecycle, error) {
	var result struct {
		Lifecycle OrderLifecycle `json:"lifecycle"`
	}
	if ref.ObjectID == "" || len(ref.ObjectID) > 128 || (ref.OperationID == "") == (ref.OperationPath == "") || len(ref.OperationID) > 128 || len(ref.OperationPath) > 1024 || (ref.OperationPath != "" && !strings.HasPrefix(ref.OperationPath, "/")) || ref.Leg < 0 {
		return result.Lifecycle, fmt.Errorf("an account, one original operation ID or absolute path, and a nonnegative leg are required")
	}
	if err := a.ensureReady(ctx); err != nil {
		return result.Lifecycle, err
	}
	query := url.Values{"leg": {strconv.Itoa(ref.Leg)}}
	if ref.OperationID != "" {
		query.Set("operationId", ref.OperationID)
	} else {
		query.Set("operationPath", ref.OperationPath)
	}
	if err := a.client.get(ctx, "/objects/"+url.PathEscape(ref.ObjectID)+"/exchange/order-lifecycle", query, &result); err != nil {
		return OrderLifecycle{}, err
	}
	id := ref.OperationID
	if id == "" {
		id = result.Lifecycle.Intent.OperationID
	}
	err := validateLifecycle(result.Lifecycle, OrderLifecycleKey{RealmID: a.currentRealmID(), ObjectID: ref.ObjectID, OperationID: id, Leg: strconv.Itoa(ref.Leg)})
	if err != nil {
		return OrderLifecycle{}, err
	}
	return result.Lifecycle, nil
}
