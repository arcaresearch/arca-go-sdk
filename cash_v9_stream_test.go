package arca

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func walletAccountFrame(revision uint64, state string) string {
	return fmt.Sprintf("id: %d\nevent: snapshot\ndata: {\"schema\":1,\"revision\":%d,\"realmId\":\"realm\",\"boundaryId\":\"bnd_1\",\"ownerAddress\":\"0x4ae85840bdb73e220d646eb0c564eee18a790197\",\"source\":null,\"walletState\":%q,\"attention\":[],\"balances\":{\"confirmedMicro\":\"3600000\",\"availableMicro\":\"3100000\",\"reservedMicro\":\"500000\",\"pendingInMicro\":\"0\",\"pendingOutMicro\":\"500000\",\"asOf\":{\"block\":46252318}},\"autoDeposit\":null,\"operations\":[]}\n\n", revision, revision, state)
}

// TestCashV9WalletAccountStream_ParsesSnapshotsAndResumes: frames are parsed
// (heartbeat comments ignored), the disconnect carries the last revision,
// and the reconnect sends it as Last-Event-ID and receives exactly one
// snapshot. The backoff between the two connections is the 1 s first step.
func TestCashV9WalletAccountStream_ParsesSnapshotsAndResumes(t *testing.T) {
	var mu sync.Mutex
	var lastEventIDs []string
	connections := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/custody/v9/cash/wallet-account/events" {
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("realmId") != "realm" || r.URL.Query().Get("boundaryId") != "bnd_1" {
			t.Error("selectors not forwarded", r.URL.Query())
		}
		if r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("headers", r.Header)
		}
		mu.Lock()
		connections++
		n := connections
		lastEventIDs = append(lastEventIDs, r.Header.Get("Last-Event-ID"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		switch n {
		case 1:
			fmt.Fprint(w, ": connected\n\n")
			fmt.Fprint(w, walletAccountFrame(41, "ready"))
			fmt.Fprint(w, ": heartbeat\n\n")
			fmt.Fprint(w, "event: control\ndata: {\"ignored\":true}\n\n")
			fmt.Fprint(w, walletAccountFrame(42, "needs_attention"))
			flusher.Flush()
			// The connection ends here, as the ingress reset does.
		case 2:
			fmt.Fprint(w, walletAccountFrame(42, "needs_attention"))
			flusher.Flush()
			// Ends too: the resume that follows must carry Last-Event-ID 42.
		default:
			fmt.Fprint(w, walletAccountFrame(42, "needs_attention"))
			flusher.Flush()
			<-r.Context().Done()
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}

	var seen []uint64
	err := a.StreamCashV9WalletAccount(context.Background(), "bnd_1", 0, func(w CashV9WalletAccount) error {
		if w.BoundaryID != "bnd_1" || w.Balances.ConfirmedMicro != "3600000" {
			t.Errorf("snapshot decoded wrong: %+v", w)
		}
		seen = append(seen, w.Revision)
		return nil
	})
	var disc *CashV9WalletAccountStreamDisconnectedError
	if !errors.As(err, &disc) || disc.LastRevision != 42 {
		t.Fatalf("expected a disconnect at revision 42, got %v", err)
	}
	if len(seen) != 2 || seen[0] != 41 || seen[1] != 42 {
		t.Fatalf("snapshots seen: %v", seen)
	}

	// Run reconnects with Last-Event-ID and the 1 s first backoff step.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen = nil
	started := time.Now()
	runErr := make(chan error, 1)
	go func() {
		runErr <- a.RunCashV9WalletAccountStream(ctx, "bnd_1", func(w CashV9WalletAccount) error {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, w.Revision)
			if len(seen) == 2 {
				cancel()
			}
			return nil
		})
	}()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not reconnect")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lastEventIDs) != 3 || lastEventIDs[1] != "" || lastEventIDs[2] != "42" {
		t.Fatalf("Last-Event-ID per connection: %q", lastEventIDs)
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Fatalf("reconnected after %v; the first backoff step is 1 s", elapsed)
	}
	if len(seen) != 2 || seen[0] != 42 || seen[1] != 42 {
		t.Fatalf("resume did not deliver exactly one snapshot per connection: %v", seen)
	}
}

// TestCashV9WalletAccountStream_RefusedConnectionIsTerminal: a 404 is the
// API error, not a disconnect, so Run stops instead of retrying forever.
func TestCashV9WalletAccountStream_RefusedConnectionIsTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"success":false,"error":{"code":"NOT_FOUND","message":"V9 cash resource not found"}}`)
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	err := a.RunCashV9WalletAccountStream(context.Background(), "bnd_missing", func(CashV9WalletAccount) error { return nil })
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected *NotFoundError, got %v", err)
	}
}

// TestCashV9WalletAccountStream_IngressErrorIsADisconnect: a 503 before the
// stream opens is the ingress mid-deploy, so Run retries it (and resumes from
// the last revision) rather than stopping as it would on a refusal.
func TestCashV9WalletAccountStream_IngressErrorIsADisconnect(t *testing.T) {
	var mu sync.Mutex
	connections := 0
	var lastEventIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connections++
		n := connections
		lastEventIDs = append(lastEventIDs, r.Header.Get("Last-Event-ID"))
		mu.Unlock()
		switch n {
		case 1, 2:
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "upstream connect error")
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, walletAccountFrame(5, "ready"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer srv.Close()
	a := &Arca{client: newHTTPClient(clientConfig{baseURL: srv.URL + "/api/v1", credential: "fixture"}), resolvedRealmID: "realm"}
	// Connection 1: the single-connection call reports the 503 as a
	// disconnect that still carries the caller's resume revision.
	err := a.StreamCashV9WalletAccount(context.Background(), "bnd_1", 7, func(CashV9WalletAccount) error { return nil })
	var disc *CashV9WalletAccountStreamDisconnectedError
	if !errors.As(err, &disc) || disc.LastRevision != 7 {
		t.Fatalf("a 503 must be a disconnect carrying the resume revision, got %v", err)
	}
	// Connections 2 (503) and 3 (stream): Run retries through the ingress
	// error after the 1 s step and delivers.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	runErr := make(chan error, 1)
	go func() {
		runErr <- a.RunCashV9WalletAccountStream(ctx, "bnd_1", func(CashV9WalletAccount) error { cancel(); return nil })
	}()
	select {
	case err := <-runErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not recover from the ingress error")
	}
	if time.Since(started) < time.Second {
		t.Fatal("run retried the 503 without backing off")
	}
	mu.Lock()
	defer mu.Unlock()
	if connections != 3 || lastEventIDs[0] != "7" || lastEventIDs[1] != "" || lastEventIDs[2] != "" {
		t.Fatalf("connections %d, Last-Event-IDs %q", connections, lastEventIDs)
	}
}

func TestCashV9StreamBackoff(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, d := range want {
		if got := cashV9StreamBackoff(i); got != d {
			t.Fatalf("attempt %d: %v, want %v", i, got, d)
		}
	}
}
