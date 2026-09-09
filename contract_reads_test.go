package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActiveAssetAvailabilityPreservesScalarAndSidePair(t *testing.T) {
	for _, wire := range []string{`"12.50"`, `["12.50","3.25"]`, `null`} {
		var out ActiveAssetData
		if err := json.Unmarshal([]byte(`{"market":"gllt:3","availableToTrade":`+wire+`}`), &out); err != nil {
			t.Fatal(err)
		}
		if string(out.AvailableToTradeRaw) != wire {
			t.Fatalf("lost availability %s", out.AvailableToTradeRaw)
		}
	}
}

func TestGetObjectSupportsBothPlatformEnvelopeGenerations(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data := any(map[string]string{"id": "account", "path": "/account"})
			if wrapped {
				data = map[string]any{"object": data}
			}
			writeEnvelope(w, 200, data)
		}))
		out, err := newTestArca(t, srv.URL).GetObject(context.Background(), "/account")
		srv.Close()
		if err != nil || out.ID != "account" {
			t.Fatalf("%+v %v", out, err)
		}
	}
}

func TestCapabilitiesRejectDifferentAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, AccountCapabilities{ObjectID: "other"})
	}))
	defer srv.Close()
	if _, err := newTestArca(t, srv.URL).GetExchangeCapabilities(context.Background(), "account"); err == nil {
		t.Fatal("foreign account capabilities accepted")
	}
}

func TestTransferPreservesExplicitDrainAndSingleDispatch(t *testing.T) {
	for _, drain := range []bool{false, true} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != "POST" || r.URL.Path != "/api/v1/transfer" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			value, present := body["drainPositions"]
			if present != drain || drain && value != true || body["path"] != "/original-key" || body["amount"] != "12.50" {
				t.Errorf("changed transfer identity or consent: %+v", body)
			}
			writeEnvelope(w, 200, map[string]any{"operation": Operation{ID: "transfer-op", State: OpPending}})
		}))
		handle := newTestArca(t, srv.URL).Transfer(context.Background(), TransferOptions{Path: "/original-key", From: "/a", To: "/b", Amount: "12.50", DrainPositions: drain})
		_, err := handle.Submitted(context.Background())
		_, again := handle.Submitted(context.Background())
		srv.Close()
		if err != nil || again != nil || calls != 1 {
			t.Fatalf("%v %v calls=%d", err, again, calls)
		}
	}
}
