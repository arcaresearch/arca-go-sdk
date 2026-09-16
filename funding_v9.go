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
	CreatedAt           string                        `json:"createdAt"`
}
type FundingV9CoreSnapshot struct {
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
