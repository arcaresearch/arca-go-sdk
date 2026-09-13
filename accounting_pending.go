package arca

import (
	"math/big"
	"sort"
	"strings"
)

// AccountingPendingExecution is one venue-confirmed execution that the ledger
// has not fully recorded into the ExchangeState that carries it.
//
// The platform confirms an order's execution (the receipt) before its ledger
// folds the fills into the account, and it folds them one fill at a time.
// Every observation in between is a real but intermediate account: the old
// position, a partly projected one, cash debited before margin is released.
// Rather than make each reader guess whether an observation is "after" an
// execution it knows about, the observation says so itself: this entry names
// the execution and exactly how much of it — UnaccountedSize, ExecutedSize
// minus AccountedSize — is still missing from Positions. Adding that signed
// quantity to the market's position yields the same book on the frame emitted
// at execution time, on every per-fill frame, and on the settled frame.
//
// Positions, side and quantity are exact from the venue, so they may be
// composed immediately. Realized P&L, fees and therefore equity are ledger
// facts; keep the previous money until AccountingSettled reports true.
type AccountingPendingExecution struct {
	OperationID string `json:"operationId"`
	// OrderID is the venue order id, empty before acknowledgement is recorded.
	OrderID string `json:"orderId,omitempty"`
	Market  string `json:"market"`
	// Side is "buy" or "sell": a buy adds UnaccountedSize to the market's
	// signed position, a sell subtracts it.
	Side            string `json:"side"`
	ExecutedSize    string `json:"executedSize"`
	AccountedSize   string `json:"accountedSize"`
	UnaccountedSize string `json:"unaccountedSize"`
	// ExecutionFinal is true once the venue has finished with the order; false
	// for a working order whose fills so far are reported here.
	ExecutionFinal bool `json:"executionFinal"`
	// AveragePrice is the venue's provisional aggregate price when known, so a
	// position this execution opens can show the price it executed at.
	AveragePrice string `json:"averagePrice,omitempty"`
}

// SignedUnaccounted is the quantity this entry adds to its market's signed
// position (long positive, short negative). ok is false for an unparsable
// entry, which a reader should treat as "cannot compose this market".
func (e AccountingPendingExecution) SignedUnaccounted() (value *big.Rat, ok bool) {
	size, ok := new(big.Rat).SetString(strings.TrimSpace(e.UnaccountedSize))
	if !ok || size.Sign() < 0 {
		return nil, false
	}
	switch strings.ToLower(e.Side) {
	case "buy", "long":
		return size, true
	case "sell", "short":
		return size.Neg(size), true
	}
	return nil, false
}

// AccountingSettled reports whether this observation's positions and money
// include every execution the platform knows about. While false, a reader
// should show ProjectedPositions and keep the previously settled balances.
func (s ExchangeState) AccountingSettled() bool {
	return len(s.AccountingPending) == 0
}

// ProjectedPositionSource says where a projected row's quantity came from.
type ProjectedPositionSource string

const (
	// ProjectedFromLedger: no pending execution touches this market; the row
	// is the ledger position verbatim.
	ProjectedFromLedger ProjectedPositionSource = "ledger"
	// ProjectedFromExecution: at least one pending execution was composed onto
	// the ledger position (or onto a flat market). Quantity and side are exact;
	// money on Ledger, if any, describes the pre-execution row.
	ProjectedFromExecution ProjectedPositionSource = "execution"
)

// ProjectedPosition is one market of the composed book: the ledger position
// with every pending execution for that market applied.
type ProjectedPosition struct {
	Market string
	Side   PositionSide
	// Size is the unsigned composed quantity, as an exact decimal string.
	Size string
	// EntryPrice is the ledger entry for an unchanged or reduced position, the
	// quantity-weighted blend for an increase whose executions carried an
	// average price, and the execution's average price for a position the
	// pending executions opened or reversed. Empty when it cannot be known.
	EntryPrice string
	Source     ProjectedPositionSource
	// Ledger is the observation's own row for this market, nil when the
	// market was flat on the ledger.
	Ledger *SimPosition
	// Pending lists the executions composed onto this market.
	Pending []AccountingPendingExecution
}

// ProjectedPositions composes Positions with AccountingPending: the book as it
// will read once the ledger has recorded everything the venue has already
// executed. Markets that compose to zero are omitted. Ledger positions are
// returned in their original order, followed by positions the pending
// executions opened, in market order. A market whose pending entries cannot
// be parsed is returned as its ledger row with Source ProjectedFromLedger, so
// a malformed entry can never invent or erase a position.
func (s ExchangeState) ProjectedPositions() []ProjectedPosition {
	pendingByMarket := make(map[string][]AccountingPendingExecution, len(s.AccountingPending))
	for _, e := range s.AccountingPending {
		pendingByMarket[e.Market] = append(pendingByMarket[e.Market], e)
	}
	out := make([]ProjectedPosition, 0, len(s.Positions)+len(pendingByMarket))
	seen := make(map[string]bool, len(s.Positions))
	for i := range s.Positions {
		p := &s.Positions[i]
		seen[p.Market] = true
		row := ProjectedPosition{Market: p.Market, Side: p.Side, Size: p.Size, EntryPrice: p.EntryPrice, Source: ProjectedFromLedger, Ledger: p}
		pending := pendingByMarket[p.Market]
		if len(pending) == 0 {
			out = append(out, row)
			continue
		}
		composed, ok := composeProjectedPosition(p, pending)
		if !ok {
			out = append(out, row)
			continue
		}
		if composed != nil {
			out = append(out, *composed)
		}
	}
	opened := make([]string, 0, len(pendingByMarket))
	for market := range pendingByMarket {
		if !seen[market] {
			opened = append(opened, market)
		}
	}
	sort.Strings(opened)
	for _, market := range opened {
		if composed, ok := composeProjectedPosition(nil, pendingByMarket[market]); ok && composed != nil {
			out = append(out, *composed)
		}
	}
	return out
}

// composeProjectedPosition applies pending executions to one market. Returns
// (nil, true) when the market composes to flat and (nil, false) when an entry
// cannot be parsed.
func composeProjectedPosition(ledger *SimPosition, pending []AccountingPendingExecution) (*ProjectedPosition, bool) {
	signed := new(big.Rat)
	var entry *big.Rat
	market := ""
	if ledger != nil {
		market = ledger.Market
		size, ok := new(big.Rat).SetString(strings.TrimSpace(ledger.Size))
		if !ok {
			return nil, false
		}
		if ledger.Side == Short {
			size.Neg(size)
		}
		signed = size
		if px, ok := new(big.Rat).SetString(strings.TrimSpace(ledger.EntryPrice)); ok && px.Sign() > 0 {
			entry = px
		}
	}
	for _, e := range pending {
		delta, ok := e.SignedUnaccounted()
		if !ok {
			return nil, false
		}
		if market == "" {
			market = e.Market
		}
		entry = blendedEntry(signed, entry, delta, e.AveragePrice)
		signed.Add(signed, delta)
	}
	if signed.Sign() == 0 {
		return nil, true
	}
	side := Long
	if signed.Sign() < 0 {
		side = Short
	}
	row := &ProjectedPosition{Market: market, Side: side, Size: ratDecimal(new(big.Rat).Abs(signed)), Source: ProjectedFromExecution, Ledger: ledger, Pending: pending}
	if entry != nil {
		row.EntryPrice = ratDecimal(entry)
	}
	return row, true
}

// blendedEntry is the entry price after applying delta to a position of size
// `held` at `entry`: unchanged for a reduction, quantity-weighted for an
// increase with a known execution price, the execution price for an open or
// the far side of a reversal, and unknown (nil) when it cannot be derived.
func blendedEntry(held, entry, delta *big.Rat, averagePrice string) *big.Rat {
	price, priced := new(big.Rat).SetString(strings.TrimSpace(averagePrice))
	priced = priced && price.Sign() > 0
	next := new(big.Rat).Add(held, delta)
	switch {
	case held.Sign() == 0 || next.Sign() == 0:
		// Opening from flat, or closing to flat (entry irrelevant once flat).
		if priced {
			return price
		}
		return nil
	case held.Sign() != next.Sign():
		// Reversal: the surviving position is entirely the execution's.
		if priced {
			return price
		}
		return nil
	case delta.Sign() != held.Sign():
		// Reduction on the same side keeps its entry.
		return entry
	default:
		// Increase: blend by quantity.
		if entry == nil || !priced {
			return nil
		}
		heldAbs, deltaAbs := new(big.Rat).Abs(held), new(big.Rat).Abs(delta)
		notional := new(big.Rat).Add(new(big.Rat).Mul(heldAbs, entry), new(big.Rat).Mul(deltaAbs, price))
		return notional.Quo(notional, new(big.Rat).Add(heldAbs, deltaAbs))
	}
}

// ratDecimal renders an exact rational as a plain decimal string, exact when
// the value terminates within 18 fractional digits and rounded there otherwise.
func ratDecimal(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	s := r.FloatString(18)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
