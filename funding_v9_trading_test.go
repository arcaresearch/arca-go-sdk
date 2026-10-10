package arca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFundingV9TradingUsesExactSelectedAccountAndOriginalConsent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/v1/custody/v9/funding/trading/propose":
			var body FundingV9TradingAuthorizationRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.RealmID != "realm" || body.ArcaID != "selected" || body.RequestID != "review" || body.BuilderFeeTenthsBps != 45 {
				t.Error("proposal changed", body)
			}
			fmt.Fprint(w, `{"success":true,"data":{"proposalId":"original","operationId":"original","kind":"authorize_trading","trading":{"type":"builder_fee","builder":"0xabc","maxFeeRate":45}}}`)
		case "/api/v1/custody/v9/funding/trading":
			var body FundingV9SubmitRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.RealmID != "realm" || body.ProposalID != "original" || len(body.Signatures) != 1 || body.Signatures[0].ActionID != "approve_builder_fee" || body.Signatures[0].Signature != "exact" {
				t.Error("consent changed", body)
			}
			fmt.Fprint(w, `{"success":true,"data":{"operationId":"original","status":"pending","stage":"authorization_submitted"}}`)
		case "/api/v1/custody/v9/funding/accounts/selected/trading":
			if r.URL.Query().Get("realmId") != "realm" {
				t.Error("realm changed")
			}
			fmt.Fprint(w, `{"success":true,"data":{"account":{"arcaId":"selected"},"state":"ready","allowed":true}}`)
		default:
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	p, err := a.ProposeFundingV9Trading(context.Background(), FundingV9TradingAuthorizationRequest{RealmID: "other", ArcaID: "selected", RequestID: "review", BuilderFeeTenthsBps: 45})
	if err != nil || p.Trading == nil || p.Trading.MaxFeeRate != 45 {
		t.Fatal(p, err)
	}
	op, err := a.SubmitFundingV9Trading(context.Background(), FundingV9SubmitRequest{ProposalID: p.ProposalID, Signatures: []FundingV9Signature{{ActionID: "approve_builder_fee", Signature: "exact"}}})
	if err != nil || op.OperationID != "original" || op.Status != "pending" {
		t.Fatal(op, err)
	}
	status, err := a.GetFundingV9TradingStatus(context.Background(), "selected")
	if err != nil || !status.Allowed || status.Account.ArcaID != "selected" || calls != 3 {
		t.Fatal(status, err, calls)
	}
}
