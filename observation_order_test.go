package arca

import (
	"testing"
	"time"
)

func TestObservedBeforeOrdersByReadTimeAndNeverByArrival(t *testing.T) {
	at := func(observedAt string) ExchangeState { return ExchangeState{ObservedAt: observedAt} }
	earlier, later := at("2026-09-13T06:37:39.706482Z"), at("2026-09-13T06:37:40.699918Z")
	if !earlier.ObservedBefore(later) || later.ObservedBefore(earlier) || later.ObservedBefore(later) {
		t.Fatal("observations must order by read time only")
	}
	// Go trims trailing zeros; every remaining digit counts.
	if !at("2026-09-13T06:37:39.706482Z").ObservedBefore(at("2026-09-13T06:37:39.7065Z")) {
		t.Fatal("sub-millisecond fractions must order")
	}
	// Platforms that stamp only the allocation's asOf order the same way; the explicit stamp wins.
	viaAsOf := ExchangeState{TradingAllocation: &TradingAllocationState{AsOf: "2026-09-13T06:37:39Z"}}
	if !viaAsOf.ObservedBefore(later) {
		t.Fatal("asOf must order an observation without observedAt")
	}
	both := ExchangeState{ObservedAt: "2026-09-13T06:37:41Z", TradingAllocation: &TradingAllocationState{AsOf: "2026-09-13T06:37:39Z"}}
	if want := time.Date(2026, 9, 13, 6, 37, 41, 0, time.UTC); !both.ObservationTime().Equal(want) {
		t.Fatalf("observation time = %v, want the explicit stamp %v", both.ObservationTime(), want)
	}
	// An unstamped observation is never "before" anything: it applies as it always did.
	var unstamped ExchangeState
	if !unstamped.ObservationTime().IsZero() || unstamped.ObservedBefore(later) || later.ObservedBefore(unstamped) {
		t.Fatal("unstamped observations must not participate in ordering")
	}
	if !at("not a time").ObservationTime().IsZero() {
		t.Fatal("an unparseable stamp must read as unstamped")
	}
}
