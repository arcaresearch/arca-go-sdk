package arca

import (
	"context"
	"net/url"
)

// OrderKeyReconciliation is an account-scoped durable dispatch verdict.
// Only retired_never_dispatched certifies an absent key cannot dispatch later.
type OrderKeyReconciliation struct {
	Status           string     `json:"status"`
	RealmID          string     `json:"realmId"`
	ExchangeObjectID string     `json:"exchangeObjectId"`
	Path             string     `json:"path"`
	Operation        *Operation `json:"operation,omitempty"`
	Reason           string     `json:"reason,omitempty"`
}

// ReconcileOrderKey checks or atomically retires the original order identity.
// It never resubmits an order. An ambiguous transport error remains unknown.
func (a *Arca) ReconcileOrderKey(ctx context.Context, objectID, path string, retireIfAbsent bool) (OrderKeyReconciliation, error) {
	var out OrderKeyReconciliation
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.post(ctx, "/objects/"+url.PathEscape(objectID)+"/exchange/order-key/reconcile", map[string]any{"path": path, "retireIfAbsent": retireIfAbsent}, &out)
	return out, err
}

// PatchObjectLabels atomically merges labels on one object, preserving siblings.
func (a *Arca) PatchObjectLabels(ctx context.Context, objectID string, labels map[string]string) error {
	rid, err := a.realmID(ctx)
	if err != nil {
		return err
	}
	return a.client.patch(ctx, "/objects/"+url.PathEscape(objectID)+"/labels", url.Values{"realmId": {rid}}, map[string]any{"labels": labels}, nil)
}
