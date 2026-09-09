package arca

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMutationsNeverReplayTransientResponses(t *testing.T) {
	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		for _, status := range []int{502, 503, 504} {
			for _, body := range []string{`{"success":false,"error":{"code":"ALLOCATION_UNAVAILABLE","message":"unknown","details":{"reason":"dispatch_uncertain"}}}`, `{"success":false,"code":"ALLOCATION_UNAVAILABLE","error":"unknown","details":{"reason":"dispatch_uncertain"}}`} {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}))
				a := newTestArca(t, srv.URL)
				err := a.client.executeWithAuthRetry(context.Background(), method, "/mutation", nil, map[string]string{"path": "/same-key"}, nil)
				srv.Close()
				var apiErr *ArcaError
				if calls != 1 || !errors.As(err, &apiErr) || apiErr.Code != "ALLOCATION_UNAVAILABLE" || apiErr.StatusCode != status || apiErr.Details["reason"] != "dispatch_uncertain" {
					t.Fatalf("%s %d: calls=%d error=%+v", method, status, calls, err)
				}
			}
		}
	}
}

func TestMutationLostResponseIsNotReplayed(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer srv.Close()
	a := newTestArca(t, srv.URL)
	_, err := a.ReconcileOrderKey(context.Background(), "obj", "/same-key", true)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestOrderKeyReconciliationPreservesIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/objects/obj/exchange/order-key/reconcile" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(w, 200, map[string]any{"status": "retired_never_dispatched", "realmId": "realm", "exchangeObjectId": "obj", "path": "/same-key"})
	}))
	defer srv.Close()
	out, err := newTestArca(t, srv.URL).ReconcileOrderKey(context.Background(), "obj", "/same-key", true)
	if err != nil || out.Status != "retired_never_dispatched" || out.Path != "/same-key" || out.ExchangeObjectID != "obj" {
		t.Fatalf("%+v %v", out, err)
	}
}
