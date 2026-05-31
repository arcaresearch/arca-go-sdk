package arca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

type chunkPeriod struct {
	key     string
	startMs int64
	endMs   int64
}

func chunkGranularity(interval CandleInterval) string {
	switch interval {
	case Interval15s, Interval1m, Interval5m, Interval15m:
		return "daily"
	case Interval1h, Interval4h:
		return "weekly"
	default:
		return "monthly"
	}
}

func dailyChunk(d time.Time) chunkPeriod {
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	return chunkPeriod{key: fmt.Sprintf("%04d-%02d-%02d", d.Year(), int(d.Month()), d.Day()), startMs: start.UnixMilli(), endMs: end.UnixMilli()}
}

func weeklyChunk(d time.Time) chunkPeriod {
	date := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(date.Weekday()) + 6) % 7 // days since Monday
	monday := date.AddDate(0, 0, -offset)
	end := monday.AddDate(0, 0, 7)
	isoYear, isoWeek := date.ISOWeek()
	return chunkPeriod{key: fmt.Sprintf("%04d-W%02d", isoYear, isoWeek), startMs: monday.UnixMilli(), endMs: end.UnixMilli()}
}

func monthlyChunk(d time.Time) chunkPeriod {
	start := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	return chunkPeriod{key: fmt.Sprintf("%04d-%02d", d.Year(), int(d.Month())), startMs: start.UnixMilli(), endMs: end.UnixMilli()}
}

func chunkForTime(interval CandleInterval, ms int64) chunkPeriod {
	d := time.UnixMilli(ms).UTC()
	switch chunkGranularity(interval) {
	case "daily":
		return dailyChunk(d)
	case "weekly":
		return weeklyChunk(d)
	default:
		return monthlyChunk(d)
	}
}

// chunksForRange returns all chunk periods overlapping [startMs, endMs).
func chunksForRange(interval CandleInterval, startMs, endMs int64) []chunkPeriod {
	if startMs >= endMs {
		return nil
	}
	var chunks []chunkPeriod
	cursor := startMs
	for cursor < endMs {
		cp := chunkForTime(interval, cursor)
		chunks = append(chunks, cp)
		cursor = cp.endMs
	}
	return chunks
}

func chunkURL(baseURL, coin string, interval CandleInterval, chunkKey string) string {
	return fmt.Sprintf("%s/candles/%s/%s/%s.json", baseURL, coin, interval, chunkKey)
}

// fetchCandlesFromCDN fetches candles for a range from the CDN, falling back to
// the REST API for the current (unclosed) chunk and any 404s.
func fetchCandlesFromCDN(
	ctx context.Context,
	cdnBaseURL, coin string,
	interval CandleInterval,
	startMs, endMs int64,
	apiFallback func(start, end int64) ([]Candle, error),
) ([]Candle, error) {
	now := time.Now().UnixMilli()
	chunks := chunksForRange(interval, startMs, endMs)
	results := make([][]Candle, len(chunks))
	errs := make([]error, len(chunks))

	clampFallback := func(c chunkPeriod) ([]Candle, error) {
		return apiFallback(maxInt64(c.startMs, startMs), minInt64(c.endMs-1, endMs))
	}

	var wg sync.WaitGroup
	for i, c := range chunks {
		wg.Add(1)
		go func(i int, c chunkPeriod) {
			defer wg.Done()
			if now < c.endMs {
				results[i], errs[i] = clampFallback(c)
				return
			}
			candles, ok := fetchCDNChunk(ctx, chunkURL(cdnBaseURL, coin, interval, c.key), startMs, endMs)
			if !ok {
				results[i], errs[i] = clampFallback(c)
				return
			}
			results[i] = candles
		}(i, c)
	}
	wg.Wait()

	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}

	var merged []Candle
	for _, batch := range results {
		merged = append(merged, batch...)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].T < merged[j].T })

	deduped := merged[:0]
	for _, c := range merged {
		if n := len(deduped); n > 0 && deduped[n-1].T == c.T {
			deduped[n-1] = c
		} else {
			deduped = append(deduped, c)
		}
	}
	return deduped, nil
}

func fetchCDNChunk(ctx context.Context, url string, startMs, endMs int64) ([]Candle, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	var candles []Candle
	if json.Unmarshal(body, &candles) != nil {
		return nil, false
	}
	filtered := candles[:0]
	for _, c := range candles {
		if c.T >= startMs && c.T < endMs {
			filtered = append(filtered, c)
		}
	}
	return filtered, true
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
