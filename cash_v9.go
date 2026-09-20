package arca

import (
	"context"
	"encoding/json"
	"net/url"
)

// CashV9Proposal is a persisted, exact owner consent. Sign TypedData with the
// user's wallet, then retain proposalId + signature until submission resolves.
// Reusing requestId with different inputs is rejected by the server.
type CashV9Proposal struct {
	BoundaryID   string          `json:"boundaryId"`
	ProposalID   string          `json:"proposalId"`
	OwnerAddress string          `json:"ownerAddress"`
	TypedData    json.RawMessage `json:"typedData"`
	ExpiresAt    int64           `json:"expiresAt"`
	Kind         string          `json:"kind"`
}
type CashV9Operation struct {
	Attribution         string `json:"attribution,omitempty"`
	AllocationAccountID string `json:"allocationAccountId,omitempty"`
	BoundaryID          string `json:"boundaryId"`
	ArcaID              string `json:"arcaId,omitempty"`
	DepositAddressID    string `json:"depositAddressId,omitempty"`
	ID                  string `json:"id"`
	OperationID         string `json:"operationId"`
	ArcaPath            string `json:"arcaPath"`
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	Amount              string `json:"amount,omitempty"`
	TxHash              string `json:"txHash,omitempty"`
	CreatedAt           string `json:"createdAt"`
	// UpdatedAt is the last status transition (or transaction attach); empty
	// on operations recorded before it existed.
	UpdatedAt string `json:"updatedAt,omitempty"`
	Error     string `json:"error,omitempty"`
	// Reason is the bounded failure code when Status is "failed":
	// consent_expired, reverted, rejected, destination_refused,
	// recovery_started, cancelled or unknown. Error stays operator text.
	Reason string `json:"reason,omitempty"`
}
type CashV9Account struct {
	BoundaryID           string                 `json:"boundaryId"`
	ArcaID               string                 `json:"arcaId"`
	DefaultArcaID        string                 `json:"defaultArcaId"`
	UnallocatedBalance   string                 `json:"unallocatedBalance"`
	ArcaBalance          string                 `json:"arcaBalance"`
	ArcaAvailableBalance string                 `json:"arcaAvailableBalance"`
	DepositAddresses     []CashV9DepositAddress `json:"depositAddresses"`
	ArcaPath             string                 `json:"arcaPath"`
	Generation           int                    `json:"generation"`
	OwnerAddress         string                 `json:"ownerAddress"`
	KernelAddress        string                 `json:"kernelAddress"`
	VenueAddress         string                 `json:"venueAddress"`
	LocalID              string                 `json:"localId"`
	Chain                string                 `json:"chain"`
	ChainID              string                 `json:"chainId"`
	TokenAddress         string                 `json:"tokenAddress"`
	Decimals             int                    `json:"decimals"`
	AccountStatus        string                 `json:"accountStatus"`
	Balance              string                 `json:"balance"`
	AvailableBalance     string                 `json:"availableBalance"`
	PendingWithdrawal    string                 `json:"pendingWithdrawal"`
	DepositAddress       *string                `json:"depositAddress,omitempty"`
	PendingDeposit       string                 `json:"pendingDeposit"`
	PermissionVersion    string                 `json:"permissionVersion"`
	RecoveryReadyAt      uint64                 `json:"recoveryReadyAt"`
	RecoveryCommitted    bool                   `json:"recoveryCommitted"`
	BlockNumber          string                 `json:"blockNumber"`
	Activity             []CashV9Operation      `json:"activity"`
}

func (a *Arca) ProposeCashV9Account(ctx context.Context, requestID, path, owner string) (CashV9Proposal, error) {
	return a.proposeCashV9(ctx, "accounts", map[string]any{"requestId": requestID, "arcaPath": path, "ownerAddress": owner})
}
func (a *Arca) ProposeCashV9Withdrawal(ctx context.Context, requestID, path, amount, destination string) (CashV9Proposal, error) {
	return a.proposeCashV9(ctx, "withdrawals", map[string]any{"requestId": requestID, "arcaPath": path, "amount": amount, "destination": destination})
}
func (a *Arca) proposeCashV9(ctx context.Context, kind string, body map[string]any) (CashV9Proposal, error) {
	var out CashV9Proposal
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body["realmId"] = rid
	err = a.client.post(ctx, "/custody/v9/cash/"+kind+"/propose", body, &out)
	return out, err
}
func (a *Arca) SubmitCashV9Account(ctx context.Context, proposalID, signature string) (CashV9Operation, error) {
	return a.submitCashV9(ctx, "accounts", proposalID, signature)
}
func (a *Arca) SubmitCashV9Withdrawal(ctx context.Context, proposalID, signature string) (CashV9Operation, error) {
	return a.submitCashV9(ctx, "withdrawals", proposalID, signature)
}
func (a *Arca) submitCashV9(ctx context.Context, kind, id, signature string) (CashV9Operation, error) {
	var out CashV9Operation
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.post(ctx, "/custody/v9/cash/"+kind, map[string]any{"realmId": rid, "proposalId": id, "signature": signature}, &out)
	return out, err
}
func (a *Arca) GetCashV9Account(ctx context.Context, path string) (CashV9Account, error) {
	var out CashV9Account
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/cash/account", url.Values{"realmId": {rid}, "arcaPath": {path}}, &out)
	return out, err
}
func (a *Arca) GetCashV9Operation(ctx context.Context, id string) (CashV9Operation, error) {
	var out CashV9Operation
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/cash/operations/"+url.PathEscape(id), url.Values{"realmId": {rid}}, &out)
	return out, err
}

type CashV9DepositAddress struct {
	ID             string `json:"id"`
	BoundaryID     string `json:"boundaryId"`
	Chain          string `json:"chain"`
	ChainID        string `json:"chainId"`
	TokenAddress   string `json:"tokenAddress"`
	Address        string `json:"address"`
	Method         string `json:"method"`
	Version        int    `json:"version"`
	RecipeID       string `json:"recipeId"`
	FactoryAddress string `json:"factoryAddress"`
	Lifecycle      string `json:"lifecycle"`
	Preferred      bool   `json:"preferred"`
	PendingBalance string `json:"pendingBalance"`
}

func (a *Arca) GetCashV9Boundary(ctx context.Context, boundaryID string) (CashV9Account, error) {
	var out CashV9Account
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/cash/account", url.Values{"realmId": {rid}, "boundaryId": {boundaryID}}, &out)
	return out, err
}

// CashV9AsOf is the point a figure is valid through. Hash and Time are empty
// when the record predates their recording.
type CashV9AsOf struct {
	Block uint64 `json:"block"`
	Hash  string `json:"hash,omitempty"`
	Time  string `json:"time,omitempty"`
}

// CashV9WalletBalances are micro-USDC integers as strings. ReservedMicro and
// PendingOutMicro are one figure seen from two sides (the ledger hold; the
// owner's money leaving). Every cash figure shares AsOf: the realm's confirmed
// observation cursor.
type CashV9WalletBalances struct {
	ConfirmedMicro  string     `json:"confirmedMicro"`
	AvailableMicro  string     `json:"availableMicro"`
	ReservedMicro   string     `json:"reservedMicro"`
	PendingInMicro  string     `json:"pendingInMicro"`
	PendingOutMicro string     `json:"pendingOutMicro"`
	AsOf            CashV9AsOf `json:"asOf"`
}

// CashV9WalletSource is the external address linked to the wallet as the
// address observer projects it. BalanceMicro is nil when unknown — never "0"
// as a stand-in. Health values are the address-observation contract's.
type CashV9WalletSource struct {
	Address              string     `json:"address"`
	BalanceMicro         *string    `json:"balanceMicro"`
	Health               string     `json:"health"`
	CompleteThroughBlock uint64     `json:"completeThroughBlock"`
	AsOf                 CashV9AsOf `json:"asOf"`
}

// CashV9WalletAutoDeposit is the automatic-deposit route state; nil until
// Arca observes it (Wallet Account stream, step 08).
type CashV9WalletAutoDeposit struct {
	State          string     `json:"state"`
	RouteID        string     `json:"routeId,omitempty"`
	AllowanceMicro string     `json:"allowanceMicro,omitempty"`
	AsOf           CashV9AsOf `json:"asOf"`
}

// CashV9WalletOperation is one owner-started operation. Kind is create_cash,
// send, deposit, receiver (allowance and policy later); State is sending,
// confirming, completed or failed (awaiting_approval and uncertain are
// product-layer states); Reason is set only when failed.
type CashV9WalletOperation struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	AmountMicro string `json:"amountMicro"`
	Destination string `json:"destination,omitempty"`
	TxHash      string `json:"txHash,omitempty"`
	StartedAt   string `json:"startedAt"`
	UpdatedAt   string `json:"updatedAt"`
	CanRetry    bool   `json:"canRetry"`
}

// CashV9WalletAccount is the Wallet Account read model: one owner-facing
// wallet — the cash boundary plus its linked external address — composed from
// Arca's durable records with no chain call. WalletState is setup_required,
// setting_up, ready, needs_attention or unavailable; Attention carries the
// bounded reasons for the last two. Revision is the realm change-journal
// sequence at composition (a stream resumes from it).
type CashV9WalletAccount struct {
	Schema         int                      `json:"schema"`
	Revision       uint64                   `json:"revision"`
	RealmID        string                   `json:"realmId"`
	BoundaryID     string                   `json:"boundaryId"`
	OwnerAddress   string                   `json:"ownerAddress"`
	DepositAddress string                   `json:"depositAddress,omitempty"`
	Source         *CashV9WalletSource      `json:"source"`
	WalletState    string                   `json:"walletState"`
	Attention      []string                 `json:"attention"`
	Balances       CashV9WalletBalances     `json:"balances"`
	AutoDeposit    *CashV9WalletAutoDeposit `json:"autoDeposit"`
	Operations     []CashV9WalletOperation  `json:"operations"`
}

// GetCashV9WalletAccount reads the Wallet Account for one boundary. Requires
// arca:ReadObject on the boundary's path — a realm-scoped device token is
// enough — and returns *NotFoundError for an unknown boundary.
func (a *Arca) GetCashV9WalletAccount(ctx context.Context, boundaryID string) (CashV9WalletAccount, error) {
	return a.getCashV9WalletAccount(ctx, url.Values{"boundaryId": {boundaryID}})
}

// GetCashV9WalletAccountByOwner locates the boundary an owner address
// controls and reads its Wallet Account; when the owner has several, the
// active one is returned. Not found when the owner has none.
func (a *Arca) GetCashV9WalletAccountByOwner(ctx context.Context, ownerAddress string) (CashV9WalletAccount, error) {
	return a.getCashV9WalletAccount(ctx, url.Values{"ownerAddress": {ownerAddress}})
}

func (a *Arca) getCashV9WalletAccount(ctx context.Context, query url.Values) (CashV9WalletAccount, error) {
	var out CashV9WalletAccount
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	query.Set("realmId", rid)
	err = a.client.get(ctx, "/custody/v9/cash/wallet-account", query, &out)
	return out, err
}
func (a *Arca) ProposeCashV9BoundaryWithdrawal(ctx context.Context, requestID, boundaryID, amount, destination string) (CashV9Proposal, error) {
	return a.proposeCashV9(ctx, "withdrawals", map[string]any{"requestId": requestID, "boundaryId": boundaryID, "amount": amount, "destination": destination})
}

// CashV9LedgerChange changes attribution within an existing custody boundary.
// Kind is create, default, edit, or allocate. Empty ArcaID clears a default;
// allocate moves Amount from SourceID to ArcaID without an on-chain transaction.
// For edit, Archived is the desired state, including when also renaming.
type CashV9LedgerChange struct {
	RequestID  string `json:"requestId"`
	BoundaryID string `json:"boundaryId"`
	Kind       string `json:"kind"`
	ArcaID     string `json:"arcaId,omitempty"`
	ArcaPath   string `json:"arcaPath,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
	SourceID   string `json:"sourceId,omitempty"`
	Amount     string `json:"amount,omitempty"`
}

type CashV9DepositRecipe struct {
	ID              string `json:"id"`
	Method          string `json:"method"`
	Version         int    `json:"version"`
	Chain           string `json:"chain"`
	ChainID         string `json:"chainId"`
	FactoryAddress  string `json:"factoryAddress"`
	TokenAddress    string `json:"tokenAddress"`
	FactoryCodeHash string `json:"factoryCodeHash"`
	StartBlock      uint64 `json:"startBlock"`
}

func (a *Arca) ChangeCashV9Ledger(ctx context.Context, change CashV9LedgerChange) (CashV9Operation, error) {
	var out CashV9Operation
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body := struct {
		RealmID string `json:"realmId"`
		CashV9LedgerChange
	}{rid, change}
	err = a.client.post(ctx, "/custody/v9/cash/ledger", body, &out)
	return out, err
}

func (a *Arca) GetCashV9DepositRecipes(ctx context.Context) ([]CashV9DepositRecipe, error) {
	var out []CashV9DepositRecipe
	rid, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	err = a.client.get(ctx, "/custody/v9/cash/deposit-recipes", url.Values{"realmId": {rid}}, &out)
	return out, err
}

// IssueCashV9DepositAddress requests a verified deployment. Only use an address
// from an active account snapshot; a pending operation is not a receiving address.
func (a *Arca) IssueCashV9DepositAddress(ctx context.Context, requestID, boundaryID, recipeID string) (CashV9Operation, error) {
	return a.changeCashV9Address(ctx, "", map[string]any{"requestId": requestID, "boundaryId": boundaryID, "recipeId": recipeID, "method": "contract_receiver"})
}

func (a *Arca) PreferCashV9DepositAddress(ctx context.Context, requestID, boundaryID, addressID string) (CashV9Operation, error) {
	return a.changeCashV9Address(ctx, "/preferred", map[string]any{"requestId": requestID, "boundaryId": boundaryID, "addressId": addressID})
}

func (a *Arca) changeCashV9Address(ctx context.Context, suffix string, body map[string]any) (CashV9Operation, error) {
	var out CashV9Operation
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	body["realmId"] = rid
	err = a.client.post(ctx, "/custody/v9/cash/deposit-addresses"+suffix, body, &out)
	return out, err
}
