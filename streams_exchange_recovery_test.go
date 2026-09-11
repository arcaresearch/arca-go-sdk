package arca

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func newRecoveryStream(fetch func(context.Context) (ExchangeState, error)) *ExchangeWatchStream {
	return &ExchangeWatchStream{WatchStream: newWatchStream[ExchangeState](), objectID: "obj", fetch: fetch}
}

// exchange.updated has no durable log and a deferred enrichment is dropped
// without a deliverySeq, so after an invalidation the next push is not
// guaranteed. The bounded recovery read is the only way back.
func TestExchangeWatchInvalidatedObservationRecoversThroughBoundedRead(t *testing.T) {
	var fetches atomic.Int32
	s := newRecoveryStream(func(context.Context) (ExchangeState, error) {
		fetches.Add(1)
		return ExchangeState{FinancialInputID: "after"}, nil
	})
	defer s.Close()
	s.emitExchangeObservation(0, ExchangeState{FinancialInputID: "before"})
	s.observationEpoch = 1
	s.invalidateExchangeObservation(1)
	if _, ok := s.Value(); ok || s.State() != WatchReconnecting {
		t.Fatal("invalidation did not clear the observation")
	}
	// Not immediate: the first attempt waits ~1s (±20%).
	time.Sleep(600 * time.Millisecond)
	if got := fetches.Load(); got != 0 {
		t.Fatalf("recovery read fired early: %d", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if v, ok := s.Value(); ok && v.FinancialInputID == "after" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery read did not restore the observation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.State() != WatchConnected {
		t.Fatalf("state = %s, want connected", s.State())
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
	// A restored state cancels the schedule: no further reads.
	time.Sleep(1500 * time.Millisecond)
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches after restore = %d, want 1", got)
	}
}

func TestExchangeWatchRecoveryKeepsBackingOffWhileReadsFailAndStopsOnClose(t *testing.T) {
	var fetches atomic.Int32
	s := newRecoveryStream(func(context.Context) (ExchangeState, error) {
		fetches.Add(1)
		return ExchangeState{}, errors.New("projection unavailable")
	})
	s.emitExchangeObservation(0, ExchangeState{FinancialInputID: "before"})
	s.observationEpoch = 1
	s.invalidateExchangeObservation(1)
	// ~1s, ~2s: two attempts within 3.6s; never one per tick.
	time.Sleep(3600 * time.Millisecond)
	if got := fetches.Load(); got < 1 || got > 3 {
		t.Fatalf("fetches = %d, want 1..3 under the doubling schedule", got)
	}
	s.Close()
	after := fetches.Load()
	time.Sleep(1500 * time.Millisecond)
	if got := fetches.Load(); got != after {
		t.Fatalf("a closed stream kept reading: %d -> %d", after, got)
	}
}

func TestExchangeWatchRefreshCoalescesAndDiscardsAnOlderRead(t *testing.T) {
	var fetches atomic.Int32
	release := make(chan ExchangeState)
	s := newRecoveryStream(func(context.Context) (ExchangeState, error) {
		fetches.Add(1)
		return <-release, nil
	})
	defer s.Close()
	s.emitExchangeObservation(0, ExchangeState{FinancialInputID: "v0"})

	s.Refresh()
	s.Refresh() // while the first read is in flight: queued, not dropped
	s.Refresh()
	time.Sleep(50 * time.Millisecond)
	if got := fetches.Load(); got != 1 {
		t.Fatalf("in-flight reads = %d, want 1", got)
	}
	// A push lands while the first read is in flight and moves the epoch.
	s.observationMu.Lock()
	s.observationEpoch++
	epoch := s.observationEpoch
	s.observationMu.Unlock()
	s.emitExchangeObservation(epoch, ExchangeState{FinancialInputID: "pushed"})
	release <- ExchangeState{FinancialInputID: "stale-read"}
	time.Sleep(50 * time.Millisecond)
	if v, _ := s.Value(); v.FinancialInputID != "pushed" {
		t.Fatalf("an older read overwrote a newer push: %q", v.FinancialInputID)
	}
	// The queued re-read ran once more (not once per Refresh call) and lands.
	if got := fetches.Load(); got != 2 {
		t.Fatalf("fetches = %d, want 2 (the burst coalesced to one queued re-read)", got)
	}
	release <- ExchangeState{FinancialInputID: "fresh"}
	deadline := time.Now().Add(time.Second)
	for {
		if v, _ := s.Value(); v.FinancialInputID == "fresh" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued re-read never applied")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Close()
	s.Refresh()
	time.Sleep(50 * time.Millisecond)
	if got := fetches.Load(); got != 2 {
		t.Fatalf("a closed stream read: %d", got)
	}
}

func TestExchangeWatchRegistryNudgesLiveStreamsOnly(t *testing.T) {
	a := &Arca{}
	var fetches atomic.Int32
	s := newRecoveryStream(func(context.Context) (ExchangeState, error) {
		fetches.Add(1)
		return ExchangeState{FinancialInputID: "nudged"}, nil
	})
	a.registerExchangeWatch("obj", s)
	s.addUnsub(func() { a.unregisterExchangeWatch("obj", s) })
	a.refreshExchangeWatches("other")
	time.Sleep(30 * time.Millisecond)
	if fetches.Load() != 0 {
		t.Fatal("a nudge for another object reached this stream")
	}
	a.refreshExchangeWatches("obj")
	deadline := time.Now().Add(time.Second)
	for fetches.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("nudge did not re-read")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Close()
	a.refreshExchangeWatches("obj")
	time.Sleep(30 * time.Millisecond)
	if got := fetches.Load(); got != 1 {
		t.Fatalf("a closed stream was nudged: %d", got)
	}
	a.exchangeWatchMu.Lock()
	remaining := len(a.exchangeWatches)
	a.exchangeWatchMu.Unlock()
	if remaining != 0 {
		t.Fatalf("closed stream still registered: %d", remaining)
	}
}
