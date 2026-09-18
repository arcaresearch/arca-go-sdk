package arca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const maxUint256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

func TestAddressObservation_RegisterSnapshotChanges_Uint256Fidelity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/api/v1/address-observations/watches":
			var req RegisterAddressWatchRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.RealmID != "realm" || req.ChainID != "999" || req.Address != "0x4ae85840bdb73e220d646eb0c564eee18a790197" {
				t.Error("request changed", req)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"success":true,"data":{"created":true,"cursor":"c0","watch":{"watchId":"wch_1","realmId":"realm","chainId":"999","tokenAddress":"0xb88339cb7199b77e23db6e890353e22632ba630f","address":"0x4ae85840bdb73e220d646eb0c564eee18a790197","lifecycle":"registered","revision":"0","balance":{"observedRaw":null,"health":"awaiting_observer"},"registeredAt":"2026-09-18T00:00:00.000000Z","updatedAt":"2026-09-18T00:00:00.000000Z"}}}`)
		case "/api/v1/address-observations/snapshot":
			if r.URL.Query().Get("pageToken") == "expired" {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"success":false,"error":{"code":"OBSERVATION_CURSOR_EXPIRED","message":"restart"}}`)
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"realmId":"realm","readAt":"2026-09-18T00:00:00.000000Z","cursor":"c1","watches":[{"watchId":"wch_1","realmId":"realm","chainId":"999","tokenAddress":"0xb883","address":"0x4ae8","lifecycle":"active","revision":"3","balance":{"observedRaw":"`+maxUint256+`","completeThroughBlock":"46252317","health":"live"},"registeredAt":"x","updatedAt":"y"}],"nextPageToken":"p2"}}`)
		case "/api/v1/address-observations/changes":
			if r.URL.Query().Get("after") != "c1" {
				t.Error("after cursor not forwarded", r.URL.Query())
			}
			fmt.Fprint(w, `{"success":true,"data":{"realmId":"realm","events":[{"schemaVersion":1,"eventId":"aob_1","realmId":"realm","sequence":"42","cursor":"c42","type":"transfer.observed","observedAt":"t","chainId":"999","amountRaw":"`+maxUint256+`","effects":[{"watchId":"wch_1","address":"0x4ae8","direction":"incoming","deltaRaw":"`+maxUint256+`","fundingDelta":"`+maxUint256+`","watchRevision":"4","balance":{"observedRaw":"`+maxUint256+`","health":"live"}}]}],"oldestValidCursor":"c1","currentCursor":"c42","nextCursor":"c42","hasMore":false}}`)
		default:
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	ctx := context.Background()

	reg, err := a.RegisterAddressWatch(ctx, RegisterAddressWatchRequest{ChainID: "999", TokenAddress: "0xb88339cb7199b77e23db6e890353e22632ba630f", Address: "0x4ae85840bdb73e220d646eb0c564eee18a790197"})
	if err != nil || !reg.Created || reg.Watch.WatchID != "wch_1" || reg.Watch.Balance.ObservedRaw != nil {
		t.Fatal(reg, err)
	}
	snap, err := a.ListAddressWatchSnapshot(ctx, "", 100)
	if err != nil || len(snap.Watches) != 1 || snap.NextPageToken != "p2" {
		t.Fatal(snap, err)
	}
	// uint256 survives the round trip unchanged, and parses exactly.
	if *snap.Watches[0].Balance.ObservedRaw != maxUint256 {
		t.Fatalf("balance lost precision: %s", *snap.Watches[0].Balance.ObservedRaw)
	}
	if v, ok := new(big.Int).SetString(*snap.Watches[0].Balance.ObservedRaw, 10); !ok || v.BitLen() != 256 {
		t.Fatal("uint256 not exactly representable")
	}
	var expired *ObservationCursorExpiredError
	if _, err := a.ListAddressWatchSnapshot(ctx, "expired", 100); !errors.As(err, &expired) || expired.Cursor != "expired" {
		t.Fatalf("expired page token not typed: %v", err)
	}
	changes, err := a.ListAddressObservationChanges(ctx, "c1", 100)
	if err != nil || len(changes.Events) != 1 || changes.Events[0].AmountRaw != maxUint256 || changes.Events[0].Effects[0].FundingDelta != maxUint256 {
		t.Fatal(changes, err)
	}
}

func TestAddressObservation_StreamParsesIdEventDataAndResumes(t *testing.T) {
	var connections atomic.Int32
	var lastEventIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/address-observations/events" || r.URL.Query().Get("realmId") != "realm" {
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
			return
		}
		n := connections.Add(1)
		lastEventIDs = append(lastEventIDs, r.Header.Get("Last-Event-ID"))
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()
		switch n {
		case 1:
			// Two frames: the second carries an id but the payload omits the
			// cursor, so the id must be used; a heartbeat sits between them.
			fmt.Fprint(w, "id: c10\nevent: changes\ndata: {\"events\":[{\"schemaVersion\":1,\"eventId\":\"e10\",\"realmId\":\"realm\",\"sequence\":\"10\",\"cursor\":\"c10\",\"type\":\"transfer.observed\",\"amountRaw\":\""+maxUint256+"\"}],\"cursor\":\"c10\"}\n\n")
			flusher.Flush()
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			fmt.Fprint(w, "id: c11\nevent: changes\ndata: {\"events\":[{\"schemaVersion\":1,\"eventId\":\"e11\",\"realmId\":\"realm\",\"sequence\":\"11\",\"cursor\":\"c11\",\"type\":\"watch.health_changed\"}]}\n\n")
			flusher.Flush()
			// Then the connection drops (handler returns): EOF is a disconnect.
		case 2:
			// The reconnecting runner's first connection: one frame, then
			// the connection drops again.
			fmt.Fprint(w, "id: c12\nevent: changes\ndata: {\"events\":[{\"schemaVersion\":1,\"eventId\":\"e12\",\"realmId\":\"realm\",\"sequence\":\"12\",\"cursor\":\"c12\",\"type\":\"transfer.observed\"}],\"cursor\":\"c12\"}\n\n")
			flusher.Flush()
		case 3:
			// Resumed exactly after c12; the server now asks for a reset.
			fmt.Fprint(w, "event: control\ndata: {\"type\":\"reset_required\",\"code\":\"OBSERVATION_CURSOR_EXPIRED\",\"message\":\"gone\",\"cursor\":\"c12\"}\n\n")
			flusher.Flush()
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var got []AddressObservationBatch
	err := a.StreamAddressObservations(ctx, "c9", func(b AddressObservationBatch) error {
		got = append(got, b)
		return nil
	})
	var disc *ObservationStreamDisconnectedError
	if !errors.As(err, &disc) {
		t.Fatalf("EOF must be a disconnect error, got %v", err)
	}
	if disc.LastCursor != "c11" || len(got) != 2 || got[0].Cursor != "c10" || got[1].Cursor != "c11" || got[0].Events[0].AmountRaw != maxUint256 {
		t.Fatalf("batches %+v last=%s", got, disc.LastCursor)
	}
	if lastEventIDs[0] != "c9" {
		t.Fatalf("Last-Event-ID not sent on first connect: %q", lastEventIDs[0])
	}

	// RunAddressObservationStream resumes from the last accepted cursor and
	// stops on the typed reset.
	err = a.RunAddressObservationStream(ctx, "c9", func(AddressObservationBatch) error { return nil })
	var reset *ObservationStreamResetError
	if !errors.As(err, &reset) || reset.Code != "OBSERVATION_CURSOR_EXPIRED" {
		t.Fatalf("reset frame not typed: %v", err)
	}
	if len(lastEventIDs) != 3 || lastEventIDs[1] != "c9" || lastEventIDs[2] != "c12" {
		t.Fatalf("reconnect did not resume from the last accepted cursor: %v", lastEventIDs)
	}
}

func TestAddressObservation_StreamCallbackErrorLeavesCursorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: c1\nevent: changes\ndata: {\"events\":[{\"schemaVersion\":1,\"eventId\":\"e1\",\"realmId\":\"realm\",\"sequence\":\"1\",\"cursor\":\"c1\",\"type\":\"transfer.observed\"}],\"cursor\":\"c1\"}\n\n")
		fmt.Fprint(w, "id: c2\nevent: changes\ndata: {\"events\":[{\"schemaVersion\":1,\"eventId\":\"e2\",\"realmId\":\"realm\",\"sequence\":\"2\",\"cursor\":\"c2\",\"type\":\"transfer.observed\"}],\"cursor\":\"c2\"}\n\n")
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	boom := errors.New("db write failed")
	seen := 0
	err := a.StreamAddressObservations(context.Background(), "", func(b AddressObservationBatch) error {
		seen++
		return boom
	})
	if !errors.Is(err, boom) || seen != 1 {
		t.Fatalf("callback error must stop delivery on the failed batch: seen=%d err=%v", seen, err)
	}

	// An unsupported schema version is refused before the callback runs.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: c1\nevent: changes\ndata: {\"events\":[{\"schemaVersion\":2,\"eventId\":\"e1\",\"realmId\":\"realm\",\"sequence\":\"1\",\"cursor\":\"c1\",\"type\":\"transfer.observed\"}],\"cursor\":\"c1\"}\n\n")
	}))
	defer srv2.Close()
	a2 := &Arca{client: newHTTPClient(clientConfig{baseURL: srv2.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	var schema *ObservationSchemaError
	if err := a2.StreamAddressObservations(context.Background(), "", func(AddressObservationBatch) error { t.Fatal("callback ran"); return nil }); !errors.As(err, &schema) || schema.Version != 2 {
		t.Fatalf("schema error not typed: %v", err)
	}

	// A 409 at connect is the typed cursor error, not a disconnect.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"success":false,"error":{"code":"OBSERVATION_CURSOR_EXPIRED","message":"take a snapshot"}}`)
	}))
	defer srv3.Close()
	a3 := &Arca{client: newHTTPClient(clientConfig{baseURL: srv3.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	var expired *ObservationCursorExpiredError
	if err := a3.StreamAddressObservations(context.Background(), "old", func(AddressObservationBatch) error { return nil }); !errors.As(err, &expired) || expired.Cursor != "old" {
		t.Fatalf("409 not typed: %v", err)
	}
	// Cancellation ends RunAddressObservationStream promptly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a3.RunAddressObservationStream(ctx, "", func(AddressObservationBatch) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
