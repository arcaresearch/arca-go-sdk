package arca

import "strconv"

// Client-side revaluation utilities. These recompute USD-denominated fields
// from a fresh mid-price map without a server round-trip. They are best-effort
// helpers for building responsive UIs; the server remains the authority for
// realized values (Axiom 10).

func revFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func fmtUSD(v float64) string { return fmtNum(v, 8) }

// RevalueExchangeState returns a copy of state with each position's
// UnrealizedPnl, PositionValue, and ReturnOnEquity recomputed from mids, and
// the margin summary's unrealized-PnL aggregates updated. Positions without a
// fresh mid are left unchanged.
func RevalueExchangeState(state ExchangeState, mids map[string]string) ExchangeState {
	out := state
	positions := make([]SimPosition, len(state.Positions))
	copy(positions, state.Positions)

	var totalUnrealized, totalNotional float64
	for i := range positions {
		p := &positions[i]
		mark, ok := revFloat(mids[p.Coin])
		size, sok := revFloat(p.Size)
		entry, eok := revFloat(p.EntryPrice)
		if !ok || !sok || !eok || mark <= 0 {
			if v, vok := revFloat(strDeref(p.UnrealizedPnl)); vok {
				totalUnrealized += v
			}
			if v, vok := revFloat(strDeref(p.PositionValue)); vok {
				totalNotional += v
			}
			continue
		}
		var upnl float64
		if p.Side == Long {
			upnl = (mark - entry) * size
		} else {
			upnl = (entry - mark) * size
		}
		notional := mark * size
		p.UnrealizedPnl = strPtr(fmtUSD(upnl))
		p.PositionValue = strPtr(fmtUSD(notional))
		if margin, mok := revFloat(p.MarginUsed); mok && margin > 0 {
			p.ReturnOnEquity = strPtr(fmtUSD(upnl / margin))
		}
		totalUnrealized += upnl
		totalNotional += notional
	}
	out.Positions = positions
	out.MarginSummary.TotalUnrealizedPnl = fmtUSD(totalUnrealized)
	out.MarginSummary.TotalNtlPos = fmtUSD(totalNotional)
	return out
}

// RevalueAggregation returns a copy of agg with spot-category breakdown entries
// re-priced from mids and TotalEquityUsd recomputed. Perp/exchange entries pass
// through unchanged.
func RevalueAggregation(agg PathAggregation, mids map[string]string) PathAggregation {
	out := agg
	breakdown := make([]AssetBreakdown, len(agg.Breakdown))
	copy(breakdown, agg.Breakdown)
	var total float64
	for i := range breakdown {
		b := &breakdown[i]
		if b.Category == "spot" {
			if mark, ok := revFloat(mids[b.Asset]); ok && mark > 0 {
				if amt, aok := revFloat(b.Amount); aok {
					b.Price = strPtr(fmtUSD(mark))
					b.ValueUsd = fmtUSD(amt * mark)
				}
			}
		}
		if v, ok := revFloat(b.ValueUsd); ok {
			total += v
		}
	}
	out.Breakdown = breakdown
	out.TotalEquityUsd = fmtUSD(total)
	return out
}

// RevalueObject returns a copy of the valuation with positions and spot
// balances re-priced from mids and ValueUsd recomputed.
func RevalueObject(val ObjectValuation, mids map[string]string) ObjectValuation {
	out := val
	var total float64

	balances := make([]BalanceValue, len(val.Balances))
	copy(balances, val.Balances)
	for i := range balances {
		b := &balances[i]
		if mark, ok := revFloat(mids[b.Denomination]); ok && mark > 0 {
			if amt, aok := revFloat(b.Amount); aok {
				b.Price = strPtr(fmtUSD(mark))
				b.ValueUsd = fmtUSD(amt * mark)
			}
		}
		if v, ok := revFloat(b.ValueUsd); ok {
			total += v
		}
	}
	out.Balances = balances

	positions := make([]PositionValue, len(val.Positions))
	copy(positions, val.Positions)
	for i := range positions {
		p := &positions[i]
		mark, ok := revFloat(mids[p.Coin])
		size, sok := revFloat(p.Size)
		entry, eok := revFloat(p.EntryPrice)
		if ok && sok && eok && mark > 0 {
			var upnl float64
			if p.Side == string(Long) {
				upnl = (mark - entry) * size
			} else {
				upnl = (entry - mark) * size
			}
			p.MarkPrice = strPtr(fmtUSD(mark))
			p.UnrealizedPnl = strPtr(fmtUSD(upnl))
			p.ValueUsd = strPtr(fmtUSD(mark * size))
		}
		if p.ValueUsd != nil {
			if v, vok := revFloat(*p.ValueUsd); vok {
				total += v
			}
		}
	}
	out.Positions = positions
	out.ValueUsd = fmtUSD(total)
	return out
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
