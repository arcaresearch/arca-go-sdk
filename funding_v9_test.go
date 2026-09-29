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

func TestFundingV9WalletSnapshotAndObservedReturnWire(t *testing.T) {
	const wallet = `{"schema":1,"realmId":"realm","ownerAddress":"0xowner","arcaPath":"/users/alice & one","cash":{"schema":1,"revision":17,"boundaryId":"cash-boundary","ownerAddress":"0xowner","walletState":"ready","balances":{"availableMicro":"1990000","confirmedMicro":"1990000"}},"accounts":[{"arcaId":"trade","observation":{"account":"0xtrade","source":"hyperevm_precompiles","observedAt":1234,"coreBlock":201,"coreBlockHash":"native-hash","beforeEvm":true,"evmBlock":101,"balance":"0.008159","spotBalance":"0.01000001","availableBalance":"0.008159","withdrawableBalance":"0.008159"}}],"movingMicro":"0","watermark":{"cashRevision":17,"cashBlock":100,"nativeBlock":201,"nativeBlockHash":"native-hash","evmBlock":101,"settlementSequence":2,"lastSettlementBlock":200}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/custody/v9/funding/wallet-snapshot" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("request identity: %s %s", r.Method, r.URL)
		}
		q := r.URL.Query()
		for k, want := range map[string]string{"realmId": "realm", "ownerAddress": "0xowner", "arcaPath": "/users/alice & one", "boundaryId": "cash-boundary"} {
			if q.Get(k) != want {
				t.Errorf("query %s: %q", k, q.Get(k))
			}
		}
		fmt.Fprintf(w, `{"success":true,"data":%s}`, wallet)
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	got, err := a.GetFundingV9WalletSnapshot(context.Background(), "0xowner", "/users/alice & one", "cash-boundary")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cash.Revision != 17 || got.Watermark.SettlementSequence != 2 || len(got.Accounts) != 1 || got.Accounts[0].Observation == nil {
		t.Fatalf("incomplete envelope: %+v", got)
	}
	assertFundingWirePreserved(t, []byte(wallet), got)
	const move = `{"route":"trading_to_cash","amountRaw":"1990000","activationFeeRaw":"0","networkFeeRaw":"10000","debitRaw":"2000000","actualNetworkFeeRaw":"1841","actualDebitRaw":"1991841","debitBooked":true,"creditBooked":true,"steps":[{"name":"perp_to_spot","state":"confirmed"},{"name":"spot_to_evm","state":"confirmed"},{"name":"move_evm","state":"confirmed"}]}`
	var state FundingV9MoveState
	if err := json.Unmarshal([]byte(move), &state); err != nil {
		t.Fatal(err)
	}
	if state.ActualNetworkFeeRaw != "1841" || state.NetworkFeeRaw != "10000" || state.ActualDebitRaw != "1991841" || state.DebitRaw != "2000000" {
		t.Fatalf("actual fee replaced quote: %+v", state)
	}
	assertFundingWirePreserved(t, []byte(move), state)
	const settlement = `{"actionKind":"usd_class_transfer","sourceDex":0,"destinationDex":4294967295,"toPerp":false,"account":"0xtrade","debitAccount":"0xtrade","amountRaw":"1990000","creditRaw":"1990000","feeRaw":"0","final":true,"snapshot":{"account":"0xtrade","coreBlock":200,"coreBlockHash":"hash","spotBalance":"1.99"}}`
	var s FundingV9CoreSettlement
	if err := json.Unmarshal([]byte(settlement), &s); err != nil {
		t.Fatal(err)
	}
	if s.ToPerp == nil || *s.ToPerp {
		t.Fatal("false class direction was lost")
	}
	// SourceDex zero is omitted intentionally by both server and SDK.
	var fields map[string]any
	json.Unmarshal([]byte(settlement), &fields)
	delete(fields, "sourceDex")
	raw, _ := json.Marshal(fields)
	assertFundingWirePreserved(t, raw, s)
}

// Compare supplied wire fields recursively, permitting omitted unrelated zero
// fields. This catches SDK DTO drift without depending on internal Go models.
func assertFundingWirePreserved(t *testing.T, raw []byte, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	var check func(string, any, any)
	check = func(path string, w, g any) {
		switch x := w.(type) {
		case map[string]any:
			y, ok := g.(map[string]any)
			if !ok {
				t.Fatalf("%s: object lost", path)
			}
			for k, v := range x {
				check(path+"."+k, v, y[k])
			}
		case []any:
			y, ok := g.([]any)
			if !ok || len(x) != len(y) {
				t.Fatalf("%s: array lost", path)
			}
			for i, v := range x {
				check(fmt.Sprintf("%s[%d]", path, i), v, y[i])
			}
		default:
			if w != g {
				t.Errorf("%s: wire %v became %v", path, w, g)
			}
		}
	}
	check("wire", want, got)
}

func TestFundingRecheckWire(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/custody/v9/funding/operations/op%2F1/recheck" && r.URL.Path != "/api/v1/custody/v9/funding/operations/op/1/recheck" {
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
			return
		}
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != http.MethodPost || req["realmId"] != "realm" {
			t.Error("recheck request changed", r.Method, req)
		}
		calls++
		w.WriteHeader(202)
		fmt.Fprint(w, `{"success":true,"data":{"operationId":"op/1","demandKey":"funding-correlation-demand/999.src.0xab.2","revision":"2","reason":"native_missing"}}`)
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	out, err := a.RecheckFundingV9Operation(context.Background(), "op/1")
	if err != nil || out.Revision != "2" || out.Reason != "native_missing" || out.OperationID != "op/1" || calls != 1 {
		t.Fatal(out, err, calls)
	}
}
