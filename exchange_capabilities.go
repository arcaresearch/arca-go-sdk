package arca

import (
	"context"
	"net/url"
)

// AccountCapabilities is the supported builder contract for an exchange account.
// It describes adapter support, not a risk/admission quote or authorization.
type AccountCapabilities struct {
	ObjectID           string   `json:"objectId"`
	OrderTypes         []string `json:"orderTypes"`
	TimeInForce        []string `json:"timeInForce"`
	MarginModes        []string `json:"marginModes"`
	LeverageSelection  bool     `json:"leverageSelection"`
	PositionTriggers   bool     `json:"positionTriggers"`
	Brackets           bool     `json:"brackets"`
	OrderKeyRetirement bool     `json:"orderKeyRetirement"`
}

// GetExchangeCapabilities reads authoritative feature support for this account.
func (a *Arca) GetExchangeCapabilities(ctx context.Context, objectID string) (AccountCapabilities, error) {
	var out AccountCapabilities
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/objects/"+url.PathEscape(objectID)+"/exchange/capabilities", nil, &out)
	if err == nil && out.ObjectID != objectID {
		return AccountCapabilities{}, newArcaError("ACCOUNT_MISMATCH", "Capabilities belong to another account", "")
	}
	return out, err
}
