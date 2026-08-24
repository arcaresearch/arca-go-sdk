package arca

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListIsolationZones_SendsPrefix pins the query shape a teardown
// depends on: the prefix has to reach the server, or the sweep lists the
// whole realm and tries to retire zones it does not own.
func TestListIsolationZones_SendsPrefix(t *testing.T) {
	var gotPath, gotPrefix, gotRealm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotPrefix = r.URL.Query().Get("prefix")
		gotRealm = r.URL.Query().Get("realmId")
		writeEnvelope(w, http.StatusOK, IsolationZoneList{
			RealmID: gotRealm,
			Zones:   []IsolationZone{{ID: "izn_1", Path: "/qa/run-1/zone", BoundaryID: "bnd_1"}},
		})
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	list, err := a.ListIsolationZones(context.Background(), &ListIsolationZonesOptions{Prefix: "/qa/run-1"})
	if err != nil {
		t.Fatalf("ListIsolationZones: %v", err)
	}
	if gotPath != "/api/v1/isolation-zones" {
		t.Errorf("path = %q, want /api/v1/isolation-zones", gotPath)
	}
	if gotPrefix != "/qa/run-1" {
		t.Errorf("prefix = %q, want /qa/run-1", gotPrefix)
	}
	if gotRealm == "" {
		t.Error("realmId was not sent")
	}
	if len(list.Zones) != 1 || list.Zones[0].Path != "/qa/run-1/zone" {
		t.Errorf("zones = %#v", list.Zones)
	}
}

// TestListIsolationZones_NilOptionsOmitsPrefix keeps "list the realm" a
// distinct request from "list a subtree" — an empty prefix parameter
// would be a raw prefix match on "" server-side, which is the same set
// but a needlessly different query.
func TestListIsolationZones_NilOptionsOmitsPrefix(t *testing.T) {
	var hadPrefix bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadPrefix = r.URL.Query()["prefix"]
		writeEnvelope(w, http.StatusOK, IsolationZoneList{})
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	if _, err := a.ListIsolationZones(context.Background(), nil); err != nil {
		t.Fatalf("ListIsolationZones: %v", err)
	}
	if hadPrefix {
		t.Error("prefix parameter was sent for a realm-wide listing")
	}
}

// TestArchiveIsolationZone_SendsTrimmedPath mirrors CreateIsolationZone's
// trailing-slash handling so a caller can round-trip a path it created.
func TestArchiveIsolationZone_SendsTrimmedPath(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		archivedAt := "2026-08-23T09:14:00.000000Z"
		writeEnvelope(w, http.StatusOK, IsolationZone{
			ID: "izn_1", Path: "/users/alice", BoundaryID: "bnd_1",
			ArchivedAt: &archivedAt, ArchivedReason: "tenant offboarded",
		})
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	zone, err := a.ArchiveIsolationZone(context.Background(), ArchiveIsolationZoneOptions{
		Path:   "/users/alice/",
		Reason: "tenant offboarded",
	})
	if err != nil {
		t.Fatalf("ArchiveIsolationZone: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/isolation-zones/archive" {
		t.Errorf("path = %q, want /api/v1/isolation-zones/archive", gotPath)
	}
	if body["path"] != "/users/alice" {
		t.Errorf("body path = %v, want /users/alice", body["path"])
	}
	if body["reason"] != "tenant offboarded" {
		t.Errorf("body reason = %v", body["reason"])
	}
	if zone.ArchivedAt == nil || *zone.ArchivedAt == "" {
		t.Error("expected archivedAt to be decoded")
	}
}

// TestArchiveIsolationZone_OmitsEmptyReason keeps the server's "no reason
// given" default reachable rather than sending an empty string it would
// have to special-case.
func TestArchiveIsolationZone_OmitsEmptyReason(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		writeEnvelope(w, http.StatusOK, IsolationZone{ID: "izn_1", Path: "/users/alice"})
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	if _, err := a.ArchiveIsolationZone(context.Background(), ArchiveIsolationZoneOptions{
		Path: "/users/alice",
	}); err != nil {
		t.Fatalf("ArchiveIsolationZone: %v", err)
	}
	if _, present := body["reason"]; present {
		t.Errorf("reason was sent for an empty Reason: %#v", body)
	}
}

// TestArchiveIsolationZone_RejectsBadPathBeforeRequest keeps a malformed
// path a local error: the platform would reject it anyway, and a sweep
// looping over paths gets a clearer failure without a round trip.
func TestArchiveIsolationZone_RejectsBadPathBeforeRequest(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		writeEnvelope(w, http.StatusOK, IsolationZone{})
	}))
	defer srv.Close()

	a := newTestArca(t, srv.URL)
	if _, err := a.ArchiveIsolationZone(context.Background(), ArchiveIsolationZoneOptions{
		Path: "users/alice",
	}); err == nil {
		t.Fatal("expected a validation error for a path without a leading slash")
	}
	if called {
		t.Error("a malformed path should not reach the server")
	}
}
