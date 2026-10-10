package arca

import (
	"context"
	"net/url"
)

type FundingV9TradingAuthorizationRequest struct {
	BuilderFeeTenthsBps uint64 `json:"builderFeeTenthsBps,omitempty"`
	RealmID             string `json:"realmId"`
	RequestID           string `json:"requestId"`
	ArcaID              string `json:"arcaId"`
}

type FundingV9TradingAgent struct {
	WalletID string `json:"walletId"`
	Address  string `json:"address"`
}

type FundingV9TradingAuthorization struct {
	Type       string                `json:"type"`
	Builder    string                `json:"builder"`
	MaxFeeRate uint64                `json:"maxFeeRate"`
	Agent      FundingV9TradingAgent `json:"agent"`
	Nonce      string                `json:"nonce"`
}

type FundingV9TradingStatus struct {
	BuilderAddress         string           `json:"builderAddress"`
	BuilderFeeMaxTenthsBps int64            `json:"builderFeeMaxTenthsBps"`
	BuilderFeeCurrent      bool             `json:"builderFeeCurrent"`
	Account                FundingV9Account `json:"account"`
	State                  string           `json:"state"`
	Allowed                bool             `json:"allowed"`
	OperationID            string           `json:"operationId,omitempty"`
	AgentAddress           string           `json:"agentAddress"`
}

// ProposeFundingV9Trading creates a bounded SetAgent or BuilderFee owner-consent proposal.
// Validate its chain, kernel, account, agent and deadline before signing.
func (a *Arca) ProposeFundingV9Trading(ctx context.Context, r FundingV9TradingAuthorizationRequest) (FundingV9Proposal, error) {
	var out FundingV9Proposal
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	r.RealmID = realm
	err = a.client.post(ctx, "/custody/v9/funding/trading/propose", r, &out)
	return out, err
}

// SubmitFundingV9Trading submits an exact consent once. On an uncertain reply,
// read GetFundingV9Proposal / StreamFundingV9Operation; do not create a new grant.
func (a *Arca) SubmitFundingV9Trading(ctx context.Context, r FundingV9SubmitRequest) (FundingV9Operation, error) {
	return a.submitFundingV9(ctx, "trading", r)
}

func (a *Arca) GetFundingV9TradingStatus(ctx context.Context, id string) (FundingV9TradingStatus, error) {
	var out FundingV9TradingStatus
	realm, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/v9/funding/accounts/"+url.PathEscape(id)+"/trading", url.Values{"realmId": {realm}}, &out)
	return out, err
}
