package arca

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three action routes are called with the realm in the query, the exact
// request body, and the answer decoded; a refused response surfaces as a
// ConflictError carrying its limitation.
func TestWalletActionRoutes(t *testing.T) {
	proposal := `{"success":true,"data":{"schema":1,"proposalId":"prp_1","requestId":"send-1","requestedAction":{"kind":"send","cashObjectId":"obj_cash","amountMicro":"500000","destination":"0x00000000000000000000000000000000000000d5"},"revision":7,"requirements":[{"id":"req_send","attemptId":"att_1","state":"awaiting_approval","schemaId":"arca.kernel.v9.Move","schemaVersion":1,"variant":"external_recipient","payloadHash":"0xabc","typedDataJSON":"{}","issuedAt":1,"expiresAt":901,"authorizationDependsOn":[],"executionDependsOn":[]}],"operations":[],"limitations":[],"createdAt":"2026-09-23T00:00:00.000000Z","updatedAt":"2026-09-23T00:00:00.000000Z"}}`
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("realmId") != "realm" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("realm or auth not forwarded: %s %v", r.URL, r.Header)
		}
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			bodies = append(bodies, body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/wallet/action-proposals":
			_, _ = io.WriteString(w, proposal)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/wallet/action-proposals/prp_1":
			_, _ = io.WriteString(w, proposal)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/wallet/action-proposals/prp_1/requirements/req_send/responses":
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"success":false,"error":{"code":"ACTION_ATTEMPT_STALE","message":"stale","details":{"limitation":"attempt_stale","requirementId":"req_send"}}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	ctx := context.Background()

	req := WalletActionRequest{RequestID: "send-1", RequestedAction: WalletRequestedAction{Kind: "send", CashObjectID: "obj_cash", AmountMicro: "500000", Destination: "0x00000000000000000000000000000000000000d5"}}
	p, err := a.ProposeWalletAction(ctx, req)
	if err != nil || p.ProposalID != "prp_1" || len(p.Requirements) != 1 || p.Requirements[0].ExpiresAt != 901 {
		t.Fatalf("propose: %+v %v", p, err)
	}
	if action := bodies[0]["requestedAction"].(map[string]any); bodies[0]["requestId"] != "send-1" || action["cashObjectId"] != "obj_cash" || action["amountMicro"] != "500000" || bodies[0]["client"] != nil {
		t.Fatalf("propose body: %v", bodies[0])
	}
	if got, err := a.GetWalletActionProposal(ctx, "prp_1"); err != nil || got.Revision != 7 {
		t.Fatalf("get: %+v %v", got, err)
	}
	_, err = a.RespondWalletRequirement(ctx, "prp_1", "req_send", WalletRequirementResponse{AttemptID: "att_0", PayloadHash: "0xabc", Decision: "approve", Signature: "0x01", IdempotencyKey: "k"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Details["limitation"] != "attempt_stale" {
		t.Fatalf("stale answer: %v", err)
	}
	if last := bodies[len(bodies)-1]; last["attemptId"] != "att_0" || last["decision"] != "approve" || last["idempotencyKey"] != "k" {
		t.Fatalf("response body: %v", last)
	}
}

// A route activation names its deposit link on the wire, and a relay-executed
// proposal refused by a Cash submit route is a conflict (step 19A-2).
func TestWalletRelayActionWireAndRefusal(t *testing.T) {
	raw, err := json.Marshal(WalletRequestedAction{Kind: "enable_automatic_deposits", PrivyObjectID: "obj_privy", SourceWalletID: "pwl_1", CashObjectID: "obj_cash", DepositLinkID: "dlk_1"})
	if err != nil || !strings.Contains(string(raw), `"depositLinkId":"dlk_1"`) {
		t.Fatalf("requested action wire: %s %v", raw, err)
	}
	var conflict *ConflictError
	if err := mapAPIError("ACTION_PROPOSAL_REQUIRED", "answer it through its action proposal", "err_1", nil); !errors.As(err, &conflict) {
		t.Fatalf("ACTION_PROPOSAL_REQUIRED: %T %v", err, err)
	}
}

// Every published action-proposal fixture decodes into the SDK type without
// losing a field.
func TestWalletActionProposalDecodesFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "backend", "libs", "arca-go", "cashv9", "testdata", "wallet-signing")
	matches, err := filepath.Glob(filepath.Join(dir, "proposal-*.json"))
	if err != nil || len(matches) == 0 {
		t.Skipf("fixtures not present at %s", dir)
	}
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var p WalletActionProposal
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		again, _ := json.Marshal(p)
		var want, got any
		_ = json.Unmarshal(raw, &want)
		_ = json.Unmarshal(again, &got)
		if !jsonEqual(want, got) {
			t.Fatalf("%s: SDK type does not round-trip the fixture\n%s\n%s", filepath.Base(path), raw, again)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
