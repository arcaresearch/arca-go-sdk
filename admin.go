package arca

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// AdminConfig configures an ArcaAdmin client.
type AdminConfig struct {
	BaseURL       string
	Token         string
	StepUpHandler StepUpHandler
	HTTPClient    *http.Client
}

// ArcaAdmin is the builder-scoped admin client for operations that don't
// require a realm context: authentication, realm management, API keys, orgs,
// members, and invitations. It carries its own bearer token.
type ArcaAdmin struct {
	client *httpClient

	orgMu       sync.Mutex
	activeOrgID string
}

// NewAdmin creates an admin client.
func NewAdmin(cfg AdminConfig) *ArcaAdmin {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	a := &ArcaAdmin{}
	a.client = newHTTPClient(clientConfig{
		credential:    cfg.Token,
		credType:      credToken,
		baseURL:       trimTrailingSlash(baseURL) + "/api/v1",
		httpClient:    cfg.HTTPClient,
		stepUpHandler: cfg.StepUpHandler,
		headerHook: func() map[string]string {
			a.orgMu.Lock()
			defer a.orgMu.Unlock()
			if a.activeOrgID == "" {
				return nil
			}
			return map[string]string{"X-Arca-Org-Id": a.activeOrgID}
		},
	})
	return a
}

// SetToken updates the auth token (e.g. after sign-in).
func (a *ArcaAdmin) SetToken(token string) { a.client.updateCredential(token) }

// SetActiveOrgID sets the active organization for multi-org users.
func (a *ArcaAdmin) SetActiveOrgID(orgID string) {
	a.orgMu.Lock()
	a.activeOrgID = orgID
	a.orgMu.Unlock()
}

// SetStepUpHandler registers (or clears) the 412 step-up handler.
func (a *ArcaAdmin) SetStepUpHandler(h StepUpHandler) { a.client.setStepUpHandler(h) }

// ---- Auth ----

// SignInWithGoogle authenticates with a Google ID token.
func (a *ArcaAdmin) SignInWithGoogle(ctx context.Context, googleIDToken, orgName string) (AuthResponse, error) {
	var out AuthResponse
	body := map[string]any{"googleIdToken": googleIDToken}
	if orgName != "" {
		body["orgName"] = orgName
	}
	err := a.client.post(ctx, "/auth/google", body, &out)
	return out, err
}

// GetMe returns the authenticated builder profile.
func (a *ArcaAdmin) GetMe(ctx context.Context) (BuilderProfile, error) {
	var out BuilderProfile
	err := a.client.get(ctx, "/auth/me", nil, &out)
	return out, err
}

// Refresh exchanges a refresh token for a new access token.
func (a *ArcaAdmin) Refresh(ctx context.Context, refreshToken string) (RefreshResponse, error) {
	var out RefreshResponse
	err := a.client.post(ctx, "/auth/refresh", map[string]any{"refreshToken": refreshToken}, &out)
	return out, err
}

// Logout invalidates a refresh token.
func (a *ArcaAdmin) Logout(ctx context.Context, refreshToken string) error {
	var out map[string]any
	return a.client.post(ctx, "/auth/logout", map[string]any{"refreshToken": refreshToken}, &out)
}

// ---- Scoped tokens ----

// MintToken mints a scoped JWT for an end-user from explicit policy statements.
func (a *ArcaAdmin) MintToken(ctx context.Context, req MintTokenRequest) (MintTokenResponse, error) {
	var out MintTokenResponse
	err := a.client.post(ctx, "/auth/token", req, &out)
	return out, err
}

// MintDeviceToken mints a scoped JWT for a single device using one of three
// presets: PermissionRead, PermissionTrade, or PermissionFull.
func (a *ArcaAdmin) MintDeviceToken(ctx context.Context, opts MintDeviceTokenOptions, permissions ApiKeyPermissions, expirationMinutes int) (MintTokenResponse, error) {
	resource := opts.ForUserPath
	if resource == "" {
		resource = "*"
	}
	if permissions == "" {
		permissions = PermissionRead
	}
	var actions []string
	switch permissions {
	case PermissionTrade:
		actions = []string{"arca:Read", "arca:Exchange"}
	case PermissionFull:
		actions = []string{"arca:Read", "arca:Write"}
	default:
		actions = []string{"arca:Read"}
	}
	return a.MintToken(ctx, MintTokenRequest{
		RealmID:           opts.RealmID,
		Sub:               opts.Sub,
		Scope:             TokenScope{Statements: []PolicyStatement{{Effect: EffectAllow, Actions: actions, Resources: []string{resource}}}},
		ExpirationMinutes: expirationMinutes,
	})
}

// ---- Realms ----

// CreateRealm creates a realm using the legacy single-axis type (defaults to
// development when empty). The asset/lifecycle/backing axes are derived
// server-side. Use CreateRealmWithAxes to set the two-axis model directly.
func (a *ArcaAdmin) CreateRealm(ctx context.Context, name string, realmType RealmType, description string) (Realm, error) {
	var out Realm
	if realmType == "" {
		realmType = RealmDevelopment
	}
	err := a.client.post(ctx, "/realms", map[string]any{"name": name, "type": realmType, "description": description}, &out)
	return out, err
}

// CreateRealmAxes carries the optional two-axis realm inputs. A zero-value
// field is omitted from the request and derived server-side.
type CreateRealmAxes struct {
	Asset     RealmAsset
	Lifecycle RealmLifecycle
	Backing   RealmBacking
}

// CreateRealmWithAxes creates a realm with explicit asset / lifecycle /
// backing axes. When Asset is set it wins and the legacy type is derived from
// it server-side; Backing defaults to chain. Any zero-value axis is omitted
// and resolved server-side from the (optional) realmType.
func (a *ArcaAdmin) CreateRealmWithAxes(ctx context.Context, name string, realmType RealmType, axes CreateRealmAxes, description string) (Realm, error) {
	var out Realm
	body := map[string]any{"name": name, "description": description}
	if realmType != "" {
		body["type"] = realmType
	}
	if axes.Asset != "" {
		body["asset"] = axes.Asset
	}
	if axes.Lifecycle != "" {
		body["lifecycle"] = axes.Lifecycle
	}
	if axes.Backing != "" {
		body["backing"] = axes.Backing
	}
	err := a.client.post(ctx, "/realms", body, &out)
	return out, err
}

// ListRealms lists realms accessible to the builder.
func (a *ArcaAdmin) ListRealms(ctx context.Context) (RealmListResponse, error) {
	var out RealmListResponse
	err := a.client.get(ctx, "/realms", nil, &out)
	return out, err
}

// GetRealm fetches a realm by id.
func (a *ArcaAdmin) GetRealm(ctx context.Context, id string) (Realm, error) {
	var out Realm
	err := a.client.get(ctx, "/realms/"+id, nil, &out)
	return out, err
}

// DeleteRealm deletes a realm.
func (a *ArcaAdmin) DeleteRealm(ctx context.Context, id string) error {
	var out map[string]any
	return a.client.delete(ctx, "/realms/"+id, &out)
}

// UpdateRealmSettings updates a realm's settings.
func (a *ArcaAdmin) UpdateRealmSettings(ctx context.Context, id string, settings RealmSettings) (Realm, error) {
	var out Realm
	err := a.client.patch(ctx, "/realms/"+id+"/settings", nil, settings, &out)
	return out, err
}

// ListSummaries returns aggregate statistics for the given realm ids.
func (a *ArcaAdmin) ListSummaries(ctx context.Context, realmIDs []string) (BatchSummariesResponse, error) {
	var out BatchSummariesResponse
	err := a.client.get(ctx, "/summaries", url.Values{"realmIds": {strings.Join(realmIDs, ",")}}, &out)
	return out, err
}

// ---- API keys ----

// CreateApiKey creates an API key. Scope and Permissions are mutually
// exclusive; RealmID locks the key to one realm.
func (a *ArcaAdmin) CreateApiKey(ctx context.Context, opts CreateApiKeyOptions) (ApiKeyCreatedResponse, error) {
	var out ApiKeyCreatedResponse
	body := map[string]any{"name": opts.Name}
	if opts.Scope != nil {
		raw, _ := json.Marshal(opts.Scope)
		body["scope"] = string(raw)
	}
	if opts.RealmID != "" {
		body["realmId"] = opts.RealmID
	}
	if opts.Permissions != "" {
		body["permissions"] = opts.Permissions
	}
	err := a.client.post(ctx, "/api-keys", body, &out)
	return out, err
}

// ListApiKeys lists the org's API keys.
func (a *ArcaAdmin) ListApiKeys(ctx context.Context) (ApiKeyListResponse, error) {
	var out ApiKeyListResponse
	err := a.client.get(ctx, "/api-keys", nil, &out)
	return out, err
}

// RevokeApiKey revokes an API key.
func (a *ArcaAdmin) RevokeApiKey(ctx context.Context, id string) error {
	var out map[string]any
	return a.client.delete(ctx, "/api-keys/"+id, &out)
}

// ---- Auth audit ----

// ListAuthAuditLog lists auth audit entries.
func (a *ArcaAdmin) ListAuthAuditLog(ctx context.Context, filters *AuthAuditFilters) (AuthAuditListResponse, error) {
	var out AuthAuditListResponse
	params := url.Values{}
	if filters != nil {
		if filters.EventType != "" {
			params.Set("eventType", string(filters.EventType))
		}
		if filters.RealmID != "" {
			params.Set("realmId", filters.RealmID)
		}
		if filters.Since != "" {
			params.Set("since", filters.Since)
		}
		if filters.Until != "" {
			params.Set("until", filters.Until)
		}
		if filters.Success != nil {
			params.Set("success", strconv.FormatBool(*filters.Success))
		}
		if filters.Limit > 0 {
			params.Set("limit", strconv.Itoa(filters.Limit))
		}
		if filters.Offset > 0 {
			params.Set("offset", strconv.Itoa(filters.Offset))
		}
	}
	err := a.client.get(ctx, "/auth/audit", params, &out)
	return out, err
}

// GetCredentialScope returns the resolved scope for a token jti or api key id.
func (a *ArcaAdmin) GetCredentialScope(ctx context.Context, tokenJti, apiKeyID string) (CredentialScopeResponse, error) {
	var out CredentialScopeResponse
	params := url.Values{}
	if tokenJti != "" {
		params.Set("tokenJti", tokenJti)
	}
	if apiKeyID != "" {
		params.Set("apiKeyId", apiKeyID)
	}
	err := a.client.get(ctx, "/auth/audit/scope", params, &out)
	return out, err
}

// ---- Organization / team ----

// ListMyOrgs lists the caller's organization memberships.
func (a *ArcaAdmin) ListMyOrgs(ctx context.Context) ([]MyOrgMembership, error) {
	var out []MyOrgMembership
	err := a.client.get(ctx, "/orgs", nil, &out)
	return out, err
}

// CreateOrg creates an organization.
func (a *ArcaAdmin) CreateOrg(ctx context.Context, name string) (CreateOrgResponse, error) {
	var out CreateOrgResponse
	err := a.client.post(ctx, "/orgs", map[string]any{"name": name}, &out)
	return out, err
}

// GetOrg returns the active organization.
func (a *ArcaAdmin) GetOrg(ctx context.Context) (Organization, error) {
	var out Organization
	err := a.client.get(ctx, "/org", nil, &out)
	return out, err
}

// GetActiveTerms returns the active builder-agreement terms.
func (a *ArcaAdmin) GetActiveTerms(ctx context.Context) (ActiveTerms, error) {
	var out ActiveTerms
	err := a.client.get(ctx, "/org/terms/active", nil, &out)
	return out, err
}

// AcceptTerms accepts a builder-agreement version.
func (a *ArcaAdmin) AcceptTerms(ctx context.Context, version int) error {
	var out map[string]any
	return a.client.post(ctx, "/org/terms/accept", map[string]any{"version": version}, &out)
}

// ListMembers lists organization members.
func (a *ArcaAdmin) ListMembers(ctx context.Context) (OrgMemberListResponse, error) {
	var out OrgMemberListResponse
	err := a.client.get(ctx, "/org/members", nil, &out)
	return out, err
}

// UpdateMember updates a member's role/scope.
func (a *ArcaAdmin) UpdateMember(ctx context.Context, memberID string, req UpdateMemberRequest) (OrgMember, error) {
	var out OrgMember
	err := a.client.patch(ctx, "/org/members/"+memberID, nil, req, &out)
	return out, err
}

// RemoveMember removes a member from the org.
func (a *ArcaAdmin) RemoveMember(ctx context.Context, memberID string) error {
	var out map[string]any
	return a.client.delete(ctx, "/org/members/"+memberID, &out)
}

// InviteMember invites a member to the org.
func (a *ArcaAdmin) InviteMember(ctx context.Context, req InviteMemberRequest) (Invitation, error) {
	var out Invitation
	err := a.client.post(ctx, "/org/invitations", req, &out)
	return out, err
}

// ListInvitations lists the org's invitations.
func (a *ArcaAdmin) ListInvitations(ctx context.Context) (InvitationListResponse, error) {
	var out InvitationListResponse
	err := a.client.get(ctx, "/org/invitations", nil, &out)
	return out, err
}

// RevokeInvitation revokes an invitation.
func (a *ArcaAdmin) RevokeInvitation(ctx context.Context, invitationID string) error {
	var out map[string]any
	return a.client.delete(ctx, "/org/invitations/"+invitationID, &out)
}

// PreviewInvitation previews an invitation by token (unauthenticated-friendly).
func (a *ArcaAdmin) PreviewInvitation(ctx context.Context, token string) (InvitationPreview, error) {
	var out InvitationPreview
	err := a.client.get(ctx, "/invitations/"+token+"/preview", nil, &out)
	return out, err
}

// AcceptInvitation accepts an invitation by token.
func (a *ArcaAdmin) AcceptInvitation(ctx context.Context, token string) (OrgMember, error) {
	var out OrgMember
	err := a.client.post(ctx, "/invitations/"+token+"/accept", nil, &out)
	return out, err
}

// ListPendingInvitations lists invitations pending for the caller.
func (a *ArcaAdmin) ListPendingInvitations(ctx context.Context) (InvitationListResponse, error) {
	var out InvitationListResponse
	err := a.client.get(ctx, "/invitations/pending", nil, &out)
	return out, err
}

// TransferOwnership transfers org ownership to another user.
func (a *ArcaAdmin) TransferOwnership(ctx context.Context, targetUserID string) error {
	var out map[string]any
	return a.client.post(ctx, "/org/transfer-ownership", map[string]any{"targetUserId": targetUserID}, &out)
}
