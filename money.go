package arca

import (
	"context"
	"encoding/json"
	"net/url"
)

// Transfer moves value between two Arca objects. The operation Path is the
// idempotency key. Returns a handle; Wait blocks until settlement.
func (a *Arca) Transfer(ctx context.Context, opts TransferOptions) *OperationHandle[TransferResponse] {
	predicted := &PredictedEffect{
		Type: "transfer",
		BalanceChanges: map[string]PredictedBalanceChange{
			opts.From: {Departing: opts.Amount},
			opts.To:   {Arriving: opts.Amount},
		},
	}
	return op(a, ctx, func() (TransferResponse, error) {
		body := map[string]any{
			"realmId":        a.currentRealmID(),
			"path":           opts.Path,
			"sourceArcaPath": opts.From,
			"targetArcaPath": opts.To,
			"amount":         opts.Amount,
		}
		if opts.FeeOverride != nil {
			body["feeOverride"] = *opts.FeeOverride
		}
		var resp TransferResponse
		err := a.client.post(ctx, "/transfer", body, &resp)
		return resp, err
	}, TransferResponse.op, (*TransferResponse).setOp, predicted, 0)
}

// EstimateFee estimates the fee for a transfer or order operation.
func (a *Arca) EstimateFee(ctx context.Context, params FeeEstimateParams) (FeeEstimate, error) {
	var out FeeEstimate
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	q := url.Values{"realmId": {rid}, "action": {params.Action}, "amount": {params.Amount}}
	if params.SourcePath != "" {
		q.Set("sourcePath", params.SourcePath)
	}
	if params.TargetPath != "" {
		q.Set("targetPath", params.TargetPath)
	}
	if params.FeeOverride != nil {
		q.Set("feeOverride", *params.FeeOverride)
	}
	err = a.client.get(ctx, "/fees/estimate", q, &out)
	return out, err
}

// FundAccount programmatically funds an Arca object (development realms). For
// production deposits use CreatePaymentLink.
func (a *Arca) FundAccount(ctx context.Context, opts FundAccountOptions) *OperationHandle[FundAccountResponse] {
	return op(a, ctx, func() (FundAccountResponse, error) {
		body := map[string]any{
			"realmId":       a.currentRealmID(),
			"arcaPath":      opts.ArcaRef,
			"amount":        opts.Amount,
			"path":          nilIfEmpty(opts.Path),
			"senderAddress": nilIfEmpty(opts.SenderAddress),
		}
		if opts.DurationSeconds != nil {
			body["durationSeconds"] = *opts.DurationSeconds
		} else {
			body["durationSeconds"] = nil
		}
		if opts.WillSucceed != nil {
			body["willSucceed"] = *opts.WillSucceed
		} else {
			body["willSucceed"] = nil
		}
		var resp FundAccountResponse
		err := a.client.post(ctx, "/fund-account", body, &resp)
		return resp, err
	}, FundAccountResponse.op, (*FundAccountResponse).setOp, nil, 0)
}

// DefundAccount programmatically withdraws from an Arca object (non-production).
func (a *Arca) DefundAccount(ctx context.Context, opts DefundAccountOptions) *OperationHandle[DefundAccountResponse] {
	return op(a, ctx, func() (DefundAccountResponse, error) {
		var resp DefundAccountResponse
		err := a.client.post(ctx, "/defund-account", map[string]any{
			"realmId":            a.currentRealmID(),
			"arcaPath":           opts.ArcaPath,
			"amount":             opts.Amount,
			"destinationAddress": opts.DestinationAddress,
			"path":               nilIfEmpty(opts.Path),
		}, &resp)
		return resp, err
	}, DefundAccountResponse.op, (*DefundAccountResponse).setOp, nil, 0)
}

// CreatePaymentLink creates a hosted deposit/withdrawal page for an end user.
func (a *Arca) CreatePaymentLink(ctx context.Context, opts CreatePaymentLinkOptions) *OperationHandle[CreatePaymentLinkResponse] {
	return op(a, ctx, func() (CreatePaymentLinkResponse, error) {
		var metadata any
		if opts.Metadata != nil {
			raw, _ := json.Marshal(opts.Metadata)
			metadata = string(raw)
		}
		body := map[string]any{
			"realmId":            a.currentRealmID(),
			"type":               opts.Type,
			"arcaPath":           opts.ArcaRef,
			"amount":             opts.Amount,
			"returnUrl":          nilIfEmpty(opts.ReturnURL),
			"returnStrategy":     nilIfEmpty(string(opts.ReturnStrategy)),
			"metadata":           metadata,
			"destinationAddress": nilIfEmpty(opts.DestinationAddress),
		}
		if opts.ExpiresInMinutes != nil {
			body["expiresInMinutes"] = *opts.ExpiresInMinutes
		} else {
			body["expiresInMinutes"] = nil
		}
		var resp CreatePaymentLinkResponse
		err := a.client.post(ctx, "/payment-links", body, &resp)
		return resp, err
	}, CreatePaymentLinkResponse.op, (*CreatePaymentLinkResponse).setOp, nil, 0)
}

// ListPaymentLinks lists payment links, optionally filtered by type or status.
func (a *Arca) ListPaymentLinks(ctx context.Context, opts *ListPaymentLinksOptions) (PaymentLinkListResponse, error) {
	var out PaymentLinkListResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	q := url.Values{"realmId": {rid}}
	if opts != nil {
		if opts.Type != "" {
			q.Set("type", opts.Type)
		}
		if opts.Status != "" {
			q.Set("status", opts.Status)
		}
	}
	err = a.client.get(ctx, "/payment-links", q, &out)
	return out, err
}

// Nonce returns the next unique nonce for a path prefix. Reserve the nonce
// before the operation and reuse the returned path on retry.
func (a *Arca) Nonce(ctx context.Context, path string, separator ...string) (NonceResponse, error) {
	var out NonceResponse
	if err := validatePath(path); err != nil {
		return out, err
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body := map[string]any{"realmId": rid, "prefix": path}
	if len(separator) > 0 {
		body["separator"] = separator[0]
	}
	err = a.client.post(ctx, "/nonce", body, &out)
	return out, err
}
