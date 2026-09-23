package arca

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestCashV9WalletAccountDecodesFixtures decodes every Wallet Account fixture
// the platform generates from its composition function
// (backend/libs/arca-go/cashv9/testdata/wallet-account/) and proves the SDK
// type round-trips it without losing or renaming a field. The fixtures are
// the contract every client decoder is held to; a failure here means the SDK
// type lags the wire shape.
// The open action-proposal attempt a phone lists on the account: only the
// awaiting one, with its deadline, never an answered or lapsed attempt.
func TestCashV9WalletAccountRequirementFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "backend", "libs", "arca-go", "cashv9", "testdata", "wallet-account", "requirement-awaiting_approval.json"))
	if err != nil {
		t.Skipf("fixture not present: %v", err)
	}
	var account CashV9WalletAccount
	if err := json.Unmarshal(raw, &account); err != nil {
		t.Fatal(err)
	}
	if len(account.Requirements) != 1 {
		t.Fatalf("requirements: %+v", account.Requirements)
	}
	r := account.Requirements[0]
	if r.ProposalID == "" || r.RequirementID != "req_send" || r.AttemptID != "att_1" || r.ActionKind != "send" || r.SchemaID != "arca.kernel.v9.Move" || r.State != "awaiting_approval" || r.ExpiresAt == 0 {
		t.Fatalf("requirement: %+v", r)
	}
}

func TestCashV9WalletAccountDecodesFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "backend", "libs", "arca-go", "cashv9", "testdata", "wallet-account")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("fixtures not present at %s: %v", dir, err)
	}
	decoded := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") || e.Name() == "vocabulary.json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var account CashV9WalletAccount
		if err := json.Unmarshal(raw, &account); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		if account.Schema != 1 || account.WalletState == "" || account.BoundaryID == "" {
			t.Fatalf("%s: decoded shape incomplete: %+v", e.Name(), account)
		}
		again, err := json.Marshal(account)
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(again, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s: SDK type does not round-trip the fixture\nfixture: %s\nsdk:     %s", e.Name(), raw, again)
		}
		decoded++
	}
	if decoded == 0 {
		t.Fatal("no fixtures decoded")
	}
	// The vocabulary the fixtures were generated under: every state a client
	// switches on must be one of these.
	raw, err := os.ReadFile(filepath.Join(dir, "vocabulary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vocabulary struct {
		WalletStates    []string `json:"walletStates"`
		OperationStates []string `json:"operationStates"`
		Reasons         []string `json:"reasons"`
	}
	if err := json.Unmarshal(raw, &vocabulary); err != nil {
		t.Fatal(err)
	}
	if len(vocabulary.WalletStates) != 5 || len(vocabulary.OperationStates) != 6 || len(vocabulary.Reasons) != 7 {
		t.Fatalf("vocabulary changed size: %+v", vocabulary)
	}
}
