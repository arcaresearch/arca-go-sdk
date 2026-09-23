package arca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestProviderAndDepositLinkRequests pins the wire shape of every provider
// and deposit-link call: paths, methods, realm scoping and bodies.
func TestProviderAndDepositLinkRequests(t *testing.T) {
	seen := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("%s %s: body is not JSON: %s", r.Method, r.URL.Path, raw)
			}
		}
		key := r.Method + " " + r.URL.Path
		body["_query"] = r.URL.RawQuery
		seen[key] = body
		switch key {
		case "POST /api/v1/objects":
			fmt.Fprint(w, `{"success":true,"data":{"object":{"id":"obj_p","path":"/users/a/privy","type":"provider"}}}`)
		case "POST /api/v1/objects/obj_p/provider/connect", "PATCH /api/v1/objects/obj_p/provider/wallets/pwl_a", "GET /api/v1/objects/obj_p/provider":
			fmt.Fprint(w, `{"success":true,"data":{"objectId":"obj_p","path":"/users/a/privy","state":{"schema":1,"provider":"privy","subject":"did:privy:a","connection":{"status":"verified","checkedAt":"t","evidence":{"kind":"privy_identity_token","issuedAt":"t","expiresAt":"t"}}},"wallets":[{"walletId":"pwl_a","chainType":"ethereum","address":"0xA","verification":{"status":"verified"},"controls":[],"observation":null}]}}`)
		case "POST /api/v1/deposit-links", "GET /api/v1/deposit-links/dlk_1", "POST /api/v1/deposit-links/dlk_1/revoke":
			fmt.Fprint(w, `{"success":true,"data":{"id":"dlk_1","requested":{"state":"active","at":"t"},"observed":{"status":"unknown","matchesDestination":false},"progress":"unknown","consent":null,"limits":null}}`)
		case "GET /api/v1/deposit-links":
			fmt.Fprint(w, `{"success":true,"data":{"links":[{"id":"dlk_1","observed":{"status":"off","matchesDestination":false},"progress":"in_sync"}]}}`)
		default:
			t.Errorf("unexpected %s", key)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	ctx := context.Background()

	if _, err := a.EnsureProvider(ctx, EnsureProviderOptions{Ref: "/users/a/privy"}).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if b := seen["POST /api/v1/objects"]; b["type"] != "provider" || b["metadata"] != `{"provider":"privy"}` || b["path"] != "/users/a/privy" {
		t.Fatalf("ensure body %v", b)
	}
	d, err := a.ConnectProvider(ctx, "obj_p", ConnectProviderOptions{Evidence: ProviderEvidence{Kind: ProviderEvidenceKindPrivyIdentityToken, IdentityToken: "id-token"},
		Wallets: []ProviderWalletRole{{Address: "0xA", Role: "primary_deposit"}}})
	if err != nil || d.State.Subject != "did:privy:a" || len(d.Wallets) != 1 || d.Wallets[0].Observation != nil {
		t.Fatalf("connect: %+v %v", d, err)
	}
	ev := seen["POST /api/v1/objects/obj_p/provider/connect"]["evidence"].(map[string]any)
	if ev["kind"] != "privy_identity_token" || ev["identityToken"] != "id-token" {
		t.Fatalf("evidence body %v", ev)
	}
	role := "signer"
	if _, err := a.UpdateProviderWallet(ctx, "obj_p", "pwl_a", UpdateProviderWalletOptions{Role: &role}); err != nil {
		t.Fatal(err)
	}
	if b := seen["PATCH /api/v1/objects/obj_p/provider/wallets/pwl_a"]; b["role"] != "signer" || b["controls"] != nil {
		t.Fatalf("patch body %v (nil controls must be omitted)", b)
	}
	if _, err := a.GetProvider(ctx, "obj_p"); err != nil {
		t.Fatal(err)
	}

	l, err := a.CreateDepositLink(ctx, CreateDepositLinkOptions{RequestID: "req-1", SourceObjectID: "obj_p", SourceWalletID: "pwl_a", DestinationObjectID: "obj_c", Adapter: "0xAd"})
	if err != nil || l.ID != "dlk_1" || l.Observed.Status != DepositRouteUnknown || l.Consent != nil {
		t.Fatalf("create: %+v %v", l, err)
	}
	if b := seen["POST /api/v1/deposit-links"]; b["realmId"] != "realm" || b["requestId"] != "req-1" || b["sourceWalletId"] != "pwl_a" || b["destinationObjectId"] != "obj_c" || b["adapter"] != "0xAd" {
		t.Fatalf("create body %v", b)
	}
	if _, err := a.GetDepositLink(ctx, "dlk_1"); err != nil || seen["GET /api/v1/deposit-links/dlk_1"]["_query"] != "realmId=realm" {
		t.Fatalf("get: %v %v", err, seen["GET /api/v1/deposit-links/dlk_1"])
	}
	links, err := a.ListDepositLinks(ctx, "obj_p")
	if err != nil || len(links) != 1 || links[0].Progress != DepositLinkInSync {
		t.Fatalf("list: %+v %v", links, err)
	}
	if _, err := a.RevokeDepositLink(ctx, "dlk_1", "rev-1"); err != nil {
		t.Fatal(err)
	}
	if b := seen["POST /api/v1/deposit-links/dlk_1/revoke"]; b["realmId"] != "realm" || b["requestId"] != "rev-1" {
		t.Fatalf("revoke body %v", b)
	}
}
