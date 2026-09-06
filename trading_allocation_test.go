package arca

import (
	"encoding/json"
	"testing"
)

func TestTradingAllocationPreservesUnavailableQuoteAndFixedIntent(t *testing.T) {
	var state ExchangeState
	err := json.Unmarshal([]byte(`{"tradingAllocation":{"revision":"9007199254740993","preferences":{"gllt:11":{"mode":"fixed","leverage":8}},"projectionUnavailable":true}}`), &state)
	if err != nil {
		t.Fatal(err)
	}
	a := state.TradingAllocation
	if a == nil || a.Revision != "9007199254740993" || !a.ProjectionUnavailable || a.Projection != nil || a.Preferences["gllt:11"].Leverage == nil || *a.Preferences["gllt:11"].Leverage != 8 {
		t.Fatalf("lost intent or invented a money quote: %+v", a)
	}
	var legacy ExchangeState
	if err := json.Unmarshal([]byte(`{}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.TradingAllocation != nil {
		t.Fatal("legacy response invented allocation support")
	}
}
