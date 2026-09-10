package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const lifecycleReadFixture = `{"intent":{"realmId":"rlm_01h2xcejqtf2nbrexx3vqjhp41","objectId":"account","operationId":"original","leg":"0","venue":"gll-testnet","venueAccountId":"123","market":"gllt:3","requestedSize":"9007199254740993.123456789","orderType":"MARKET","side":"buy","timeInForce":"GTC","executionTimeInForce":"IOC","isTrigger":false,"isMarketTrigger":false,"sizeToMax":false,"reduceOnly":false},"venueOrderId":"3:order","submission":"accepted","working":false,"execution":"partial","terminal":true,"executedSize":"3.123456789","executionQuantityFinal":true,"requestedSizeKnown":true,"remainingSize":"9007199254740990","remainingDisposition":"canceled","accountedSize":"0","accountingComplete":false,"averagePrice":"2000.000000001","averagePriceFinal":false,"recoveryRequired":false}`

func TestOrderLifecycleReadPreservesExactServerEvidenceWithoutDispatch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/v1/objects/account/exchange/order-lifecycle" || r.URL.Query().Get("operationPath") != "/alice/original" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		writeEnvelope(w, 200, map[string]any{"lifecycle": json.RawMessage(lifecycleReadFixture)})
	}))
	defer server.Close()
	v, err := newTestArca(t, server.URL).GetOrderLifecycle(context.Background(), OriginalOrderReference{ObjectID: "account", OperationPath: "/alice/original"})
	if err != nil || calls != 1 || v.Intent.RequestedSize != "9007199254740993.123456789" || v.ExecutedSize != "3.123456789" || v.AccountingComplete || v.AveragePriceFinal || v.Working || v.RemainingSize != "9007199254740990" {
		t.Fatalf("changed server evidence: %+v %v calls=%d", v, err, calls)
	}
}
func TestOrderLifecycleReadRejectsForeignAndIncompleteEvidence(t *testing.T) {
	for _, bad := range []string{strings.Replace(lifecycleReadFixture, `"objectId":"account"`, `"objectId":"foreign"`, 1), strings.Replace(lifecycleReadFixture, `"operationId":"original"`, `"operationId":"foreign"`, 1), strings.Replace(lifecycleReadFixture, `"accountingComplete":false,`, "", 1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeEnvelope(w, 200, map[string]any{"lifecycle": json.RawMessage(bad)})
		}))
		_, err := newTestArca(t, server.URL).GetOrderLifecycle(context.Background(), OriginalOrderReference{ObjectID: "account", OperationID: "original"})
		server.Close()
		if err == nil {
			t.Fatal("accepted foreign or incomplete lifecycle")
		}
	}
}
