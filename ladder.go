package arca

import (
	"math"
	"time"
)

// Resolution is a chart resolution rung. The ladder mirrors the backend
// (backend/services/aggregation/internal/ladder/ladder.go) and PickResolution
// must agree byte-for-byte with it.
type Resolution string

const (
	Res1m  Resolution = "1m"
	Res5m  Resolution = "5m"
	Res15m Resolution = "15m"
	Res30m Resolution = "30m"
	Res1h  Resolution = "1h"
	Res4h  Resolution = "4h"
	Res1d  Resolution = "1d"
)

// AllResolutions is the ordered ladder (finest to coarsest).
var AllResolutions = []Resolution{Res1m, Res5m, Res15m, Res30m, Res1h, Res4h, Res1d}

const (
	msSecond = int64(1000)
	msMinute = 60 * msSecond
	msHour   = 60 * msMinute
	msDay    = 24 * msHour
)

const (
	// DefaultChartPoints is the default chart point budget.
	DefaultChartPoints = 1000
	// MinChartPoints is the minimum chart point budget.
	MinChartPoints = 50
)

// BucketMs returns the bucket size of a resolution in milliseconds.
func BucketMs(r Resolution) int64 {
	switch r {
	case Res1m:
		return msMinute
	case Res5m:
		return 5 * msMinute
	case Res15m:
		return 15 * msMinute
	case Res30m:
		return 30 * msMinute
	case Res1h:
		return msHour
	case Res4h:
		return 4 * msHour
	case Res1d:
		return msDay
	default:
		return msMinute
	}
}

// RetentionMs returns the server retention window of a resolution in ms.
func RetentionMs(r Resolution) int64 {
	switch r {
	case Res1m:
		return 7 * msDay
	case Res5m:
		return 30 * msDay
	case Res15m:
		return 45 * msDay
	case Res30m:
		return 60 * msDay
	case Res1h:
		return 90 * msDay
	case Res4h:
		return 180 * msDay
	case Res1d:
		return 10 * 365 * msDay
	default:
		return 7 * msDay
	}
}

// PickResolution picks the finest rung r where ceil(rangeMs/bucket(r)) <=
// targetPoints. If none fits, returns the coarsest rung.
func PickResolution(rangeMs int64, targetPoints int) Resolution {
	pts := targetPoints
	if pts < MinChartPoints {
		pts = MinChartPoints
	}
	if pts > DefaultChartPoints {
		pts = DefaultChartPoints
	}
	for _, r := range AllResolutions {
		b := BucketMs(r)
		n := int64(math.Ceil(float64(rangeMs) / float64(b)))
		if n <= int64(pts) {
			return r
		}
	}
	return AllResolutions[len(AllResolutions)-1]
}

// AlignDown aligns ts down to the bucket boundary at-or-before ts (UTC).
func AlignDown(ts int64, r Resolution) int64 {
	b := BucketMs(r)
	return (ts / b) * b
}

// AlignUp aligns ts up to the bucket boundary at-or-after ts (UTC).
func AlignUp(ts int64, r Resolution) int64 {
	down := AlignDown(ts, r)
	if down == ts {
		return down
	}
	return down + BucketMs(r)
}

// Boundaries enumerates bucket boundaries in [from, to] inclusive.
func Boundaries(from, to int64, r Resolution) []int64 {
	if to <= from {
		return nil
	}
	start := AlignUp(from, r)
	end := AlignDown(to, r)
	if start > end {
		return nil
	}
	b := BucketMs(r)
	var out []int64
	for cur := start; cur <= end; cur += b {
		out = append(out, cur)
	}
	return out
}

// PromotionTarget returns the next-coarser rung above r, or "" if r is the
// coarsest.
func PromotionTarget(r Resolution) Resolution {
	for i, x := range AllResolutions {
		if x == r && i+1 < len(AllResolutions) {
			return AllResolutions[i+1]
		}
	}
	return ""
}

// ChartRangePreset is a sliding range preset for the chart helpers.
type ChartRangePreset string

const (
	Range1h  ChartRangePreset = "1h"
	Range24h ChartRangePreset = "24h"
	Range7d  ChartRangePreset = "7d"
	Range30d ChartRangePreset = "30d"
	Range3m  ChartRangePreset = "3m"
	Range1y  ChartRangePreset = "1y"
	RangeYtd ChartRangePreset = "ytd"
	RangeAll ChartRangePreset = "all"
)

// ComputeChartRange returns from/to RFC3339 timestamps for a range preset,
// anchored to the current wall clock.
func ComputeChartRange(rangePreset ChartRangePreset) (from string, to string) {
	now := time.Now().UTC()
	var start time.Time
	switch rangePreset {
	case Range1h:
		start = now.Add(-time.Hour)
	case Range24h:
		start = now.Add(-24 * time.Hour)
	case Range7d:
		start = now.AddDate(0, 0, -7)
	case Range30d:
		start = now.AddDate(0, 0, -30)
	case Range3m:
		start = now.AddDate(0, -3, 0)
	case Range1y:
		start = now.AddDate(-1, 0, 0)
	case RangeYtd:
		start = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	case RangeAll:
		start = now.AddDate(-5, 0, 0)
	default:
		start = now.Add(-24 * time.Hour)
	}
	return start.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
}
