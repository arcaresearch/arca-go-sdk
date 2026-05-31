package arca

import (
	"context"
	"net/url"
	"strconv"
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

// Asset returns a single asset by canonical coin id, lazily caching market
// metadata. Returns nil if the coin is not found.
func (a *Arca) Asset(ctx context.Context, coin string) (*SimMetaAsset, error) {
	m, err := a.ensureMetaLoaded(ctx)
	if err != nil {
		return nil, err
	}
	if asset, ok := m[coin]; ok {
		cp := asset
		return &cp, nil
	}
	return nil, nil
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

func (a *Arca) ensureMetaLoaded(ctx context.Context) (map[string]SimMetaAsset, error) {
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
	m := make(map[string]SimMetaAsset, len(resp.Universe))
	for _, asset := range resp.Universe {
		m[asset.Name] = asset
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
		out = CandlesResponse{Coin: coin, Interval: string(interval), Candles: candles}
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
