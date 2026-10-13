package arca

import "context"

// PermissionCheck is one concrete action (for example "arca:PlaceOrder") on
// one resource ("*" or a path such as "/users/u1/trading").
type PermissionCheck struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

type PermissionCheckResult struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Allowed  bool   `json:"allowed"`
}

type PermissionCheckResponse struct {
	RealmID string                  `json:"realmId"`
	Results []PermissionCheckResult `json:"results"`
}

// Denied returns the pairs the credential's policy does not grant.
func (r PermissionCheckResponse) Denied() []PermissionCheck {
	var out []PermissionCheck
	for _, res := range r.Results {
		if !res.Allowed {
			out = append(out, PermissionCheck{Action: res.Action, Resource: res.Resource})
		}
	}
	return out
}

// CheckPermissions reports whether this client's credential grants each
// action on its resource in the client's realm (at most 32 pairs). It
// evaluates policy only: nothing is consumed or recorded, and realm gates
// such as V9 trading readiness and step-up still apply to the real request.
// Call it at startup to catch a key missing an action you depend on.
func (a *Arca) CheckPermissions(ctx context.Context, checks ...PermissionCheck) (PermissionCheckResponse, error) {
	var out PermissionCheckResponse
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body := struct {
		RealmID string            `json:"realmId"`
		Checks  []PermissionCheck `json:"checks"`
	}{RealmID: realm, Checks: checks}
	err = a.client.post(ctx, "/auth/permission-check", body, &out)
	return out, err
}
