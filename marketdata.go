package arca

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var intervalMs = map[CandleInterval]int64{
	Interval15s: 15_000, Interval1m: 60_000, Interval5m: 300_000, Interval15m: 900_000,
	Interval1h: 3_600_000, Interval4h: 14_400_000, Interval1d: 86_400_000,
}

// GetMarketMeta returns market metadata (supported assets).
func (a *Arca) GetMarketMeta(ctx context.Context) (SimMetaResponse, error) {
	var out SimMetaResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/exchange/market/meta", nil, &out)
	return out, err
}

// Market looks up a single market by its exact canonical market id — the
// readable {exchange}:{dexIndex}:{symbol} form (e.g. "hl:0:BTC",
// "hl:1:TSLA"). Pass the Name field of a Market, not a bare symbol like
// "BTC".
//
// This is an exact-id lookup. To go from a human symbol to its market(s),
// use ResolveMarkets / ResolveMarket. Market metadata is lazily fetched and
// cached on first call; subsequent calls return from cache without a network
// request. Returns nil (and a nil error) when no market has that id.
func (a *Arca) Market(ctx context.Context, id string) (*Market, error) {
	m, err := a.ensureMetaLoaded(ctx)
	if err != nil {
		return nil, err
	}
	if market, ok := m[id]; ok {
		cp := market
		return &cp, nil
	}
	return nil, nil
}

// ResolveMarketsOptions narrows a symbol resolution. A zero/empty field
// means "do not filter on that dimension".
type ResolveMarketsOptions struct {
	Exchange string
	Dex      string
}

func (o *ResolveMarketsOptions) filterDesc() string {
	if o == nil || (o.Exchange == "" && o.Dex == "") {
		return ""
	}
	var parts []string
	if o.Exchange != "" {
		parts = append(parts, "exchange="+o.Exchange)
	}
	if o.Dex != "" {
		parts = append(parts, "dex="+o.Dex)
	}
	return " (filters: " + strings.Join(parts, ", ") + ")"
}

// ResolveMarkets resolves a human symbol (e.g. "BTC", "TSLA") to the
// market(s) that carry it, returning a slice because one symbol can
// legitimately map to many markets across exchanges and HIP-3 dexes (e.g. a
// native BTC and a builder-deployed BTC on a different dex).
//
// This never fails silently: an empty (non-nil) slice is an explicit "no
// market has this symbol", not a guess. The match is exact and
// case-sensitive on the Market.Symbol field ("kSHIB" != "KSHIB"). Narrow
// ambiguous symbols with opts.Exchange (require Market.Exchange == opts.Exchange)
// and/or opts.Dex (require Market.Dex == opts.Dex); an empty filter field is
// ignored. opts may be nil.
//
// Each result's Name is the canonical market id in the readable
// {exchange}:{dexIndex}:{symbol} form (e.g. "hl:0:BTC", "hl:1:TSLA"). For the
// "I expect exactly one" case, use ResolveMarket. If you already hold a
// canonical id, use Market instead. Sources from the same lazily-loaded,
// cached metadata as Market — no extra fetch.
func (a *Arca) ResolveMarkets(ctx context.Context, symbol string, opts *ResolveMarketsOptions) ([]Market, error) {
	m, err := a.ensureMetaLoaded(ctx)
	if err != nil {
		return nil, err
	}
	out := []Market{}
	for _, market := range m {
		if market.Symbol != symbol {
			continue
		}
		if opts != nil && opts.Exchange != "" && market.Exchange != opts.Exchange {
			continue
		}
		if opts != nil && opts.Dex != "" && market.Dex != opts.Dex {
			continue
		}
		out = append(out, market)
	}
	return out, nil
}

// ResolveMarket resolves a human symbol to the single market that carries
// it, returning an error when the result is not exactly one.
//
// Use this when your code assumes a symbol is unambiguous (often after
// narrowing with opts.Exchange / opts.Dex). Returns a *ValidationError when
// zero markets match (so a typo never silently no-ops) and when more than
// one matches (so an ambiguous symbol never silently picks the wrong one).
// The returned Market.Name is the canonical market id in the readable
// {exchange}:{dexIndex}:{symbol} form (e.g. "hl:0:BTC", "hl:1:TSLA"). opts
// may be nil.
func (a *Arca) ResolveMarket(ctx context.Context, symbol string, opts *ResolveMarketsOptions) (Market, error) {
	matches, err := a.ResolveMarkets(ctx, symbol, opts)
	if err != nil {
		return Market{}, err
	}
	where := opts.filterDesc()
	if len(matches) == 0 {
		return Market{}, &ValidationError{newArcaError("VALIDATION_ERROR",
			fmt.Sprintf("No market found for symbol %q%s. Pass a canonical id to Market(), or list candidates with ResolveMarkets(%q).", symbol, where, symbol), "")}
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, market := range matches {
			names = append(names, market.Name)
		}
		sort.Strings(names)
		return Market{}, &ValidationError{newArcaError("VALIDATION_ERROR",
			fmt.Sprintf("Symbol %q%s is ambiguous — %d markets match: %s. Narrow with Exchange / Dex, or call Market(id) with the exact canonical id.", symbol, where, len(matches), strings.Join(names, ", ")), "")}
	}
	return matches[0], nil
}

// PreloadMarketMeta eagerly fetches and caches market metadata.
func (a *Arca) PreloadMarketMeta(ctx context.Context) error {
	_, err := a.ensureMetaLoaded(ctx)
	return err
}

// RefreshMarketMeta forces a re-fetch of market metadata.
func (a *Arca) RefreshMarketMeta(ctx context.Context) error {
	a.metaMu.Lock()
	a.metaCache = nil
	a.metaMu.Unlock()
	_, err := a.ensureMetaLoaded(ctx)
	return err
}

func (a *Arca) ensureMetaLoaded(ctx context.Context) (map[string]Market, error) {
	a.metaMu.Lock()
	if a.metaCache != nil {
		m := a.metaCache
		a.metaMu.Unlock()
		return m, nil
	}
	a.metaMu.Unlock()

	resp, err := a.GetMarketMeta(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]Market, len(resp.Universe))
	for _, market := range resp.Universe {
		m[market.Name] = market
	}
	a.metaMu.Lock()
	a.metaCache = m
	a.metaMu.Unlock()
	return m, nil
}

// GetMarketMids returns current mid prices for all assets.
func (a *Arca) GetMarketMids(ctx context.Context) (SimMidsResponse, error) {
	var out SimMidsResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/exchange/market/mids", nil, &out)
	return out, err
}

// GetMarketTickers returns 24h ticker data for all assets.
func (a *Arca) GetMarketTickers(ctx context.Context) (MarketTickersResponse, error) {
	var out MarketTickersResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/exchange/market/tickers", nil, &out)
	return out, err
}

// GetOrderBook returns the L2 order book for a coin.
func (a *Arca) GetOrderBook(ctx context.Context, coin string) (SimBookResponse, error) {
	var out SimBookResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/exchange/market/book/"+coin, nil, &out)
	return out, err
}

// GetCandles returns OHLCV candle data. When a candle CDN is configured,
// finalized chunks are fetched from the CDN and the current period falls back
// to the REST API transparently.
func (a *Arca) GetCandles(ctx context.Context, coin string, interval CandleInterval, opts *GetCandlesOptions) (CandlesResponse, error) {
	var out CandlesResponse
	ivMs := intervalMs[interval]
	if ivMs == 0 {
		ivMs = 60_000
	}
	now := time.Now().UnixMilli()
	effectiveEnd := (now / ivMs) * ivMs
	var startTimePtr, endTimePtr *int64
	skipBackfill := false
	if opts != nil {
		startTimePtr = opts.StartTime
		endTimePtr = opts.EndTime
		skipBackfill = opts.SkipBackfill
	}
	if endTimePtr != nil {
		effectiveEnd = *endTimePtr
	}
	keyParams := map[string]string{"coin": coin, "interval": string(interval), "endTime": strconv.FormatInt(effectiveEnd, 10)}
	if startTimePtr != nil {
		keyParams["startTime"] = strconv.FormatInt(*startTimePtr, 10)
	}
	key := buildCacheKey("candles", keyParams)
	if cached, ok := a.cache.get(key); ok {
		return cached.(CandlesResponse), nil
	}
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}

	apiFetch := func(start, end int64) ([]Candle, error) {
		params := url.Values{"interval": {string(interval)}, "startTime": {strconv.FormatInt(start, 10)}, "endTime": {strconv.FormatInt(end, 10)}}
		if skipBackfill {
			params.Set("skipBackfill", "true")
		}
		var r CandlesResponse
		if err := a.client.get(ctx, "/exchange/market/candles/"+coin, params, &r); err != nil {
			return nil, err
		}
		return r.Candles, nil
	}

	if a.candleCDNBaseURL != "" && interval != Interval15s {
		startMs := now - ivMs*300
		if startTimePtr != nil {
			startMs = *startTimePtr
		}
		candles, err := fetchCandlesFromCDN(ctx, a.candleCDNBaseURL, coin, interval, startMs, effectiveEnd, apiFetch)
		if err != nil {
			return out, err
		}
		out = CandlesResponse{Market: coin, Interval: string(interval), Candles: candles}
		a.cache.set(key, out)
		return out, nil
	}

	params := url.Values{"interval": {string(interval)}}
	if startTimePtr != nil {
		params.Set("startTime", strconv.FormatInt(*startTimePtr, 10))
	}
	if endTimePtr != nil {
		params.Set("endTime", strconv.FormatInt(*endTimePtr, 10))
	}
	if skipBackfill {
		params.Set("skipBackfill", "true")
	}
	if err := a.client.get(ctx, "/exchange/market/candles/"+coin, params, &out); err != nil {
		return out, err
	}
	a.cache.set(key, out)
	return out, nil
}

// GetSparklines returns 24 hourly close prices for all tracked coins.
func (a *Arca) GetSparklines(ctx context.Context) (SparklinesResponse, error) {
	var out SparklinesResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/exchange/market/sparklines", nil, &out)
	return out, err
}
