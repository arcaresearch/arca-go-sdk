package arca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFundingBundleAndSnapshotRecovery(t *testing.T) {
	stop := errors.New("snapshot saved")
	submits := 0
	streams := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/api/v1/custody/v9/funding/deposits":
			var req FundingV9SubmitRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.ProposalID != "proposal" || len(req.Signatures) != 1 || req.Signatures[0].ActionID != "receive_authorization" || req.Signatures[0].Signature != "one" {
				t.Error("bundle changed", req)
			}
			submits++
			fmt.Fprint(w, `{"success":true,"data":{"operationId":"original","status":"pending","stage":"venue_pending"}}`)
		case "/api/v1/custody/v9/funding/events":
			if r.URL.Query().Get("operationId") != "original" || r.URL.Query().Get("realmId") != "realm" {
				t.Error("stream target changed")
			}
			streams++
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: snapshot\ndata: {\"operationId\":\"original\",\"stage\":\"venue_pending\",\"status\":\"pending\",\"sourceDebitEvidence\":{\"amount\":\"250.000000\"}}\n\n")
		default:
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	ctx := context.Background()
	r := FundingV9SubmitRequest{ProposalID: "proposal", Signatures: []FundingV9Signature{{ActionID: "receive_authorization", Signature: "one"}}}
	for i := 0; i < 2; i++ {
		op, err := a.SubmitFundingV9Deposit(ctx, r)
		if err != nil || op.OperationID != "original" {
			t.Fatal(op, err)
		}
	}
	for i := 0; i < 2; i++ {
		err := a.StreamFundingV9Operation(ctx, "original", func(op FundingV9Operation) error {
			if op.SourceDebitEvidence == nil || op.SourceDebitEvidence.Amount != "250.000000" || op.Status != "pending" {
				t.Error("source/native evidence changed", op)
			}
			return stop
		})
		if err != stop {
			t.Fatal(err)
		}
	}
	if submits != 2 || streams != 2 {
		t.Fatal("missing exact replay/reconnect")
	}
}
