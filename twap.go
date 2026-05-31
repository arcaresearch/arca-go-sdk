package arca

import (
	"context"
	"net/url"
)

// PlaceTwap starts a TWAP order that executes a total size over a duration.
func (a *Arca) PlaceTwap(ctx context.Context, opts PlaceTwapOptions) *OperationHandle[TwapOperationResponse] {
	return op(a, ctx, func() (TwapOperationResponse, error) {
		body := map[string]any{
			"realmId":         a.currentRealmID(),
			"path":            opts.Path,
			"coin":            opts.Coin,
			"side":            opts.Side,
			"totalSize":       opts.TotalSize,
			"durationMinutes": opts.DurationMinutes,
		}
		if opts.IntervalSeconds != nil {
			body["intervalSeconds"] = *opts.IntervalSeconds
		}
		if opts.Randomize != nil {
			body["randomize"] = *opts.Randomize
		}
		if opts.ReduceOnly != nil {
			body["reduceOnly"] = *opts.ReduceOnly
		}
		if opts.Leverage != nil {
			body["leverage"] = *opts.Leverage
		}
		if opts.SlippageBps != nil {
			body["slippageBps"] = *opts.SlippageBps
		}
		var resp TwapOperationResponse
		err := a.client.post(ctx, "/objects/"+opts.ExchangeID+"/exchange/twap", body, &resp)
		return resp, err
	}, TwapOperationResponse.op, (*TwapOperationResponse).setOp, nil, 0)
}

// CancelTwap cancels an active TWAP (idempotent).
func (a *Arca) CancelTwap(ctx context.Context, exchangeID, operationID string) *OperationHandle[TwapOperationResponse] {
	return op(a, ctx, func() (TwapOperationResponse, error) {
		var resp TwapOperationResponse
		q := url.Values{"realmId": {a.currentRealmID()}}
		err := a.client.delete(ctx, "/objects/"+exchangeID+"/exchange/twap/"+operationID+"?"+q.Encode(), &resp)
		return resp, err
	}, TwapOperationResponse.op, (*TwapOperationResponse).setOp, nil, 0)
}

// GetTwap returns TWAP status and progress by parent operation id.
func (a *Arca) GetTwap(ctx context.Context, exchangeID, operationID string) (TwapOperationResponse, error) {
	var out TwapOperationResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/objects/"+exchangeID+"/exchange/twap/"+operationID, url.Values{"realmId": {rid}}, &out)
	return out, err
}

// ListTwaps lists TWAPs for an exchange object.
func (a *Arca) ListTwaps(ctx context.Context, exchangeID string, activeOnly bool) ([]Twap, error) {
	rid, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	params := url.Values{"realmId": {rid}}
	if activeOnly {
		params.Set("active", "true")
	}
	var out []Twap
	err = a.client.get(ctx, "/objects/"+exchangeID+"/exchange/twaps", params, &out)
	return out, err
}

// GetTwapLimits returns TWAP limits + recommendation curve. Cached for the
// process lifetime.
func (a *Arca) GetTwapLimits(ctx context.Context) (TwapLimits, error) {
	a.twapMu.Lock()
	if a.twapLimits != nil {
		out := *a.twapLimits
		a.twapMu.Unlock()
		return out, nil
	}
	a.twapMu.Unlock()
	var out TwapLimits
	if err := a.client.get(ctx, "/twap/limits", nil, &out); err != nil {
		return out, err
	}
	a.twapMu.Lock()
	a.twapLimits = &out
	a.twapMu.Unlock()
	return out, nil
}

// RecommendedIntervalSeconds returns the recommended slice interval for a TWAP
// of the given duration.
func (a *Arca) RecommendedIntervalSeconds(ctx context.Context, durationMinutes int) (int, error) {
	limits, err := a.GetTwapLimits(ctx)
	if err != nil {
		return 0, err
	}
	for _, b := range limits.Recommendation.Buckets {
		if durationMinutes <= b.MaxDurationMinutes {
			return b.RecommendedIntervalSeconds, nil
		}
	}
	return limits.Limits.DefaultIntervalSeconds, nil
}
