package arca

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Money figures (documents/contracts/sdk-money-figure.md): one shape for
// every balance a product shows, projected from one wallet snapshot. A figure
// is Arca's figure verbatim plus the legs of the operations moving money into
// or out of it; nothing here is summed into a figure on the client.
// Conservation (every dollar in exactly one place, counted once) is Arca's
// guarantee at the snapshot's journal position; this file renders it and
// never re-derives it. Mirrors the TypeScript, Swift and Kotlin SDKs.

// MoneyDirection is which end of a leg a figure is, from its point of view.
type MoneyDirection string

const (
	// MoneyIncoming is money on its way into the figure.
	MoneyIncoming MoneyDirection = "incoming"
	// MoneyOutgoing is money committed to leave, or already gone from, the figure.
	MoneyOutgoing MoneyDirection = "outgoing"
)

// MoneyLegStage is where a leg is: reserved is still inside its source's
// figure, in_transit has left its source and not reached its destination,
// expected is a provider-confirmed deposit not on chain yet (listed, never
// counted).
type MoneyLegStage string

const (
	MoneyLegReserved  MoneyLegStage = "reserved"
	MoneyLegInTransit MoneyLegStage = "in_transit"
	MoneyLegExpected  MoneyLegStage = "expected"
)

// MoneyCounterpartyKind classifies the other end of a leg from the frame alone.
type MoneyCounterpartyKind string

const (
	MoneyCounterpartyCash     MoneyCounterpartyKind = "cash"
	MoneyCounterpartyAccount  MoneyCounterpartyKind = "account"
	MoneyCounterpartyExternal MoneyCounterpartyKind = "external"
	// MoneyCounterpartyUnknown is a counterparty the frame does not list (an
	// account past the catalog page or scan, an archived one, another owner's
	// wallet); the leg still renders on the end that is visible.
	MoneyCounterpartyUnknown MoneyCounterpartyKind = "unknown"
)

// MoneyCounterparty is the other end of a leg.
type MoneyCounterparty struct {
	Kind MoneyCounterpartyKind `json:"kind"`
	// ArcaID is set for account and, when known, unknown counterparties.
	ArcaID string `json:"arcaId,omitempty"`
	// Name is the account's label when the frame lists it.
	Name string `json:"name,omitempty"`
	// Address is the outside address when observed.
	Address string `json:"address,omitempty"`
}

// CrossesWallet reports whether the money crosses the wallet boundary.
func (c MoneyCounterparty) CrossesWallet() bool { return c.Kind == MoneyCounterpartyExternal }

func (c MoneyCounterparty) listed() bool {
	return c.Kind == MoneyCounterpartyCash || c.Kind == MoneyCounterpartyAccount
}

// MoneyLeg is one operation's money moving into or out of a figure: a
// moving[] entry of the snapshot projected onto one end, or a Predicted leg
// from an accepted operation the stream has not framed yet.
type MoneyLeg struct {
	OperationID  string            `json:"operationId"`
	RequestID    string            `json:"requestId,omitempty"`
	Kind         string            `json:"kind"`
	Direction    MoneyDirection    `json:"direction"`
	Stage        MoneyLegStage     `json:"stage"`
	AmountMicro  string            `json:"amountMicro"`
	Counterparty MoneyCounterparty `json:"counterparty"`
	Predicted    bool              `json:"predicted"`
	StartedAt    string            `json:"startedAt,omitempty"`
}

// ID is the leg's identity on one end: one operation, one direction.
func (l MoneyLeg) ID() string { return l.OperationID + ":" + string(l.Direction) }

// MoneyChangeCause names why a figure last moved.
type MoneyChangeCause struct {
	// Venue is set when the venue moved the figure (a fill, a funding
	// payment, a liquidation or its mark) with no operation touching it.
	Venue bool `json:"venue,omitempty"`
	// OperationID and OperationKind name the operation that touched the
	// figure and changed stage, leg or status.
	OperationID   string `json:"operationId,omitempty"`
	OperationKind string `json:"operationKind,omitempty"`
}

// MoneyChange is why a figure last moved, named only when the frame makes
// it attributable. DeltaMicro is a caption, never a figure.
type MoneyChange struct {
	Cause      MoneyChangeCause `json:"cause"`
	DeltaMicro string           `json:"deltaMicro"`
	Sequence   uint64           `json:"sequence"`
}

// MoneyFigureKind is which snapshot figure a MoneyFigure renders.
type MoneyFigureKind string

const (
	MoneyFigureTotal   MoneyFigureKind = "total"
	MoneyFigureCash    MoneyFigureKind = "cash"
	MoneyFigureAccount MoneyFigureKind = "account"
)

// MoneyFigure is a balance as a product shows it: Arca's figure, what of it
// is spendable, and the operations moving money into or out of it.
// SettledMicro is nil only when the frame has no figure.
type MoneyFigure struct {
	Kind   MoneyFigureKind `json:"kind"`
	ArcaID string          `json:"arcaId,omitempty"`
	// SettledMicro is Arca's figure verbatim: Cash confirmedMicro, an
	// account's balances.totalMicro, the wallet's totals.totalMicro.
	SettledMicro *string `json:"settledMicro"`
	// AvailableMicro is Cash availableMicro or an account's
	// withdrawableMicro; nil for the total.
	AvailableMicro *string    `json:"availableMicro"`
	Arriving       []MoneyLeg `json:"arriving"`
	Departing      []MoneyLeg `json:"departing"`
	AsOf           string     `json:"asOf,omitempty"`
	Current        bool       `json:"current"`
	Source         string     `json:"source"`
	// RevaluedMicro is the venue frame's equity used as is when newer than
	// AsOf and no settlement toward the account is pending; empty otherwise.
	RevaluedMicro string       `json:"revaluedMicro,omitempty"`
	Change        *MoneyChange `json:"change,omitempty"`
}

// InMotion reports whether any leg is reserved or in transit.
func (f MoneyFigure) InMotion() bool {
	for _, leg := range append(append([]MoneyLeg{}, f.Arriving...), f.Departing...) {
		if leg.Stage != MoneyLegExpected {
			return true
		}
	}
	return false
}

// ArrivingMicro is the caption total of the arriving legs in motion; an
// annotation for one line of text, never a figure.
func (f MoneyFigure) ArrivingMicro() string {
	var values []string
	for _, leg := range f.Arriving {
		if leg.Stage != MoneyLegExpected {
			values = append(values, leg.AmountMicro)
		}
	}
	return MicroSum(values)
}

// DepartingMicro is the caption total of the departing legs.
func (f MoneyFigure) DepartingMicro() string {
	var values []string
	for _, leg := range f.Departing {
		values = append(values, leg.AmountMicro)
	}
	return MicroSum(values)
}

// ExpectedMicro is the caption total of expected deposits.
func (f MoneyFigure) ExpectedMicro() string {
	var values []string
	for _, leg := range f.Arriving {
		if leg.Stage == MoneyLegExpected {
			values = append(values, leg.AmountMicro)
		}
	}
	return MicroSum(values)
}

// MoneyDiagnostic is an engineering-only observation about a frame. Never a
// gate and never a product signal: the frame is published as Arca composed it.
type MoneyDiagnostic struct {
	// Kind is totals_mismatch, prediction_expired or venue_frame_ignored.
	Kind          string `json:"kind"`
	ExpectedMicro string `json:"expectedMicro,omitempty"`
	ComposedMicro string `json:"composedMicro,omitempty"`
	RequestID     string `json:"requestId,omitempty"`
	ArcaID        string `json:"arcaId,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// MoneyPrediction is an accepted operation the stream has not framed yet:
// only what the server returned. It never changes a figure.
type MoneyPrediction struct {
	RequestID   string
	OperationID string
	Kind        string
	AmountMicro string
	From        FundingV9WalletEndpoint
	To          FundingV9WalletEndpoint
}

// MoneyRevaluation is a venue frame for one account from an exchange-state
// watch.
type MoneyRevaluation struct {
	ArcaID      string
	EquityMicro string
	ObservedAt  time.Time
}

// WalletMoney is every money figure of one wallet at one journal position.
// One frame, one value: a product renders all of it or none.
type WalletMoney struct {
	Total    MoneyFigure
	Cash     MoneyFigure
	Accounts map[string]MoneyFigure
	// AccountOrder is the arcaIds in the snapshot's order.
	AccountOrder []string
	Operations   []FundingV9WalletOperation
	Watermark    FundingV9WalletWatermark
	Complete     bool
	Current      bool
	Diagnostics  []MoneyDiagnostic
	Snapshot     FundingV9WalletSnapshot
}

// Account is the figure of one account, nil when the snapshot does not list it.
func (m WalletMoney) Account(arcaID string) *MoneyFigure {
	if f, ok := m.Accounts[arcaID]; ok {
		return &f
	}
	return nil
}

// ComposeWalletMoneyOptions tunes ComposeWalletMoney.
type ComposeWalletMoneyOptions struct {
	// Previous is the last frame of the same wallet, so Change can be attributed.
	Previous *WalletMoney
	// Predictions are accepted operations the stream has not framed yet;
	// those it has are dropped, so a leg is never shown twice.
	Predictions []MoneyPrediction
	// Revaluations are venue frames per account arcaId.
	Revaluations map[string]MoneyRevaluation
}

// WalletSnapshotSupersedes reports whether next still describes the wallet
// previous does (same owner, root and realm) and is at least as new. Older
// or foreign frames are ignored.
func WalletSnapshotSupersedes(next FundingV9WalletSnapshot, previous *FundingV9WalletSnapshot) bool {
	if previous == nil {
		return true
	}
	if !strings.EqualFold(previous.OwnerAddress, next.OwnerAddress) || previous.ArcaPath != next.ArcaPath || previous.RealmID != next.RealmID {
		return false
	}
	return next.Watermark.Sequence >= previous.Watermark.Sequence
}

// ComposeWalletMoney projects a snapshot onto money figures.
func ComposeWalletMoney(snapshot FundingV9WalletSnapshot, opts ComposeWalletMoneyOptions) WalletMoney {
	var diagnostics []MoneyDiagnostic
	legs := moneyLegsByEnd(snapshot)
	sequence := snapshot.Watermark.Sequence
	cashLegs := moneyCashOwnLegs(snapshot)

	settled := snapshot.Cash.Balances.ConfirmedMicro
	available := snapshot.Cash.Balances.AvailableMicro
	cash := MoneyFigure{Kind: MoneyFigureCash, SettledMicro: &settled, AvailableMicro: &available,
		Arriving: append(append([]MoneyLeg{}, legs.cash.arriving...), cashLegs.arriving...),
		Departing: append(append([]MoneyLeg{}, legs.cash.departing...), cashLegs.departing...),
		AsOf: snapshot.Cash.Balances.AsOf.Time, Current: snapshot.Cash.WalletState != "unavailable", Source: "cash"}

	accounts := map[string]MoneyFigure{}
	var order []string
	for _, account := range snapshot.Accounts {
		ends := legs.accounts[account.ArcaID]
		figure := MoneyFigure{Kind: MoneyFigureAccount, ArcaID: account.ArcaID, Arriving: ends.arriving, Departing: ends.departing,
			AsOf: account.AsOf, Current: account.Current, Source: "unavailable"}
		if figure.Arriving == nil {
			figure.Arriving = []MoneyLeg{}
		}
		if figure.Departing == nil {
			figure.Departing = []MoneyLeg{}
		}
		if b := account.Balances; b != nil {
			total, withdrawable := b.TotalMicro, b.WithdrawableMicro
			figure.SettledMicro, figure.AvailableMicro, figure.Source = &total, &withdrawable, b.Source
		}
		if revaluation, ok := opts.Revaluations[account.ArcaID]; ok {
			if micro, reason := moneyRevalue(revaluation, account, figure); reason == "" {
				figure.RevaluedMicro = micro
			} else {
				diagnostics = append(diagnostics, MoneyDiagnostic{Kind: "venue_frame_ignored", ArcaID: account.ArcaID, Reason: reason})
			}
		}
		accounts[account.ArcaID] = figure
		order = append(order, account.ArcaID)
	}

	totalMicro := snapshot.Totals.TotalMicro
	total := MoneyFigure{Kind: MoneyFigureTotal, SettledMicro: &totalMicro,
		Arriving: append(append([]MoneyLeg{}, legs.total.arriving...), cashLegs.arriving...),
		Departing: append(append([]MoneyLeg{}, legs.total.departing...), cashLegs.departing...),
		AsOf: snapshot.ComposedAt, Current: snapshot.Totals.Current, Source: "totals"}

	framed := map[string]bool{}
	for _, op := range snapshot.Operations {
		framed[op.OperationID] = true
		if op.RequestID != "" {
			framed[op.RequestID] = true
		}
	}
	for _, prediction := range opts.Predictions {
		if framed[prediction.RequestID] || (prediction.OperationID != "" && framed[prediction.OperationID]) {
			continue
		}
		predicted := moneyPredictedLegs(prediction, snapshot)
		cash.Arriving = append(cash.Arriving, predicted.cash.arriving...)
		cash.Departing = append(cash.Departing, predicted.cash.departing...)
		for arcaID, ends := range predicted.accounts {
			figure, ok := accounts[arcaID]
			if !ok {
				continue
			}
			figure.Arriving = append(figure.Arriving, ends.arriving...)
			figure.Departing = append(figure.Departing, ends.departing...)
			accounts[arcaID] = figure
		}
		total.Arriving = append(total.Arriving, predicted.total.arriving...)
		total.Departing = append(total.Departing, predicted.total.departing...)
	}

	if snapshot.Totals.Complete {
		if composed, ok := moneyComposedTotal(snapshot); ok && composed != snapshot.Totals.TotalMicro {
			diagnostics = append(diagnostics, MoneyDiagnostic{Kind: "totals_mismatch", ExpectedMicro: snapshot.Totals.TotalMicro, ComposedMicro: composed})
		}
	}

	if previous := opts.Previous; previous != nil && previous.Snapshot.Watermark.Sequence < sequence {
		cash.Change = moneyChange(&previous.Cash, cash, snapshot, previous, sequence, nil)
		for _, arcaID := range order {
			figure := accounts[arcaID]
			var before *MoneyFigure
			if b, ok := previous.Accounts[arcaID]; ok {
				before = &b
			}
			var account *FundingV9WalletTradingAccount
			for i := range snapshot.Accounts {
				if snapshot.Accounts[i].ArcaID == arcaID {
					account = &snapshot.Accounts[i]
				}
			}
			figure.Change = moneyChange(before, figure, snapshot, previous, sequence, account)
			accounts[arcaID] = figure
		}
		total.Change = moneyChange(&previous.Total, total, snapshot, previous, sequence, nil)
	}

	return WalletMoney{Total: total, Cash: cash, Accounts: accounts, AccountOrder: order, Operations: snapshot.Operations,
		Watermark: snapshot.Watermark, Complete: snapshot.Totals.Complete, Current: snapshot.Totals.Current, Diagnostics: diagnostics, Snapshot: snapshot}
}

type moneyLegEnds struct {
	arriving  []MoneyLeg
	departing []MoneyLeg
}

type moneyLegsByEndT struct {
	cash     moneyLegEnds
	accounts map[string]moneyLegEnds
	total    moneyLegEnds
}

func moneyCounterparty(endpoint FundingV9WalletEndpoint, snapshot FundingV9WalletSnapshot) MoneyCounterparty {
	if endpoint.Kind == "cash" || (endpoint.BoundaryID != "" && snapshot.BoundaryID != "" && endpoint.BoundaryID == snapshot.BoundaryID) {
		return MoneyCounterparty{Kind: MoneyCounterpartyCash}
	}
	if endpoint.ArcaID != "" {
		for _, account := range snapshot.Accounts {
			if account.ArcaID == endpoint.ArcaID {
				return MoneyCounterparty{Kind: MoneyCounterpartyAccount, ArcaID: endpoint.ArcaID, Name: account.Labels["name"]}
			}
		}
		return MoneyCounterparty{Kind: MoneyCounterpartyUnknown, ArcaID: endpoint.ArcaID}
	}
	if endpoint.Kind == "external" || endpoint.Address != "" || (endpoint.Kind != "" && endpoint.Kind != "cash") {
		return MoneyCounterparty{Kind: MoneyCounterpartyExternal, Address: endpoint.Address}
	}
	return MoneyCounterparty{Kind: MoneyCounterpartyUnknown}
}

func moneyPlace(leg MoneyLeg, party MoneyCounterparty, legs *moneyLegsByEndT) {
	switch party.Kind {
	case MoneyCounterpartyCash:
		if leg.Direction == MoneyOutgoing {
			legs.cash.departing = append(legs.cash.departing, leg)
		} else {
			legs.cash.arriving = append(legs.cash.arriving, leg)
		}
	case MoneyCounterpartyAccount:
		ends := legs.accounts[party.ArcaID]
		if leg.Direction == MoneyOutgoing {
			ends.departing = append(ends.departing, leg)
		} else {
			ends.arriving = append(ends.arriving, leg)
		}
		legs.accounts[party.ArcaID] = ends
	}
}

// moneyLegsByEnd projects every moving[] entry onto the ends the snapshot
// lists. A reserved leg shows at both ends (the money is still inside its
// source); an in_transit leg shows at its destination when the frame lists
// it, else at its source (a send on its way out of the wallet); an expected
// leg shows only at its destination.
func moneyLegsByEnd(snapshot FundingV9WalletSnapshot) moneyLegsByEndT {
	out := moneyLegsByEndT{accounts: map[string]moneyLegEnds{}}
	operations := map[string]FundingV9WalletOperation{}
	for _, op := range snapshot.Operations {
		operations[op.OperationID] = op
	}
	for _, entry := range snapshot.Moving {
		stage := MoneyLegStage(entry.Leg)
		if stage != MoneyLegReserved && stage != MoneyLegInTransit && stage != MoneyLegExpected {
			continue
		}
		op, known := operations[entry.OperationID]
		fromParty := moneyCounterparty(entry.From, snapshot)
		toParty := moneyCounterparty(entry.To, snapshot)
		departing := MoneyLeg{OperationID: entry.OperationID, Kind: entry.Kind, Direction: MoneyOutgoing, Stage: stage, AmountMicro: entry.AmountMicro, Counterparty: toParty}
		if known {
			departing.RequestID, departing.StartedAt = op.RequestID, op.CreatedAt
		}
		arriving := departing
		arriving.Direction, arriving.Counterparty = MoneyIncoming, fromParty
		switch stage {
		case MoneyLegReserved:
			moneyPlace(departing, fromParty, &out)
			moneyPlace(arriving, toParty, &out)
		case MoneyLegInTransit:
			if toParty.listed() {
				moneyPlace(arriving, toParty, &out)
			} else {
				moneyPlace(departing, fromParty, &out)
			}
		case MoneyLegExpected:
			moneyPlace(arriving, toParty, &out)
		}
		if toParty.CrossesWallet() && !fromParty.CrossesWallet() {
			out.total.departing = append(out.total.departing, departing)
		}
		if fromParty.CrossesWallet() && !toParty.CrossesWallet() {
			out.total.arriving = append(out.total.arriving, arriving)
		}
	}
	return out
}

// moneyCashOwnLegs is the Cash read model's own in-flight money: a send in
// cash.operations is reserved while sending and in_transit while confirming;
// pendingInMicro is one arriving leg from outside the wallet.
func moneyCashOwnLegs(snapshot FundingV9WalletSnapshot) moneyLegEnds {
	var ends moneyLegEnds
	for _, op := range snapshot.Cash.Operations {
		if op.Kind != "send" {
			continue
		}
		var stage MoneyLegStage
		switch op.State {
		case "sending":
			stage = MoneyLegReserved
		case "confirming":
			stage = MoneyLegInTransit
		default:
			continue
		}
		ends.departing = append(ends.departing, MoneyLeg{OperationID: op.ID, RequestID: op.RequestID, Kind: "send", Direction: MoneyOutgoing, Stage: stage,
			AmountMicro: op.AmountMicro, Counterparty: MoneyCounterparty{Kind: MoneyCounterpartyExternal, Address: op.Destination}, StartedAt: op.StartedAt})
	}
	if pending, ok := microParse(snapshot.Cash.Balances.PendingInMicro); ok && pending.Sign() > 0 {
		ends.arriving = append(ends.arriving, MoneyLeg{OperationID: "cash:pending-in", Kind: "deposit", Direction: MoneyIncoming, Stage: MoneyLegInTransit,
			AmountMicro: snapshot.Cash.Balances.PendingInMicro, Counterparty: MoneyCounterparty{Kind: MoneyCounterpartyExternal}})
	}
	return ends
}

func moneyPredictedLegs(prediction MoneyPrediction, snapshot FundingV9WalletSnapshot) moneyLegsByEndT {
	out := moneyLegsByEndT{accounts: map[string]moneyLegEnds{}}
	fromParty := moneyCounterparty(prediction.From, snapshot)
	toParty := moneyCounterparty(prediction.To, snapshot)
	id := prediction.OperationID
	if id == "" {
		id = prediction.RequestID
	}
	departing := MoneyLeg{OperationID: id, RequestID: prediction.RequestID, Kind: prediction.Kind, Direction: MoneyOutgoing, Stage: MoneyLegReserved,
		AmountMicro: prediction.AmountMicro, Counterparty: toParty, Predicted: true}
	arriving := departing
	arriving.Direction, arriving.Counterparty = MoneyIncoming, fromParty
	moneyPlace(departing, fromParty, &out)
	moneyPlace(arriving, toParty, &out)
	if toParty.CrossesWallet() && !fromParty.CrossesWallet() {
		out.total.departing = append(out.total.departing, departing)
	}
	if fromParty.CrossesWallet() && !toParty.CrossesWallet() {
		out.total.arriving = append(out.total.arriving, arriving)
	}
	return out
}

// moneyRevalue uses a venue frame as is only when it is newer than the
// account's figure and the frame lists no settlement toward the account.
func moneyRevalue(revaluation MoneyRevaluation, account FundingV9WalletTradingAccount, figure MoneyFigure) (micro, reason string) {
	if account.HeldBy != "" {
		return "", "account held by " + account.HeldBy
	}
	for _, leg := range figure.Arriving {
		if leg.Stage != MoneyLegExpected {
			return "", "settlement pending toward the account"
		}
	}
	figureTime, err := time.Parse(time.RFC3339Nano, account.AsOf)
	if account.AsOf == "" || err != nil {
		if account.Balances == nil {
			return "", "no figure to revalue"
		}
		return revaluation.EquityMicro, ""
	}
	if revaluation.ObservedAt.Before(figureTime) {
		return "", "venue frame older than the figure"
	}
	return revaluation.EquityMicro, ""
}

// moneyComposedTotal is cash.confirmed + moving(in transit) + sum(accounts),
// for the diagnostic only.
func moneyComposedTotal(snapshot FundingV9WalletSnapshot) (string, bool) {
	terms := []string{snapshot.Cash.Balances.ConfirmedMicro}
	for _, account := range snapshot.Accounts {
		if account.Lifecycle == "archived" {
			continue
		}
		if account.Balances == nil {
			return "", false
		}
		terms = append(terms, account.Balances.TotalMicro)
	}
	for _, entry := range snapshot.Moving {
		if entry.Leg == string(MoneyLegInTransit) {
			terms = append(terms, entry.AmountMicro)
		}
	}
	return MicroSum(terms), true
}

func moneyChange(previous *MoneyFigure, figure MoneyFigure, snapshot FundingV9WalletSnapshot, previousMoney *WalletMoney, sequence uint64, account *FundingV9WalletTradingAccount) *MoneyChange {
	var delta string
	hasDelta := false
	if previous != nil && previous.SettledMicro != nil && figure.SettledMicro != nil {
		if d, ok := MicroDifference(*figure.SettledMicro, *previous.SettledMicro); ok {
			delta, hasDelta = d, true
		}
	}
	cause := moneyOperationThatMoved(figure, previous, snapshot, previousMoney)
	if !hasDelta {
		if cause == nil {
			return nil
		}
		return &MoneyChange{Cause: *cause, DeltaMicro: "0", Sequence: sequence}
	}
	if cause != nil {
		return &MoneyChange{Cause: *cause, DeltaMicro: delta, Sequence: sequence}
	}
	if delta == "0" {
		return nil
	}
	if account != nil && account.Balances != nil && account.Balances.Source == "hyperliquid_ws" {
		return &MoneyChange{Cause: MoneyChangeCause{Venue: true}, DeltaMicro: delta, Sequence: sequence}
	}
	return nil
}

func moneyTouches(op FundingV9WalletOperation, figure MoneyFigure, snapshot FundingV9WalletSnapshot) bool {
	ends := []MoneyCounterparty{moneyCounterparty(op.From, snapshot), moneyCounterparty(op.To, snapshot)}
	for _, end := range ends {
		switch figure.Kind {
		case MoneyFigureCash:
			if end.Kind == MoneyCounterpartyCash {
				return true
			}
		case MoneyFigureAccount:
			if end.Kind == MoneyCounterpartyAccount && end.ArcaID == figure.ArcaID {
				return true
			}
		case MoneyFigureTotal:
			if end.CrossesWallet() {
				return true
			}
		}
	}
	return false
}

func moneyOperationThatMoved(figure MoneyFigure, previous *MoneyFigure, snapshot FundingV9WalletSnapshot, previousMoney *WalletMoney) *MoneyChangeCause {
	nowLegs := map[string]MoneyLeg{}
	for _, leg := range append(append([]MoneyLeg{}, figure.Arriving...), figure.Departing...) {
		nowLegs[leg.ID()] = leg
	}
	beforeLegs := map[string]MoneyLeg{}
	if previous != nil {
		for _, leg := range append(append([]MoneyLeg{}, previous.Arriving...), previous.Departing...) {
			beforeLegs[leg.ID()] = leg
		}
	}
	type touched struct{ id, kind string }
	var hits []touched
	// Deterministic order: current legs in figure order, then retired legs.
	for _, leg := range append(append([]MoneyLeg{}, figure.Arriving...), figure.Departing...) {
		if before, ok := beforeLegs[leg.ID()]; !ok || before.Stage != leg.Stage {
			hits = append(hits, touched{leg.OperationID, leg.Kind})
		}
	}
	if previous != nil {
		for _, leg := range append(append([]MoneyLeg{}, previous.Arriving...), previous.Departing...) {
			if _, ok := nowLegs[leg.ID()]; !ok {
				hits = append(hits, touched{leg.OperationID, leg.Kind})
			}
		}
	}
	before := map[string]FundingV9WalletOperation{}
	for _, op := range previousMoney.Operations {
		before[op.OperationID] = op
	}
	for _, op := range snapshot.Operations {
		if !moneyTouches(op, figure, snapshot) {
			continue
		}
		old, ok := before[op.OperationID]
		if !ok || old.Stage != op.Stage || old.Status != op.Status || old.CreditBooked != op.CreditBooked || old.DebitBooked != op.DebitBooked {
			hits = append(hits, touched{op.OperationID, op.Kind})
		}
	}
	if figure.Kind == MoneyFigureCash || figure.Kind == MoneyFigureTotal {
		beforeCash := map[string]CashV9WalletOperation{}
		for _, op := range previousMoney.Snapshot.Cash.Operations {
			beforeCash[op.ID] = op
		}
		for _, op := range snapshot.Cash.Operations {
			if old, ok := beforeCash[op.ID]; (!ok || old.State != op.State) && (op.Kind == "send" || figure.Kind == MoneyFigureCash) {
				hits = append(hits, touched{op.ID, op.Kind})
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return &MoneyChangeCause{OperationID: hits[0].id, OperationKind: hits[0].kind}
}

// Integer arithmetic on micro-USDC strings for captions and diagnostics. A
// figure is never produced here; only differences and caption totals.

func microParse(raw string) (*big.Int, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	n, ok := new(big.Int).SetString(trimmed, 10)
	return n, ok
}

// MicroSum adds micro-USDC integer strings, ignoring values that are not integers.
func MicroSum(values []string) string {
	total := new(big.Int)
	for _, value := range values {
		if n, ok := microParse(value); ok {
			total.Add(total, n)
		}
	}
	return total.String()
}

// MicroDifference is a - b as a signed integer string; false when either is
// not an integer.
func MicroDifference(a, b string) (string, bool) {
	x, okA := microParse(a)
	y, okB := microParse(b)
	if !okA || !okB {
		return "", false
	}
	return new(big.Int).Sub(x, y).String(), true
}

// MicroFromDecimal converts a decimal USD string ("6.49") to micro-USDC,
// truncated toward zero past six decimals.
func MicroFromDecimal(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("arca: empty decimal")
	}
	negative := false
	if strings.HasPrefix(text, "-") {
		negative, text = true, text[1:]
	} else if strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	whole, fraction := text, ""
	if i := strings.IndexByte(text, '.'); i >= 0 {
		whole, fraction = text[:i], text[i+1:]
	}
	if whole == "" && fraction == "" {
		return "", fmt.Errorf("arca: %q is not a decimal", raw)
	}
	for _, r := range whole + fraction {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("arca: %q is not a decimal", raw)
		}
	}
	if whole == "" {
		whole = "0"
	}
	padded := (fraction + "000000")[:6]
	w, _ := new(big.Int).SetString(whole, 10)
	f, _ := new(big.Int).SetString(padded, 10)
	micro := new(big.Int).Mul(w, big.NewInt(1_000_000))
	micro.Add(micro, f)
	if negative {
		micro.Neg(micro)
	}
	return micro.String(), nil
}
