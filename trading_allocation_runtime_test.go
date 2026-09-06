package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTradingAllocationRuntimeUsesExistingLeverageAndExactQuotes(t *testing.T) {
	var bodies []map[string]any
	var mu sync.Mutex
	bodyAt := func(index int) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return bodies[index]
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			bodies = append(bodies, body)
			mu.Unlock()
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/leverage"):
			writeEnvelope(w, 200, map[string]any{"accountId": "a", "market": "gllt:3", "leverage": nil, "previousLeverage": nil, "mode": "fixed", "intendedLeverage": 8, "revision": "9007199254740993", "projectionUnavailable": true})
		case strings.HasSuffix(r.URL.Path, "/allocation/quote"):
			writeEnvelope(w, 200, map[string]any{"inputId": "input", "market": "gllt:3", "referencePrice": "1000", "limitPrice": "1010.0", "allocation": map[string]any{"revision": "2", "preferences": map[string]any{}, "projectionUnavailable": false}, "maximum": map[string]any{"revision": "2", "maxSize": "1.234567890123456789", "maxNotional": "1234.567890123456789"}, "affordable": false})
		case strings.HasSuffix(r.URL.Path, "/allocation"):
			if r.URL.Query().Get("market") != "gllt:3" {
				t.Error("market query missing")
			}
			writeEnvelope(w, 200, map[string]any{"enabled": false, "inputId": "input", "unavailableReason": "applied_risk_unavailable", "allocation": map[string]any{"revision": "9007199254740993", "preferences": map[string]any{}, "projectionUnavailable": true}})
		default:
			t.Errorf("unexpected endpoint %s", r.URL)
			writeEnvelope(w, 404, map[string]any{})
		}
	}))
	defer server.Close()
	a := newTestArca(t, server.URL)
	ctx := context.Background()
	result, err := a.UpdateLeverage(ctx, UpdateLeverageOptions{ObjectID: "obj", Market: "gllt:3", Leverage: 8, Mode: LeverageFixed, CommandID: "retry-same"})
	if err != nil || result.Leverage != nil || result.IntendedLeverage == nil || *result.IntendedLeverage != 8 || result.Revision != "9007199254740993" {
		t.Fatalf("unavailable effective setting: %+v %v", result, err)
	}
	if bodyAt(0)["mode"] != "fixed" || bodyAt(0)["commandId"] != "retry-same" || bodyAt(0)["leverage"] != float64(8) {
		t.Fatalf("existing leverage body: %v", bodyAt(0))
	}
	_, err = a.UpdateLeverage(ctx, UpdateLeverageOptions{ObjectID: "obj", Market: "gllt:3", Leverage: 10, Mode: LeverageVenueDefault})
	if err != nil || bodyAt(1)["leverage"] != nil || bodyAt(1)["mode"] != "venue-default" || bodyAt(1)["commandId"] == nil {
		t.Fatalf("follow intent: %v %v", bodyAt(1), err)
	}
	_, err = a.UpdateLeverage(ctx, UpdateLeverageOptions{ObjectID: "obj", Market: "hl:0:BTC", Leverage: 5})
	if err != nil || len(bodyAt(2)) != 2 || bodyAt(2)["leverage"] != float64(5) {
		t.Fatalf("HL payload changed: %v %v", bodyAt(2), err)
	}
	read, err := a.GetTradingAllocation(ctx, "obj", "gllt:3")
	if err != nil || read.Enabled || !read.Allocation.ProjectionUnavailable || read.Allocation.Projection != nil {
		t.Fatalf("unavailable read: %+v %v", read, err)
	}
	one := 1
	quote, err := a.QuoteTradingAllocation(ctx, "obj", TradingAllocationQuoteRequest{Market: "gllt:3", Side: Buy, OrderType: "market", Selection: TradingLeverageSelection{Mode: LeverageFixed, Leverage: &one}})
	if err != nil || quote.Maximum.MaxSize != "1.234567890123456789" || quote.Affordable == nil || *quote.Affordable || bodyAt(3)["price"] != nil {
		t.Fatalf("quote precision or semantics: %+v %v", quote, err)
	}
}

func TestTradingAllocationStreamInvalidationRejectsOlderObservation(t *testing.T) {
	s := &ExchangeWatchStream{WatchStream: newWatchStream[ExchangeState]()}
	defer s.Close()
	s.emitExchangeObservation(0, ExchangeState{FinancialInputID: "before"})
	s.observationEpoch = 1
	s.invalidateExchangeObservation(1)
	s.emitExchangeObservation(0, ExchangeState{FinancialInputID: "late-old-read"})
	if _, ok := s.Value(); ok || s.State() != WatchReconnecting {
		t.Fatal("stale money survived invalidation")
	}
	s.emitExchangeObservation(2, ExchangeState{FinancialInputID: "after"})
	if value, ok := s.Value(); !ok || value.FinancialInputID != "after" || s.State() != WatchConnected {
		t.Fatal("coherent push did not restore state")
	}
}

func TestTradingAllocationQuietObservationExpires(t *testing.T) {
	s := &ExchangeWatchStream{WatchStream: newWatchStream[ExchangeState]()}
	defer s.Close()
	expired := make(chan struct{}, 1)
	s.OnStateChange(func(state WatchState) {
		if state == WatchReconnecting {
			select {
			case expired <- struct{}{}:
			default:
			}
		}
	})
	now := time.Now()
	s.emitExchangeObservation(0, ExchangeState{FinancialInputID: "timed", TradingAllocation: &TradingAllocationState{AsOf: now.Format(time.RFC3339Nano), ValidUntil: now.Add(20 * time.Millisecond).Format(time.RFC3339Nano)}})
	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("quiet observation did not expire")
	}
	if _, ok := s.Value(); ok {
		t.Fatal("expired money retained")
	}
}
