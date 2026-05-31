package arca

import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
)

// custodySelectors are the pre-computed 4-byte function selectors for the
// ArcaCustodyPool contract (keccak256(signature)[:4]).
var custodySelectors = map[string]string{
	"lockBoundary(bytes32)":              "0x67f82a1d",
	"unlockBoundary(bytes32)":            "0x83a2f7cd",
	"withdraw(bytes32)":                  "0x8e19899e",
	"assignRecoveryKey(bytes32,address)": "0xf5b7e3a6",
	"setTimeLock(bytes32,uint256)":       "0x3e41d187",
	"cancelPendingExit(bytes32)":         "0x5e50fbe8",
	"recoveryWithdrawAccount(bytes32)":   "0xba7b2fbd",
}

func encodeCustodyCall(signature string, args []string) (string, error) {
	selector, ok := custodySelectors[signature]
	if !ok {
		return "", fmt.Errorf("arca: unknown custody function: %s", signature)
	}
	var b strings.Builder
	b.WriteString(selector)
	for _, arg := range args {
		enc, err := abiEncodeParam(arg)
		if err != nil {
			return "", err
		}
		b.WriteString(enc)
	}
	return b.String(), nil
}

func abiEncodeParam(value string) (string, error) {
	if strings.HasPrefix(value, "0x") && len(value) == 66 {
		return value[2:], nil
	}
	if strings.HasPrefix(value, "0x") && len(value) == 42 {
		return leftPad(strings.ToLower(value[2:]), 64), nil
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return "", fmt.Errorf("arca: cannot ABI-encode custody param %q (expected bytes32/address hex or a decimal integer)", value)
	}
	return leftPad(n.Text(16), 64), nil
}

func leftPad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

// GetCustodyStatus returns custody status for the realm.
func (a *Arca) GetCustodyStatus(ctx context.Context) (CustodyStatus, error) {
	var out CustodyStatus
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/status", url.Values{"realmId": {rid}}, &out)
	return out, err
}

// GetBoundary returns a single isolation boundary by id.
func (a *Arca) GetBoundary(ctx context.Context, boundaryID string) (CustodyBoundary, error) {
	var out CustodyBoundary
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/custody/boundaries/"+boundaryID, url.Values{"realmId": {rid}}, &out)
	return out, err
}

// ListBoundaries lists all isolation boundaries for the realm.
func (a *Arca) ListBoundaries(ctx context.Context) ([]CustodyBoundary, error) {
	rid, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	var out CustodyBoundaryListResponse
	err = a.client.get(ctx, "/custody/boundaries", url.Values{"realmId": {rid}}, &out)
	return out.Boundaries, err
}

// ListExchangeArcas lists exchange arcas, optionally filtered by boundary.
func (a *Arca) ListExchangeArcas(ctx context.Context, boundaryID string) ([]CustodyExchangeArca, error) {
	rid, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	params := url.Values{"realmId": {rid}}
	if boundaryID != "" {
		params.Set("boundaryId", boundaryID)
	}
	var out CustodyExchangeArcaListResponse
	err = a.client.get(ctx, "/custody/exchange-arcas", params, &out)
	return out.ExchangeArcas, err
}

// RegisterRecoveryKey registers a recovery key for an isolation boundary
// (operator-initiated; valid only when no recovery key is set).
func (a *Arca) RegisterRecoveryKey(ctx context.Context, opts RegisterRecoveryKeyOptions) (RegisterRecoveryKeyResponse, error) {
	var out RegisterRecoveryKeyResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.post(ctx, "/custody/recovery-key", map[string]any{
		"realmId":       rid,
		"boundaryId":    opts.BoundaryID,
		"walletAddress": opts.WalletAddress,
	}, &out)
	return out, err
}

// PrepareLockBoundary prepares an unsigned lockBoundary transaction.
func PrepareLockBoundary(contractAddress string, chainID int64, boundaryID string) (PreparedCustodyTransaction, error) {
	return prepareCustody(contractAddress, chainID, "lockBoundary(bytes32)", []string{boundaryID})
}

// PrepareUnlockBoundary prepares an unsigned unlockBoundary transaction.
func PrepareUnlockBoundary(contractAddress string, chainID int64, boundaryID string) (PreparedCustodyTransaction, error) {
	return prepareCustody(contractAddress, chainID, "unlockBoundary(bytes32)", []string{boundaryID})
}

// PrepareWithdraw prepares an unsigned withdraw transaction (drains the boundary
// to the recovery key holder; requires the boundary locked by the caller).
func PrepareWithdraw(contractAddress string, chainID int64, boundaryID string) (PreparedCustodyTransaction, error) {
	return prepareCustody(contractAddress, chainID, "withdraw(bytes32)", []string{boundaryID})
}

// PrepareAssignRecoveryKey prepares an unsigned assignRecoveryKey transaction
// for changing an existing key (current holder only).
func PrepareAssignRecoveryKey(contractAddress string, chainID int64, boundaryID, newRecoveryKey string) (PreparedCustodyTransaction, error) {
	return prepareCustody(contractAddress, chainID, "assignRecoveryKey(bytes32,address)", []string{boundaryID, newRecoveryKey})
}

// PrepareSetTimeLock prepares an unsigned setTimeLock transaction.
func PrepareSetTimeLock(contractAddress string, chainID int64, boundaryID string, durationSeconds int64) (PreparedCustodyTransaction, error) {
	return prepareCustody(contractAddress, chainID, "setTimeLock(bytes32,uint256)", []string{boundaryID, strconv.FormatInt(durationSeconds, 10)})
}

// PrepareCancelPendingExit prepares an unsigned cancelPendingExit transaction.
func PrepareCancelPendingExit(contractAddress string, chainID int64, boundaryID string) (PreparedCustodyTransaction, error) {
	return prepareCustody(contractAddress, chainID, "cancelPendingExit(bytes32)", []string{boundaryID})
}

// PrepareRecoveryWithdrawAccount prepares an unsigned recoveryWithdrawAccount
// transaction (sweeps an exchange arca's balance to its boundary).
func PrepareRecoveryWithdrawAccount(contractAddress string, chainID int64, arcaID string) (PreparedCustodyTransaction, error) {
	return prepareCustody(contractAddress, chainID, "recoveryWithdrawAccount(bytes32)", []string{arcaID})
}

func prepareCustody(contractAddress string, chainID int64, signature string, args []string) (PreparedCustodyTransaction, error) {
	data, err := encodeCustodyCall(signature, args)
	if err != nil {
		return PreparedCustodyTransaction{}, err
	}
	return PreparedCustodyTransaction{To: contractAddress, Data: data, ChainID: chainID, Value: "0"}, nil
}
