package arca

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A spent co-sign nonce is an ordinary lifecycle outcome — a retry racing the
// original, or a user who cancelled — and the caller's correct response is to
// re-propose, not to tell the user their approval didn't match the request.
// These pin the typed error that makes that branch possible without
// string-matching a message.

func TestMapAPIError_CosignNonceUsed(t *testing.T) {
	err := mapAPIError("COSIGN_NONCE_USED", "co-sign nonce has already been used; re-propose the action", "err_1",
		map[string]any{
			"boundaryId": "bnd_armed",
			"nonce":      "4611686018427387904",
			"reason":     "nonce_consumed",
			"resolution": "re-propose the action to obtain a fresh nonce, re-sign, and resubmit",
		})

	var typed *CosignNonceUsedError
	if !errors.As(err, &typed) {
		t.Fatalf("error is %T, want *CosignNonceUsedError", err)
	}
	if typed.Details.BoundaryID != "bnd_armed" {
		t.Errorf("BoundaryID = %q", typed.Details.BoundaryID)
	}
	if typed.Details.Nonce != "4611686018427387904" {
		t.Errorf("Nonce = %q, want the refused value", typed.Details.Nonce)
	}
	if typed.Details.Reason != "nonce_consumed" {
		t.Errorf("Reason = %q", typed.Details.Reason)
	}
	if typed.Details.Resolution == "" {
		t.Error("Resolution is empty; a 412 whose remedy the caller must infer is how this became a reported signing failure")
	}
	// Must not be confusable with a bad signature.
	var validation *ValidationError
	if errors.As(err, &validation) {
		t.Error("a spent nonce must not surface as a ValidationError")
	}
	// The base error stays reachable for Code/Message.
	var base *ArcaError
	if !errors.As(err, &base) || base.Code != "COSIGN_NONCE_USED" {
		t.Errorf("base error unreachable or wrong code: %+v", base)
	}
}

// Both lanes share one code deliberately: the action is identical, so an
// integrator spanning kernel generations needs a single branch. Gobi has two
// kernel-5 realms, so the counter lane is live.
func TestMapAPIError_CosignNonceUsed_CounterStaleSharesTheType(t *testing.T) {
	err := mapAPIError("COSIGN_NONCE_USED", "co-sign nonce is stale; re-propose the action", "",
		map[string]any{"boundaryId": "bnd_k5", "nonce": "8", "reason": "counter_stale"})

	var typed *CosignNonceUsedError
	if !errors.As(err, &typed) {
		t.Fatalf("error is %T, want *CosignNonceUsedError for the frozen-counter lane too", err)
	}
	if typed.Details.Reason != "counter_stale" {
		t.Errorf("Reason = %q, want counter_stale so logs can still tell the lanes apart", typed.Details.Reason)
	}
}

// Details with no boundary id are unattributable, so the mapping falls back to
// the base error rather than inventing a typed one with an empty boundary.
func TestMapAPIError_CosignNonceUsed_NoBoundaryFallsBack(t *testing.T) {
	err := mapAPIError("COSIGN_NONCE_USED", "refused", "", map[string]any{"reason": "nonce_consumed"})
	var typed *CosignNonceUsedError
	if errors.As(err, &typed) {
		t.Fatal("expected the base error when no boundary id is present")
	}
}

// Reason names the nonce lane; Disposition names the cause. Only the second
// answers "did my customer's money leave?", which is what has to be known
// before re-sending anything.
func TestMapAPIError_CosignNonceUsed_ExecutedCarriesOperation(t *testing.T) {
	err := mapAPIError("COSIGN_NONCE_USED", "already used", "", map[string]any{
		"boundaryId":  "bnd_armed",
		"nonce":       "42",
		"reason":      "nonce_consumed",
		"disposition": "executed",
		"txHash":      "0xabc",
		"operationId": "op_01k",
	})

	var typed *CosignNonceUsedError
	if !errors.As(err, &typed) {
		t.Fatalf("error is %T, want *CosignNonceUsedError", err)
	}
	if typed.Details.Disposition != CosignNonceExecuted {
		t.Errorf("Disposition = %q, want executed", typed.Details.Disposition)
	}
	if typed.Details.OperationID != "op_01k" {
		t.Errorf("OperationID = %q, want the operation to reconcile against", typed.Details.OperationID)
	}
	if typed.Details.TxHash != "0xabc" {
		t.Errorf("TxHash = %q", typed.Details.TxHash)
	}
}

func TestMapAPIError_CosignNonceUsed_RevokedCarriesNoOperation(t *testing.T) {
	err := mapAPIError("COSIGN_NONCE_USED", "already used", "", map[string]any{
		"boundaryId":  "bnd_armed",
		"reason":      "nonce_consumed",
		"disposition": "revoked",
		"txHash":      "0xdead",
	})

	var typed *CosignNonceUsedError
	if !errors.As(err, &typed) {
		t.Fatalf("error is %T, want *CosignNonceUsedError", err)
	}
	if typed.Details.Disposition != CosignNonceRevoked {
		t.Errorf("Disposition = %q, want revoked", typed.Details.Disposition)
	}
	// The owner acted directly on the kernel with their sovereign key, so
	// there is no send of ours to name.
	if typed.Details.OperationID != "" {
		t.Errorf("OperationID = %q, want empty for a revocation", typed.Details.OperationID)
	}
}

// The safety-critical narrowing. A disposition this SDK does not recognize must
// land on unknown, never reach a caller's `default` arm as an opaque string —
// that arm gets written as "not executed, so nothing moved" far more often than
// as "unrecognized, go reconcile", and being wrong about it loses money.
func TestMapAPIError_CosignNonceUsed_UnrecognizedDispositionNarrowsToUnknown(t *testing.T) {
	err := mapAPIError("COSIGN_NONCE_USED", "refused", "", map[string]any{
		"boundaryId":  "bnd_future",
		"disposition": "superseded_by_something_new",
	})

	var typed *CosignNonceUsedError
	if !errors.As(err, &typed) {
		t.Fatalf("error is %T, want *CosignNonceUsedError", err)
	}
	if typed.Details.Disposition != CosignNonceUnknown {
		t.Errorf("Disposition = %q, want %q", typed.Details.Disposition, CosignNonceUnknown)
	}
}

// An older server sends no disposition. Empty must stay empty so "this
// deployment doesn't report it" is distinguishable from "we looked and
// couldn't tell".
func TestMapAPIError_CosignNonceUsed_OmittedDispositionStaysEmpty(t *testing.T) {
	err := mapAPIError("COSIGN_NONCE_USED", "refused", "", map[string]any{
		"boundaryId": "bnd_old",
		"reason":     "nonce_consumed",
	})

	var typed *CosignNonceUsedError
	if !errors.As(err, &typed) {
		t.Fatalf("error is %T, want *CosignNonceUsedError", err)
	}
	if typed.Details.Disposition != "" {
		t.Errorf("Disposition = %q, want empty when the server omits it", typed.Details.Disposition)
	}
}

// The follow-up read after a refusal: not just "is it gone" but "did the money
// move, and against which operation do I reconcile".
func TestGetCosignNonceState_ReportsExecutedDisposition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, CosignNonceState{
			BoundaryID:  "bnd_v7",
			Nonce:       "42",
			Spendable:   false,
			Consumed:    true,
			Unordered:   true,
			Disposition: CosignNonceExecuted,
			TxHash:      "0xabc",
			OperationID: "op_01k",
		})
	}))
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	state, err := a.GetCosignNonceState(context.Background(), "bnd_v7", "42")
	if err != nil {
		t.Fatalf("GetCosignNonceState: %v", err)
	}
	if state.Disposition != CosignNonceExecuted {
		t.Errorf("Disposition = %q, want executed", state.Disposition)
	}
	if state.OperationID != "op_01k" {
		t.Errorf("OperationID = %q", state.OperationID)
	}
}

// A spendable slot has no burn, so there is nothing to attribute. An "unknown"
// here would read as "gone, cause unclear" for a slot that is perfectly open.
func TestGetCosignNonceState_SpendableCarriesNoDisposition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, CosignNonceState{
			BoundaryID: "bnd_v7",
			Nonce:      "42",
			Spendable:  true,
			Unordered:  true,
		})
	}))
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	state, err := a.GetCosignNonceState(context.Background(), "bnd_v7", "42")
	if err != nil {
		t.Fatalf("GetCosignNonceState: %v", err)
	}
	if state.Disposition != "" {
		t.Errorf("Disposition = %q, want empty for an unburned slot", state.Disposition)
	}
}

func TestGetCosignNonceState_UnorderedLane(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		writeEnvelope(w, 200, CosignNonceState{
			BoundaryID: "bnd_v7",
			Nonce:      "9223372036854775807",
			Spendable:  false,
			Consumed:   true,
			Unordered:  true,
		})
	}))
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	state, err := a.GetCosignNonceState(context.Background(), "bnd_v7", "9223372036854775807")
	if err != nil {
		t.Fatalf("GetCosignNonceState: %v", err)
	}
	if state.Spendable || !state.Consumed {
		t.Errorf("spendable=%v consumed=%v, want a burned slot to read not-spendable", state.Spendable, state.Consumed)
	}
	// The nonce crosses the wire as a decimal string because a 63-bit value
	// exceeds what a JSON number can carry losslessly in every consumer.
	if state.Nonce != "9223372036854775807" {
		t.Errorf("Nonce = %q, want an exact echo", state.Nonce)
	}
	if !strings.HasSuffix(gotPath, "/custody/boundaries/bnd_v7/cosign-nonces/9223372036854775807") {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "realmId=") {
		t.Errorf("query = %q, want the realm scoped in", gotQuery)
	}
}

// The trap: a frozen-counter kernel has no burn set, so Consumed is
// structurally false even for a nonce it will refuse. Callers must be able to
// trust Spendable alone.
func TestGetCosignNonceState_CounterLaneIgnoresConsumed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, 200, CosignNonceState{
			BoundaryID:   "bnd_k5",
			Nonce:        "8",
			Spendable:    false,
			Consumed:     false,
			Unordered:    false,
			CounterNonce: "9",
		})
	}))
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	state, err := a.GetCosignNonceState(context.Background(), "bnd_k5", "8")
	if err != nil {
		t.Fatalf("GetCosignNonceState: %v", err)
	}
	if state.Consumed {
		t.Error("consumed must be false on a kernel with no burn set")
	}
	if state.Spendable {
		t.Fatal("spendable must be false — consumed=false is structural on this lane, not evidence the envelope is live")
	}
	if state.CounterNonce != "9" {
		t.Errorf("CounterNonce = %q, want the value the kernel will accept", state.CounterNonce)
	}
}
