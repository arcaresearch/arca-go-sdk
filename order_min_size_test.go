package arca

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// TestMinOrderSize_CeilRoundsToSzDecimals pins that the notional→size
// conversion rounds UP to the market's szDecimals precision so the returned
// size always clears the venue's minimum notional floor.
func TestMinOrderSize_CeilRoundsToSzDecimals(t *testing.T) {
	a, _ := newMetaTestArca(t, resolveFixtureMeta())

	cases := []struct {
		name        string
		szDecimals  int
		minNotional float64
		price       string
		wantMinSize string
	}{
		{name: "exact tick boundary", szDecimals: 5, minNotional: 10, price: "100000", wantMinSize: "0.0001"},
		{name: "ceil up to 2dp", szDecimals: 2, minNotional: 10, price: "3", wantMinSize: "3.34"},
		{name: "integer sizes (szDecimals 0)", szDecimals: 0, minNotional: 10, price: "0.0001", wantMinSize: "100000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Market{Name: "hl:0:X", SzDecimals: tc.szDecimals, MinOrderNotionalUsd: tc.minNotional}
			got, err := a.MinOrderSize(context.Background(), MinOrderSizeOptions{Market: &m, Price: tc.price})
			if err != nil {
				t.Fatalf("MinOrderSize: %v", err)
			}
			if got.MinSize != tc.wantMinSize {
				t.Errorf("MinSize = %q, want %q", got.MinSize, tc.wantMinSize)
			}
			if got.MinNotionalUsd != tc.minNotional {
				t.Errorf("MinNotionalUsd = %v, want %v", got.MinNotionalUsd, tc.minNotional)
			}
			// The rounded-up size must clear the notional floor.
			sz, _ := strconv.ParseFloat(got.MinSize, 64)
			px, _ := strconv.ParseFloat(tc.price, 64)
			if sz*px < tc.minNotional-1e-9 {
				t.Errorf("min size %s at price %s = %.6f notional, below floor %v", got.MinSize, tc.price, sz*px, tc.minNotional)
			}
		})
	}
}

// TestMinOrderSize_Exemptions pins that reduce-only orders and unsized trigger
// orders are exempt: they return one size tick and a zero notional floor.
func TestMinOrderSize_Exemptions(t *testing.T) {
	a, _ := newMetaTestArca(t, resolveFixtureMeta())
	m := Market{Name: "hl:0:BTC", SzDecimals: 5, MinOrderNotionalUsd: 10}

	reduce, err := a.MinOrderSize(context.Background(), MinOrderSizeOptions{Market: &m, Price: "100000", ReduceOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if reduce.MinSize != "0.00001" || reduce.MinNotionalUsd != 0 {
		t.Errorf("reduce-only = %+v, want {0.00001 0}", reduce)
	}

	trig, err := a.MinOrderSize(context.Background(), MinOrderSizeOptions{Market: &m, Price: "100000", IsTrigger: true, SizeToMax: true})
	if err != nil {
		t.Fatal(err)
	}
	if trig.MinNotionalUsd != 0 {
		t.Errorf("unsized trigger MinNotionalUsd = %v, want 0", trig.MinNotionalUsd)
	}
}

// TestMinOrderSize_FallsBackToVenueDefault pins that a market with no
// MinOrderNotionalUsd (older server) uses the venue-wide GetOrderLimits
// default, and that the id-based entry point fetches metadata.
func TestMinOrderSize_FallsBackToVenueDefault(t *testing.T) {
	a, _ := newMetaTestArca(t, resolveFixtureMeta())

	// The fixture markets carry no MinOrderNotionalUsd, so the id path must
	// fall back to the $10 venue default (hl:0:BTC has szDecimals 5).
	got, err := a.MinOrderSize(context.Background(), MinOrderSizeOptions{MarketID: "hl:0:BTC", Price: "50000"})
	if err != nil {
		t.Fatalf("MinOrderSize: %v", err)
	}
	if got.MinNotionalUsd != 10 {
		t.Errorf("MinNotionalUsd = %v, want 10 (venue default)", got.MinNotionalUsd)
	}
	// $10 / 50000 = 0.0002 at 5 decimals.
	if got.MinSize != "0.0002" {
		t.Errorf("MinSize = %q, want 0.0002", got.MinSize)
	}
}

// TestValidateOrderSize pins the ok/blocked verdicts and the reason string.
func TestValidateOrderSize(t *testing.T) {
	a, _ := newMetaTestArca(t, resolveFixtureMeta())
	m := Market{Name: "hl:0:BTC", SzDecimals: 5, MinOrderNotionalUsd: 10}

	blocked, err := a.ValidateOrderSize(context.Background(), ValidateOrderSizeOptions{Market: &m, Price: "100000", Size: "0.00005"})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.OK {
		t.Errorf("size 0.00005 (=$5) should be blocked, got ok")
	}
	if !strings.Contains(blocked.Reason, "below venue minimum") {
		t.Errorf("reason = %q, want it to mention the venue minimum", blocked.Reason)
	}

	ok, err := a.ValidateOrderSize(context.Background(), ValidateOrderSizeOptions{Market: &m, Price: "100000", Size: "0.0001"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok.OK || ok.Reason != "" {
		t.Errorf("size 0.0001 (=$10) should be ok, got %+v", ok)
	}

	exempt, err := a.ValidateOrderSize(context.Background(), ValidateOrderSizeOptions{Market: &m, Price: "100000", Size: "0.00001", ReduceOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !exempt.OK {
		t.Errorf("reduce-only order of any positive size should be ok, got %+v", exempt)
	}

	nonPositive, err := a.ValidateOrderSize(context.Background(), ValidateOrderSizeOptions{Market: &m, Price: "100000", Size: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if nonPositive.OK || !strings.Contains(nonPositive.Reason, "positive") {
		t.Errorf("zero size should be blocked as non-positive, got %+v", nonPositive)
	}
}
