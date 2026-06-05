package arca

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// resolveFixtureMeta mirrors the TypeScript META_RESPONSE fixture: two markets
// share the symbol "BTC" (a native hl:0:BTC and a builder-deployed hl:1:BTC on
// the "xyz" dex) to exercise the many-match case, plus a single hl:0:ETH.
func resolveFixtureMeta() []Market {
	return []Market{
		{Name: "hl:0:BTC", Symbol: "BTC", VenueSymbol: "BTC", Exchange: "hl", Index: 0, SzDecimals: 5, MaxLeverage: 50},
		{Name: "hl:0:ETH", Symbol: "ETH", Exchange: "hl", Index: 1, SzDecimals: 4, MaxLeverage: 50},
		{Name: "hl:1:BTC", Dex: "xyz", Symbol: "BTC", VenueSymbol: "xyz:BTC", DisplayName: "Bitcoin (xyz)", Exchange: "hl", IsHip3: true, Index: 3, SzDecimals: 5, MaxLeverage: 20},
	}
}

// newMetaTestArca stands up a mock server that serves the market-meta fixture
// and returns a client plus a counter of how many times /exchange/market/meta
// was hit (to assert cache reuse).
func newMetaTestArca(t *testing.T, meta []Market) (*Arca, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/exchange/market/meta") {
			atomic.AddInt32(&calls, 1)
			writeEnvelope(w, 200, SimMetaResponse{Universe: meta})
			return
		}
		writeEnvelope(w, 200, map[string]any{})
	}))
	t.Cleanup(srv.Close)
	return newTestArca(t, srv.URL), &calls
}

func marketNames(markets []Market) []string {
	names := make([]string, 0, len(markets))
	for _, m := range markets {
		names = append(names, m.Name)
	}
	sort.Strings(names)
	return names
}

func TestMarket_ExactCanonicalIDLookup(t *testing.T) {
	a, calls := newMetaTestArca(t, resolveFixtureMeta())

	btc, err := a.Market(context.Background(), "hl:0:BTC")
	if err != nil {
		t.Fatalf("Market: %v", err)
	}
	if btc == nil || btc.Symbol != "BTC" || btc.Exchange != "hl" {
		t.Fatalf("Market(hl:0:BTC) = %+v", btc)
	}

	// A bare symbol is not a canonical id — exact-id lookup must miss.
	if got, _ := a.Market(context.Background(), "BTC"); got != nil {
		t.Errorf("Market(\"BTC\") = %+v, want nil (bare symbol is not an id)", got)
	}

	// Unknown id returns nil, nil.
	if got, err := a.Market(context.Background(), "hl:0:DOESNOTEXIST"); got != nil || err != nil {
		t.Errorf("Market(unknown) = %+v, %v; want nil, nil", got, err)
	}

	if n := atomic.LoadInt32(calls); n != 1 {
		t.Errorf("meta fetched %d times, want 1 (cache reuse)", n)
	}
}

func TestResolveMarkets(t *testing.T) {
	cases := []struct {
		name      string
		symbol    string
		opts      *ResolveMarketsOptions
		wantNames []string
	}{
		{"many matches", "BTC", nil, []string{"hl:0:BTC", "hl:1:BTC"}},
		{"single match", "ETH", nil, []string{"hl:0:ETH"}},
		{"no match returns empty slice", "NOPE", nil, []string{}},
		{"exact case-sensitive on symbol", "btc", nil, []string{}},
		{"exchange filter (matches)", "BTC", &ResolveMarketsOptions{Exchange: "hl"}, []string{"hl:0:BTC", "hl:1:BTC"}},
		{"exchange filter (no match)", "BTC", &ResolveMarketsOptions{Exchange: "pm"}, []string{}},
		{"dex filter narrows to one", "BTC", &ResolveMarketsOptions{Dex: "xyz"}, []string{"hl:1:BTC"}},
		{"empty filter field is ignored", "BTC", &ResolveMarketsOptions{}, []string{"hl:0:BTC", "hl:1:BTC"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := newMetaTestArca(t, resolveFixtureMeta())
			got, err := a.ResolveMarkets(context.Background(), c.symbol, c.opts)
			if err != nil {
				t.Fatalf("ResolveMarkets: %v", err)
			}
			if got == nil {
				t.Fatalf("ResolveMarkets returned nil; want non-nil (possibly empty) slice")
			}
			names := marketNames(got)
			want := append([]string{}, c.wantNames...)
			sort.Strings(want)
			if !equalStrings(names, want) {
				t.Errorf("ResolveMarkets(%q, %+v) = %v, want %v", c.symbol, c.opts, names, want)
			}
		})
	}
}

func TestResolveMarkets_SourcesFromSharedCache(t *testing.T) {
	a, calls := newMetaTestArca(t, resolveFixtureMeta())

	if _, err := a.Market(context.Background(), "hl:0:ETH"); err != nil {
		t.Fatalf("Market: %v", err)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("meta fetched %d times after Market, want 1", n)
	}

	if _, err := a.ResolveMarkets(context.Background(), "BTC", nil); err != nil {
		t.Fatalf("ResolveMarkets: %v", err)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Errorf("meta fetched %d times after ResolveMarkets, want 1 (cache reuse)", n)
	}
}

func TestResolveMarket(t *testing.T) {
	t.Run("returns the single market when exactly one matches", func(t *testing.T) {
		a, _ := newMetaTestArca(t, resolveFixtureMeta())
		eth, err := a.ResolveMarket(context.Background(), "ETH", nil)
		if err != nil {
			t.Fatalf("ResolveMarket: %v", err)
		}
		if eth.Name != "hl:0:ETH" {
			t.Errorf("ResolveMarket(ETH).Name = %q, want hl:0:ETH", eth.Name)
		}
	})

	t.Run("errors when zero markets match", func(t *testing.T) {
		a, _ := newMetaTestArca(t, resolveFixtureMeta())
		_, err := a.ResolveMarket(context.Background(), "NOPE", nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
		if !strings.Contains(ve.Message, "No market found") {
			t.Errorf("message = %q, want it to mention 'No market found'", ve.Message)
		}
	})

	t.Run("errors when more than one market matches", func(t *testing.T) {
		a, _ := newMetaTestArca(t, resolveFixtureMeta())
		_, err := a.ResolveMarket(context.Background(), "BTC", nil)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("expected ValidationError, got %v", err)
		}
		if !strings.Contains(ve.Message, "ambiguous") {
			t.Errorf("message = %q, want it to mention 'ambiguous'", ve.Message)
		}
		// Both candidate names must be listed to help the caller narrow.
		if !strings.Contains(ve.Message, "hl:0:BTC") || !strings.Contains(ve.Message, "hl:1:BTC") {
			t.Errorf("ambiguous message must list candidate names, got %q", ve.Message)
		}
	})

	t.Run("returns the single match once narrowed by a filter", func(t *testing.T) {
		a, _ := newMetaTestArca(t, resolveFixtureMeta())
		btc, err := a.ResolveMarket(context.Background(), "BTC", &ResolveMarketsOptions{Dex: "xyz"})
		if err != nil {
			t.Fatalf("ResolveMarket: %v", err)
		}
		if btc.Name != "hl:1:BTC" {
			t.Errorf("ResolveMarket(BTC, dex=xyz).Name = %q, want hl:1:BTC", btc.Name)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
