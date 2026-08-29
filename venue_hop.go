package arca

import (
	"context"
	"errors"
	"net/url"
)

// CosignDigestSigner signs a co-sign digest with the boundary's co-sign key.
//
// It receives the kernel-derived EIP-712 digest (0x-prefixed 32 bytes) and
// returns a 0x-prefixed 65-byte secp256k1 signature. HopVenues has already
// re-derived the digest from the proposal's own fields by the time this is
// called, so the hash is verified rather than merely relayed; the proposal is
// passed alongside so the signer can show the user what it commits to.
//
// The SDK never holds this key — that is the entire property a co-signature
// provides. Wire it to whatever does: an HSM, a KMS, a hardware wallet, or a
// user's device over some transport of your choosing.
type CosignDigestSigner func(ctx context.Context, digest string, proposal VenueHopProposal) (string, error)

// ProposeVenueHopOptions asks for the signable fields of a venue-to-venue hop.
type ProposeVenueHopOptions struct {
	// Path is the operation path the hop will be submitted at (idempotency
	// key). Required here, not just at submit: the signed ref is derived from
	// it, so a submit at a different path produces a different digest and is
	// refused.
	Path string
	// From is the source exchange arca. Its boundary is the debited one, and
	// therefore the one that signs.
	From string
	// To is the target exchange arca.
	To string
	// Amount is a decimal string, exact at 6 decimal places.
	Amount string
	// Deadline is an optional unix-seconds co-signature expiry. Zero means
	// the server picks a default window.
	Deadline int64
}

// CosignDomain is the EIP-712 domain a co-sign digest is bound to.
// VerifyingContract is the realm's kernel proxy.
type CosignDomain struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	ChainID           int64  `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"`
}

// VenueHopProposal is everything a signer needs to re-derive and sign a hop.
//
// AmountRaw — not Amount — is what the paramsHash commits to. Encoding the
// decimal string produces a different hash than the kernel and the signature
// is rejected.
type VenueHopProposal struct {
	// Action is the CosignAction discriminator (11 = TransferBetweenVenues).
	Action uint8 `json:"action"`
	// BoundaryID is the debited (source) boundary — the co-sign owner.
	BoundaryID  string `json:"boundaryId"`
	BoundaryKey string `json:"boundaryKey"`
	// TargetBoundaryID is the credited boundary. Equal to BoundaryID for a
	// same-boundary rebalance.
	TargetBoundaryID  string `json:"targetBoundaryId"`
	TargetBoundaryKey string `json:"targetBoundaryKey"`
	// FromVenue / ToVenue are the venue CONTRACT addresses the hop routes
	// between. Both are inside the signed digest.
	FromVenue string `json:"fromVenue"`
	ToVenue   string `json:"toVenue"`
	// ToVenueAccountKey is the destination venue sub-account (bytes32).
	ToVenueAccountKey string       `json:"toVenueAccountKey"`
	Token             string       `json:"token"`
	Domain            CosignDomain `json:"domain"`
	Amount            string       `json:"amount"`
	// AmountRaw is the uint256 the paramsHash commits to. Encode THIS.
	AmountRaw string `json:"amountRaw"`
	// Ref is the correlation word bound into the digest, derived from the
	// realm and the operation path.
	Ref        string `json:"ref"`
	Nonce      string `json:"nonce"`
	Deadline   int64  `json:"deadline"`
	ParamsHash string `json:"paramsHash"`
	// Digest is read from the kernel so a signer can cross-check its own
	// derivation against the authoritative source.
	Digest string `json:"digest"`
}

// SubmitVenueHopOptions carries a co-signed hop to the platform.
type SubmitVenueHopOptions struct {
	// Path must match the path used at propose — the signed ref derives from it.
	Path   string
	From   string
	To     string
	Amount string
	// Nonce and Deadline must be the values the signer signed over.
	Nonce    string
	Deadline int64
	// Signature is the 0x-prefixed 65-byte co-signature.
	Signature string
	// Ref is optional. The server derives the authoritative ref from Path;
	// supplying one only cross-checks it, so a ref from a different path is
	// refused by name rather than surfacing as an opaque signature mismatch.
	Ref string
}

// VenueHopResponse is the accepted co-signed hop.
type VenueHopResponse struct {
	Operation Operation `json:"operation"`
	// BoundaryID is the debited boundary; TargetBoundaryID is credited.
	BoundaryID       string `json:"boundaryId"`
	TargetBoundaryID string `json:"targetBoundaryId"`
}

func (r VenueHopResponse) op() *Operation { return &r.Operation }
func (r *VenueHopResponse) setOp(o *Operation) {
	if o != nil {
		r.Operation = *o
	}
}

// HopVenuesOptions moves capital from one exchange arca to another.
type HopVenuesOptions struct {
	// Path is the operation path (idempotency key).
	Path string
	// From and To are both exchange arcas.
	From string
	To   string
	// Amount is a decimal string.
	Amount string
	// Sign is called ONLY if the source boundary is co-sign armed.
	//
	// Leave it nil on unarmed boundaries — the hop needs no signature there
	// and this is never invoked. Leaving it nil on an ARMED boundary returns
	// the server's *CosignRequiredError unchanged.
	Sign CosignDigestSigner
	// Deadline is an optional unix-seconds co-signature expiry, used only on
	// the signed path.
	Deadline int64
}

// HopVenues moves capital straight from one exchange arca to another.
//
// A transfer whose source and target are both exchange arcas hops
// venue-to-venue in a single on-chain frame — no intermediate denominated
// arca, and the value never rests at a boundary. Hops carry no transfer fee
// and work across isolation boundaries.
//
// This is Transfer plus the co-sign fallback. On an unarmed source boundary it
// is exactly a transfer. On an armed one the plain call is refused (the kernel
// will not move value out without the owner's signature), so this proposes the
// hop, hands the digest to Sign, and submits.
//
//	h := a.HopVenues(ctx, arca.HopVenuesOptions{
//	    Path:   "/op/transfer/rebalance-1",
//	    From:   "/users/alice/exchange/hl",
//	    To:     "/users/alice/exchange/paper",
//	    Amount: "500",
//	    Sign:   mySigner,
//	})
//	res, err := h.Wait(ctx)
//
// Destinations are limited to Hyperliquid and the paper venue; a GLL-paper
// target is refused, because its account is credited by faucet rather than by
// value landing at the venue contract.
//
// The returned handle carries a VenueHopResponse on both paths. On the unarmed
// path the boundary fields are empty — the plain transfer endpoint does not
// report them — while Operation is populated either way.
func (a *Arca) HopVenues(ctx context.Context, opts HopVenuesOptions) *OperationHandle[VenueHopResponse] {
	predicted := &PredictedEffect{
		Type: "transfer",
		BalanceChanges: map[string]PredictedBalanceChange{
			opts.From: {Departing: opts.Amount},
			opts.To:   {Arriving: opts.Amount},
		},
	}
	return op(a, ctx, func() (VenueHopResponse, error) {
		var plain TransferResponse
		err := a.client.post(ctx, "/transfer", map[string]any{
			"realmId":        a.currentRealmID(),
			"path":           opts.Path,
			"sourceArcaPath": opts.From,
			"targetArcaPath": opts.To,
			"amount":         opts.Amount,
		}, &plain)
		if err == nil {
			return VenueHopResponse{Operation: plain.Operation}, nil
		}

		// Only the armed-boundary refusal is recoverable here, and only with
		// a signer. Anything else — an unhoppable destination, a fee, an
		// insufficient balance — is the caller's to fix, and retrying it
		// under a signature would fail again after asking for one.
		var cosignErr *CosignRequiredError
		if !errors.As(err, &cosignErr) || opts.Sign == nil {
			return VenueHopResponse{}, err
		}

		proposal, err := a.ProposeVenueHop(ctx, ProposeVenueHopOptions{
			Path:     opts.Path,
			From:     opts.From,
			To:       opts.To,
			Amount:   opts.Amount,
			Deadline: opts.Deadline,
		})
		if err != nil {
			return VenueHopResponse{}, err
		}
		// Re-derive before handing the key a hash it cannot read. A server
		// that returned a digest for a different destination or a larger
		// amount is caught here rather than on-chain.
		if err := proposal.Verify(); err != nil {
			return VenueHopResponse{}, err
		}
		signature, err := opts.Sign(ctx, proposal.Digest, proposal)
		if err != nil {
			return VenueHopResponse{}, err
		}
		return a.submitVenueHop(ctx, SubmitVenueHopOptions{
			Path:      opts.Path,
			From:      opts.From,
			To:        opts.To,
			Amount:    opts.Amount,
			Nonce:     proposal.Nonce,
			Deadline:  proposal.Deadline,
			Signature: signature,
			Ref:       proposal.Ref,
		})
	}, VenueHopResponse.op, (*VenueHopResponse).setOp, predicted, 0)
}

// ProposeVenueHop returns the signable fields for a co-signed venue hop.
// Nothing is persisted and no funds move.
//
// Only the SOURCE boundary signs — value arriving is consent-free, so the
// destination's owner never has to be online.
//
// Path is required because the signed ref derives from it: a submit at a
// different path produces a different digest and is refused. A careful signer
// should re-derive Digest from the returned fields (encoding AmountRaw, never
// Amount) and refuse on mismatch rather than blind-signing the server's hash.
//
// Most callers want HopVenues, which does propose → sign → submit and skips
// all of it on an unarmed boundary.
func (a *Arca) ProposeVenueHop(ctx context.Context, opts ProposeVenueHopOptions) (VenueHopProposal, error) {
	var out VenueHopProposal
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body := map[string]any{
		"path":           opts.Path,
		"sourceArcaPath": opts.From,
		"targetArcaPath": opts.To,
		"amount":         opts.Amount,
		"deadline":       opts.Deadline,
	}
	err = a.client.postQuery(ctx, "/custody/venue-hops/propose", url.Values{"realmId": {rid}}, body, &out)
	return out, err
}

// SubmitVenueHop submits a venue hop co-signed by the source boundary's
// wallet.
//
// The server re-derives the digest and verifies the signature against that
// boundary's on-chain co-sign key before anything moves. Path, Amount, Nonce,
// and Deadline must match what was signed.
func (a *Arca) SubmitVenueHop(ctx context.Context, opts SubmitVenueHopOptions) *OperationHandle[VenueHopResponse] {
	predicted := &PredictedEffect{
		Type: "transfer",
		BalanceChanges: map[string]PredictedBalanceChange{
			opts.From: {Departing: opts.Amount},
			opts.To:   {Arriving: opts.Amount},
		},
	}
	return op(a, ctx, func() (VenueHopResponse, error) {
		return a.submitVenueHop(ctx, opts)
	}, VenueHopResponse.op, (*VenueHopResponse).setOp, predicted, 0)
}

// submitVenueHop is the bare HTTP call, shared by SubmitVenueHop and the
// fallback inside HopVenues (which is already running inside an op wrapper and
// must not start a second one).
func (a *Arca) submitVenueHop(ctx context.Context, opts SubmitVenueHopOptions) (VenueHopResponse, error) {
	var out VenueHopResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body := map[string]any{
		"path":           opts.Path,
		"sourceArcaPath": opts.From,
		"targetArcaPath": opts.To,
		"amount":         opts.Amount,
		"nonce":          opts.Nonce,
		"deadline":       opts.Deadline,
		"signature":      opts.Signature,
	}
	if opts.Ref != "" {
		body["ref"] = opts.Ref
	}
	err = a.client.postQuery(ctx, "/custody/venue-hops", url.Values{"realmId": {rid}}, body, &out)
	return out, err
}
