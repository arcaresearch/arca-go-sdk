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
)

// hopMockState records every request the hop flow issues so the tests can
// assert on ordering, bodies, and the query string.
type hopMockState struct {
	mu      sync.Mutex
	paths   []string
	queries []string
	bodies  []map[string]any
	// armed makes the plain /transfer answer 412 COSIGN_REQUIRED, which is
	// how a co-sign-armed source boundary presents.
	armed bool
	// tampered makes propose return a destination that does not match the
	// paramsHash it also returns — a server trying to buy a signature for a
	// hop other than the one it described.
	tampered bool
}

// hopProposal is the golden vector dressed as a live proposal, so the armed
// path exercises a digest that actually verifies rather than a placeholder.
func hopProposal(tampered bool) VenueHopProposal {
	p := goldenProposal()
	p.BoundaryID = "bnd_src"
	p.TargetBoundaryID = "bnd_dst"
	if tampered {
		p.ToVenue = "0x000000000000000000000000000000000000dead"
	}
	return p
}

func (m *hopMockState) record(r *http.Request, body map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paths = append(m.paths, r.URL.Path)
	m.queries = append(m.queries, r.URL.RawQuery)
	m.bodies = append(m.bodies, body)
}

func newHopTestServer(m *hopMockState) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.record(r, body)

		switch {
		case strings.HasSuffix(r.URL.Path, "/transfer"):
			if m.armed {
				writeError(w, http.StatusPreconditionFailed, "COSIGN_REQUIRED",
					"The source exchange object's boundary requires user co-signed value-out operations.",
					map[string]any{
						"surface":        "transfer.venue_hop",
						"boundaryId":     "bnd_src",
						"sourceArcaPath": "/users/a/exchange/hl",
						"targetArcaPath": "/users/b/exchange/paper",
						"propose":        "/api/v1/custody/venue-hops/propose",
						"submit":         "/api/v1/custody/venue-hops",
					})
				return
			}
			writeEnvelope(w, 200, TransferResponse{Operation: Operation{ID: "op_plain", State: OpCompleted}})
		case strings.HasSuffix(r.URL.Path, "/custody/venue-hops/propose"):
			writeEnvelope(w, 200, hopProposal(m.tampered))
		case strings.HasSuffix(r.URL.Path, "/custody/venue-hops"):
			writeEnvelope(w, 200, VenueHopResponse{
				Operation:        Operation{ID: "op_hop", State: OpCompleted},
				BoundaryID:       "bnd_src",
				TargetBoundaryID: "bnd_dst",
			})
		default:
			writeEnvelope(w, 200, map[string]any{})
		}
	}))
}

func hopOpts(sign CosignDigestSigner) HopVenuesOptions {
	return HopVenuesOptions{
		Path:   "/op/transfer/hop-1",
		From:   "/users/a/exchange/hl",
		To:     "/users/b/exchange/paper",
		Amount: "500",
		Sign:   sign,
	}
}

// TestHopVenues_UnarmedBoundary_IsAPlainTransfer pins the reason HopVenues
// exists: a caller should not have to know whether the source boundary is
// armed to move money between two venues.
func TestHopVenues_UnarmedBoundary_IsAPlainTransfer(t *testing.T) {
	m := &hopMockState{}
	srv := newHopTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	signed := false
	resp, err := a.HopVenues(context.Background(), hopOpts(
		func(context.Context, string, VenueHopProposal) (string, error) {
			signed = true
			return "0xsig", nil
		},
	)).Submitted(context.Background())
	if err != nil {
		t.Fatalf("HopVenues: %v", err)
	}
	if resp.Operation.ID != "op_plain" {
		t.Errorf("operation id = %q, want the plain transfer's", resp.Operation.ID)
	}
	if signed {
		t.Error("the signer ran on an unarmed boundary; no signature is needed there")
	}
	if len(m.paths) != 1 {
		t.Errorf("issued %d requests (%v), want just the transfer", len(m.paths), m.paths)
	}
}

// TestHopVenues_ArmedBoundary_ProposesSignsSubmits is the whole point of the
// method: the propose → sign → submit round trip the raw REST pair makes a
// caller write by hand.
func TestHopVenues_ArmedBoundary_ProposesSignsSubmits(t *testing.T) {
	m := &hopMockState{armed: true}
	srv := newHopTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	var sawDigest string
	resp, err := a.HopVenues(context.Background(), hopOpts(
		func(_ context.Context, digest string, p VenueHopProposal) (string, error) {
			sawDigest = digest
			if p.AmountRaw != vecHopAmount {
				t.Errorf("proposal.AmountRaw = %q; the signer needs the raw amount, not the decimal", p.AmountRaw)
			}
			return "0xsignature", nil
		},
	)).Submitted(context.Background())
	if err != nil {
		t.Fatalf("HopVenues: %v", err)
	}

	if !hexEqual(sawDigest, vecHopDigest) {
		t.Errorf("signer saw digest %q, want the kernel-derived one", sawDigest)
	}
	if resp.BoundaryID != "bnd_src" || resp.TargetBoundaryID != "bnd_dst" {
		t.Errorf("boundaries = %q → %q, want bnd_src → bnd_dst", resp.BoundaryID, resp.TargetBoundaryID)
	}
	if len(m.paths) != 3 {
		t.Fatalf("issued %d requests (%v), want transfer + propose + submit", len(m.paths), m.paths)
	}

	// The routes read the realm from the query string, and the SDK's ordinary
	// post() sends none — so these two must go through postQuery.
	for i, idx := range []int{1, 2} {
		if !strings.Contains(m.queries[idx], "realmId=") {
			t.Errorf("request %d (%s) carried no realmId query: %q", i, m.paths[idx], m.queries[idx])
		}
	}

	proposeBody, submitBody := m.bodies[1], m.bodies[2]
	if proposeBody["path"] != "/op/transfer/hop-1" {
		t.Errorf("propose path = %v", proposeBody["path"])
	}
	// The signed ref derives from the operation path, so submitting at a
	// different path than was proposed would be refused server-side.
	if submitBody["path"] != proposeBody["path"] {
		t.Errorf("submit path %v != propose path %v", submitBody["path"], proposeBody["path"])
	}
	if submitBody["signature"] != "0xsignature" {
		t.Errorf("submit signature = %v", submitBody["signature"])
	}
	if submitBody["nonce"] != vecHopNonce {
		t.Errorf("submit nonce = %v, want the proposed one", submitBody["nonce"])
	}
}

// TestHopVenues_TamperedProposal_NeverReachesTheSigner is the reason the
// digest is re-derived rather than relayed. A server that describes one hop
// and asks for a signature over another is caught before the key is used, so
// the co-signature means what the user was shown.
func TestHopVenues_TamperedProposal_NeverReachesTheSigner(t *testing.T) {
	m := &hopMockState{armed: true, tampered: true}
	srv := newHopTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	signed := false
	_, err := a.HopVenues(context.Background(), hopOpts(
		func(context.Context, string, VenueHopProposal) (string, error) {
			signed = true
			return "0xsignature", nil
		},
	)).Submitted(context.Background())
	if err == nil {
		t.Fatal("expected the hop to refuse a proposal whose digest does not match its parameters")
	}
	if signed {
		t.Fatal("the signer was handed a digest the proposal's own fields do not produce")
	}
	for _, p := range m.paths {
		if strings.HasSuffix(p, "/custody/venue-hops") && !strings.HasSuffix(p, "/propose") {
			t.Error("submitted a hop built from a proposal that failed verification")
		}
	}
}

// TestHopVenues_ArmedWithoutSigner_SurfacesTheChallenge pins that the refusal
// reaches the caller as a typed error carrying the challenge, not a bare
// ArcaError they have to string-match.
func TestHopVenues_ArmedWithoutSigner_SurfacesTheChallenge(t *testing.T) {
	m := &hopMockState{armed: true}
	srv := newHopTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	_, err := a.HopVenues(context.Background(), hopOpts(nil)).Submitted(context.Background())
	if err == nil {
		t.Fatal("expected a refusal with no signer on an armed boundary")
	}
	var cosignErr *CosignRequiredError
	if !errors.As(err, &cosignErr) {
		t.Fatalf("error is %T, want *CosignRequiredError", err)
	}
	if cosignErr.Challenge.Surface != "transfer.venue_hop" {
		t.Errorf("challenge.Surface = %q", cosignErr.Challenge.Surface)
	}
	if cosignErr.Challenge.BoundaryID != "bnd_src" {
		t.Errorf("challenge.BoundaryID = %q", cosignErr.Challenge.BoundaryID)
	}
	if cosignErr.Challenge.Propose == "" {
		t.Error("challenge carries no propose endpoint; the caller has nowhere to go")
	}
	if len(m.paths) != 1 {
		t.Errorf("issued %d requests, want only the refused transfer", len(m.paths))
	}
}

// TestHopVenues_NonCosignRefusal_DoesNotSign pins that a refusal a signature
// cannot fix does not trigger the co-sign round trip. Retrying it under a
// signature would fail again, after bothering the key holder for one.
func TestHopVenues_NonCosignRefusal_DoesNotSign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"Venue-to-venue transfers cannot charge a transfer fee", nil)
	}))
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	signed := false
	_, err := a.HopVenues(context.Background(), hopOpts(
		func(context.Context, string, VenueHopProposal) (string, error) {
			signed = true
			return "0xsig", nil
		},
	)).Submitted(context.Background())
	if err == nil {
		t.Fatal("expected the validation refusal to propagate")
	}
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error is %T, want *ValidationError", err)
	}
	if signed {
		t.Error("the signer ran for a refusal a signature cannot fix")
	}
}

// TestHopVenues_SignerError_Propagates pins that a declined signature fails
// the hop rather than falling through to an unsigned submit.
func TestHopVenues_SignerError_Propagates(t *testing.T) {
	m := &hopMockState{armed: true}
	srv := newHopTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	declined := errors.New("user declined")
	_, err := a.HopVenues(context.Background(), hopOpts(
		func(context.Context, string, VenueHopProposal) (string, error) {
			return "", declined
		},
	)).Submitted(context.Background())
	if !errors.Is(err, declined) {
		t.Fatalf("error = %v, want the signer's", err)
	}
	for _, p := range m.paths {
		if strings.HasSuffix(p, "/custody/venue-hops") {
			t.Error("submitted a hop after the signer declined")
		}
	}
}

func TestSubmitVenueHop_PostsTheSignedEnvelope(t *testing.T) {
	m := &hopMockState{}
	srv := newHopTestServer(m)
	defer srv.Close()
	a := newTestArca(t, srv.URL)

	_, err := a.SubmitVenueHop(context.Background(), SubmitVenueHopOptions{
		Path: "/op/transfer/hop-2", From: "/a/exchange", To: "/b/exchange",
		Amount: "25", Nonce: "3", Deadline: 1893456000, Signature: "0xsig",
	}).Submitted(context.Background())
	if err != nil {
		t.Fatalf("SubmitVenueHop: %v", err)
	}
	body := m.bodies[0]
	if body["signature"] != "0xsig" || body["nonce"] != "3" {
		t.Errorf("submit body = %v", body)
	}
	// Ref is optional and must be omitted rather than sent empty — the server
	// treats a supplied ref as a cross-check and an empty one would fail it.
	if _, present := body["ref"]; present {
		t.Error("an unset Ref was sent; it must be omitted so the server derives it")
	}
	if !strings.Contains(m.queries[0], "realmId=") {
		t.Errorf("submit carried no realmId query: %q", m.queries[0])
	}
}
