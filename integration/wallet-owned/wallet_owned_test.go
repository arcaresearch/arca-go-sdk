package walletowned

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	. "github.com/arcaresearch/arca-go-sdk/v2"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// The loopback harness is real platform routes, RealmGuard, Spanner and frozen
// chain-999 bytecode. Only mint/stop are fixture controls. No builder relay,
// gas key, direct chain call or REST fallback participates in this SDK client.
func TestOwnedWalletSDKRoundTrip(t *testing.T) {
	file := os.Getenv("V9_OWNED_SDK_HARNESS")
	if file == "" {
		t.Skip("set V9_OWNED_SDK_HARNESS to isolated harness metadata")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m["baseURL"], "http://127.0.0.1:") || m["executionOwner"] != "arca" {
		t.Fatal("isolated owned harness required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	control := func(path string, body any) {
		t.Helper()
		b, _ := json.Marshal(body)
		r, _ := http.NewRequestWithContext(ctx, "POST", m["baseURL"]+path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+m["token"])
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			t.Fatalf("fixture control %s: %s", path, resp.Status)
		}
	}
	defer control("/test/stop", nil)
	a, err := FromToken(m["token"], Config{BaseURL: m["baseURL"], Realm: m["realmId"]})
	if err != nil {
		t.Fatal(err)
	}
	ownerKey, err := crypto.HexToECDSA(strings.TrimPrefix(m["ownerPrivateKey"], "0x"))
	if err != nil {
		t.Fatal(err)
	}
	owner := crypto.PubkeyToAddress(ownerKey.PublicKey).Hex()
	approve := func(p WalletActionProposal) WalletActionProposal {
		t.Helper()
		requirements := append([]WalletActionRequirement(nil), p.Requirements...)
		for _, r := range requirements {
			var td apitypes.TypedData
			if err := json.Unmarshal([]byte(r.TypedDataJSON), &td); err != nil {
				t.Fatal(err)
			}
			hash, _, err := apitypes.TypedDataAndHash(td)
			if err != nil {
				t.Fatal(err)
			}
			sig, err := crypto.Sign(hash, ownerKey)
			if err != nil {
				t.Fatal(err)
			}
			sig[64] += 27
			p, err = a.RespondWalletRequirement(ctx, p.ProposalID, r.ID, WalletRequirementResponse{AttemptID: r.AttemptID, PayloadHash: r.PayloadHash, Decision: "approve", Signature: hexutil.Encode(sig), IdempotencyKey: r.ID})
			if err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	setup, err := a.ProposeWalletAction(ctx, WalletActionRequest{RequestID: "owned-sdk-create", RequestedAction: WalletRequestedAction{Kind: "set_up_account", CashPath: "/fixtures/cash", SourceAddress: owner}})
	if err != nil {
		t.Fatal(err)
	}
	setup = approve(setup)
	account, err := a.GetCashV9Account(ctx, "/fixtures/cash")
	if err != nil {
		t.Fatal(err)
	}
	done := errors.New("observed target")
	awaitOperation := func(id string) {
		t.Helper()
		err := a.StreamCashV9WalletAccount(ctx, account.BoundaryID, 0, func(w CashV9WalletAccount) error {
			for _, op := range w.Operations {
				if op.ID == id {
					if op.State == "failed" {
						return errors.New("operation failed: " + op.Reason)
					}
					if op.State == "completed" {
						return done
					}
				}
			}
			return nil
		})
		if !errors.Is(err, done) {
			t.Fatal(err)
		}
	}
	awaitOperation(setup.Requirements[0].OperationID)
	account, err = a.GetCashV9Account(ctx, "/fixtures/cash")
	if err != nil {
		t.Fatal(err)
	}
	link, err := a.CreateDepositLink(ctx, CreateDepositLinkOptions{RequestID: "owned-sdk-link", SourceObjectID: m["providerObjectId"], SourceWalletID: m["providerWalletId"], DestinationObjectID: account.DefaultArcaID, Adapter: m["adapter"], MaxDepositMicro: "3000000", MaxCashMicro: "10000000"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.ProposeWalletAction(ctx, WalletActionRequest{RequestID: "owned-sdk-enable", RequestedAction: WalletRequestedAction{Kind: "enable_automatic_deposits", PrivyObjectID: m["providerObjectId"], SourceWalletID: m["providerWalletId"], CashObjectID: account.DefaultArcaID, DepositLinkID: link.ID}})
	if err != nil {
		t.Fatal(err)
	}
	p = approve(p)
	awaitOperation(p.Requirements[0].OperationID)
	control("/test/mint", map[string]string{"Address": owner, "Amount": "4.970454"})
	frames := 0
	var final FundingV9WalletSnapshot
	err = a.StreamFundingV9Wallet(ctx, owner, "/fixtures", account.BoundaryID, "", func(w FundingV9WalletSnapshot) error {
		frames++
		x := w.Accounting
		if x == nil || !x.Complete || !x.Current {
			return nil
		}
		if *x.TotalMicro != "0" && *x.TotalMicro != "4970454" {
			return errors.New("money changed during internal deposit: " + *x.TotalMicro)
		}
		if x.CashMicro == "4970454" && *x.SourceMicro == "0" {
			final = w
			return done
		}
		return nil
	})
	if !errors.Is(err, done) {
		t.Fatal(err)
	}
	if final.Accounting.AvailableCashMicro != "4970454" {
		t.Fatal("cash unavailable after accounting commit")
	}
	reread, err := a.GetFundingV9WalletSnapshot(ctx, owner, "/fixtures", account.BoundaryID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Accounting == nil || !reread.Accounting.Complete || *reread.Accounting.TotalMicro != "4970454" {
		t.Fatal("typed SDK read lost committed money")
	}
	t.Logf("SDK -> real API -> owned runner -> frozen adapter -> Spanner -> SDK SSE: %d frames, total=%s cash=%s source=%s", frames, *final.Accounting.TotalMicro, final.Accounting.CashMicro, *final.Accounting.SourceMicro)
}
