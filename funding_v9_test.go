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

func TestFundingMoveQuoteCreateAndRead(t *testing.T) {
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/custody/v9/funding/moves/quote":
			var req FundingV9MoveQuoteRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.RealmID != "realm" || req.FromArcaID != "cash" || req.ToArcaID != "trading" || req.AmountRaw != "5000000" {
				t.Error("quote request changed", req)
			}
			fmt.Fprint(w, `{"success":true,"data":{"route":"cash_to_trading","from":{"kind":"cash","arcaId":"cash"},"to":{"kind":"trading","arcaId":"trading"},"amountRaw":"5000000","activationFeeRaw":"0","networkFeeRaw":"0","arrivesRaw":"5000000","debitRaw":"5000000","minimumRaw":"1000001","maxRaw":"20000000","fromActivation":"none","toActivation":"none","toActivationAfter":"owed","expiresAt":99}}`)
		case "/api/v1/custody/v9/funding/moves":
			var req FundingV9MoveRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.RequestID != "move-1" || req.ToArcaID != "trading" || req.AmountRaw != "5000000" || req.QuoteExpiresAt != 99 || req.ActivationFeeRaw != "0" {
				t.Error("create request changed", req)
			}
			creates++
			w.WriteHeader(202)
			fmt.Fprint(w, `{"success":true,"data":{"operation":{"operationId":"move","kind":"cash_to_trading","stage":"accepted","status":"pending","move":{"route":"cash_to_trading","amountRaw":"5000000","steps":[{"name":"fund_account","state":"planned"}]}}}}`)
		case "/api/v1/custody/v9/funding/moves/move":
			fmt.Fprint(w, `{"success":true,"data":{"operation":{"operationId":"move","stage":"completed","status":"succeeded","move":{"debitBooked":true,"creditBooked":true}}}}`)
		default:
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	ctx := context.Background()
	q, err := a.QuoteFundingV9Move(ctx, FundingV9MoveQuoteRequest{FromArcaID: "cash", ToArcaID: "trading", AmountRaw: "5000000"})
	if err != nil || q.ArrivesRaw != "5000000" || q.ToActivationAfter != "owed" {
		t.Fatal(q, err)
	}
	for i := 0; i < 2; i++ {
		out, err := a.CreateFundingV9Move(ctx, FundingV9MoveRequestFromQuote("move-1", q))
		if err != nil || out.Operation.OperationID != "move" || out.Operation.Move == nil || out.Operation.Move.Steps[0].Name != "fund_account" {
			t.Fatal(out, err)
		}
	}
	got, err := a.GetFundingV9Move(ctx, "move")
	if err != nil || got.Operation.Status != "succeeded" || !got.Operation.Move.CreditBooked {
		t.Fatal(got, err)
	}
	if creates != 2 {
		t.Fatal("each create retry reaches the server with the same request")
	}
}
