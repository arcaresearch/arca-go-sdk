package arca

import (
	"context"
	"net/url"
	"strconv"
)

// HistoryScopeOptions scopes equity/PnL history. Default (Kind "path" or "")
// aggregates across the prefix; Kind "object" charts a single object by id.
type HistoryScopeOptions struct {
	Kind     string // "path" (default) or "object"
	ObjectID string // required when Kind == "object"
}

// PnlHistoryOptions extends HistoryScopeOptions with an anchor. Anchor "zero"
// (default) is a standard P&L series; "equity" shifts P&L by external flows.
type PnlHistoryOptions struct {
	HistoryScopeOptions
	Anchor string // "zero" (default) or "equity"
}

func resolveHistoryScope(kind, objectID string) (string, string) {
	if kind == "object" {
		return "object", objectID
	}
	return "path", ""
}

// GetPathAggregation returns aggregated valuation for objects at a path.
func (a *Arca) GetPathAggregation(ctx context.Context, path string, asOf string) (PathAggregation, error) {
	var out PathAggregation
	if err := validatePath(path); err != nil {
		return out, err
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}, "prefix": {path}}
	if asOf != "" {
		params.Set("asOf", asOf)
	}
	err = a.client.get(ctx, "/objects/aggregate", params, &out)
	return out, err
}

// GetPnl returns P&L for objects at a path over a time range.
func (a *Arca) GetPnl(ctx context.Context, path, from, to string) (PnlResponse, error) {
	var out PnlResponse
	if err := validatePath(path); err != nil {
		return out, err
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/objects/pnl", url.Values{"realmId": {rid}, "prefix": {path}, "from": {from}, "to": {to}}, &out)
	return out, err
}

type v2HistoryPoint struct {
	Ts             string           `json:"ts"`
	EquityUsd      string           `json:"equityUsd"`
	Status         ChartPointStatus `json:"status,omitempty"`
	CumInflowsUsd  string           `json:"cumInflowsUsd,omitempty"`
	CumOutflowsUsd string           `json:"cumOutflowsUsd,omitempty"`
	LastEventOpID  string           `json:"lastEventOpId,omitempty"`
	MidSetID       string           `json:"midSetId,omitempty"`
	PnlUsd         string           `json:"pnlUsd,omitempty"`
	ValueUsd       string           `json:"valueUsd,omitempty"`
}

type v2HistoryResponse struct {
	Resolution          string           `json:"resolution"`
	ResolutionRequested string           `json:"resolutionRequested"`
	ServerNow           string           `json:"serverNow"`
	Points              []v2HistoryPoint `json:"points"`
}

type v2PnlHistoryResponse struct {
	Resolution          string              `json:"resolution"`
	ResolutionRequested string              `json:"resolutionRequested"`
	ServerNow           string              `json:"serverNow"`
	StartEquityUsd      string              `json:"startEquityUsd"`
	StartingEquityUsd   string              `json:"startingEquityUsd"`
	EffectiveFrom       string              `json:"effectiveFrom"`
	ExternalFlows       []ExternalFlowEntry `json:"externalFlows"`
	MidPrices           map[string]string   `json:"midPrices"`
	Points              []v2HistoryPoint    `json:"points"`
}

// GetEquityHistory returns an equity time series for objects at a path.
func (a *Arca) GetEquityHistory(ctx context.Context, path, from, to string, points int, opts *HistoryScopeOptions) (EquityHistoryResponse, error) {
	var out EquityHistoryResponse
	if err := validatePath(path); err != nil {
		return out, err
	}
	if points <= 0 {
		points = 1000
	}
	kind, objectID := "path", ""
	if opts != nil {
		kind, objectID = resolveHistoryScope(opts.Kind, opts.ObjectID)
	}
	key := buildCacheKey("equityHistory", map[string]string{"target": path, "kind": kind, "objectId": objectID, "from": from, "to": to, "points": strconv.Itoa(points)})
	if cached, ok := a.cache.get(key); ok {
		return cached.(EquityHistoryResponse), nil
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}, "target": {path}, "kind": {kind}, "from": {from}, "to": {to}, "points": {strconv.Itoa(points)}}
	if objectID != "" {
		params.Set("objectId", objectID)
	}
	var v2 v2HistoryResponse
	if err := a.client.get(ctx, "/objects/aggregate/history", params, &v2); err != nil {
		return out, err
	}
	out = normalizeV2EquityHistory(path, from, to, v2)
	a.cache.set(key, out)
	return out, nil
}

// GetPnlHistory returns a P&L time series for objects at a path.
func (a *Arca) GetPnlHistory(ctx context.Context, path, from, to string, points int, opts *PnlHistoryOptions) (PnlHistoryResponse, error) {
	var out PnlHistoryResponse
	if err := validatePath(path); err != nil {
		return out, err
	}
	if points <= 0 {
		points = 1000
	}
	anchor := "zero"
	kind, objectID := "path", ""
	if opts != nil {
		if opts.Anchor != "" {
			anchor = opts.Anchor
		}
		kind, objectID = resolveHistoryScope(opts.Kind, opts.ObjectID)
	}
	key := buildCacheKey("pnlHistory", map[string]string{"target": path, "kind": kind, "objectId": objectID, "from": from, "to": to, "points": strconv.Itoa(points), "anchor": anchor})
	if cached, ok := a.cache.get(key); ok {
		return cached.(PnlHistoryResponse), nil
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}, "target": {path}, "kind": {kind}, "from": {from}, "to": {to}, "points": {strconv.Itoa(points)}, "anchor": {anchor}}
	if objectID != "" {
		params.Set("objectId", objectID)
	}
	var v2 v2PnlHistoryResponse
	if err := a.client.get(ctx, "/objects/pnl/history", params, &v2); err != nil {
		return out, err
	}
	out = normalizeV2PnlHistory(path, from, to, v2)
	a.cache.set(key, out)
	return out, nil
}

func normalizeV2EquityHistory(path, from, to string, resp v2HistoryResponse) EquityHistoryResponse {
	pts := make([]EquityPoint, 0, len(resp.Points))
	for _, p := range resp.Points {
		pts = append(pts, EquityPoint{
			Timestamp: p.Ts, EquityUsd: p.EquityUsd, Status: p.Status,
			CumInflowsUsd: p.CumInflowsUsd, CumOutflowsUsd: p.CumOutflowsUsd,
			LastEventOpID: p.LastEventOpID, MidSetID: p.MidSetID,
		})
	}
	return EquityHistoryResponse{
		Prefix: path, From: from, To: to, Points: len(pts),
		Resolution: resp.Resolution, ResolutionRequested: resp.ResolutionRequested,
		ServerNow: resp.ServerNow, EquityPoints: pts,
	}
}

func normalizeV2PnlHistory(path, from, to string, resp v2PnlHistoryResponse) PnlHistoryResponse {
	pts := make([]PnlPoint, 0, len(resp.Points))
	for _, p := range resp.Points {
		pts = append(pts, PnlPoint{
			Timestamp: p.Ts, PnlUsd: p.PnlUsd, EquityUsd: p.EquityUsd, ValueUsd: p.ValueUsd,
			Status: p.Status, CumInflowsUsd: p.CumInflowsUsd, CumOutflowsUsd: p.CumOutflowsUsd,
			LastEventOpID: p.LastEventOpID, MidSetID: p.MidSetID,
		})
	}
	start := resp.StartEquityUsd
	if start == "" {
		start = resp.StartingEquityUsd
	}
	if start == "" {
		start = "0"
	}
	flows := resp.ExternalFlows
	if flows == nil {
		flows = []ExternalFlowEntry{}
	}
	mids := resp.MidPrices
	if mids == nil {
		mids = map[string]string{}
	}
	return PnlHistoryResponse{
		Prefix: path, From: from, To: to, Points: len(pts),
		Resolution: resp.Resolution, ResolutionRequested: resp.ResolutionRequested,
		ServerNow: resp.ServerNow, StartingEquityUsd: start, EffectiveFrom: resp.EffectiveFrom,
		PnlPoints: pts, ExternalFlows: flows, MidPrices: mids,
	}
}

// CreateAggregationWatch creates a server-side aggregation watch over a set of
// sources, returning the watch id and the initial aggregation.
func (a *Arca) CreateAggregationWatch(ctx context.Context, sources []AggregationSource) (CreateWatchResponse, error) {
	var out CreateWatchResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.post(ctx, "/aggregations/watch", map[string]any{"realmId": rid, "sources": sources}, &out)
	return out, err
}

// GetWatchAggregation returns the current aggregation for a watch.
func (a *Arca) GetWatchAggregation(ctx context.Context, watchID string) (PathAggregation, error) {
	if err := a.ensureReady(ctx); err != nil {
		return PathAggregation{}, err
	}
	var out CreateWatchResponse
	if err := a.client.get(ctx, "/aggregations/watch/"+watchID, nil, &out); err != nil {
		return PathAggregation{}, err
	}
	return out.Aggregation, nil
}

// DestroyAggregationWatch destroys a server-side aggregation watch.
func (a *Arca) DestroyAggregationWatch(ctx context.Context, watchID string) error {
	if err := a.ensureReady(ctx); err != nil {
		return err
	}
	return a.client.delete(ctx, "/aggregations/watch/"+watchID, nil)
}
