package arca

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type FundingV9Account struct {
	FundingV9AccountFacts
	ArcaID              string                 `json:"arcaId"`
	BoundaryID          string                 `json:"boundaryId"`
	ArcaPath            string                 `json:"arcaPath"`
	Kind                string                 `json:"kind"`
	OwnerAddress        string                 `json:"ownerAddress"`
	VenueAddress        string                 `json:"venueAddress"`
	LocalID             string                 `json:"localId"`
	AccountAddress      string                 `json:"accountAddress"`
	KernelAddress       string                 `json:"kernelAddress"`
	ChainID             string                 `json:"chainId"`
	TokenAddress        string                 `json:"tokenAddress"`
	DestinationDex      uint32                 `json:"destinationDex"`
	SetupStatus         string                 `json:"setupStatus"`
	Readiness           string                 `json:"readiness"`
	ReadinessReason     string                 `json:"readinessReason,omitempty"`
	AvailableBalance    string                 `json:"availableBalance"`
	WithdrawableBalance string                 `json:"withdrawableBalance"`
	Observation         *FundingV9CoreSnapshot `json:"observation,omitempty"`
	PermissionVersion   uint64                 `json:"permissionVersion"`
	Labels              map[string]string      `json:"labels,omitempty"`
	Lifecycle           string                 `json:"lifecycle,omitempty"`
}

// FundingV9DeclareRequest declares a trading account in an owner's wallet
// without a chain action. Give ArcaPath (under the wallet root) or omit it
// for Arca to choose; give ArcaID to catalogue an account that already
// exists. Labels may carry "name" and "client"; an omitted name becomes
// "Hyperliquid N" at the next unused ordinal.
type FundingV9DeclareRequest struct {
	RealmID      string            `json:"realmId"`
	RequestID    string            `json:"requestId"`
	OwnerAddress string            `json:"ownerAddress"`
	BoundaryID   string            `json:"boundaryId"`
	Kind         string            `json:"kind"`
	ArcaPath     string            `json:"arcaPath,omitempty"`
	ArcaID       string            `json:"arcaId,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// FundingV9AccountPatch renames (Labels["name"]) or archives (Lifecycle
// "archived") a catalogued account. Revision, when set, must be the
// account's current revision.
type FundingV9AccountPatch struct {
	RealmID   string            `json:"realmId"`
	RequestID string            `json:"requestId,omitempty"`
	Revision  uint64            `json:"revision,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Lifecycle string            `json:"lifecycle,omitempty"`
}

// FundingV9CatalogAccount is one account in a wallet's catalogue.
type FundingV9CatalogAccount struct {
	FundingV9AccountFacts
	ArcaID           string            `json:"arcaId"`
	ArcaPath         string            `json:"arcaPath"`
	BoundaryID       string            `json:"boundaryId,omitempty"`
	WalletBoundaryID string            `json:"walletBoundaryId"`
	Kind             string            `json:"kind"`
	OwnerAddress     string            `json:"ownerAddress"`
	Labels           map[string]string `json:"labels"`
	SetupStatus      string            `json:"setupStatus"`
	Lifecycle        string            `json:"lifecycle"`
	DeclaredAt       string            `json:"declaredAt,omitempty"`
	Revision         uint64            `json:"revision"`
}
type FundingV9Action struct {
	ID        string          `json:"id"`
	TypedData json.RawMessage `json:"typedData"`
}
type FundingV9Proposal struct {
	ConsentVersion string            `json:"consentVersion,omitempty"`
	FundingIntent  json.RawMessage   `json:"fundingIntent,omitempty"`
	ProposalID     string            `json:"proposalId"`
	OperationID    string            `json:"operationId"`
	Kind           string            `json:"kind"`
	ArcaID         string            `json:"arcaId"`
	BoundaryID     string            `json:"boundaryId"`
	Target         FundingV9Account  `json:"target"`
	SourceAddress  string            `json:"sourceAddress"`
	RouterAddress  string            `json:"routerAddress"`
	Amount         string            `json:"amount"`
	AmountRaw      string            `json:"amountRaw"`
	ActivationFee  string            `json:"activationFee"`
	ExpectedCredit string            `json:"expectedCredit"`
	GasSponsored   bool              `json:"gasSponsored"`
	ExpiresAt      int64             `json:"expiresAt"`
	Actions        []FundingV9Action `json:"actions"`
}
type FundingV9Signature struct {
	ActionID  string `json:"actionId"`
	Signature string `json:"signature"`
}
type FundingV9SourceDebitEvidence struct {
	TxHash             string `json:"txHash"`
	BlockNumber        uint64 `json:"blockNumber"`
	BlockHash          string `json:"blockHash"`
	TokenAddress       string `json:"tokenAddress"`
	SourceAddress      string `json:"sourceAddress"`
	Amount             string `json:"amount"`
	AuthorizationNonce string `json:"authorizationNonce"`
}
type FundingV9Operation struct {
	Requirements        []FundingV9WalletRequirement  `json:"requirements"`
	SetupState          string                        `json:"setupState"`
	FeePayments         []FundingV9FeePayment         `json:"feePayments,omitempty"`
	SafeToReviewAgain   bool                          `json:"safeToReviewAgain"`
	OperationID         string                        `json:"operationId"`
	ProposalID          string                        `json:"proposalId"`
	ArcaID              string                        `json:"arcaId"`
	BoundaryID          string                        `json:"boundaryId"`
	Kind                string                        `json:"kind"`
	Stage               string                        `json:"stage"`
	Status              string                        `json:"status"`
	Amount              string                        `json:"amount"`
	ActivationFee       string                        `json:"activationFee"`
	ExpectedCredit      string                        `json:"expectedCredit"`
	SourceAddress       string                        `json:"sourceAddress"`
	Target              FundingV9Account              `json:"target"`
	AllConsentsAccepted bool                          `json:"allConsentsAccepted"`
	SourceDebitEvidence *FundingV9SourceDebitEvidence `json:"sourceDebitEvidence,omitempty"`
	Settlement          *FundingV9CoreSettlement      `json:"settlement,omitempty"`
	TxHash              string                        `json:"txHash,omitempty"`
	Error               string                        `json:"error,omitempty"`
	// Attention is the typed reason an operation stopped at stage
	// needs_attention: value may be in transit, so nothing is released.
	Attention string `json:"attention,omitempty"`
	CreatedAt string `json:"createdAt"`
	// Move is set on a trading-account move; Kind is its route.
	Move *FundingV9MoveState `json:"move,omitempty"`
}
type FundingV9CoreSnapshot struct {
	// BeforeEVM identifies native state before that EVM block executes.
	BeforeEVM bool   `json:"beforeEvm,omitempty"`
	EVMBlock  uint64 `json:"evmBlock,omitempty"`
	// SpotBalance is owned spot USDC, separate from Balance (perp equity).
	SpotBalance         string `json:"spotBalance,omitempty"`
	ObservedAt          int64  `json:"observedAt,omitempty"`
	Source              string `json:"source,omitempty"`
	Account             string `json:"account"`
	DestinationDex      uint32 `json:"destinationDex"`
	CoreBlock           uint64 `json:"coreBlock"`
	CoreBlockHash       string `json:"coreBlockHash"`
	Balance             string `json:"balance"`
	AvailableBalance    string `json:"availableBalance"`
	WithdrawableBalance string `json:"withdrawableBalance"`
}
type FundingV9CoreSettlement struct {
	ActionKind      string                `json:"actionKind,omitempty"`
	SourceDex       uint32                `json:"sourceDex,omitempty"`
	ToPerp          *bool                 `json:"toPerp,omitempty"`
	Refusal         *FundingV9CoreRefusal `json:"refusal,omitempty"`
	Sequence        uint64                `json:"sequence"`
	ChainID         string                `json:"chainId"`
	SourceTxHash    string                `json:"sourceTxHash"`
	SourceBlockHash string                `json:"sourceBlockHash"`
	SourceLogIndex  uint                  `json:"sourceLogIndex"`
	NativeTxHash    string                `json:"nativeTxHash"`
	Account         string                `json:"account"`
	Token           string                `json:"token"`
	DestinationDex  uint32                `json:"destinationDex"`
	AmountRaw       string                `json:"amountRaw"`
	FeeRaw          string                `json:"feeRaw"`
	CreditRaw       string                `json:"creditRaw"`
	Final           bool                  `json:"final"`
	Snapshot        FundingV9CoreSnapshot `json:"snapshot"`
	// DebitAccount is the HyperCore account a move's native action debited.
	DebitAccount string `json:"debitAccount,omitempty"`
}
type FundingV9CoreRefusal struct {
	Reason         string `json:"reason"`
	Detail         string `json:"detail"`
	DemandRevision string `json:"demandRevision,omitempty"`
	ExpectedNonce  string `json:"expectedNonce,omitempty"`
}

type FundingV9SetupRequest struct {
	RealmID      string `json:"realmId"`
	RequestID    string `json:"requestId"`
	ArcaPath     string `json:"arcaPath"`
	OwnerAddress string `json:"ownerAddress"`
	Kind         string `json:"kind"`
}
type FundingV9DepositRequest struct {
	RealmID       string `json:"realmId"`
	RequestID     string `json:"requestId"`
	ArcaID        string `json:"arcaId"`
	SourceAddress string `json:"sourceAddress"`
	Amount        string `json:"amount"`
}
type FundingV9SubmitRequest struct {
	RealmID    string               `json:"realmId"`
	ProposalID string               `json:"proposalId"`
	Signatures []FundingV9Signature `json:"signatures"`
}

type FundingV9ProposalState struct {
	Proposal          FundingV9Proposal   `json:"proposal"`
	Status            string              `json:"status"`
	Operation         *FundingV9Operation `json:"operation,omitempty"`
	SafeToReviewAgain bool                `json:"safeToReviewAgain"`
}

// New funding proposals require one USDC authorization. Validate the unsigned
// readable FundingIntent and its digest against the authorization nonce before
// signing. Persist the exact signature and keep it on timeouts. Legacy two-action
// proposals retain their original ordered bundle and must not be reinterpreted.
func (a *Arca) ProposeFundingV9Account(ctx context.Context, r FundingV9SetupRequest) (FundingV9Proposal, error) {
	var out FundingV9Proposal
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	r.RealmID = realm
	err = a.client.post(ctx, "/custody/v9/funding/accounts/propose", r, &out)
	return out, err
}

// DeclareFundingV9Account records a trading account and its labels; the
// first move into it performs its setup. Replaying a RequestID returns the
// same account.
func (a *Arca) DeclareFundingV9Account(ctx context.Context, r FundingV9DeclareRequest) (FundingV9CatalogAccount, error) {
	var out FundingV9CatalogAccount
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	r.RealmID = realm
	err = a.client.post(ctx, "/custody/v9/funding/accounts/declare", r, &out)
	return out, err
}

// UpdateFundingV9Account renames or archives a catalogued account.
func (a *Arca) UpdateFundingV9Account(ctx context.Context, id string, p FundingV9AccountPatch) (FundingV9CatalogAccount, error) {
	var out FundingV9CatalogAccount
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	p.RealmID = realm
	err = a.client.patch(ctx, "/custody/v9/funding/accounts/"+url.PathEscape(id), nil, p, &out)
	return out, err
}
func (a *Arca) ProposeFundingV9Deposit(ctx context.Context, r FundingV9DepositRequest) (FundingV9Proposal, error) {
	var out FundingV9Proposal
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	r.RealmID = realm
	err = a.client.post(ctx, "/custody/v9/funding/deposits/propose", r, &out)
	return out, err
}
func (a *Arca) SubmitFundingV9Account(ctx context.Context, r FundingV9SubmitRequest) (FundingV9Operation, error) {
	return a.submitFundingV9(ctx, "accounts", r)
}
func (a *Arca) SubmitFundingV9Deposit(ctx context.Context, r FundingV9SubmitRequest) (FundingV9Operation, error) {
	return a.submitFundingV9(ctx, "deposits", r)
}
func (a *Arca) submitFundingV9(ctx context.Context, kind string, r FundingV9SubmitRequest) (FundingV9Operation, error) {
	var out FundingV9Operation
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	r.RealmID = realm
	err = a.client.post(ctx, "/custody/v9/funding/"+kind, r, &out)
	return out, err
}
func (a *Arca) FundingV9Targets(ctx context.Context, owner, root string) ([]FundingV9Account, error) {
	var out []FundingV9Account
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/targets", url.Values{"realmId": {realm}, "ownerAddress": {owner}, "arcaPath": {root}}, &out)
	return out, err
}
func (a *Arca) GetFundingV9Account(ctx context.Context, id string) (FundingV9Account, error) {
	var out FundingV9Account
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/accounts/"+url.PathEscape(id), url.Values{"realmId": {realm}}, &out)
	return out, err
}
func (a *Arca) GetFundingV9Operation(ctx context.Context, id string) (FundingV9Operation, error) {
	var out FundingV9Operation
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/operations/"+url.PathEscape(id), url.Values{"realmId": {realm}}, &out)
	return out, err
}
func (a *Arca) GetFundingV9Proposal(ctx context.Context, id string) (FundingV9ProposalState, error) {
	var out FundingV9ProposalState
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/proposals/"+url.PathEscape(id), url.Values{"realmId": {realm}}, &out)
	return out, err
}
func (a *Arca) RetireFundingV9Proposal(ctx context.Context, id string) (FundingV9ProposalState, error) {
	var out FundingV9ProposalState
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.post(ctx, "/custody/v9/funding/proposals/"+url.PathEscape(id)+"/retire", map[string]string{"realmId": realm}, &out)
	return out, err
}

// FundingV9Recheck is the answer to a native-refusal recheck: the demand
// revision the correlation source will answer next and the reason rechecked.
type FundingV9Recheck struct {
	OperationID string `json:"operationId"`
	DemandKey   string `json:"demandKey"`
	Revision    string `json:"revision"`
	Reason      string `json:"reason"`
}

// RecheckFundingV9Operation re-asks the native correlation source to
// attribute the source of a deposit or move stopped at `needs_attention` by a
// native refusal. Nothing is booked or released: a new correlation supersedes
// the refusal and the operation resumes; a repeated refusal leaves it stopped.
// Attention raised by Arca's own comparisons is not recheckable (409
// V9_OPERATION_NOT_RECHECKABLE).
func (a *Arca) RecheckFundingV9Operation(ctx context.Context, id string) (FundingV9Recheck, error) {
	var out FundingV9Recheck
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.post(ctx, "/custody/v9/funding/operations/"+url.PathEscape(id)+"/recheck", map[string]string{"realmId": realm}, &out)
	return out, err
}

// StreamFundingV9Operation consumes authoritative full snapshots with bounded
// buffering. Reconnect by calling again with the same operation ID; the initial
// snapshot recovers missed progress. Cancellation closes the HTTP request. A
// callback error stops delivery; no 404 authorizes a replacement deposit.
func (a *Arca) StreamFundingV9Operation(ctx context.Context, id string, accept func(FundingV9Operation) error) error {
	if accept == nil {
		return fmt.Errorf("funding snapshot callback required")
	}
	realm, err := a.realmID(ctx)
	if err != nil {
		return err
	}
	endpoint, err := a.client.buildURL("/custody/v9/funding/events", url.Values{"realmId": {realm}, "operationId": {id}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.client.getCredential())
	if a.client.headerHook != nil {
		for k, v := range a.client.headerHook() {
			req.Header.Set(k, v)
		}
	}
	client := *a.client.http
	client.Timeout = 0
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return a.client.unwrap(response, nil)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("funding snapshot stream unavailable")
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && data != "" {
			var op FundingV9Operation
			if err = json.Unmarshal([]byte(data), &op); err != nil {
				return err
			}
			if op.OperationID != id {
				return fmt.Errorf("funding snapshot identity mismatch")
			}
			if err = accept(op); err != nil {
				return err
			}
			data = ""
		} else if strings.HasPrefix(line, "data:") {
			if data != "" {
				return fmt.Errorf("invalid funding SSE frame")
			}
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

// FundingV9MoveSetup names a destination trading account that does not exist
// yet; the move waits for its owner-signed creation.
type FundingV9MoveSetup struct {
	ArcaPath     string `json:"arcaPath"`
	OwnerAddress string `json:"ownerAddress"`
}

// FundingV9MoveQuoteRequest amounts are integer raw USDC strings (6
// decimals). An empty AmountRaw returns the bounds only.
type FundingV9MoveQuoteRequest struct {
	RealmID    string              `json:"realmId"`
	FromArcaID string              `json:"fromArcaId"`
	ToArcaID   string              `json:"toArcaId,omitempty"`
	Setup      *FundingV9MoveSetup `json:"setup,omitempty"`
	AmountRaw  string              `json:"amountRaw,omitempty"`
}

type FundingV9MoveSide struct {
	Kind           string `json:"kind"`
	ArcaID         string `json:"arcaId"`
	ArcaPath       string `json:"arcaPath"`
	BoundaryID     string `json:"boundaryId"`
	AccountAddress string `json:"accountAddress"`
}

type FundingV9MoveRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// FundingV9MoveQuote binds raw-unit amounts. ArrivesRaw equals AmountRaw;
// activation is charged on top. NetworkFeeRaw is an upper allowance, included
// in DebitRaw; actual charges appear on the settled move. MinimumRaw/MaxRaw
// are inclusive. Refusal explains an invalid amount without admitting it.
type FundingV9MoveQuote struct {
	FundingV9DeferredActivationFee
	Route               string                `json:"route"`
	From                FundingV9MoveSide     `json:"from"`
	To                  FundingV9MoveSide     `json:"to"`
	AmountRaw           string                `json:"amountRaw"`
	ActivationFeeRaw    string                `json:"activationFeeRaw"`
	NetworkFeeRaw       string                `json:"networkFeeRaw"`
	ArrivesRaw          string                `json:"arrivesRaw"`
	DebitRaw            string                `json:"debitRaw"`
	MinimumRaw          string                `json:"minimumRaw"`
	MaxRaw              string                `json:"maxRaw"`
	FromActivation      string                `json:"fromActivation"`
	ToActivation        string                `json:"toActivation"`
	FromActivationAfter string                `json:"fromActivationAfter"`
	ToActivationAfter   string                `json:"toActivationAfter"`
	RequiresSetup       bool                  `json:"requiresSetup"`
	ExpiresAt           int64                 `json:"expiresAt"`
	Refusal             *FundingV9MoveRefusal `json:"refusal,omitempty"`
}

// FundingV9MoveRequest admits a move from a reviewed quote: repeat its terms
// exactly. RequestID makes the create idempotent; SetupDeadline (unix
// seconds, at most five minutes ahead) is required with Setup.
type FundingV9MoveRequest struct {
	RealmID          string              `json:"realmId"`
	RequestID        string              `json:"requestId"`
	FromArcaID       string              `json:"fromArcaId"`
	ToArcaID         string              `json:"toArcaId,omitempty"`
	Setup            *FundingV9MoveSetup `json:"setup,omitempty"`
	AmountRaw        string              `json:"amountRaw"`
	ActivationFeeRaw string              `json:"activationFeeRaw"`
	NetworkFeeRaw    string              `json:"networkFeeRaw"`
	FromActivation   string              `json:"fromActivation"`
	ToActivation     string              `json:"toActivation"`
	QuoteExpiresAt   int64               `json:"quoteExpiresAt"`
	SetupDeadline    int64               `json:"setupDeadline,omitempty"`
}

// FundingV9MoveRequestFromQuote copies a quote's reviewed terms into a create.
func FundingV9MoveRequestFromQuote(requestID string, q FundingV9MoveQuote) FundingV9MoveRequest {
	r := FundingV9MoveRequest{RequestID: requestID, FromArcaID: q.From.ArcaID, AmountRaw: q.AmountRaw, ActivationFeeRaw: q.ActivationFeeRaw, NetworkFeeRaw: q.NetworkFeeRaw, FromActivation: q.FromActivation, ToActivation: q.ToActivation, QuoteExpiresAt: q.ExpiresAt}
	if !q.RequiresSetup {
		r.ToArcaID = q.To.ArcaID
	}
	return r
}

type FundingV9MoveStep struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	TxHash      string `json:"txHash,omitempty"`
	BlockNumber uint64 `json:"blockNumber,omitempty"`
	BlockHash   string `json:"blockHash,omitempty"`
}

type FundingV9MoveState struct {
	FundingV9DeferredActivationFee
	// Actual amounts are absent before the correlated return debit is booked.
	// NetworkFeeRaw and DebitRaw remain the immutable quote's upper bounds.
	ActualDebitRaw      string              `json:"actualDebitRaw,omitempty"`
	ActualNetworkFeeRaw string              `json:"actualNetworkFeeRaw,omitempty"`
	Route               string              `json:"route"`
	From                FundingV9Account    `json:"from"`
	To                  FundingV9Account    `json:"to"`
	AmountRaw           string              `json:"amountRaw"`
	ActivationFeeRaw    string              `json:"activationFeeRaw"`
	NetworkFeeRaw       string              `json:"networkFeeRaw"`
	DebitRaw            string              `json:"debitRaw"`
	FromActivation      string              `json:"fromActivation"`
	ToActivation        string              `json:"toActivation"`
	FromActivationAfter string              `json:"fromActivationAfter"`
	ToActivationAfter   string              `json:"toActivationAfter"`
	Ref                 string              `json:"ref"`
	Steps               []FundingV9MoveStep `json:"steps"`
	DebitBooked         bool                `json:"debitBooked"`
	CreditBooked        bool                `json:"creditBooked"`
	SetupOperationID    string              `json:"setupOperationId,omitempty"`
	SetupDeadline       int64               `json:"setupDeadline,omitempty"`
}

// FundingV9MoveAdmission is a move and, when its destination needs setup,
// the owner-signed creation it waits for (sign and submit it with
// SubmitFundingV9Account before SetupDeadline).
type FundingV9MoveAdmission struct {
	Operation FundingV9Operation `json:"operation"`
	Setup     *FundingV9Proposal `json:"setup,omitempty"`
}

// QuoteFundingV9Move prices a move between Cash and the owner's own trading
// accounts, or between two trading accounts. The realm must enable moves.
func (a *Arca) QuoteFundingV9Move(ctx context.Context, r FundingV9MoveQuoteRequest) (FundingV9MoveQuote, error) {
	var out FundingV9MoveQuote
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	r.RealmID = realm
	err = a.client.post(ctx, "/custody/v9/funding/moves/quote", r, &out)
	return out, err
}

// CreateFundingV9Move admits a move. It needs no signature: the operator
// executes it under SameOwner. Retry with the same RequestID after a lost
// reply; a changed quote is refused with V9_MOVE_QUOTE_CHANGED.
func (a *Arca) CreateFundingV9Move(ctx context.Context, r FundingV9MoveRequest) (FundingV9MoveAdmission, error) {
	var out FundingV9MoveAdmission
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	r.RealmID = realm
	err = a.client.post(ctx, "/custody/v9/funding/moves", r, &out)
	return out, err
}

// RetireFundingV9Move fences an expired request against late admission. Retired
// is true only for an explicit durable fence; an ordinary 404 is never proof.
// If admission won the race, the original admitted move is returned instead.
func (a *Arca) RetireFundingV9Move(ctx context.Context, r FundingV9MoveRequest) (admission FundingV9MoveAdmission, retired bool, err error) {
	realm, err := a.realmID(ctx)
	if err != nil {
		return admission, false, err
	}
	r.RealmID = realm
	var out struct {
		Status      string                  `json:"status"`
		RequestID   string                  `json:"requestId"`
		OperationID string                  `json:"operationId"`
		Admission   *FundingV9MoveAdmission `json:"admission,omitempty"`
	}
	if err = a.client.post(ctx, "/custody/v9/funding/moves/retire", r, &out); err != nil {
		return admission, false, err
	}
	if out.RequestID != r.RequestID || out.OperationID == "" {
		return admission, false, fmt.Errorf("arca: move retirement identity mismatch")
	}
	switch {
	case out.Status == "retired" && out.Admission == nil:
		return admission, true, nil
	case out.Status == "accepted" && out.Admission != nil && out.Admission.Operation.OperationID == out.OperationID:
		return *out.Admission, false, nil
	default:
		return admission, false, fmt.Errorf("arca: incomplete move retirement evidence")
	}
}

// Move resolution statuses returned by ResolveFundingV9Move.
const (
	// FundingV9MoveAccepted: the request was admitted; Admission carries it.
	FundingV9MoveAccepted = "accepted"
	// FundingV9MoveRetired: a durable fence proves the request can never be
	// admitted. Nothing moved.
	FundingV9MoveRetired = "retired"
	// FundingV9MoveAbsent: nothing durable names the key yet. The request may
	// still arrive, or be re-sent identically, until its quote expires; after
	// that RetireFundingV9Move settles it. Absent is not a fence.
	FundingV9MoveAbsent = "absent"
)

// FundingV9MoveResolution is what became of a request key.
type FundingV9MoveResolution struct {
	Status      string                  `json:"status"`
	RequestID   string                  `json:"requestId"`
	OperationID string                  `json:"operationId"`
	Admission   *FundingV9MoveAdmission `json:"admission,omitempty"`
}

// ResolveFundingV9Move answers a request key without admitting or retiring
// anything: FundingV9MoveAccepted with the admission a lost create reply
// would have carried, FundingV9MoveRetired, or FundingV9MoveAbsent. It
// requires arca:ReadObject at the realm root (the product backend's lane).
// The lost-reply protocol is: re-send the identical CreateFundingV9Move (it
// returns the admission before any gate), or resolve here; once the quote
// has expired and the key is still absent, RetireFundingV9Move fences it.
func (a *Arca) ResolveFundingV9Move(ctx context.Context, requestID string) (FundingV9MoveResolution, error) {
	var out FundingV9MoveResolution
	if requestID == "" {
		return out, fmt.Errorf("arca: requestId required")
	}
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	if err = a.client.get(ctx, "/custody/v9/funding/moves", url.Values{"realmId": {realm}, "requestId": {requestID}}, &out); err != nil {
		return out, err
	}
	if out.RequestID != requestID || out.OperationID == "" {
		return out, fmt.Errorf("arca: move resolution identity mismatch")
	}
	switch out.Status {
	case FundingV9MoveAccepted:
		if out.Admission == nil || out.Admission.Operation.OperationID != out.OperationID {
			return out, fmt.Errorf("arca: incomplete move resolution evidence")
		}
	case FundingV9MoveRetired, FundingV9MoveAbsent:
		if out.Admission != nil {
			return out, fmt.Errorf("arca: incomplete move resolution evidence")
		}
	default:
		return out, fmt.Errorf("arca: unknown move resolution status %q", out.Status)
	}
	return out, nil
}

// GetFundingV9Move reads a move. StreamFundingV9Operation follows it by id.
func (a *Arca) GetFundingV9Move(ctx context.Context, id string) (FundingV9MoveAdmission, error) {
	var out FundingV9MoveAdmission
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/moves/"+url.PathEscape(id), url.Values{"realmId": {realm}}, &out)
	return out, err
}

// FundingV9WalletSnapshotSchema is the wallet snapshot schema this SDK reads.
const FundingV9WalletSnapshotSchema = 2

// FundingV9WalletWatermark states what the snapshot's figures describe.
// Sequence is the realm journal position it was read at (a stream resumes
// after it); Revision changes only when the wallet's own boundaries change.
type FundingV9WalletWatermark struct {
	Sequence            uint64 `json:"sequence"`
	Revision            uint64 `json:"revision"`
	CashBlock           uint64 `json:"cashBlock"`
	CashBlockHash       string `json:"cashBlockHash,omitempty"`
	NativeObservedAt    int64  `json:"nativeObservedAt"`
	SettlementSequence  uint64 `json:"settlementSequence"`
	LastSettlementBlock uint64 `json:"lastSettlementBlock"`
}

type FundingV9WalletNativeBalances struct {
	TotalMicro        string `json:"totalMicro"`
	PerpMicro         string `json:"perpMicro"`
	WithdrawableMicro string `json:"withdrawableMicro"`
	SpotMicro         string `json:"spotMicro"`
	Source            string `json:"source"`
}

// FundingV9WalletTradingAccount is one declared or deployed trading account.
// Balances is null only for a deployed account never observed; AsOf is the
// venue time the figures describe and Current is false once they go quiet.
// HeldBy names the operation whose money is listed in Moving instead.
type FundingV9WalletTradingAccount struct {
	LocalID  string `json:"localId,omitempty"`
	Revision uint64 `json:"revision"`
	FundingV9AccountFacts
	ArcaID         string                         `json:"arcaId"`
	ArcaPath       string                         `json:"arcaPath"`
	BoundaryID     string                         `json:"boundaryId"`
	Kind           string                         `json:"kind"`
	AccountAddress string                         `json:"accountAddress"`
	Labels         map[string]string              `json:"labels"`
	State          string                         `json:"state"`
	SetupStatus    string                         `json:"setupStatus"`
	Lifecycle      string                         `json:"lifecycle"`
	DeclaredAt     string                         `json:"declaredAt,omitempty"`
	Balances       *FundingV9WalletNativeBalances `json:"balances"`
	AsOf           string                         `json:"asOf,omitempty"`
	Current        bool                           `json:"current"`
	HeldBy         string                         `json:"heldBy,omitempty"`
	// Routes are the standing shapes of cash_to_trading (into this account)
	// and trading_to_cash (out of it) at this snapshot: availability with its
	// reason, inclusive MinimumRaw/MaxRaw, the activation fee and the
	// network-fee bound, from the same pure quote logic admission re-runs.
	// Render fee hints and limits from them; quote only for a priced review.
	Routes []FundingV9WalletRoutePreview `json:"routes"`
}

// FundingV9WalletRoutePreview is one route's standing shape on a trading
// account. It is advisory: a quote is the priced, expiring review admission
// re-prices; a preview never admits or reserves.
type FundingV9WalletRoutePreview struct {
	Route            string `json:"route"`
	Allowed          bool   `json:"allowed"`
	Reason           string `json:"reason,omitempty"`
	MinimumRaw       string `json:"minimumRaw,omitempty"`
	MaxRaw           string `json:"maxRaw,omitempty"`
	ActivationFeeRaw string `json:"activationFeeRaw,omitempty"`
	NetworkFeeRaw    string `json:"networkFeeRaw,omitempty"`
	RequiresSetup    bool   `json:"requiresSetup"`
}

type FundingV9WalletEndpoint struct {
	ArcaPath   string `json:"arcaPath,omitempty"`
	Kind       string `json:"kind"`
	ArcaID     string `json:"arcaId,omitempty"`
	BoundaryID string `json:"boundaryId,omitempty"`
	Address    string `json:"address,omitempty"`
}

// FundingV9WalletMoving is money in flight: Leg is reserved (still held by
// the source) or in_transit (left the source, not yet credited).
type FundingV9WalletMoving struct {
	OperationID string                  `json:"operationId"`
	Kind        string                  `json:"kind"`
	Leg         string                  `json:"leg"`
	AmountMicro string                  `json:"amountMicro"`
	From        FundingV9WalletEndpoint `json:"from"`
	To          FundingV9WalletEndpoint `json:"to"`
}

type FundingV9WalletCashLeg struct {
	Leg         string `json:"leg"`
	State       string `json:"state"`
	AmountMicro string `json:"amountMicro"`
	TxHash      string `json:"txHash,omitempty"`
}

// FundingV9WalletRequirement is an owner answer the operation waits on;
// Proposal carries the typed data to sign.
type FundingV9WalletRequirement struct {
	Kind        string             `json:"kind"`
	OperationID string             `json:"operationId"`
	ProposalID  string             `json:"proposalId"`
	ActionIDs   []string           `json:"actionIds"`
	ExpiresAt   int64              `json:"expiresAt"`
	Proposal    *FundingV9Proposal `json:"proposal,omitempty"`
}

type FundingV9WalletSettlement struct {
	Sequence      uint64 `json:"sequence"`
	NativeTxHash  string `json:"nativeTxHash,omitempty"`
	CoreBlock     uint64 `json:"coreBlock"`
	CoreBlockHash string `json:"coreBlockHash,omitempty"`
	AmountMicro   string `json:"amountMicro"`
	FeeMicro      string `json:"feeMicro"`
	CreditMicro   string `json:"creditMicro"`
	Final         bool   `json:"final"`
}

// FundingV9WalletOperation is one funding operation with everything needed
// to render its progress: stage, steps, requirements and settlement.
type FundingV9WalletOperation struct {
	ActualDebitMicro      string                `json:"actualDebitMicro,omitempty"`
	ActualNetworkFeeMicro string                `json:"actualNetworkFeeMicro,omitempty"`
	SetupState            string                `json:"setupState"`
	FeePayments           []FundingV9FeePayment `json:"feePayments,omitempty"`
	FundingV9DeferredActivationFee
	OperationID        string                       `json:"operationId"`
	RequestID          string                       `json:"requestId,omitempty"`
	Kind               string                       `json:"kind"`
	Stage              string                       `json:"stage"`
	Status             string                       `json:"status"`
	Attention          string                       `json:"attention,omitempty"`
	Error              string                       `json:"error,omitempty"`
	SafeToReviewAgain  bool                         `json:"safeToReviewAgain"`
	AmountMicro        string                       `json:"amountMicro"`
	ArrivesMicro       string                       `json:"arrivesMicro"`
	DebitMicro         string                       `json:"debitMicro"`
	ActivationFeeMicro string                       `json:"activationFeeMicro"`
	NetworkFeeMicro    string                       `json:"networkFeeMicro"`
	From               FundingV9WalletEndpoint      `json:"from"`
	To                 FundingV9WalletEndpoint      `json:"to"`
	Steps              []FundingV9MoveStep          `json:"steps"`
	DebitBooked        bool                         `json:"debitBooked"`
	CreditBooked       bool                         `json:"creditBooked"`
	CashLeg            *FundingV9WalletCashLeg      `json:"cashLeg,omitempty"`
	SetupOperationID   string                       `json:"setupOperationId,omitempty"`
	SetupDeadline      int64                        `json:"setupDeadline,omitempty"`
	Requirements       []FundingV9WalletRequirement `json:"requirements"`
	Settlement         *FundingV9WalletSettlement   `json:"settlement,omitempty"`
	TxHash             string                       `json:"txHash,omitempty"`
	CreatedAt          string                       `json:"createdAt"`
}

// FundingV9WalletTotals holds TotalMicro = CashMicro + MovingMicro +
// TradingMicro at every snapshot.
type FundingV9WalletTotals struct {
	CashMicro    string `json:"cashMicro"`
	MovingMicro  string `json:"movingMicro"`
	TradingMicro string `json:"tradingMicro"`
	TotalMicro   string `json:"totalMicro"`
	Complete     bool   `json:"complete"`
	Current      bool   `json:"current"`
}

// FundingV9WalletSnapshot is the complete wallet at one journal position:
// Cash, every trading account, money in flight and funding operations.
// Replace it as a whole; it is never refused for movement in progress.
//
// A wallet exists from the moment its funding source is registered (a
// verified provider wallet), not only from Cash setup: before a Cash
// boundary exists the snapshot has an empty BoundaryID, Cash in
// setup_required with zero balances and the source as observed, no accounts,
// nothing moving, no operations and no Accounting. Key such a wallet by
// (OwnerAddress, ArcaPath); the Cash wallet that supersedes it carries the
// same pair with its BoundaryID set.
type FundingV9WalletSnapshot struct {
	Accounting   *FundingV9WalletAccounting      `json:"accounting,omitempty"`
	CashTarget   FundingV9WalletEndpoint         `json:"cashTarget"`
	CashAccount  *FundingV9WalletCashAccount     `json:"cashAccount,omitempty"`
	Schema       int                             `json:"schema"`
	RealmID      string                          `json:"realmId"`
	OwnerAddress string                          `json:"ownerAddress"`
	ArcaPath     string                          `json:"arcaPath"`
	BoundaryID   string                          `json:"boundaryId"`
	Cash         CashV9WalletAccount             `json:"cash"`
	Accounts     []FundingV9WalletTradingAccount `json:"accounts"`
	Moving       []FundingV9WalletMoving         `json:"moving"`
	Operations   []FundingV9WalletOperation      `json:"operations"`
	Totals       FundingV9WalletTotals           `json:"totals"`
	Attention    []string                        `json:"attention"`
	Watermark    FundingV9WalletWatermark        `json:"watermark"`
	ComposedAt   string                          `json:"composedAt"`
}

// GetFundingV9WalletSnapshot reads Cash and every owned trading account under
// arcaPath in a single complete envelope. Requires arca:ReadObject for both
// the wallet root and Cash path. It is a one-shot read; to follow a wallet,
// use StreamFundingV9Wallet.
func (a *Arca) GetFundingV9WalletSnapshot(ctx context.Context, ownerAddress, arcaPath, boundaryID string) (FundingV9WalletSnapshot, error) {
	var out FundingV9WalletSnapshot
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/wallet-snapshot", url.Values{"realmId": {realm}, "ownerAddress": {ownerAddress}, "arcaPath": {arcaPath}, "boundaryId": {boundaryID}}, &out)
	return out, err
}

// FundingV9WalletStreamDisconnectedError ends a wallet stream; LastEventID is
// the id to resume with.
type FundingV9WalletStreamDisconnectedError struct {
	LastEventID string
	Cause       error
}

func (e *FundingV9WalletStreamDisconnectedError) Error() string {
	return fmt.Sprintf("arca: wallet stream disconnected after %q: %v", e.LastEventID, e.Cause)
}
func (e *FundingV9WalletStreamDisconnectedError) Unwrap() error { return e.Cause }

// FundingV9WalletError precedes connection closure without a checkpoint.
// Mark the wallet stale; reconnect resnapshots it even without another write.
type FundingV9WalletError struct {
	BoundaryID   string `json:"boundaryId"`
	OwnerAddress string `json:"ownerAddress"`
	ArcaPath     string `json:"arcaPath"`
	Error        string `json:"error"`
}

// FundingV9WalletsEvent is one realm stream frame: exactly one of Wallet,
// WalletError, Position or CaughtUp is set. Persist Position only after the
// preceding wallet projections are durable. CaughtUp is not an absence proof.
type FundingV9WalletsEvent struct {
	// Transport is connected or heartbeat; neither certifies accounting freshness.
	Transport   string
	Wallet      *FundingV9WalletSnapshot
	WalletError *FundingV9WalletError
	Position    uint64
	// CaughtUp follows a complete bootstrap/journal drain. It is not an
	// acknowledgment or evidence that an absent operation never existed.
	CaughtUp *FundingV9WalletPosition
}

type FundingV9WalletPosition struct {
	Sequence uint64 `json:"sequence"`
}

// StreamFundingV9Wallet follows one wallet: a complete snapshot on connect
// and after every change to it. Each snapshot replaces the last. Returns
// *FundingV9WalletStreamDisconnectedError when the connection ends.
func (a *Arca) StreamFundingV9Wallet(ctx context.Context, ownerAddress, arcaPath, boundaryID, lastEventID string, accept func(FundingV9WalletSnapshot) error) error {
	if accept == nil {
		return fmt.Errorf("arca: wallet snapshot callback required")
	}
	realm, err := a.realmID(ctx)
	if err != nil {
		return err
	}
	q := url.Values{"realmId": {realm}, "ownerAddress": {ownerAddress}, "arcaPath": {arcaPath}, "boundaryId": {boundaryID}}
	return a.readFundingWalletSSE(ctx, "/custody/v9/funding/wallet-stream", q, lastEventID, func(event string, data []byte) error {
		if event != "snapshot" {
			return nil
		}
		var s FundingV9WalletSnapshot
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("arca: wallet snapshot frame: %w", err)
		}
		if s.Schema != FundingV9WalletSnapshotSchema {
			return fmt.Errorf("arca: wallet snapshot schema %d unsupported", s.Schema)
		}
		return accept(s)
	})
}

// StreamFundingV9Wallets is a product backend's realm-wide feed: the complete
// snapshot of every wallet that changed after `after`, then a position. A
// callback error stops delivery without advancing past the frame. Returns
// *FundingV9WalletStreamDisconnectedError carrying the last position.
func (a *Arca) StreamFundingV9Wallets(ctx context.Context, after uint64, accept func(FundingV9WalletsEvent) error) error {
	return a.streamFundingV9Wallets(ctx, after, false, accept)
}

// StreamFundingV9WalletsWithTransport also delivers connection/heartbeat
// notifications for durable consumers that maintain a liveness watchdog.
// These notifications do not advance the journal checkpoint.
func (a *Arca) StreamFundingV9WalletsWithTransport(ctx context.Context, after uint64, accept func(FundingV9WalletsEvent) error) error {
	return a.streamFundingV9Wallets(ctx, after, true, accept)
}

func (a *Arca) streamFundingV9Wallets(ctx context.Context, after uint64, transport bool, accept func(FundingV9WalletsEvent) error) error {
	if accept == nil {
		return fmt.Errorf("arca: wallet stream callback required")
	}
	realm, err := a.realmID(ctx)
	if err != nil {
		return err
	}
	last := ""
	if after > 0 {
		last = fmt.Sprint(after)
	}
	return a.readFundingWalletSSE(ctx, "/custody/v9/funding/wallet-snapshots/stream", url.Values{"realmId": {realm}}, last, func(event string, data []byte) error {
		var ev FundingV9WalletsEvent
		switch event {
		case ":connected", ":heartbeat":
			if !transport {
				return nil
			}
			ev.Transport = strings.TrimPrefix(event, ":")
		case "wallet":
			ev.Wallet = new(FundingV9WalletSnapshot)
			if err := json.Unmarshal(data, ev.Wallet); err != nil {
				return fmt.Errorf("arca: wallet frame: %w", err)
			}
			if ev.Wallet.Schema != FundingV9WalletSnapshotSchema {
				return fmt.Errorf("arca: wallet snapshot schema %d unsupported", ev.Wallet.Schema)
			}
		case "wallet_error":
			ev.WalletError = new(FundingV9WalletError)
			if err := json.Unmarshal(data, ev.WalletError); err != nil {
				return fmt.Errorf("arca: wallet error frame: %w", err)
			}
		case "caught_up":
			ev.CaughtUp = new(FundingV9WalletPosition)
			if err := json.Unmarshal(data, ev.CaughtUp); err != nil {
				return fmt.Errorf("arca: caught_up frame: %w", err)
			}
		case "position":
			var p struct {
				Sequence uint64 `json:"sequence"`
			}
			if err := json.Unmarshal(data, &p); err != nil || p.Sequence == 0 {
				return fmt.Errorf("arca: position frame %q", data)
			}
			ev.Position = p.Sequence
		default:
			return nil
		}
		if err := accept(ev); err != nil {
			return err
		}
		if ev.WalletError != nil {
			return fmt.Errorf("arca: wallet %s not composable: %s", ev.WalletError.BoundaryID, ev.WalletError.Error)
		}
		return nil
	})
}

// readFundingWalletSSE delivers each named frame to accept; the returned
// disconnect error carries the last frame id seen after accept succeeded.
func (a *Arca) readFundingWalletSSE(ctx context.Context, path string, q url.Values, lastEventID string, accept func(event string, data []byte) error) error {
	endpoint, err := a.client.buildURL(path, q)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.client.getCredential())
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	if a.client.headerHook != nil {
		for k, v := range a.client.headerHook() {
			req.Header.Set(k, v)
		}
	}
	client := *a.client.http
	client.Timeout = 0
	response, err := client.Do(req)
	if err != nil {
		return &FundingV9WalletStreamDisconnectedError{LastEventID: lastEventID, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return a.client.unwrap(response, nil)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("arca: wallet stream unavailable (content-type %q)", response.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	var event, id string
	var data []string
	frameBytes := 0
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				payload := []byte(strings.Join(data, "\n"))
				// Realm frames acknowledge their own sequence only. A mismatched
				// SSE id would otherwise skip uncommitted money on reconnection.
				if strings.HasSuffix(path, "/wallet-snapshots/stream") && event == "position" {
					var position struct {
						Sequence *uint64 `json:"sequence"`
					}
					if err := json.Unmarshal(payload, &position); err != nil || position.Sequence == nil || id != fmt.Sprint(*position.Sequence) {
						return &FundingV9WalletStreamDisconnectedError{LastEventID: lastEventID, Cause: fmt.Errorf("arca: wallet frame position mismatch")}
					}
				}
				if err := accept(event, payload); err != nil {
					return &FundingV9WalletStreamDisconnectedError{LastEventID: lastEventID, Cause: err}
				}
				if id != "" && (!strings.HasSuffix(path, "/wallet-snapshots/stream") || event == "position") {
					lastEventID = id
				}
			}
			event, id, data = "", "", nil
			frameBytes = 0
		case strings.HasPrefix(line, ":"):
			comment := strings.TrimSpace(strings.TrimPrefix(line, ":"))
			if comment == "connected" || comment == "heartbeat" {
				if err := accept(":"+comment, nil); err != nil {
					return &FundingV9WalletStreamDisconnectedError{LastEventID: lastEventID, Cause: err}
				}
			}
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			frameBytes += len(line)
			if frameBytes > 8<<20 {
				return &FundingV9WalletStreamDisconnectedError{LastEventID: lastEventID, Cause: fmt.Errorf("arca: wallet frame exceeds 8 MiB")}
			}
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	cause := scanner.Err()
	if cause == nil {
		cause = io.ErrUnexpectedEOF
	}
	return &FundingV9WalletStreamDisconnectedError{LastEventID: lastEventID, Cause: cause}
}

// FundingV9AccountFacts are durable fee and lifecycle facts, not balance adjustments.
type FundingV9AccountFacts struct {
	FundingV9DeferredActivationFee
	FactsSequence      uint64                                `json:"factsSequence"`
	SetupRetryable     bool                                  `json:"setupRetryable"`
	Activation         string                                `json:"activation"`
	ActivationEvidence *FundingV9ActivationEvidence          `json:"activationEvidence,omitempty"`
	FundingState       string                                `json:"fundingState"`
	Capabilities       map[string]FundingV9AccountCapability `json:"capabilities"`
}
type FundingV9AccountCapability struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}
type FundingV9DeferredActivationFee struct {
	DeferredActivationFeeRaw     string `json:"deferredActivationFeeRaw,omitempty"`
	DeferredActivationFeeState   string `json:"deferredActivationFeeState,omitempty"`
	DeferredActivationFeeTrigger string `json:"deferredActivationFeeTrigger,omitempty"`
}
type FundingV9ActivationEvidence struct {
	Source       string `json:"source"`
	OperationID  string `json:"operationId,omitempty"`
	NativeTxHash string `json:"nativeTxHash,omitempty"`
	ObservedAt   int64  `json:"observedAt,omitempty"`
}

// FundingV9FeePayment classifies an actual charge within the total source debit.
type FundingV9FeePayment struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	PayerArcaID  string   `json:"payerArcaId"`
	AccountIDs   []string `json:"accountIds"`
	OperationID  string   `json:"operationId"`
	AmountRaw    string   `json:"amountRaw"`
	NativeTxHash string   `json:"nativeTxHash"`
}

// GetFundingV9CatalogAccount retrieves labels, lifecycle and fee evidence without a chain read.
func (a *Arca) GetFundingV9CatalogAccount(ctx context.Context, arcaID string) (FundingV9CatalogAccount, error) {
	var out FundingV9CatalogAccount
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/accounts/"+url.PathEscape(arcaID)+"/catalog", url.Values{"realmId": {realm}}, &out)
	return out, err
}

// FundingV9WalletAccounting is the authoritative whole-wallet monetary envelope.
// Nil amounts remain unknown. Replace the envelope as one unit.
type FundingV9WalletAccounting struct {
	Revision           uint64  `json:"revision"`
	SourceMicro        *string `json:"sourceMicro"`
	CashMicro          string  `json:"cashMicro"`
	AvailableCashMicro string  `json:"availableCashMicro"`
	TradingMicro       string  `json:"tradingMicro"`
	MovingMicro        string  `json:"movingMicro"`
	TotalMicro         *string `json:"totalMicro"`
	Complete           bool    `json:"complete"`
	Current            bool    `json:"current"`
	CashBlock          uint64  `json:"cashBlock"`
	CashBlockHash      string  `json:"cashBlockHash"`
}

type FundingV9WalletCashAccount struct {
	ArcaID            string                 `json:"arcaId"`
	DefaultArcaID     string                 `json:"defaultArcaId"`
	ArcaPath          string                 `json:"arcaPath"`
	LocalID           string                 `json:"localId"`
	AccountStatus     string                 `json:"accountStatus"`
	PermissionVersion uint64                 `json:"permissionVersion"`
	RecoveryReadyAt   uint64                 `json:"recoveryReadyAt"`
	RecoveryCommitted bool                   `json:"recoveryCommitted"`
	DefaultMicro      string                 `json:"defaultMicro"`
	DefaultAvailMicro string                 `json:"defaultAvailableMicro"`
	UnallocatedMicro  string                 `json:"unallocatedMicro"`
	DepositAddresses  []CashV9DepositAddress `json:"depositAddresses"`
	Activity          []CashV9Operation      `json:"activity"`
}
