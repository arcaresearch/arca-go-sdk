package arca

import (
	"encoding/json"
	"testing"
)

func pendingState(positions []SimPosition, pending ...AccountingPendingExecution) ExchangeState {
	return ExchangeState{Positions: positions, AccountingPending: pending}
}

func gold(size string, side PositionSide) SimPosition {
	return SimPosition{ID: "pos_gold", Market: "gllt:13", Side: side, Size: size, EntryPrice: "4353.6", Leverage: 10, MarginUsed: "500"}
}

// The invariant the field exists for: the frame emitted at execution time, the
// per-fill frames while the ledger folds the execution in, and the settled
// frame all compose to the same book.
func TestProjectedPositionsAreIdenticalAcrossTheAccountingWindow(t *testing.T) {
	close := AccountingPendingExecution{OperationID: "op", Market: "gllt:13", Side: "sell", ExecutedSize: "1.148517", ExecutionFinal: true}
	frames := []ExchangeState{
		pendingState([]SimPosition{gold("1.148517", Long)}, withAccounted(close, "0")),
		pendingState([]SimPosition{gold("0.088517", Long)}, withAccounted(close, "1.06")),
		pendingState([]SimPosition{gold("0.000517", Long)}, withAccounted(close, "1.148")),
		pendingState(nil),
	}
	for i, frame := range frames {
		if rows := frame.ProjectedPositions(); len(rows) != 0 {
			t.Fatalf("frame %d should compose flat, got %+v", i, rows)
		}
		if settled := frame.AccountingSettled(); settled != (i == len(frames)-1) {
			t.Fatalf("frame %d settled=%v", i, settled)
		}
	}
}

func withAccounted(e AccountingPendingExecution, accounted string) AccountingPendingExecution {
	e.AccountedSize = accounted
	switch accounted {
	case "0":
		e.UnaccountedSize = e.ExecutedSize
	case "1.06":
		e.UnaccountedSize = "0.088517"
	case "1.148":
		e.UnaccountedSize = "0.000517"
	}
	return e
}

func TestProjectedPositionsReduceIncreaseOpenAndReverse(t *testing.T) {
	reduce := pendingState([]SimPosition{gold("1.148517", Long)},
		AccountingPendingExecution{OperationID: "op", Market: "gllt:13", Side: "sell", ExecutedSize: "1.06", AccountedSize: "0", UnaccountedSize: "1.06", AveragePrice: "4300"})
	rows := reduce.ProjectedPositions()
	if len(rows) != 1 || rows[0].Size != "0.088517" || rows[0].Side != Long || rows[0].EntryPrice != "4353.6" || rows[0].Source != ProjectedFromExecution || rows[0].Ledger == nil {
		t.Fatalf("reduction keeps entry and identity: %+v", rows)
	}

	increase := pendingState([]SimPosition{{Market: "gllt:13", Side: Long, Size: "2", EntryPrice: "100"}},
		AccountingPendingExecution{OperationID: "op", Market: "gllt:13", Side: "buy", ExecutedSize: "1", AccountedSize: "0", UnaccountedSize: "1", AveragePrice: "200"})
	rows = increase.ProjectedPositions()
	if len(rows) != 1 || rows[0].Size != "3" || rows[0].EntryPrice != "133.333333333333333333" {
		t.Fatalf("increase blends entry by quantity: %+v", rows)
	}

	open := pendingState([]SimPosition{{Market: "gllt:14", Side: Short, Size: "3", EntryPrice: "40"}},
		AccountingPendingExecution{OperationID: "op", Market: "gllt:13", Side: "buy", ExecutedSize: "2", AccountedSize: "0", UnaccountedSize: "2", AveragePrice: "4353.4"})
	rows = open.ProjectedPositions()
	if len(rows) != 2 || rows[0].Market != "gllt:14" || rows[0].Source != ProjectedFromLedger ||
		rows[1].Market != "gllt:13" || rows[1].Side != Long || rows[1].Size != "2" || rows[1].EntryPrice != "4353.4" || rows[1].Ledger != nil {
		t.Fatalf("open appends after ledger rows at the execution price: %+v", rows)
	}

	reverse := pendingState([]SimPosition{{Market: "gllt:13", Side: Long, Size: "2", EntryPrice: "100"}},
		AccountingPendingExecution{OperationID: "op", Market: "gllt:13", Side: "sell", ExecutedSize: "5", AccountedSize: "0", UnaccountedSize: "5", AveragePrice: "90"})
	rows = reverse.ProjectedPositions()
	if len(rows) != 1 || rows[0].Side != Short || rows[0].Size != "3" || rows[0].EntryPrice != "90" {
		t.Fatalf("reversal is the execution's position: %+v", rows)
	}
}

func TestProjectedPositionsNeverInventOrEraseOnMalformedEntries(t *testing.T) {
	state := pendingState([]SimPosition{gold("1", Long)},
		AccountingPendingExecution{OperationID: "op", Market: "gllt:13", Side: "sell", UnaccountedSize: "not-a-number"},
		AccountingPendingExecution{OperationID: "op2", Market: "gllt:99", Side: "sideways", UnaccountedSize: "1"})
	rows := state.ProjectedPositions()
	if len(rows) != 1 || rows[0].Source != ProjectedFromLedger || rows[0].Size != "1" {
		t.Fatalf("malformed pending must fall back to the ledger row and add nothing: %+v", rows)
	}
	if state.AccountingSettled() {
		t.Fatal("malformed entries still mean the observation is not settled")
	}
}

func TestAccountingPendingDecodesFromTheWireAndIsAbsentWhenSettled(t *testing.T) {
	var state ExchangeState
	raw := `{"account":{"id":"a"},"marginSummary":{},"positions":[],"openOrders":[],"pendingIntents":[],
	  "accountingPending":[{"operationId":"op","orderId":"o","market":"gllt:13","side":"sell","executedSize":"1.148517","accountedSize":"0","unaccountedSize":"1.148517","executionFinal":true,"averagePrice":"4353.4"}]}`
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.AccountingPending) != 1 || state.AccountingPending[0].UnaccountedSize != "1.148517" || !state.AccountingPending[0].ExecutionFinal {
		t.Fatalf("%+v", state.AccountingPending)
	}
	var settled ExchangeState
	if err := json.Unmarshal([]byte(`{"account":{"id":"a"},"marginSummary":{},"positions":[],"openOrders":[],"pendingIntents":[]}`), &settled); err != nil {
		t.Fatal(err)
	}
	if !settled.AccountingSettled() {
		t.Fatal("no field means nothing pending")
	}
}
