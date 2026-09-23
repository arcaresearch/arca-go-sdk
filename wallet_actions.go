package arca

import (
	"context"
	"net/url"
)

// Wallet action proposals (documents/contracts/v9-wallet-intents.md). A
// product requests one action for a wallet owner; Arca answers with the exact
// authorizations it needs (requirements), their operations and any typed
// limitation. The owner answers each requirement attempt; an accepted answer
// is an ordinary Cash operation that Arca executes without the client.
// Supported kinds: set_up_account, send, enable_automatic_deposits (two
// requirements, route consent and token permit, admitted together),
// revoke_automatic_deposits and source_send. The last three execute through
// the realm's execution relay; a realm without one answers them with the
// limitation unsupported_action.

// WalletRequestedAction names the action and the objects it concerns.
// set_up_account takes CashPath and SourceAddress (the owner); send takes
// CashObjectID, AmountMicro and exactly one of Destination or
// DestinationCashObjectID. For set_up_account and send, PrivyObjectID and
// SourceWalletID are the product's own references, echoed back. The
// relay-executed kinds take PrivyObjectID, SourceWalletID (a verified wallet
// of that provider object, the Cash account's owner address) and
// CashObjectID; source_send adds AmountMicro and Destination, and a route
// activation or revocation may name the DepositLinkID it is about.
type WalletRequestedAction struct {
	Kind                    string `json:"kind"`
	PrivyObjectID           string `json:"privyObjectId,omitempty"`
	SourceWalletID          string `json:"sourceWalletId,omitempty"`
	CashObjectID            string `json:"cashObjectId,omitempty"`
	CashPath                string `json:"cashPath,omitempty"`
	SourceAddress           string `json:"sourceAddress,omitempty"`
	AmountMicro             string `json:"amountMicro,omitempty"`
	Destination             string `json:"destination,omitempty"`
	DestinationCashObjectID string `json:"destinationCashObjectId,omitempty"`
	DepositLinkID           string `json:"depositLinkId,omitempty"`
}

// WalletSchemaCapability is one signing schema, at one version, and the
// semantic variants of it the client can verify.
type WalletSchemaCapability struct {
	ID       string   `json:"id"`
	Version  int      `json:"version"`
	Variants []string `json:"variants"`
}

// WalletClientCapabilities, when sent, lets Arca answer with a typed
// limitation instead of a payload the client cannot verify.
type WalletClientCapabilities struct {
	Schemas []WalletSchemaCapability `json:"schemas"`
}

// WalletActionRequest requests one action. RequestID is the idempotency key:
// the identical request returns the same proposal (and, once an attempt
// expired unsigned, a replacement attempt); a changed one is refused with
// ACTION_REQUEST_CONFLICT.
type WalletActionRequest struct {
	RequestID       string                    `json:"requestId"`
	RequestedAction WalletRequestedAction     `json:"requestedAction"`
	Client          *WalletClientCapabilities `json:"client,omitempty"`
}

// WalletAccountRefScope is an on-chain account reference.
type WalletAccountRefScope struct {
	Kernel  string `json:"kernel"`
	Venue   string `json:"venue"`
	LocalID string `json:"localId"`
}

// WalletRequirementScope is the reviewed semantic scope of a requirement; the
// populated keys are fixed per schema variant. Amounts are micro-USDC.
type WalletRequirementScope struct {
	ChainID            int64                  `json:"chainId"`
	Owner              string                 `json:"owner,omitempty"`
	Source             string                 `json:"source,omitempty"`
	ExpectedOwner      string                 `json:"expectedOwner,omitempty"`
	Account            *WalletAccountRefScope `json:"account,omitempty"`
	DestinationAccount *WalletAccountRefScope `json:"destinationAccount,omitempty"`
	Recipient          string                 `json:"recipient,omitempty"`
	Spender            string                 `json:"spender,omitempty"`
	Adapter            string                 `json:"adapter,omitempty"`
	Router             string                 `json:"router,omitempty"`
	Token              string                 `json:"token,omitempty"`
	AmountMicro        string                 `json:"amountMicro,omitempty"`
	Allowance          string                 `json:"allowance,omitempty"`
	Limit              string                 `json:"limit,omitempty"`
	Unlimited          *bool                  `json:"unlimited,omitempty"`
	Expiry             *uint64                `json:"expiry,omitempty"`
	Revision           string                 `json:"revision,omitempty"`
	PermissionVersion  string                 `json:"permissionVersion,omitempty"`
	Nonce              string                 `json:"nonce,omitempty"`
	Ref                string                 `json:"ref,omitempty"`
	BoundDepositHash   string                 `json:"boundDepositHash,omitempty"`
	Deadline           int64                  `json:"deadline,omitempty"`
	Lifetime           string                 `json:"lifetime,omitempty"`
}

// WalletActionRequirement is one attempt at one authorization: exactly one
// signature over TypedDataJSON, whose EIP-712 digest is PayloadHash. State is
// not_ready, awaiting_approval, accepted, declined, expired or superseded.
// Verify the payload against your own shipped policy before signing.
type WalletActionRequirement struct {
	ID                     string                  `json:"id"`
	AttemptID              string                  `json:"attemptId"`
	State                  string                  `json:"state"`
	SchemaID               string                  `json:"schemaId"`
	SchemaVersion          int                     `json:"schemaVersion"`
	Variant                string                  `json:"variant"`
	PayloadHash            string                  `json:"payloadHash,omitempty"`
	TypedDataJSON          string                  `json:"typedDataJSON,omitempty"`
	IssuedAt               int64                   `json:"issuedAt,omitempty"`
	ExpiresAt              int64                   `json:"expiresAt,omitempty"`
	AuthorizationDependsOn []string                `json:"authorizationDependsOn"`
	ExecutionDependsOn     []string                `json:"executionDependsOn"`
	Scope                  *WalletRequirementScope `json:"scope,omitempty"`
	Decision               string                  `json:"decision,omitempty"`
	SupersededBy           string                  `json:"supersededBy,omitempty"`
	OperationID            string                  `json:"operationId,omitempty"`
}

// WalletOperationLink is an operation an accepted requirement produced, with
// its Cash status and the generic explorer state.
type WalletOperationLink struct {
	OperationID   string `json:"operationId"`
	Kind          string `json:"kind"`
	State         string `json:"state"`
	ExplorerState string `json:"explorerState"`
	TxHash        string `json:"txHash,omitempty"`
}

// WalletLimitation is a typed reason the action cannot be resolved. Branch on
// Code; Detail is operator text.
type WalletLimitation struct {
	Code          string   `json:"code"`
	Objects       []string `json:"objects"`
	RequirementID string   `json:"requirementId,omitempty"`
	Detail        string   `json:"detail,omitempty"`
}

// WalletActionProposal is Arca's answer to one requested action. Revision is
// the realm change-journal sequence at composition.
type WalletActionProposal struct {
	Schema          int                       `json:"schema"`
	ProposalID      string                    `json:"proposalId"`
	RequestID       string                    `json:"requestId"`
	RequestedAction WalletRequestedAction     `json:"requestedAction"`
	Revision        uint64                    `json:"revision"`
	Requirements    []WalletActionRequirement `json:"requirements"`
	Operations      []WalletOperationLink     `json:"operations"`
	Limitations     []WalletLimitation        `json:"limitations"`
	CreatedAt       string                    `json:"createdAt"`
	UpdatedAt       string                    `json:"updatedAt"`
}

// WalletRequirementResponse answers one attempt. Decision is "approve" (with
// the owner's 65-byte signature) or "decline". Repeating an answer already
// applied is safe; answering any attempt but the open one is refused with
// ACTION_ATTEMPT_STALE.
type WalletRequirementResponse struct {
	AttemptID      string `json:"attemptId"`
	PayloadHash    string `json:"payloadHash"`
	Decision       string `json:"decision"`
	Signature      string `json:"signature,omitempty"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// ProposeWalletAction requests one action. The answer lists the requirements
// to sign now, or the limitations that stop the action; it never starts work
// the owner has not accepted. Permissions are those of the Cash routes on the
// objects named (arca:CreateObject for setup, arca:CosignWithdraw on the Cash
// object and arca:ReceiveTo on a destination Cash object for sends).
func (a *Arca) ProposeWalletAction(ctx context.Context, req WalletActionRequest) (WalletActionProposal, error) {
	var out WalletActionProposal
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.postQuery(ctx, "/wallet/action-proposals", url.Values{"realmId": {rid}}, req, &out)
	return out, err
}

// GetWalletActionProposal reads one proposal. It never issues or replaces an
// attempt.
func (a *Arca) GetWalletActionProposal(ctx context.Context, proposalID string) (WalletActionProposal, error) {
	var out WalletActionProposal
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/wallet/action-proposals/"+url.PathEscape(proposalID), url.Values{"realmId": {rid}}, &out)
	return out, err
}

// RespondWalletRequirement answers one requirement attempt and returns the
// proposal as it stands after the answer. A refused answer carries its reason
// in the error's Details["limitation"]: attempt_stale (a *ConflictError,
// code ACTION_ATTEMPT_STALE or V9_PROPOSAL_CLOSED) or consent_expired (code
// V9_CONSENT_EXPIRED). A lost reply is retried with the same response; it
// never produces a second operation.
func (a *Arca) RespondWalletRequirement(ctx context.Context, proposalID, requirementID string, resp WalletRequirementResponse) (WalletActionProposal, error) {
	var out WalletActionProposal
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	path := "/wallet/action-proposals/" + url.PathEscape(proposalID) + "/requirements/" + url.PathEscape(requirementID) + "/responses"
	err = a.client.postQuery(ctx, path, url.Values{"realmId": {rid}}, resp, &out)
	return out, err
}
