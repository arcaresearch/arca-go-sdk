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

func TestFundingV9DeclareAndUpdateAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/custody/v9/funding/accounts/declare":
			var req FundingV9DeclareRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.RealmID != "realm" || req.RequestID != "declare-1" || req.Kind != "hyperliquid" || req.BoundaryID != "cash-b" {
				t.Error("declaration changed", req)
			}
			fmt.Fprint(w, `{"success":true,"data":{"arcaId":"obj_t","arcaPath":"/u/trading/1","walletBoundaryId":"cash-b","kind":"hyperliquid","ownerAddress":"0x01","labels":{"name":"Hyperliquid 1","ordinal":"1","venue":"hyperliquid"},"setupStatus":"declared","lifecycle":"active","revision":1}}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/custody/v9/funding/accounts/obj_t":
			var p FundingV9AccountPatch
			json.NewDecoder(r.Body).Decode(&p)
			if p.RealmID != "realm" || p.Revision != 1 || p.Labels["name"] != "Scalps" {
				t.Error("patch changed", p)
			}
			fmt.Fprint(w, `{"success":true,"data":{"arcaId":"obj_t","arcaPath":"/u/trading/1","walletBoundaryId":"cash-b","kind":"hyperliquid","ownerAddress":"0x01","labels":{"name":"Scalps","ordinal":"1","venue":"hyperliquid"},"setupStatus":"declared","lifecycle":"active","revision":2}}`)
		default:
			t.Error("unexpected endpoint", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	ctx := context.Background()
	d, err := a.DeclareFundingV9Account(ctx, FundingV9DeclareRequest{RequestID: "declare-1", OwnerAddress: "0x01", BoundaryID: "cash-b", Kind: "hyperliquid"})
	if err != nil || d.SetupStatus != "declared" || d.Labels["name"] != "Hyperliquid 1" || d.Revision != 1 {
		t.Fatal(d, err)
	}
	u, err := a.UpdateFundingV9Account(ctx, d.ArcaID, FundingV9AccountPatch{Revision: d.Revision, Labels: map[string]string{"name": "Scalps"}})
	if err != nil || u.Labels["name"] != "Scalps" || u.Revision != 2 {
		t.Fatal(u, err)
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
	const wallet = `{"schema":2,"realmId":"realm","ownerAddress":"0xowner","arcaPath":"/users/alice & one","boundaryId":"cash-boundary","cash":{"schema":1,"revision":17,"boundaryId":"cash-boundary","ownerAddress":"0xowner","walletState":"ready","balances":{"availableMicro":"1990000","confirmedMicro":"1990000"}},"accounts":[{"arcaId":"trade","arcaPath":"/users/alice & one/hl-1","boundaryId":"trade-boundary","kind":"hyperliquid_perp","accountAddress":"0xtrade","labels":{"name":"Hyperliquid 1","ordinal":"1"},"state":"ready","setupStatus":"active","lifecycle":"active","balances":{"totalMicro":"1010000","perpMicro":"1000000","withdrawableMicro":"1000000","spotMicro":"10000","source":"hyperliquid_ws"},"asOf":"2026-09-29T05:35:03Z","current":true,"heldBy":"opr_1"},{"arcaId":"trade-2","arcaPath":"/users/alice & one/hl-2","boundaryId":"trade-2","kind":"hyperliquid_perp","accountAddress":"","labels":{"name":"Hyperliquid 2"},"state":"declared","setupStatus":"declared","lifecycle":"active","declaredAt":"2026-09-29T05:00:00Z","balances":{"totalMicro":"0","perpMicro":"0","withdrawableMicro":"0","spotMicro":"0","source":"declared"},"current":true}],"moving":[{"operationId":"opr_1","kind":"move","leg":"in_transit","amountMicro":"500000","from":{"kind":"cash","boundaryId":"cash-boundary"},"to":{"kind":"trading","arcaId":"trade","boundaryId":"trade-boundary","address":"0xtrade"}}],"operations":[{"operationId":"opr_1","requestId":"req-1","kind":"move","stage":"arriving","status":"pending","safeToReviewAgain":false,"amountMicro":"500000","arrivesMicro":"500000","debitMicro":"510000","activationFeeMicro":"0","networkFeeMicro":"10000","from":{"kind":"cash","boundaryId":"cash-boundary"},"to":{"kind":"trading","arcaId":"trade"},"steps":[{"name":"move_evm","state":"confirmed","txHash":"0xabc","blockNumber":7}],"debitBooked":true,"creditBooked":false,"cashLeg":{"leg":"debit","state":"booked","amountMicro":"510000","txHash":"0xabc"},"requirements":[{"kind":"setup_signature","operationId":"opr_0","proposalId":"prop","actionIds":["act"],"expiresAt":99}],"settlement":{"sequence":3,"coreBlock":202,"amountMicro":"500000","feeMicro":"0","creditMicro":"500000","final":false},"createdAt":"2026-09-29T05:30:00Z"}],"totals":{"cashMicro":"1990000","movingMicro":"500000","tradingMicro":"1010000","totalMicro":"3500000","complete":true,"current":true},"attention":[],"watermark":{"sequence":41,"revision":40,"cashBlock":100,"cashBlockHash":"cash-hash","nativeObservedAt":1234,"settlementSequence":2,"lastSettlementBlock":200},"composedAt":"2026-09-29T05:35:04Z"}`
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
	if got.Cash.Revision != 17 || got.Watermark.Sequence != 41 || len(got.Accounts) != 2 || got.Accounts[0].Balances == nil || len(got.Moving) != 1 || got.Operations[0].CashLeg == nil {
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

// TestFundingV9WalletsStreamBootstrapOption pins the resume knob: the plain
// stream sends no bootstrap flag (the server decides from the realm), and
// Bootstrap asks for every wallet again with `bootstrap=1` while still
// resuming from the position.
func TestFundingV9WalletsStreamBootstrapOption(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.Header.Get("Last-Event-ID")+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": connected\n\nevent: caught_up\ndata: {\"sequence\":5}\n\n")
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	accept := func(FundingV9WalletsEvent) error { return nil }
	_ = a.StreamFundingV9Wallets(context.Background(), 5, accept)
	_ = a.StreamFundingV9WalletsWithOptions(context.Background(), 5, FundingV9WalletsStreamOptions{Bootstrap: true}, accept)
	if len(queries) != 2 || queries[0] != "5?realmId=realm" || queries[1] != "5?bootstrap=1&realmId=realm" {
		t.Fatalf("requests: %v", queries)
	}
}

func TestFundingV9WalletsStreamResumesFromPosition(t *testing.T) {
	const snap = `{"schema":2,"realmId":"realm","boundaryId":"cash-boundary","cash":{"schema":1,"boundaryId":"cash-boundary"},"accounts":[],"moving":[],"operations":[],"totals":{"cashMicro":"1","movingMicro":"0","tradingMicro":"0","totalMicro":"1","complete":true,"current":true},"attention":[],"watermark":{"sequence":8,"revision":8}}`
	var lastIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Path == "/api/v1/custody/v9/funding/wallet-stream" {
			fmt.Fprintf(w, "id: 8\nevent: snapshot\ndata: %s\n\n", snap)
			return
		}
		if r.URL.Path != "/api/v1/custody/v9/funding/wallet-snapshots/stream" || r.URL.Query().Get("realmId") != "realm" {
			t.Errorf("request: %s", r.URL)
		}
		lastIDs = append(lastIDs, r.Header.Get("Last-Event-ID"))
		fmt.Fprintf(w, ": connected\n\nevent: wallet\ndata: %s\n\nid: 9\nevent: position\ndata: {\"sequence\":9}\n\nevent: caught_up\ndata: {\"sequence\":9}\n\nevent: wallet_error\ndata: {\"boundaryId\":\"gone\",\"error\":\"unavailable\"}\n\nid: 10\nevent: position\ndata: {\"sequence\":10}\n\n", snap)
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	var got []FundingV9WalletsEvent
	err := a.StreamFundingV9Wallets(context.Background(), 5, func(ev FundingV9WalletsEvent) error {
		got = append(got, ev)
		return nil
	})
	var disc *FundingV9WalletStreamDisconnectedError
	if !errors.As(err, &disc) || disc.LastEventID != "9" {
		t.Fatalf("disconnect must carry the last position: %v", err)
	}
	if len(got) != 4 || got[0].Wallet == nil || got[0].Wallet.Totals.TotalMicro != "1" || got[1].Position != 9 || got[2].CaughtUp == nil || got[2].CaughtUp.Sequence != 9 || got[3].WalletError == nil {
		t.Fatalf("frames: %+v", got)
	}
	if len(lastIDs) != 1 || lastIDs[0] != "5" {
		t.Fatalf("resume header: %v", lastIDs)
	}
	stop := errors.New("stop")
	err = a.StreamFundingV9Wallet(context.Background(), "0xowner", "/users/a", "cash-boundary", "", func(FundingV9WalletSnapshot) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("a callback error must stop the wallet stream: %v", err)
	}
}

func TestFundingV9CatalogFeeFactsAndSetupProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("realmId") != "realm" {
			t.Error("realm scope missing")
		}
		switch r.URL.Path {
		case "/api/v1/custody/v9/funding/accounts/a/catalog":
			fmt.Fprint(w, `{"success":true,"data":{"arcaId":"a","factsSequence":18,"setupRetryable":false,"activation":"owed","activationEvidence":{"source":"circle_credit","operationId":"deposit","nativeTxHash":"native"},"deferredActivationFeeRaw":"1000000","deferredActivationFeeState":"owed","deferredActivationFeeTrigger":"first_outbound_send_asset","fundingState":"funded","capabilities":{"move_in":{"allowed":true},"move_out":{"allowed":false,"reason":"route_disabled"}}}}`)
		case "/api/v1/custody/v9/funding/operations/move":
			fmt.Fprint(w, `{"success":true,"data":{"operationId":"move","status":"pending","stage":"awaiting_setup","setupState":"deploying","requirements":[]}}`)
		default:
			t.Error("unexpected path", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	facts, err := a.GetFundingV9CatalogAccount(context.Background(), "a")
	if err != nil || facts.FactsSequence != 18 || facts.Activation != "owed" || facts.DeferredActivationFeeRaw != "1000000" || facts.ActivationEvidence == nil || facts.ActivationEvidence.NativeTxHash != "native" || !facts.Capabilities["move_in"].Allowed {
		t.Fatalf("facts %+v %v", facts, err)
	}
	op, err := a.GetFundingV9Operation(context.Background(), "move")
	if err != nil || op.SetupState != "deploying" || op.Status != "pending" || len(op.Requirements) != 0 {
		t.Fatalf("accepted setup %+v %v", op, err)
	}
	for _, raw := range []string{`{"activation":"unknown","deferredActivationFeeState":"unknown"}`, `{"activation":"paid","deferredActivationFeeRaw":"0","deferredActivationFeeState":"paid"}`} {
		var f FundingV9AccountFacts
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			t.Fatal(err)
		}
		if (f.Activation == "paid") != (f.DeferredActivationFeeRaw == "0") {
			t.Fatalf("unknown collapsed to paid: %+v", f)
		}
	}
}

func TestWalletStreamRejectsPositionMismatchWithoutAcknowledging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: 8\nevent: position\ndata: {\"sequence\":9}\n\n")
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	calls := 0
	err := a.StreamFundingV9Wallets(context.Background(), 5, func(FundingV9WalletsEvent) error { calls++; return nil })
	var disconnected *FundingV9WalletStreamDisconnectedError
	if !errors.As(err, &disconnected) || disconnected.LastEventID != "5" || calls != 0 {
		t.Fatalf("mismatched position accepted: %d %v", calls, err)
	}
}

func TestRetireFundingMoveRequiresDurableScopedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		status      int
		retired, ok bool
	}{
		{"retired", `{"status":"retired","requestId":"original","operationId":"op_1"}`, 200, true, true},
		{"accepted", `{"status":"accepted","requestId":"original","operationId":"op_1","admission":{"operation":{"operationId":"op_1","status":"pending"}}}`, 200, false, true},
		{"wrong request", `{"status":"retired","requestId":"other","operationId":"op_1"}`, 200, false, false},
		{"wrong operation", `{"status":"accepted","requestId":"original","operationId":"op_1","admission":{"operation":{"operationId":"op_2"}}}`, 200, false, false},
		{"contradiction", `{"status":"retired","requestId":"original","operationId":"op_1","admission":{"operation":{"operationId":"op_1"}}}`, 200, false, false},
		{"plain not found", `{}`, 404, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v1/custody/v9/funding/moves/retire" {
					t.Error("unexpected request", r.Method, r.URL)
				}
				var req FundingV9MoveRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.RealmID != "realm" || req.RequestID != "original" || req.QuoteExpiresAt != 99 || req.AmountRaw != "5000000" {
					t.Error("original terms changed", req)
				}
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"success":true,"data":%s}`, tc.body)
			}))
			defer srv.Close()
			a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
			adm, retired, err := a.RetireFundingV9Move(context.Background(), FundingV9MoveRequest{RealmID: "untrusted", RequestID: "original", QuoteExpiresAt: 99, AmountRaw: "5000000"})
			if (err == nil) != tc.ok || retired != tc.retired {
				t.Fatal(retired, err)
			}
			if tc.name == "accepted" && adm.Operation.OperationID != "op_1" {
				t.Fatal("lost original acceptance")
			}
		})
	}
}

// TestResolveFundingV9MoveReadsByRequestKey pins the product backend's
// lost-reply read: a GET on the request key (never a write), the three
// statuses, and refusal of evidence that does not add up (an accepted answer
// without its admission, or a status the SDK does not know).
func TestResolveFundingV9MoveReadsByRequestKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		ok     bool
		status string
	}{
		{"accepted", `{"status":"accepted","requestId":"k1","operationId":"op_1","admission":{"operation":{"operationId":"op_1","kind":"cash_to_trading"}}}`, true, FundingV9MoveAccepted},
		{"retired", `{"status":"retired","requestId":"k1","operationId":"op_1"}`, true, FundingV9MoveRetired},
		{"absent", `{"status":"absent","requestId":"k1","operationId":"op_1"}`, true, FundingV9MoveAbsent},
		{"accepted without admission", `{"status":"accepted","requestId":"k1","operationId":"op_1"}`, false, ""},
		{"absent with admission", `{"status":"absent","requestId":"k1","operationId":"op_1","admission":{"operation":{"operationId":"op_1"}}}`, false, ""},
		{"foreign key", `{"status":"absent","requestId":"other","operationId":"op_1"}`, false, ""},
		{"unknown status", `{"status":"maybe","requestId":"k1","operationId":"op_1"}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/custody/v9/funding/moves" || r.URL.Query().Get("requestId") != "k1" || r.URL.Query().Get("realmId") != "realm" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				fmt.Fprintf(w, `{"success":true,"data":%s}`, tc.body)
			}))
			defer srv.Close()
			a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
			got, err := a.ResolveFundingV9Move(context.Background(), "k1")
			if (err == nil) != tc.ok {
				t.Fatalf("ok=%v err=%v", tc.ok, err)
			}
			if tc.ok && (got.Status != tc.status || got.OperationID != "op_1" || (tc.status == FundingV9MoveAccepted) != (got.Admission != nil)) {
				t.Fatalf("resolution: %+v", got)
			}
		})
	}
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: "http://unused/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	if _, err := a.ResolveFundingV9Move(context.Background(), ""); err == nil {
		t.Fatal("an empty request key must be refused before any request")
	}
}
