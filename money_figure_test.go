package arca

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Money figures (documents/contracts/sdk-money-figure.md): every figure is
// Arca's figure verbatim, legs render on the ends the frame lists, a
// prediction only adds legs, a venue frame never outranks a booked
// settlement. The fixture sequence is shared with the TypeScript, Swift and
// Kotlin SDKs.

const walletMoneyFixtures = "../../backend/libs/arca-go/fundingv9/testdata/wallet-money"

func loadWalletMoneyFixture(t *testing.T, name string) FundingV9WalletSnapshot {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(walletMoneyFixtures, name+".json"))
	if err != nil {
		t.Skipf("fixtures not present: %v", err)
	}
	var snapshot FundingV9WalletSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return snapshot
}

func TestWalletMoneyFiguresAreArcasFiguresVerbatim(t *testing.T) {
	entries, err := os.ReadDir(walletMoneyFixtures)
	if err != nil {
		t.Skipf("fixtures not present: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(names)
	if len(names) < 19 {
		t.Fatalf("fixture set shrank: %d", len(names))
	}
	for _, name := range names {
		snapshot := loadWalletMoneyFixture(t, name)
		if snapshot.Schema != FundingV9WalletSnapshotSchema {
			t.Errorf("%s: schema %d", name, snapshot.Schema)
		}
		money := ComposeWalletMoney(snapshot, ComposeWalletMoneyOptions{})
		if *money.Total.SettledMicro != snapshot.Totals.TotalMicro {
			t.Errorf("%s: total %s != totals.totalMicro %s", name, *money.Total.SettledMicro, snapshot.Totals.TotalMicro)
		}
		if *money.Cash.SettledMicro != snapshot.Cash.Balances.ConfirmedMicro || *money.Cash.AvailableMicro != snapshot.Cash.Balances.AvailableMicro {
			t.Errorf("%s: Cash is not confirmed/available verbatim", name)
		}
		for _, account := range snapshot.Accounts {
			figure := money.Account(account.ArcaID)
			if figure == nil {
				t.Fatalf("%s: %s missing", name, account.ArcaID)
			}
			if account.Balances == nil {
				if figure.SettledMicro != nil {
					t.Errorf("%s: %s should have no figure", name, account.ArcaID)
				}
			} else if *figure.SettledMicro != account.Balances.TotalMicro || *figure.AvailableMicro != account.Balances.WithdrawableMicro {
				t.Errorf("%s: %s is not balances verbatim", name, account.ArcaID)
			}
		}
		if len(money.Diagnostics) != 0 {
			t.Errorf("%s: diagnostics %+v", name, money.Diagnostics)
		}
		if money.Complete != snapshot.Totals.Complete {
			t.Errorf("%s: complete", name)
		}
	}
}

func TestWalletMoneyEveryDollarVisibleOnceAtEveryStage(t *testing.T) {
	rest := ComposeWalletMoney(loadWalletMoneyFixture(t, "00-rest"), ComposeWalletMoneyOptions{})
	reserved := ComposeWalletMoney(loadWalletMoneyFixture(t, "01-move-reserved"), ComposeWalletMoneyOptions{Previous: &rest})
	if *reserved.Cash.AvailableMicro != "23500000" || *reserved.Cash.SettledMicro != "28510000" {
		t.Fatalf("reserved Cash: %+v", reserved.Cash)
	}
	if len(reserved.Cash.Departing) != 1 || reserved.Cash.Departing[0].Stage != MoneyLegReserved || reserved.Cash.Departing[0].Counterparty != (MoneyCounterparty{Kind: MoneyCounterpartyAccount, ArcaID: "obj_hl1", Name: "Hyperliquid 1"}) {
		t.Fatalf("reserved Cash legs: %+v", reserved.Cash.Departing)
	}
	hl1 := reserved.Account("obj_hl1")
	if len(hl1.Arriving) != 1 || hl1.Arriving[0].OperationID != reserved.Cash.Departing[0].OperationID || hl1.Arriving[0].Counterparty.Kind != MoneyCounterpartyCash {
		t.Fatalf("reserved destination legs: %+v", hl1.Arriving)
	}
	if len(reserved.Total.Arriving)+len(reserved.Total.Departing) != 0 || *reserved.Total.SettledMicro != "31010000" {
		t.Fatalf("an internal move never reaches the total: %+v", reserved.Total)
	}
	if reserved.Cash.Change == nil || reserved.Cash.Change.Cause.OperationID != "opr_move1" {
		t.Fatalf("reserved Cash change: %+v", reserved.Cash.Change)
	}

	inTransit := ComposeWalletMoney(loadWalletMoneyFixture(t, "02-move-in-transit"), ComposeWalletMoneyOptions{Previous: &reserved})
	if len(inTransit.Cash.Departing) != 0 {
		t.Fatalf("a leg in transit is no longer the source's: %+v", inTransit.Cash.Departing)
	}
	if got := inTransit.Account("obj_hl1"); len(got.Arriving) != 1 || got.Arriving[0].Stage != MoneyLegInTransit || got.ArrivingMicro() != "5000000" {
		t.Fatalf("in-transit destination: %+v", got)
	}
	if *inTransit.Total.SettledMicro != "31000000" || inTransit.Cash.Change.DeltaMicro != "-5010000" {
		t.Fatalf("in-transit total/change: %s %+v", *inTransit.Total.SettledMicro, inTransit.Cash.Change)
	}

	completed := ComposeWalletMoney(loadWalletMoneyFixture(t, "03-move-completed"), ComposeWalletMoneyOptions{Previous: &inTransit})
	if got := completed.Account("obj_hl1"); *got.SettledMicro != "6500000" || got.Source != "arca_settlement" || len(got.Arriving) != 0 || got.Change == nil || got.Change.DeltaMicro != "5000000" || got.Change.Cause.OperationID != "opr_move1" {
		t.Fatalf("completed destination: %+v", got)
	}

	fill := ComposeWalletMoney(loadWalletMoneyFixture(t, "04-venue-fill"), ComposeWalletMoneyOptions{Previous: &completed})
	if got := fill.Account("obj_hl1"); got.Change == nil || !got.Change.Cause.Venue || got.Change.DeltaMicro != "-80000" {
		t.Fatalf("venue change: %+v", got.Change)
	}
	if fill.Cash.Change != nil {
		t.Fatalf("Cash did not move: %+v", fill.Cash.Change)
	}

	failed := ComposeWalletMoney(loadWalletMoneyFixture(t, "05-move-failed-before-pnr"), ComposeWalletMoneyOptions{Previous: &reserved})
	if *failed.Cash.AvailableMicro != "28510000" || len(failed.Cash.Departing) != 0 {
		t.Fatalf("failed: %+v", failed.Cash)
	}
	if c := failed.Cash.Change; c == nil || c.OperationStatus != "failed" || c.Retired == nil || c.Retired.Direction != MoneyOutgoing {
		t.Fatalf("a released reservation is not money sent: %+v", c)
	}
	compensated := ComposeWalletMoney(loadWalletMoneyFixture(t, "06-move-compensated"), ComposeWalletMoneyOptions{Previous: &inTransit})
	if *compensated.Cash.SettledMicro != "28500000" || compensated.Cash.Change == nil || compensated.Cash.Change.DeltaMicro != "5000000" {
		t.Fatalf("compensated: %+v", compensated.Cash)
	}
}

func TestWalletMoneyBoundaryCrossingLegsDepositsAndSends(t *testing.T) {
	expected := ComposeWalletMoney(loadWalletMoneyFixture(t, "07-deposit-expected"), ComposeWalletMoneyOptions{})
	if got := expected.Account("obj_hl2"); got.ExpectedMicro() != "2000000" || got.ArrivingMicro() != "0" || got.InMotion() {
		t.Fatalf("expected is listed, never counted: %+v", got)
	}
	observed := ComposeWalletMoney(loadWalletMoneyFixture(t, "08-deposit-in-transit"), ComposeWalletMoneyOptions{Previous: &expected})
	if len(observed.Total.Arriving) != 1 || observed.Total.Arriving[0].AmountMicro != "2000000" || *observed.Total.SettledMicro != "32920000" {
		t.Fatalf("observed deposit: %+v", observed.Total)
	}
	landed := ComposeWalletMoney(loadWalletMoneyFixture(t, "09-deposit-landed"), ComposeWalletMoneyOptions{Previous: &observed})
	if got := landed.Account("obj_hl2"); *got.SettledMicro != "3000000" || len(got.Arriving) != 0 || got.Change == nil || got.Change.Cause.OperationID != "opr_dep1" {
		t.Fatalf("landed deposit: %+v", got)
	}
	if c := landed.Total.Change; c == nil || c.DeltaMicro != "0" || c.Retired == nil || c.Retired.AmountMicro != "2000000" || c.Retired.Direction != MoneyIncoming || c.OperationStatus != "succeeded" {
		t.Fatalf("the total names the leg that landed: %+v", c)
	}
	sendReserved := ComposeWalletMoney(loadWalletMoneyFixture(t, "10-send-reserved"), ComposeWalletMoneyOptions{})
	sendInTransit := ComposeWalletMoney(loadWalletMoneyFixture(t, "11-send-in-transit"), ComposeWalletMoneyOptions{Previous: &sendReserved})
	if got := sendInTransit.Account("obj_hl1"); len(got.Departing) != 1 || got.Departing[0].Stage != MoneyLegInTransit || got.Departing[0].Counterparty.Kind != MoneyCounterpartyExternal {
		t.Fatalf("a send keeps its source line until paid: %+v", got.Departing)
	}
	if *sendInTransit.Total.SettledMicro != "32920000" || len(sendInTransit.Total.Departing) != 1 {
		t.Fatalf("send in transit total: %+v", sendInTransit.Total)
	}
	paid := ComposeWalletMoney(loadWalletMoneyFixture(t, "12-send-paid"), ComposeWalletMoneyOptions{Previous: &sendInTransit})
	if *paid.Total.SettledMicro != "31920000" || len(paid.Total.Departing) != 0 {
		t.Fatalf("send paid: %+v", paid.Total)
	}
	cash := ComposeWalletMoney(loadWalletMoneyFixture(t, "14-cash-send-and-pending-in"), ComposeWalletMoneyOptions{})
	if len(cash.Cash.Departing) != 1 || cash.Cash.Departing[0].Stage != MoneyLegReserved || cash.Cash.Departing[0].AmountMicro != "2000000" {
		t.Fatalf("Cash send: %+v", cash.Cash.Departing)
	}
	if len(cash.Cash.Arriving) != 1 || cash.Cash.Arriving[0].AmountMicro != "750000" || len(cash.Total.Arriving) != 1 || len(cash.Total.Departing) != 1 {
		t.Fatalf("Cash pending in: %+v %+v", cash.Cash.Arriving, cash.Total)
	}
}

func TestWalletMoneyCashDepositIsExpectedThenPendingInOnce(t *testing.T) {
	expected := ComposeWalletMoney(loadWalletMoneyFixture(t, "17-cash-deposit-expected"), ComposeWalletMoneyOptions{})
	if len(expected.Cash.Arriving) != 1 || expected.Cash.Arriving[0].Stage != MoneyLegExpected || expected.Cash.Arriving[0].Counterparty != (MoneyCounterparty{Kind: MoneyCounterpartyExternal}) {
		t.Fatalf("an announced Cash deposit is listed on Cash: %+v", expected.Cash.Arriving)
	}
	if expected.Cash.ExpectedMicro() != "3000000" || expected.Cash.ArrivingMicro() != "0" || expected.Cash.InMotion() || *expected.Total.SettledMicro != "30920000" {
		t.Fatalf("expected is listed, never counted: %+v", expected.Cash)
	}
	onChain := ComposeWalletMoney(loadWalletMoneyFixture(t, "18-cash-deposit-pending-in"), ComposeWalletMoneyOptions{Previous: &expected})
	if len(onChain.Cash.Arriving) != 1 || onChain.Cash.Arriving[0].Stage != MoneyLegInTransit || onChain.Cash.ArrivingMicro() != "3000000" || onChain.Cash.ExpectedMicro() != "0" {
		t.Fatalf("the deposit on chain is Cash's pending figure alone: %+v", onChain.Cash.Arriving)
	}
	if len(onChain.Total.Arriving) != 1 || onChain.Total.Arriving[0].AmountMicro != "3000000" || *onChain.Total.SettledMicro != "30920000" {
		t.Fatalf("total: %+v", onChain.Total)
	}
}

func TestWalletMoneyLegRendersOnTheVisibleEndAloneAndPartialCatalogKeepsTheTotal(t *testing.T) {
	money := ComposeWalletMoney(loadWalletMoneyFixture(t, "13-off-page-counterparty"), ComposeWalletMoneyOptions{})
	if got := money.Account("obj_hl1"); len(got.Departing) != 1 || got.Departing[0].Counterparty != (MoneyCounterparty{Kind: MoneyCounterpartyUnknown, ArcaID: "obj_hl9"}) {
		t.Fatalf("off-page leg: %+v", got.Departing)
	}
	if money.Account("obj_hl9") != nil || len(money.Total.Departing) != 0 {
		t.Fatalf("the other end is not in this frame")
	}
	unobserved := loadWalletMoneyFixture(t, "16-account-unobserved")
	partial := ComposeWalletMoney(unobserved, ComposeWalletMoneyOptions{})
	if partial.Account("obj_hl2").SettledMicro != nil || partial.Complete || *partial.Total.SettledMicro != unobserved.Totals.TotalMicro {
		t.Fatalf("partial catalog: %+v", partial.Total)
	}
	pre := ComposeWalletMoney(loadWalletMoneyFixture(t, "15-pre-cash-wallet"), ComposeWalletMoneyOptions{})
	if *pre.Cash.SettledMicro != "0" || len(pre.Accounts) != 0 || *pre.Total.SettledMicro != "0" {
		t.Fatalf("pre-Cash wallet: %+v", pre.Total)
	}
}

func TestWalletMoneyPredictionAddsLegsOnlyAndRetires(t *testing.T) {
	rest := loadWalletMoneyFixture(t, "00-rest")
	prediction := MoneyPrediction{RequestID: "req-move1", Kind: "cash_to_trading", AmountMicro: "5000000",
		From: FundingV9WalletEndpoint{Kind: "cash"}, To: FundingV9WalletEndpoint{Kind: "hyperliquid", ArcaID: "obj_hl1"}}
	money := ComposeWalletMoney(rest, ComposeWalletMoneyOptions{Predictions: []MoneyPrediction{prediction}})
	if *money.Cash.AvailableMicro != "28510000" || len(money.Cash.Departing) != 1 || !money.Cash.Departing[0].Predicted || len(money.Total.Departing) != 0 {
		t.Fatalf("prediction: %+v", money.Cash)
	}
	if got := money.Account("obj_hl1"); len(got.Arriving) != 1 || !got.Arriving[0].Predicted {
		t.Fatalf("predicted destination: %+v", got.Arriving)
	}
	framed := ComposeWalletMoney(loadWalletMoneyFixture(t, "01-move-reserved"), ComposeWalletMoneyOptions{Predictions: []MoneyPrediction{prediction}})
	if len(framed.Cash.Departing) != 1 || framed.Cash.Departing[0].Predicted {
		t.Fatalf("the framed leg replaces the prediction: %+v", framed.Cash.Departing)
	}
}

func TestWalletMoneyBookedSettlementOutranksStaleVenueFrame(t *testing.T) {
	booked := loadWalletMoneyFixture(t, "03-move-completed")
	stale := MoneyRevaluation{ArcaID: "obj_hl1", EquityMicro: "1500000", ObservedAt: time.Date(2026, 10, 17, 18, 0, 5, 0, time.UTC)}
	money := ComposeWalletMoney(booked, ComposeWalletMoneyOptions{Revaluations: map[string]MoneyRevaluation{"obj_hl1": stale}})
	if got := money.Account("obj_hl1"); got.RevaluedMicro != "" || *got.SettledMicro != "6500000" {
		t.Fatalf("stale venue frame used: %+v", got)
	}
	if len(money.Diagnostics) != 1 || money.Diagnostics[0].Reason != "venue frame older than the figure" {
		t.Fatalf("diagnostics: %+v", money.Diagnostics)
	}
	fresh := MoneyRevaluation{ArcaID: "obj_hl1", EquityMicro: "6420000", ObservedAt: time.Date(2026, 10, 17, 18, 0, 11, 0, time.UTC)}
	completed := ComposeWalletMoney(booked, ComposeWalletMoneyOptions{})
	ticking := ComposeWalletMoney(booked, ComposeWalletMoneyOptions{Previous: &completed, Revaluations: map[string]MoneyRevaluation{"obj_hl1": fresh}})
	if got := ticking.Account("obj_hl1"); got.RevaluedMicro != "6420000" || *got.SettledMicro != "6500000" {
		t.Fatalf("revaluation: %+v", got)
	}
	folded := ComposeWalletMoney(loadWalletMoneyFixture(t, "04-venue-fill"), ComposeWalletMoneyOptions{Previous: &ticking, Revaluations: map[string]MoneyRevaluation{"obj_hl1": fresh}})
	if got := folded.Account("obj_hl1"); got.RevaluedMicro != "" || *got.SettledMicro != "6420000" || got.Change == nil || got.Change.DeltaMicro != "-80000" {
		t.Fatalf("one fill, one change: %+v", got)
	}
	held := ComposeWalletMoney(loadWalletMoneyFixture(t, "02-move-in-transit"), ComposeWalletMoneyOptions{Revaluations: map[string]MoneyRevaluation{"obj_hl1": {ArcaID: "obj_hl1", EquityMicro: "6500000", ObservedAt: time.Date(2026, 10, 17, 18, 0, 30, 0, time.UTC)}}})
	if held.Account("obj_hl1").RevaluedMicro != "" {
		t.Fatalf("a settlement toward the account is pending")
	}
}

func TestWalletMoneyMismatchIsDiagnosedNeverGatedAndOlderFramesAreIgnored(t *testing.T) {
	snapshot := loadWalletMoneyFixture(t, "00-rest")
	snapshot.Totals.TotalMicro = "99000000"
	money := ComposeWalletMoney(snapshot, ComposeWalletMoneyOptions{})
	if *money.Total.SettledMicro != "99000000" || len(money.Diagnostics) != 1 || money.Diagnostics[0].Kind != "totals_mismatch" || money.Diagnostics[0].ComposedMicro != "31010000" {
		t.Fatalf("mismatch: %+v %+v", money.Total, money.Diagnostics)
	}
	rest := loadWalletMoneyFixture(t, "00-rest")
	reserved := loadWalletMoneyFixture(t, "01-move-reserved")
	if !WalletSnapshotSupersedes(reserved, &rest) || WalletSnapshotSupersedes(rest, &reserved) {
		t.Fatalf("watermark ordering")
	}
	other := reserved
	other.OwnerAddress = "0xother"
	if WalletSnapshotSupersedes(other, &rest) {
		t.Fatalf("a foreign frame never supersedes")
	}
}

func TestMicroHelpersAreExactIntegerArithmetic(t *testing.T) {
	if MicroSum([]string{"1", "2", "3"}) != "6" {
		t.Fatal("sum")
	}
	if d, ok := MicroDifference("5000000", "5010000"); !ok || d != "-10000" {
		t.Fatal("difference")
	}
	if _, ok := MicroDifference("x", "1"); ok {
		t.Fatal("difference accepted garbage")
	}
	for raw, want := range map[string]string{"6.49": "6490000", "-0.5": "-500000", "1500": "1500000000", "0.1234567": "123456"} {
		if got, err := MicroFromDecimal(raw); err != nil || got != want {
			t.Fatalf("%s: %s %v", raw, got, err)
		}
	}
	if _, err := MicroFromDecimal("abc"); err == nil {
		t.Fatal("decimal accepted garbage")
	}
}
