package arca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// attachServer answers the operation read and the order read, recording every
// request so a test can prove attaching mutates nothing.
type attachServer struct {
	mu       sync.Mutex
	requests []string
	orderErr bool
	// fillsComplete answers of the order read, consumed in order; the last
	// value repeats.
	fillsComplete []bool
	operation     map[string]any
}

func (s *attachServer) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
}

func (s *attachServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.requests...)
}

func (s *attachServer) nextComplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fillsComplete) == 0 {
		return true
	}
	value := s.fillsComplete[0]
	if len(s.fillsComplete) > 1 {
		s.fillsComplete = s.fillsComplete[1:]
	}
	return value
}

func attachOperation(overrides map[string]any) map[string]any {
	input, _ := json.Marshal(map[string]any{
		"exchangeObjectId": "obj-1", "market": "hl:0:BTC", "side": "buy", "size": "0.01", "orderType": "MARKET",
	})
	outcome, _ := json.Marshal(map[string]any{
		"orderId": "ord_abc", "status": "filled", "filledSize": "0.01", "avgFillPrice": "50000",
	})
	operation := map[string]any{
		"id": "op_place", "realmId": "rlm", "path": "/op/order/btc-1", "type": "order", "state": "completed",
		"input": string(input), "outcome": string(outcome),
		"createdAt": "2026-09-11T00:00:00.000000Z", "updatedAt": "2026-09-11T00:00:00.000000Z",
	}
	for k, v := range overrides {
		operation[k] = v
	}
	return operation
}

func newAttachServer(t *testing.T, s *attachServer) *Arca {
	t.Helper()
	if s.operation == nil {
		s.operation = attachOperation(nil)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handle's event capture dials the socket exactly as a placed
		// handle's does. Refusing the upgrade leaves it degraded to reads,
		// which is what these tests exercise; it is not an API request and
		// does not belong in the recorded set.
		if r.URL.Path == "/api/v1/ws" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.record(r)
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/operations/"):
			writeEnvelope(w, 200, map[string]any{"operation": s.operation, "events": []any{}, "deltas": []any{}})
		case strings.Contains(r.URL.Path, "/exchange/orders/"):
			writeEnvelope(w, 200, map[string]any{
				"order": map[string]any{
					"id": "ord_abc", "accountId": "acc-1", "realmId": "rlm", "market": "hl:0:BTC", "side": "buy",
					"orderType": "MARKET", "size": "0.01", "filledSize": "0.01", "avgFillPrice": "50000",
					"status": "FILLED", "reduceOnly": false, "timeInForce": "IOC", "leverage": 1,
					"createdAt": "", "updatedAt": "",
				},
				"fills":         []any{},
				"fillsComplete": s.nextComplete(),
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			writeEnvelope(w, 404, map[string]any{})
		}
	}))
	t.Cleanup(server.Close)
	return newTestArca(t, server.URL)
}

func TestOrderHandleForAttachesWithASingleReadAndNoMutation(t *testing.T) {
	// The whole point: an integration whose backend placed this order must be
	// able to obtain a handle without placing, cancelling or resizing anything.
	s := &attachServer{}
	a := newAttachServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handle, err := a.OrderHandleFor(ctx, "obj-1", "op_place")
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := handle.Submitted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Operation.ID != "op_place" || handle.placementPath != "/op/order/btc-1" {
		t.Fatalf("handle not bound to the read operation: %+v %q", submitted.Operation, handle.placementPath)
	}
	if got := s.seen(); len(got) != 1 || got[0] != "GET /api/v1/operations/op_place" {
		t.Fatalf("attach issued %v, want exactly one operation read", got)
	}
}

func TestOrderHandleForAccountedResolvesOnAnAlreadyRecordedOrder(t *testing.T) {
	s := &attachServer{fillsComplete: []bool{true}}
	a := newAttachServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handle, err := a.OrderHandleFor(ctx, "obj-1", "op_place")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := handle.Accounted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if detail.FillsComplete == nil || !*detail.FillsComplete {
		t.Fatalf("fillsComplete = %v, want true", detail.FillsComplete)
	}
	for _, request := range s.seen() {
		if strings.HasPrefix(request, "POST ") || strings.HasPrefix(request, "PATCH ") || strings.HasPrefix(request, "DELETE ") {
			t.Fatalf("attach+accounted issued a mutation: %s", request)
		}
	}
}

func TestOrderHandleForAccountedConvergesOnAPendingOrder(t *testing.T) {
	s := &attachServer{fillsComplete: []bool{false, true}}
	a := newAttachServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handle, err := a.OrderHandleFor(ctx, "obj-1", "op_place")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Accounted(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOrderHandleForRefusesAnotherAccountsOperation(t *testing.T) {
	// An id from another account must never resolve into a handle on this one.
	s := &attachServer{}
	a := newAttachServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := a.OrderHandleFor(ctx, "obj-other", "op_place")
	var arcaErr *ArcaError
	if !errors.As(err, &arcaErr) || arcaErr.Code != "ORDER_IDENTITY_MISMATCH" {
		t.Fatalf("err = %v, want ORDER_IDENTITY_MISMATCH", err)
	}
	if got := s.seen(); len(got) != 1 {
		t.Fatalf("refusal issued %v, want only the operation read", got)
	}
}

func TestOrderHandleForRefusesANonOrderOperation(t *testing.T) {
	s := &attachServer{operation: attachOperation(map[string]any{"type": "transfer"})}
	a := newAttachServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := a.OrderHandleFor(ctx, "obj-1", "op_place")
	var arcaErr *ArcaError
	if !errors.As(err, &arcaErr) || arcaErr.Code != "ORDER_IDENTITY_MISMATCH" {
		t.Fatalf("err = %v, want ORDER_IDENTITY_MISMATCH", err)
	}
	if !strings.Contains(err.Error(), "not an order") {
		t.Fatalf("message %q should name the actual type", err.Error())
	}
}

func TestOrderHandleForAcceptsAnOperationWithNoRecordedAccount(t *testing.T) {
	// Absent input is not a mismatch — an older operation may not carry it.
	s := &attachServer{operation: attachOperation(map[string]any{"input": nil})}
	a := newAttachServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := a.OrderHandleFor(ctx, "obj-1", "op_place"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedImmediateIntentReconstructsConfirmationSemantics(t *testing.T) {
	// Confirmed waits for execution on a market/IOC order and for placement on
	// a resting one; an attached handle must classify the same way PlaceOrder
	// did, from what the operation recorded.
	intent := func(fields map[string]any) Operation {
		raw, _ := json.Marshal(fields)
		text := string(raw)
		return Operation{Input: &text}
	}
	for _, tc := range []struct {
		name   string
		fields map[string]any
		want   bool
	}{
		{"market", map[string]any{"orderType": "MARKET"}, true},
		{"limit IOC", map[string]any{"orderType": "LIMIT", "timeInForce": "IOC"}, true},
		{"limit FOK", map[string]any{"orderType": "LIMIT", "timeInForce": "FOK"}, true},
		{"resting limit", map[string]any{"orderType": "LIMIT", "timeInForce": "GTC"}, false},
		{"trigger", map[string]any{"orderType": "MARKET", "isTrigger": true}, false},
		{"unrecorded type", map[string]any{}, true},
	} {
		if got := recordedImmediateIntent(intent(tc.fields)); got != tc.want {
			t.Errorf("%s: immediate = %v, want %v", tc.name, got, tc.want)
		}
	}
	if recordedImmediateIntent(Operation{}) != true {
		t.Error("an operation with no recorded input defaults to immediate, like an unspecified order type")
	}
}
