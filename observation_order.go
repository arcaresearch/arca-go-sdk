package arca

import "time"

// ObservationTime is the instant the platform began the read behind this
// observation: ObservedAt, or the mirror allocation's AsOf from platforms that
// stamp only that. The zero time when the observation carries no read time.
func (s ExchangeState) ObservationTime() time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s.ObservedAt); err == nil && s.ObservedAt != "" {
		return t
	}
	if a := s.TradingAllocation; a != nil && a.AsOf != "" {
		if t, err := time.Parse(time.RFC3339Nano, a.AsOf); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ObservedBefore reports whether this observation describes an earlier ledger
// state than other. Reads of one account race — a push, a re-read after a gap
// and an application's own GetExchangeState can complete in any order — and a
// frame that resolves later is not therefore newer. Applying an observation
// only when this is false for the one already applied keeps positions and
// balances monotonic: a fill never appears, disappears and reappears because a
// pre-fill read landed late. False when either side carries no read time, so
// unstamped observations apply as before.
func (s ExchangeState) ObservedBefore(other ExchangeState) bool {
	mine, theirs := s.ObservationTime(), other.ObservationTime()
	if mine.IsZero() || theirs.IsZero() {
		return false
	}
	return mine.Before(theirs)
}
