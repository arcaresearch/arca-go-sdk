package arca

// ---- Auth / Builder ----

type OrgRole string

const (
	RoleOwner     OrgRole = "owner"
	RoleAdmin     OrgRole = "admin"
	RoleDeveloper OrgRole = "developer"
	RoleViewer    OrgRole = "viewer"
)

type MemberType string

const (
	MemberUser           MemberType = "user"
	MemberServiceAccount MemberType = "service_account"
)

type InvitationStatus string

const (
	InvitePending  InvitationStatus = "pending"
	InviteAccepted InvitationStatus = "accepted"
	InviteExpired  InvitationStatus = "expired"
	InviteRevoked  InvitationStatus = "revoked"
)

type BuilderProfile struct {
	ID              string  `json:"id"`
	Email           string  `json:"email"`
	OrgName         string  `json:"orgName"`
	OrgID           string  `json:"orgId,omitempty"`
	Role            OrgRole `json:"role,omitempty"`
	IsPlatformAdmin bool    `json:"isPlatformAdmin,omitempty"`
}

type AuthResponse struct {
	Token        string         `json:"token"`
	RefreshToken string         `json:"refreshToken,omitempty"`
	ExpiresAt    string         `json:"expiresAt"`
	Builder      BuilderProfile `json:"builder"`
}

type RefreshResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt"`
}

// ---- Organization / Team ----

type Organization struct {
	ID                              string `json:"id"`
	Name                            string `json:"name"`
	Slug                            string `json:"slug"`
	CreatedBy                       string `json:"createdBy"`
	CreatedAt                       string `json:"createdAt"`
	UpdatedAt                       string `json:"updatedAt"`
	KybStatus                       string `json:"kybStatus,omitempty"`
	BuilderAgreementVersionAccepted *int   `json:"builderAgreementVersionAccepted,omitempty"`
}

type ActiveTerms struct {
	Version    int    `json:"version"`
	Title      string `json:"title"`
	ContentURL string `json:"contentUrl"`
	IsActive   bool   `json:"isActive"`
	CreatedAt  string `json:"createdAt"`
}

type MyOrgMembership struct {
	OrgID          string      `json:"orgId"`
	OrgName        string      `json:"orgName"`
	OrgSlug        string      `json:"orgSlug"`
	Role           OrgRole     `json:"role"`
	RealmTypeScope []RealmType `json:"realmTypeScope,omitempty"`
}

type CreateOrgResponse struct {
	Organization Organization    `json:"organization"`
	Membership   MyOrgMembership `json:"membership"`
}

type OrgMember struct {
	ID             string      `json:"id"`
	OrgID          string      `json:"orgId"`
	UserID         string      `json:"userId"`
	Email          string      `json:"email,omitempty"`
	MemberType     MemberType  `json:"memberType"`
	Role           OrgRole     `json:"role"`
	RealmTypeScope []RealmType `json:"realmTypeScope,omitempty"`
	InvitedBy      string      `json:"invitedBy,omitempty"`
	CreatedAt      string      `json:"createdAt"`
}

type OrgMemberListResponse struct {
	Members []OrgMember `json:"members"`
	Total   int         `json:"total"`
}

type Invitation struct {
	ID             string           `json:"id"`
	OrgID          string           `json:"orgId"`
	OrgName        string           `json:"orgName,omitempty"`
	Email          string           `json:"email"`
	Role           OrgRole          `json:"role"`
	RealmTypeScope []RealmType      `json:"realmTypeScope,omitempty"`
	Status         InvitationStatus `json:"status"`
	ExpiresAt      string           `json:"expiresAt"`
	InvitedBy      string           `json:"invitedBy"`
	InviteLink     string           `json:"inviteLink,omitempty"`
	CreatedAt      string           `json:"createdAt"`
}

type InvitationListResponse struct {
	Invitations []Invitation `json:"invitations"`
	Total       int          `json:"total"`
}

type InvitationPreview struct {
	OrgName   string           `json:"orgName"`
	Email     string           `json:"email"`
	Role      OrgRole          `json:"role"`
	Status    InvitationStatus `json:"status"`
	ExpiresAt string           `json:"expiresAt"`
}

type InviteMemberRequest struct {
	Email          string      `json:"email"`
	Role           OrgRole     `json:"role"`
	RealmTypeScope []RealmType `json:"realmTypeScope,omitempty"`
}

type UpdateMemberRequest struct {
	Role           OrgRole     `json:"role,omitempty"`
	RealmTypeScope []RealmType `json:"realmTypeScope,omitempty"`
}

// ---- API keys ----

type ApiKeyStatus string

const (
	ApiKeyActive  ApiKeyStatus = "active"
	ApiKeyRevoked ApiKeyStatus = "revoked"
)

// ApiKeyPermissions is a least-privilege preset: read (view-only),
// trade (view + place/cancel orders), or full (no action restriction).
type ApiKeyPermissions string

const (
	PermissionRead  ApiKeyPermissions = "read"
	PermissionTrade ApiKeyPermissions = "trade"
	PermissionFull  ApiKeyPermissions = "full"
)

type ApiKey struct {
	ID        string       `json:"id"`
	OrgID     string       `json:"orgId"`
	CreatedBy string       `json:"createdBy"`
	Name      string       `json:"name"`
	KeyPrefix string       `json:"keyPrefix"`
	Status    ApiKeyStatus `json:"status"`
	Scope     *string      `json:"scope"`
	RealmID   *string      `json:"realmId,omitempty"`
	CreatedAt string       `json:"createdAt"`
	RevokedAt *string      `json:"revokedAt"`
	UpdatedAt string       `json:"updatedAt"`
}

type ApiKeyCreatedResponse struct {
	ApiKey ApiKey `json:"apiKey"`
	RawKey string `json:"rawKey"`
}

type ApiKeyListResponse struct {
	ApiKeys []ApiKey `json:"apiKeys"`
	Total   int      `json:"total"`
}

// CreateApiKeyOptions configures a new API key. Scope and Permissions are
// mutually exclusive. RealmID locks the key to a single realm.
type CreateApiKeyOptions struct {
	Name        string
	Scope       *TokenScope
	RealmID     string
	Permissions ApiKeyPermissions
}

// ---- Scoped token minting ----

type MintTokenRequest struct {
	RealmID           string     `json:"realmId"`
	Sub               string     `json:"sub"`
	Scope             TokenScope `json:"scope"`
	ExpirationMinutes int        `json:"expirationMinutes,omitempty"`
}

type MintTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// MintDeviceTokenOptions mints a scoped device token for an end user.
type MintDeviceTokenOptions struct {
	RealmID     string      `json:"realmId"`
	Sub         string      `json:"sub"`
	ForUserPath string      `json:"forUserPath,omitempty"`
	Permissions *TokenScope `json:"permissions,omitempty"`
}

type CredentialScopeResponse struct {
	CredentialType string  `json:"credentialType"`
	CredentialID   string  `json:"credentialId"`
	Subject        *string `json:"subject"`
	Scope          *string `json:"scope"`
	FullAccess     bool    `json:"fullAccess"`
	CreatedAt      *string `json:"createdAt"`
	ExpiresAt      *string `json:"expiresAt"`
}

// ---- Auth audit ----

type AuthAuditEventType string

const (
	AuditSignIn              AuthAuditEventType = "sign_in"
	AuditTokenMinted         AuthAuditEventType = "token_minted"
	AuditApiKeyAuthenticated AuthAuditEventType = "api_key_authenticated"
	AuditPermissionDenied    AuthAuditEventType = "permission_denied"
)

type AuthAuditEntry struct {
	ID             string             `json:"id"`
	BuilderID      string             `json:"builderId"`
	EventType      AuthAuditEventType `json:"eventType"`
	ActorType      string             `json:"actorType"`
	ActorID        string             `json:"actorId"`
	RealmID        *string            `json:"realmId"`
	Subject        *string            `json:"subject"`
	Action         *string            `json:"action"`
	Resource       *string            `json:"resource"`
	ScopeSummary   *string            `json:"scopeSummary"`
	IPAddress      *string            `json:"ipAddress"`
	TokenJti       *string            `json:"tokenJti"`
	ExpiresAt      *string            `json:"expiresAt"`
	Success        bool               `json:"success"`
	ErrorMessage   *string            `json:"errorMessage"`
	OperationCount int                `json:"operationCount,omitempty"`
	CreatedAt      string             `json:"createdAt"`
}

type AuthAuditListResponse struct {
	Entries []AuthAuditEntry `json:"entries"`
	Total   int              `json:"total"`
}

type AuthAuditFilters struct {
	EventType AuthAuditEventType
	RealmID   string
	Since     string
	Until     string
	Success   *bool
	Limit     int
	Offset    int
}

type BatchSummariesResponse struct {
	Summaries map[string]ExplorerSummary `json:"summaries"`
}

// ---- Custody ----

type CustodyBoundaryStatus string

const (
	CustodyActive CustodyBoundaryStatus = "active"
	CustodyLocked CustodyBoundaryStatus = "locked"
)

type CustodyExchangeArcaStatus string

const (
	CustodyArcaActive  CustodyExchangeArcaStatus = "active"
	CustodyArcaDeleted CustodyExchangeArcaStatus = "deleted"
)

type CustodyVenueHaltType string

const (
	HaltWithdrawals CustodyVenueHaltType = "withdrawals"
	HaltAllFlows    CustodyVenueHaltType = "all_flows"
)

type CustodyPendingExit struct {
	Amount      string `json:"amount"`
	Destination string `json:"destination"`
	ExecutesAt  string `json:"executesAt"`
}

type CustodyBoundary struct {
	ID           string                `json:"id"`
	RealmID      string                `json:"realmId"`
	Balance      string                `json:"balance"`
	Status       CustodyBoundaryStatus `json:"status"`
	LockedBy     *string               `json:"lockedBy"`
	RecoveryKey  *string               `json:"recoveryKey"`
	AgentAddress *string               `json:"agentAddress"`
	TimeLock     int64                 `json:"timeLock"`
	PendingExit  *CustodyPendingExit   `json:"pendingExit"`
}

type CustodyExchangeArca struct {
	ID           string                    `json:"id"`
	BoundaryID   string                    `json:"boundaryId"`
	Status       CustodyExchangeArcaStatus `json:"status"`
	Balance      string                    `json:"balance"`
	VaultAddress string                    `json:"vaultAddress"`
}

type CustodyVenueHalt struct {
	Type      CustodyVenueHaltType `json:"type"`
	ExpiresAt string               `json:"expiresAt"`
}

type CustodyStatus struct {
	ContractAddress string                `json:"contractAddress"`
	ChainID         int64                 `json:"chainId"`
	TotalBalance    string                `json:"totalBalance"`
	Boundaries      []CustodyBoundary     `json:"boundaries"`
	ExchangeArcas   []CustodyExchangeArca `json:"exchangeArcas"`
	VenueHalt       *CustodyVenueHalt     `json:"venueHalt"`
}

type CustodyBoundaryListResponse struct {
	Boundaries []CustodyBoundary `json:"boundaries"`
}

type CustodyExchangeArcaListResponse struct {
	ExchangeArcas []CustodyExchangeArca `json:"exchangeArcas"`
}

type RegisterRecoveryKeyOptions struct {
	BoundaryID    string
	WalletAddress string
}

type RegisterRecoveryKeyResponse struct {
	TransactionHash string `json:"transactionHash"`
	BoundaryID      string `json:"boundaryId"`
	WalletAddress   string `json:"walletAddress"`
}

// PreparedCustodyTransaction is unsigned EVM transaction data for a
// user-signed custody operation. Submit via any Ethereum-compatible wallet.
type PreparedCustodyTransaction struct {
	To      string `json:"to"`
	Data    string `json:"data"`
	ChainID int64  `json:"chainId"`
	Value   string `json:"value"`
}

// GeneratedRecoveryKey is a client-side generated recovery key. The mnemonic
// must be backed up by the user — it is never sent to a server.
type GeneratedRecoveryKey struct {
	Mnemonic string `json:"mnemonic"`
	Address  string `json:"address"`
}
