package arca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// EnsurePerpsExchange creates (or returns) a perpetuals exchange Arca object.
// The default settlement wait is 60s (exchange creation is slower than transfers).
func (a *Arca) EnsurePerpsExchange(ctx context.Context, opts CreatePerpsExchangeOptions) *OperationHandle[EnsureArcaObjectResponse] {
	exType := opts.ExchangeType
	if exType == "" {
		exType = "hyperliquid"
	}
	meta, _ := json.Marshal(map[string]string{"exchangeType": exType})
	return op(a, ctx, func() (EnsureArcaObjectResponse, error) {
		var resp EnsureArcaObjectResponse
		err := a.client.post(ctx, "/objects", map[string]any{
			"realmId":       a.currentRealmID(),
			"path":          opts.Ref,
			"type":          "exchange",
			"metadata":      string(meta),
			"operationPath": nilIfEmpty(opts.OperationPath),
		}, &resp)
		return resp, err
	}, EnsureArcaObjectResponse.op, (*EnsureArcaObjectResponse).setOp, nil, 60*time.Second)
}

// GetExchangeState returns equity, margin, positions, and orders for an
// exchange object.
func (a *Arca) GetExchangeState(ctx context.Context, objectID string) (ExchangeState, error) {
	var out ExchangeState
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/state", nil, &out)
	return out, err
}

// GetActiveAssetData returns max trade sizes, available margin, mark price, and
// fee rate for a coin on an exchange object.
func (a *Arca) GetActiveAssetData(ctx context.Context, objectID, coin string, builderFeeBps, leverage int) (ActiveAssetData, error) {
	var out ActiveAssetData
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	params := url.Values{"coin": {coin}}
	if builderFeeBps > 0 {
		params.Set("builderFeeBps", strconv.Itoa(builderFeeBps))
	}
	if leverage > 0 {
		params.Set("leverage", strconv.Itoa(leverage))
	}
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/active-asset-data", params, &out)
	return out, err
}

// GetAssetFees returns effective taker/maker fee rates for every tradeable
// asset on an exchange account.
func (a *Arca) GetAssetFees(ctx context.Context, objectID string, builderFeeBps int) ([]AssetFeeEntry, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	params := url.Values{}
	if builderFeeBps > 0 {
		params.Set("builderFeeBps", strconv.Itoa(builderFeeBps))
	}
	var out []AssetFeeEntry
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/asset-fees", params, &out)
	return out, err
}

// UpdateLeverage sets per-coin leverage on an exchange object.
func (a *Arca) UpdateLeverage(ctx context.Context, opts UpdateLeverageOptions) (UpdateLeverageResponse, error) {
	var out UpdateLeverageResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.post(ctx, "/objects/"+opts.ObjectID+"/exchange/leverage",
		map[string]any{"coin": opts.Coin, "leverage": opts.Leverage}, &out)
	return out, err
}

// ListLeverageSettings returns all per-coin leverage settings for an exchange
// object.
func (a *Arca) ListLeverageSettings(ctx context.Context, objectID string) ([]LeverageSetting, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	var out []LeverageSetting
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/leverage", nil, &out)
	return out, err
}

// GetLeverage returns the leverage setting for a single coin on an exchange
// object.
func (a *Arca) GetLeverage(ctx context.Context, objectID, coin string) (LeverageSetting, error) {
	var out LeverageSetting
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/leverage", url.Values{"coin": {coin}}, &out)
	return out, err
}

func (a *Arca) orderHandleDeps() orderHandleDeps {
	return orderHandleDeps{
		getOrder: func(ctx context.Context, objectID, orderID string) (SimOrderWithFills, error) {
			return a.GetOrder(ctx, objectID, orderID)
		},
		onFillEvent: func(handler func(RealmEvent)) func() {
			a.ws.EnsureConnected()
			return a.ws.On(EventExchangeFill, handler)
		},
		cancelOrder: func(ctx context.Context, opts CancelOrderOptions) *OperationHandle[OrderOperationResponse] {
			return a.CancelOrder(ctx, opts)
		},
		listFills: func(ctx context.Context, objectID string) (FillListResponse, error) {
			return a.ListFills(ctx, objectID, nil)
		},
	}
}

func (a *Arca) emitOptimisticFill(operation Operation, coin string, side OrderSide, path, price string) {
	if operation.Outcome == nil || *operation.Outcome == "" {
		return
	}
	var outcome struct {
		FilledSize   string `json:"filledSize"`
		OrderID      string `json:"orderId"`
		AvgFillPrice string `json:"avgFillPrice"`
		Cloid        string `json:"cloid"`
	}
	if json.Unmarshal([]byte(*operation.Outcome), &outcome) != nil {
		return
	}
	if outcome.FilledSize == "" {
		return
	}
	if f, err := strconv.ParseFloat(outcome.FilledSize, 64); err != nil || f <= 0 {
		return
	}
	fillPrice := outcome.AvgFillPrice
	if fillPrice == "" {
		fillPrice = price
	}
	if fillPrice == "" {
		fillPrice = "0"
	}
	a.ws.EmitLocal(RealmEvent{
		Type:       EventExchangeFill,
		RealmID:    a.currentRealmID(),
		EntityPath: path,
		Fill: &SimFill{
			ID:            fmt.Sprintf("fil_opt_%d", msNow()),
			OrderID:       outcome.OrderID,
			Coin:          coin,
			Side:          side,
			Price:         fillPrice,
			Size:          outcome.FilledSize,
			Fee:           "0",
			RealizedPnl:   nil,
			IsLiquidation: false,
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
			IsOptimistic:  true,
			Cloid:         outcome.Cloid,
		},
	})
}

// PlaceOrder places an order on an exchange object. The operation Path is the
// idempotency key. Returns an OrderHandle: Wait blocks until placement, then
// use Filled/OnFill/FillSummary/Cancel for the fill lifecycle.
func (a *Arca) PlaceOrder(ctx context.Context, opts PlaceOrderOptions) *OrderHandle {
	call := func() (OrderOperationResponse, error) {
		var resp OrderOperationResponse
		if err := a.ensureReady(ctx); err != nil {
			return resp, err
		}
		body := map[string]any{
			"realmId":     a.currentRealmID(),
			"path":        opts.Path,
			"coin":        opts.Coin,
			"side":        opts.Side,
			"orderType":   opts.OrderType,
			"size":        opts.Size,
			"reduceOnly":  opts.ReduceOnly,
			"timeInForce": defaultStr(opts.TimeInForce, "GTC"),
		}
		if opts.Price != "" {
			body["price"] = opts.Price
		}
		if opts.Leverage != nil {
			body["leverage"] = *opts.Leverage
		}
		if opts.BuilderFeeBps != nil {
			body["builderFeeBps"] = *opts.BuilderFeeBps
		}
		if opts.FeeTargets != nil {
			body["feeTargets"] = opts.FeeTargets
		}
		if opts.IsTrigger {
			body["isTrigger"] = true
		}
		if opts.TriggerPx != "" {
			body["triggerPx"] = opts.TriggerPx
		}
		if opts.IsMarket != nil {
			body["isMarket"] = *opts.IsMarket
		}
		if opts.Tpsl != "" {
			body["tpsl"] = opts.Tpsl
		}
		if opts.Grouping != "" {
			body["grouping"] = opts.Grouping
		}
		if opts.UseMax {
			body["useMax"] = true
		}
		if opts.SizeTolerance != nil {
			body["sizeTolerance"] = *opts.SizeTolerance
		}
		if opts.Isolated {
			body["isolated"] = true
		}
		if err := a.client.post(ctx, "/objects/"+opts.ObjectID+"/exchange/orders", body, &resp); err != nil {
			return resp, err
		}
		if resp.Operation.State == OpFailed || resp.Operation.State == OpExpired {
			return resp, newOperationFailedError(resp.Operation.snapshot())
		}
		a.emitOptimisticFill(resp.Operation, opts.Coin, opts.Side, opts.Path, opts.Price)
		return resp, nil
	}
	base := newOperationHandle(call, OrderOperationResponse.op, (*OrderOperationResponse).setOp,
		func(c context.Context, id string, t time.Duration) (*Operation, error) {
			return a.waitForOperation(c, id, t)
		},
		nil, 0)
	return newOrderHandle(base, opts.ObjectID, opts.Path, a.orderHandleDeps())
}

// ListOrders lists orders for an exchange object, optionally filtered by status.
func (a *Arca) ListOrders(ctx context.Context, objectID, status string) ([]SimOrder, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	params := url.Values{}
	if status != "" {
		params.Set("status", status)
	}
	var out OrderListResponse
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/orders", params, &out)
	return out.Orders, err
}

// GetOrder fetches a specific order with its fills.
func (a *Arca) GetOrder(ctx context.Context, objectID, orderID string) (SimOrderWithFills, error) {
	var out SimOrderWithFills
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/orders/"+orderID, nil, &out)
	return out, err
}

// CancelOrder cancels an order. The operation Path is the idempotency key.
func (a *Arca) CancelOrder(ctx context.Context, opts CancelOrderOptions) *OperationHandle[OrderOperationResponse] {
	return op(a, ctx, func() (OrderOperationResponse, error) {
		var resp OrderOperationResponse
		q := url.Values{"realmId": {a.currentRealmID()}, "path": {opts.Path}}
		err := a.client.delete(ctx, "/objects/"+opts.ObjectID+"/exchange/orders/"+opts.OrderID+"?"+q.Encode(), &resp)
		return resp, err
	}, OrderOperationResponse.op, (*OrderOperationResponse).setOp, nil, 0)
}

// ListPositions lists open positions for an exchange object.
func (a *Arca) ListPositions(ctx context.Context, objectID string) ([]SimPosition, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	var out PositionListResponse
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/positions", nil, &out)
	return out.Positions, err
}

// ClosePosition closes an open position (fully or partially) with reduceOnly
// enforced. Auto-fills isolated/leverage from the position and market metadata.
func (a *Arca) ClosePosition(ctx context.Context, opts ClosePositionOptions) *OrderHandle {
	call := func() (OrderOperationResponse, error) {
		var resp OrderOperationResponse
		positions, err := a.ListPositions(ctx, opts.ObjectID)
		if err != nil {
			return resp, err
		}
		var position *SimPosition
		for i := range positions {
			if positions[i].Coin == opts.Coin {
				position = &positions[i]
				break
			}
		}
		if position == nil {
			return resp, &NotFoundError{newArcaError("NOT_FOUND", "No open position for "+opts.Coin, "")}
		}
		side := Buy
		if position.Side == Long {
			side = Sell
		}
		size := position.Size
		if opts.Size != "" {
			req, _ := strconv.ParseFloat(opts.Size, 64)
			avail, _ := strconv.ParseFloat(position.Size, 64)
			if req > avail {
				size = position.Size
			} else {
				size = opts.Size
			}
		}
		leverage := position.Leverage
		if opts.Leverage != nil {
			leverage = *opts.Leverage
		}
		isolated := false
		if opts.Isolated != nil {
			isolated = *opts.Isolated
		} else if meta, merr := a.Asset(ctx, opts.Coin); merr == nil && meta != nil {
			isolated = meta.OnlyIsolated
		}

		if err := a.ensureReady(ctx); err != nil {
			return resp, err
		}
		body := map[string]any{
			"realmId":     a.currentRealmID(),
			"path":        opts.Path,
			"coin":        opts.Coin,
			"side":        side,
			"orderType":   "MARKET",
			"size":        size,
			"reduceOnly":  true,
			"timeInForce": defaultStr(opts.TimeInForce, "IOC"),
		}
		if leverage > 0 {
			body["leverage"] = leverage
		}
		if isolated {
			body["isolated"] = true
		}
		if opts.BuilderFeeBps != nil {
			body["builderFeeBps"] = *opts.BuilderFeeBps
		}
		if opts.FeeTargets != nil {
			body["feeTargets"] = opts.FeeTargets
		}
		if err := a.client.post(ctx, "/objects/"+opts.ObjectID+"/exchange/orders", body, &resp); err != nil {
			return resp, err
		}
		if resp.Operation.State == OpFailed || resp.Operation.State == OpExpired {
			return resp, newOperationFailedError(resp.Operation.snapshot())
		}
		a.emitOptimisticFill(resp.Operation, opts.Coin, side, opts.Path, "")
		return resp, nil
	}
	base := newOperationHandle(call, OrderOperationResponse.op, (*OrderOperationResponse).setOp,
		func(c context.Context, id string, t time.Duration) (*Operation, error) {
			return a.waitForOperation(c, id, t)
		},
		nil, 0)
	return newOrderHandle(base, opts.ObjectID, opts.Path, a.orderHandleDeps())
}

// ListFills lists historical fills for an exchange object.
func (a *Arca) ListFills(ctx context.Context, objectID string, opts *ListFillsOptions) (FillListResponse, error) {
	var out FillListResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	params := url.Values{}
	if opts != nil {
		if opts.Market != "" {
			params.Set("market", opts.Market)
		}
		if opts.StartTime != "" {
			params.Set("startTime", opts.StartTime)
		}
		if opts.EndTime != "" {
			params.Set("endTime", opts.EndTime)
		}
		if opts.Limit > 0 {
			params.Set("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Cursor != "" {
			params.Set("cursor", opts.Cursor)
		}
	}
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/fills", params, &out)
	return out, err
}

// TradeSummary returns per-market P&L aggregation for an exchange object.
func (a *Arca) TradeSummary(ctx context.Context, objectID string, opts *TradeSummaryOptions) (TradeSummaryResponse, error) {
	var out TradeSummaryResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	params := url.Values{}
	if opts != nil {
		if opts.StartTime != "" {
			params.Set("startTime", opts.StartTime)
		}
		if opts.EndTime != "" {
			params.Set("endTime", opts.EndTime)
		}
	}
	err := a.client.get(ctx, "/objects/"+objectID+"/exchange/trade-summary", params, &out)
	return out, err
}

// GetOrderLimits returns venue-wide order limits (e.g. the $10 minimum
// notional). Static; no network call.
func (a *Arca) GetOrderLimits() OrderLimits {
	return OrderLimits{MinOrderNotionalUsd: 10}
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
