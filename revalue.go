package arca

import (
	"math/big"
	"strconv"
	"strings"
)

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
		mark, ok := revFloat(mids[p.Market])
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
		mark, ok := revFloat(mids[p.Market])
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

// dec is an exact decimal (mantissa × 10^-scale) for re-marking projected
// rows, so a re-marked figure never picks up float64 error.
type dec struct {
	m *big.Int
	s int
}

func parseDec(raw string) (dec, bool) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return dec{}, false
	}
	neg := false
	switch t[0] {
	case '-':
		neg, t = true, t[1:]
	case '+':
		t = t[1:]
	}
	intPart, frac, _ := strings.Cut(t, ".")
	if intPart == "" && frac == "" {
		return dec{}, false
	}
	digits := intPart + frac
	if digits == "" {
		digits = "0"
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return dec{}, false
		}
	}
	m, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return dec{}, false
	}
	if neg {
		m.Neg(m)
	}
	return dec{m: m, s: len(frac)}, true
}

func parseDecPtr(raw *string) (dec, bool) {
	if raw == nil {
		return dec{}, false
	}
	return parseDec(*raw)
}

func decZero() dec { return dec{m: new(big.Int), s: 0} }

func (a dec) scaled(s int) *big.Int {
	if s == a.s {
		return new(big.Int).Set(a.m)
	}
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(s-a.s)), nil)
	return pow.Mul(pow, a.m)
}

func (a dec) add(b dec) dec {
	s := max(a.s, b.s)
	return dec{m: new(big.Int).Add(a.scaled(s), b.scaled(s)), s: s}
}

func (a dec) sub(b dec) dec {
	return a.add(dec{m: new(big.Int).Neg(b.m), s: b.s})
}

func (a dec) mul(b dec) dec {
	return dec{m: new(big.Int).Mul(a.m, b.m), s: a.s + b.s}
}

func (a dec) neg() dec { return dec{m: new(big.Int).Neg(a.m), s: a.s} }

func (a dec) abs() dec { return dec{m: new(big.Int).Abs(a.m), s: a.s} }

func (a dec) String() string {
	neg := a.m.Sign() < 0
	digits := new(big.Int).Abs(a.m).String()
	if a.s > 0 {
		if len(digits) <= a.s {
			digits = strings.Repeat("0", a.s-len(digits)+1) + digits
		}
		whole, frac := digits[:len(digits)-a.s], strings.TrimRight(digits[len(digits)-a.s:], "0")
		digits = whole
		if frac != "" {
			digits += "." + frac
		}
	}
	if digits == "0" {
		return "0"
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// remarkProjectedPosition re-marks one projected position at a new mid: the
// position and the change in its P&L from the delivered row, or ok == false
// when the mid or the position's numbers are unusable. The change is measured
// from the delivered mark; a position the server could not mark was left out
// of its row's figures, so its whole P&L is the change.
func remarkProjectedPosition(p PositionValue, mids map[string]string) (PositionValue, dec, bool) {
	mid, ok := mids[p.Market]
	if !ok {
		return p, dec{}, false
	}
	midDec, ok := parseDec(mid)
	if !ok {
		return p, dec{}, false
	}
	size, ok := parseDec(p.Size)
	if !ok {
		return p, dec{}, false
	}
	signed := size
	switch p.Side {
	case "short":
		signed = size.abs().neg()
	case "long":
		signed = size.abs()
	}
	delivered, hasDelivered := parseDecPtr(p.UnrealizedPnl)
	var pnl, shift dec
	if mark, hasMark := parseDecPtr(p.MarkPrice); hasMark && hasDelivered {
		shift = signed.mul(midDec.sub(mark))
		pnl = delivered.add(shift)
	} else {
		entry, ok := parseDec(p.EntryPrice)
		if !ok {
			return p, dec{}, false
		}
		pnl = signed.mul(midDec.sub(entry))
		if !hasDelivered {
			delivered = decZero()
		}
		shift = pnl.sub(delivered)
	}
	out := p
	out.MarkPrice = strPtr(mid)
	out.UnrealizedPnl = strPtr(pnl.String())
	out.ValueUsd = strPtr(pnl.String())
	return out, shift, true
}

// RevalueProjectedValuation returns a copy of a projected row re-marked at
// the given mids, in exact decimals.
//
// One rule covers every price-derived figure: Equity, UnrealizedValue and the
// windowed Pnl each shift by the change in position P&L from the delivered
// row to the new mids. Everything else the server put in them (cash,
// unsettled funding, the P&L baseline) is kept as delivered. Volume is never
// re-marked.
//
// Rows the server prices (PricingMode "server"), rows without positions, and
// positions without a mid pass through unchanged. Always re-mark the row as
// delivered, never an already re-marked one.
func RevalueProjectedValuation(v ProjectedValuation, mids map[string]string) ProjectedValuation {
	if v.PricingMode == "server" || len(v.Positions) == 0 {
		return v
	}
	shift := decZero()
	moved, allMarked := false, true
	positions := make([]PositionValue, len(v.Positions))
	for i, p := range v.Positions {
		next, delta, ok := remarkProjectedPosition(p, mids)
		if !ok {
			if p.MarkPrice == nil {
				allMarked = false
			}
			positions[i] = p
			continue
		}
		moved = true
		shift = shift.add(delta)
		positions[i] = next
	}
	if !moved {
		return v
	}
	shifted := func(raw *string) *string {
		d, ok := parseDecPtr(raw)
		if !ok {
			return raw
		}
		return strPtr(d.add(shift).String())
	}
	out := v
	out.Positions = positions
	out.Equity = shifted(v.Equity)
	out.UnrealizedValue = shifted(v.UnrealizedValue)
	if v.MidsComplete != nil && !*v.MidsComplete && allMarked {
		out.MidsComplete = nil
	}
	if v.Pnl != nil && v.Pnl.Status != WindowedPending && v.Pnl.PnlUsd != nil {
		if _, ok := parseDec(*v.Pnl.PnlUsd); ok {
			pnl := *v.Pnl
			pnl.PnlUsd = shifted(v.Pnl.PnlUsd)
			out.Pnl = &pnl
		}
	}
	return out
}
