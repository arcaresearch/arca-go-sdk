package arca

import (
	"context"
	"net/url"
)

// Provider objects and deposit links. A provider object is an ordinary Arca
// object (type "provider") naming one account at an external wallet provider
// — Privy today — at whatever path the product chooses. A zone may hold zero,
// one or several; none is implied by a zone, and a provider object grants no
// spending authority. A deposit link is a durable relationship from one of its
// wallets to one Cash account through the realm's automatic-deposit adapter.

// ProviderEvidenceKindPrivyIdentityToken is the evidence kind ConnectProvider
// sends: a Privy identity token (it lists the user's linked accounts).
const ProviderEvidenceKindPrivyIdentityToken = "privy_identity_token"

// ProviderEvidence is the provider-issued proof of the account's subject and
// wallets. It is verified and discarded; only its facts are stored.
type ProviderEvidence struct {
	Kind          string `json:"kind"`
	IdentityToken string `json:"identityToken"`
	AccessToken   string `json:"accessToken,omitempty"`
}

// ProviderWalletRole assigns an application role to one attested wallet.
type ProviderWalletRole struct {
	Address string `json:"address"`
	Role    string `json:"role"`
}

// ConnectProviderOptions are ConnectProvider's inputs.
type ConnectProviderOptions struct {
	Evidence ProviderEvidence     `json:"evidence"`
	Wallets  []ProviderWalletRole `json:"wallets,omitempty"`
}

// ProviderWalletControl is a directed relationship: the wallet holding it
// stands in Relation to Target, another wallet of the same object. It is an
// application fact and grants no authority.
type ProviderWalletControl struct {
	Target   string `json:"target"`
	Relation string `json:"relation"`
}

// UpdateProviderWalletOptions edits one wallet. A nil field is unchanged; an
// empty role or empty controls list clears it.
type UpdateProviderWalletOptions struct {
	Role     *string                  `json:"role,omitempty"`
	Controls *[]ProviderWalletControl `json:"controls,omitempty"`
}

// ProviderEvidenceFacts records which proof established the connection.
type ProviderEvidenceFacts struct {
	Kind      string `json:"kind"`
	IssuedAt  string `json:"issuedAt"`
	ExpiresAt string `json:"expiresAt"`
	KeyID     string `json:"keyId,omitempty"`
}

// ProviderConnection is `unverified` (created, not yet connected) or
// `verified` at CheckedAt.
type ProviderConnection struct {
	Status    string                 `json:"status"`
	CheckedAt string                 `json:"checkedAt,omitempty"`
	Evidence  *ProviderEvidenceFacts `json:"evidence,omitempty"`
}

// ProviderState is a provider object's versioned public state.
type ProviderState struct {
	Schema     int                `json:"schema"`
	Provider   string             `json:"provider"`
	Subject    string             `json:"subject,omitempty"`
	Connection ProviderConnection `json:"connection"`
}

// AddressObservation is the realm's observation of one address. BalanceMicro
// is nil when unknown — never "0" as a stand-in; a Health other than "live"
// means the figure may be stale.
type AddressObservation struct {
	WatchID              string  `json:"watchId"`
	ChainID              string  `json:"chainId"`
	TokenAddress         string  `json:"tokenAddress"`
	BalanceMicro         *string `json:"balanceMicro"`
	Health               string  `json:"health"`
	CompleteThroughBlock uint64  `json:"completeThroughBlock"`
	AsOfBlockHash        string  `json:"asOfBlockHash,omitempty"`
	AsOfTime             string  `json:"asOfTime,omitempty"`
}

// ProviderWalletVerification is `verified`, or `unlinked` when an earlier
// verification listed the wallet and the latest does not.
type ProviderWalletVerification struct {
	Status           string `json:"status"`
	VerifiedAt       string `json:"verifiedAt,omitempty"`
	EvidenceKind     string `json:"evidenceKind,omitempty"`
	EvidenceIssuedAt string `json:"evidenceIssuedAt,omitempty"`
}

// ProviderWallet is one attested wallet. Observation is nil when the realm
// does not observe the address.
type ProviderWallet struct {
	WalletID         string                     `json:"walletId"`
	ChainType        string                     `json:"chainType"`
	Address          string                     `json:"address"`
	WalletClientType string                     `json:"walletClientType,omitempty"`
	ProviderWalletID string                     `json:"providerWalletId,omitempty"`
	Role             string                     `json:"role,omitempty"`
	Verification     ProviderWalletVerification `json:"verification"`
	Controls         []ProviderWalletControl    `json:"controls"`
	Observation      *AddressObservation        `json:"observation"`
}

// ProviderDetail is a provider object's state and wallets.
type ProviderDetail struct {
	ObjectID string           `json:"objectId"`
	Path     string           `json:"path"`
	State    ProviderState    `json:"state"`
	Wallets  []ProviderWallet `json:"wallets"`
}

// EnsureProviderOptions creates (or returns) a provider object.
type EnsureProviderOptions struct {
	Ref      string
	Provider string // "privy"; empty defaults to it
	Labels   map[string]string
}

// EnsureProvider creates (or returns) an unverified provider object at Ref.
// ConnectProvider then proves its subject and wallets.
func (a *Arca) EnsureProvider(ctx context.Context, opts EnsureProviderOptions) *OperationHandle[EnsureArcaObjectResponse] {
	name := opts.Provider
	if name == "" {
		name = "privy"
	}
	return a.EnsureArca(ctx, EnsureArcaOptions{Ref: opts.Ref, Type: ObjectProvider, Metadata: `{"provider":"` + name + `"}`, Labels: opts.Labels})
}

// GetProvider reads a provider object's detail.
func (a *Arca) GetProvider(ctx context.Context, objectID string) (ProviderDetail, error) {
	var out ProviderDetail
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/objects/"+url.PathEscape(objectID)+"/provider", nil, &out)
	return out, err
}

// ConnectProvider verifies provider-issued evidence and records the subject
// and the wallets it lists; wallets absent from it become `unlinked`.
func (a *Arca) ConnectProvider(ctx context.Context, objectID string, opts ConnectProviderOptions) (ProviderDetail, error) {
	var out ProviderDetail
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.post(ctx, "/objects/"+url.PathEscape(objectID)+"/provider/connect", opts, &out)
	return out, err
}

// UpdateProviderWallet edits one wallet's role and control relationships.
func (a *Arca) UpdateProviderWallet(ctx context.Context, objectID, walletID string, opts UpdateProviderWalletOptions) (ProviderDetail, error) {
	var out ProviderDetail
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.patch(ctx, "/objects/"+url.PathEscape(objectID)+"/provider/wallets/"+url.PathEscape(walletID), nil, opts, &out)
	return out, err
}

// Deposit-link route observation statuses (DepositLink.Observed.Status).
// Unknown and unwatched mean nothing is known; off is a known absent route.
const (
	DepositRouteUnwatched      = "unwatched"
	DepositRouteUnknown        = "unknown"
	DepositRouteOff            = "off"
	DepositRouteMismatched     = "mismatched"
	DepositRouteActive         = "active"
	DepositRouteNeedsApproval  = "needs_approval"
	DepositRouteNeedsAttention = "needs_attention"
)

// Deposit-link progress (DepositLink.Progress).
const (
	DepositLinkInSync          = "in_sync"
	DepositLinkSetupPending    = "setup_pending"
	DepositLinkSetupAccepted   = "setup_accepted"
	DepositLinkRevokePending   = "revoke_pending"
	DepositLinkRevokeInFlight  = "revoke_in_flight"
	DepositLinkProgressUnknown = "unknown"
)

// CreateDepositLinkOptions names both ends explicitly. RequestID is the
// idempotency key.
type CreateDepositLinkOptions struct {
	RequestID           string `json:"requestId"`
	SourceObjectID      string `json:"sourceObjectId"`
	SourceWalletID      string `json:"sourceWalletId"`
	DestinationObjectID string `json:"destinationObjectId"`
	Adapter             string `json:"adapter"`
}

// DepositLinkSource is the provider end of a link.
type DepositLinkSource struct {
	ObjectID string `json:"objectId"`
	Path     string `json:"path"`
	WalletID string `json:"walletId"`
	Address  string `json:"address"`
}

// AccountRef is a full on-chain V9 account reference.
type AccountRef struct {
	Kernel  string `json:"kernel"`
	Venue   string `json:"venue"`
	LocalID string `json:"localId"`
}

// DepositLinkDestination is the Cash end of a link.
type DepositLinkDestination struct {
	ObjectID   string     `json:"objectId"`
	Path       string     `json:"path"`
	BoundaryID string     `json:"boundaryId"`
	Account    AccountRef `json:"account"`
}

// DepositLinkAdapter is the forwarding contract.
type DepositLinkAdapter struct {
	Address         string `json:"address"`
	Kind            string `json:"kind"`
	RuntimeCodeHash string `json:"runtimeCodeHash,omitempty"`
}

// DepositLinkRequested is the application-requested state.
type DepositLinkRequested struct {
	State             string `json:"state"`
	At                string `json:"at"`
	By                string `json:"by,omitempty"`
	RevokeRequestedAt string `json:"revokeRequestedAt,omitempty"`
	RevokeRequestedBy string `json:"revokeRequestedBy,omitempty"`
}

// DepositLinkConsent is the signed route consent an accepted setup carried.
type DepositLinkConsent struct {
	PermissionVersion string `json:"permissionVersion"`
	Nonce             string `json:"nonce"`
	AllowanceRaw      string `json:"allowanceRaw"`
	Deadline          int64  `json:"deadline"`
}

// DepositLinkLimits are a link's standing limits.
type DepositLinkLimits struct {
	AllowanceMicro string `json:"allowanceMicro,omitempty"`
}

// DepositRouteObservation is the observed route state of a link.
type DepositRouteObservation struct {
	Status             string      `json:"status"`
	RouteID            string      `json:"routeId,omitempty"`
	Account            string      `json:"account,omitempty"`
	LocalID            string      `json:"localId,omitempty"`
	PermissionVersion  *uint64     `json:"permissionVersion,omitempty"`
	AllowanceMicro     string      `json:"allowanceMicro,omitempty"`
	MatchesDestination bool        `json:"matchesDestination"`
	Health             string      `json:"health,omitempty"`
	AsOf               *CashV9AsOf `json:"asOf,omitempty"`
}

// DepositLinkOperations links accepted operations about the link.
type DepositLinkOperations struct {
	SetupOperationID  string `json:"setupOperationId,omitempty"`
	RevokeOperationID string `json:"revokeOperationId,omitempty"`
}

// DepositLink is one link with requested, observed and accepted-operation
// state reported separately.
type DepositLink struct {
	ID          string                  `json:"id"`
	RealmID     string                  `json:"realmId"`
	Source      DepositLinkSource       `json:"source"`
	Destination DepositLinkDestination  `json:"destination"`
	ChainID     string                  `json:"chainId"`
	Token       string                  `json:"token"`
	Adapter     DepositLinkAdapter      `json:"adapter"`
	Requested   DepositLinkRequested    `json:"requested"`
	Lifetime    string                  `json:"lifetime"`
	Consent     *DepositLinkConsent     `json:"consent"`
	Limits      *DepositLinkLimits      `json:"limits"`
	Observed    DepositRouteObservation `json:"observed"`
	Operations  DepositLinkOperations   `json:"operations"`
	Progress    string                  `json:"progress"`
	CreatedAt   string                  `json:"createdAt"`
	UpdatedAt   string                  `json:"updatedAt"`
}

// CreateDepositLink records a deposit link. It signs nothing and moves no
// value: activating the route needs the owner's signatures.
func (a *Arca) CreateDepositLink(ctx context.Context, opts CreateDepositLinkOptions) (DepositLink, error) {
	var out DepositLink
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body := struct {
		RealmID string `json:"realmId"`
		CreateDepositLinkOptions
	}{rid, opts}
	err = a.client.post(ctx, "/deposit-links", body, &out)
	return out, err
}

// GetDepositLink reads one link (arca:ReadObject on both ends).
func (a *Arca) GetDepositLink(ctx context.Context, linkID string) (DepositLink, error) {
	var out DepositLink
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/deposit-links/"+url.PathEscape(linkID), url.Values{"realmId": {rid}}, &out)
	return out, err
}

// ListDepositLinks lists the links an object is either end of.
func (a *Arca) ListDepositLinks(ctx context.Context, objectID string) ([]DepositLink, error) {
	var out struct {
		Links []DepositLink `json:"links"`
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	err = a.client.get(ctx, "/deposit-links", url.Values{"realmId": {rid}, "objectId": {objectID}}, &out)
	return out.Links, err
}

// RevokeDepositLink records the application's request to revoke a link. The
// on-chain route is revoked only by the owner's signed revoke.
func (a *Arca) RevokeDepositLink(ctx context.Context, linkID, requestID string) (DepositLink, error) {
	var out DepositLink
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.post(ctx, "/deposit-links/"+url.PathEscape(linkID)+"/revoke", map[string]string{"realmId": rid, "requestId": requestID}, &out)
	return out, err
}

// CashV9WalletDepositLink is one explicit deposit link into a Wallet
// Account's boundary. RequestedState is `active` or `revoked`; ObservedStatus
// is a DepositRoute* status.
type CashV9WalletDepositLink struct {
	LinkID         string `json:"linkId"`
	SourceObjectID string `json:"sourceObjectId"`
	SourceWalletID string `json:"sourceWalletId"`
	SourceAddress  string `json:"sourceAddress"`
	Adapter        string `json:"adapter"`
	RequestedState string `json:"requestedState"`
	ObservedStatus string `json:"observedStatus"`
}
