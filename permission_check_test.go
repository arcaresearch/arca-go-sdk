package arca

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckPermissionsReportsDeniedPairsForTheClientRealm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/auth/permission-check" {
			t.Error("unexpected endpoint", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		var body struct {
			RealmID string            `json:"realmId"`
			Checks  []PermissionCheck `json:"checks"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.RealmID != "realm" || len(body.Checks) != 2 || body.Checks[1].Action != "arca:PlaceOrder" {
			t.Error("request changed", body)
		}
		fmt.Fprint(w, `{"success":true,"data":{"realmId":"realm","results":[{"action":"arca:ReadObject","resource":"/users/probe","allowed":true},{"action":"arca:PlaceOrder","resource":"/users/probe","allowed":false}]}}`)
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	got, err := a.CheckPermissions(context.Background(), PermissionCheck{Action: "arca:ReadObject", Resource: "/users/probe"}, PermissionCheck{Action: "arca:PlaceOrder", Resource: "/users/probe"})
	if err != nil {
		t.Fatal(err)
	}
	denied := got.Denied()
	if got.RealmID != "realm" || len(denied) != 1 || denied[0].Action != "arca:PlaceOrder" {
		t.Fatalf("denied %v from %+v", denied, got)
	}
}
