package arca

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// accountedFixture wires an OrderHandle whose placement is already terminal
// (ExecutionReceipt resolves from one order read) so the tests exercise only
// the accounting wait.
type accountedFixture struct {
	reads        atomic.Int32
	listCalls    atomic.Int32
	nudged       []string
	mu           sync.Mutex
	fillHandlers []func(RealmEvent)
}

func (f *accountedFixture) deps(getOrder func(read int32) SimOrderWithFills, listFills func(call int32) FillListResponse) orderHandleDeps {
	return orderHandleDeps{
		getOrder: func(_ context.Context, account, id string) (SimOrderWithFills, error) {
			if account != "obj_exchange" || id != "ord_abc" {
				return SimOrderWithFills{}, errors.New("wrong identity " + account + "/" + id)
			}
			return getOrder(f.reads.Add(1)), nil
		},
		onFillEvent: func(handler func(RealmEvent)) func() {
			f.mu.Lock()
			f.fillHandlers = append(f.fillHandlers, handler)
			f.mu.Unlock()
			return func() {}
		},
		listFills: func(context.Context, string) (FillListResponse, error) {
			if listFills == nil {
				return FillListResponse{}, nil
			}
			return listFills(f.listCalls.Add(1)), nil
		},
		exchangeStateChanged: func(objectID string) {
			f.mu.Lock()
			f.nudged = append(f.nudged, objectID)
			f.mu.Unlock()
		},
	}
}

func (f *accountedFixture) recorded(ev RealmEvent) {
	f.mu.Lock()
	handlers := append([]func(RealmEvent){}, f.fillHandlers...)
	f.mu.Unlock()
	for _, h := range handlers {
		h(ev)
	}
}

func (f *accountedFixture) nudges() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.nudged...)
}

func boolPtr(b bool) *bool { return &b }

func terminalOrder(filled string, complete *bool) SimOrderWithFills {
	price := "2000"
	return SimOrderWithFills{
		FillsComplete: complete,
		Order:         SimOrder{ID: "ord_abc", Status: OrderFilled, Size: filled, FilledSize: filled, AvgFillPrice: &price},
	}
}

func TestAccountedResolvesWhenTheReadCarriesFillsComplete(t *testing.T) {
	f := &accountedFixture{}
	h := newSettledOrderHandle("/order", "ord_abc", f.deps(func(int32) SimOrderWithFills { return terminalOrder("1", boolPtr(true)) }, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	detail, err := h.Accounted(ctx)
	if err != nil || detail.FillsComplete == nil || !*detail.FillsComplete {
		t.Fatalf("%+v %v", detail, err)
	}
	// One read for the receipt, one accounting check.
	if got := f.reads.Load(); got != 2 {
		t.Fatalf("reads = %d, want 2", got)
	}
	if got := f.nudges(); len(got) != 1 || got[0] != "obj_exchange" {
		t.Fatalf("account watch nudge = %v, want [obj_exchange]", got)
	}
}

func TestAccountedWaitsForTheRecordedFillPushThenReReads(t *testing.T) {
	f := &accountedFixture{}
	// Execution known (terminal) but the ledger has not caught up: the state
	// Home read when it refreshed at receipt time. Read 1 is the receipt.
	h := newSettledOrderHandle("/order", "ord_abc", f.deps(func(read int32) SimOrderWithFills {
		return terminalOrder("1", boolPtr(read >= 3))
	}, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	go func() {
		time.Sleep(100 * time.Millisecond)
		// A recorded fill for another order is ignored; ours triggers the re-read.
		f.recorded(RealmEvent{Type: EventFillRecorded, EntityID: "obj_exchange", Fill: &SimFill{ID: "pl_x", OrderID: "ord_other", Size: "1"}})
		time.Sleep(50 * time.Millisecond)
		f.recorded(RealmEvent{Type: EventFillRecorded, EntityID: "obj_exchange", Fill: &SimFill{ID: "pl_1", OrderID: "ord_abc", Size: "1"}})
	}()
	detail, err := h.Accounted(ctx)
	if err != nil || detail.FillsComplete == nil || !*detail.FillsComplete {
		t.Fatalf("%+v %v", detail, err)
	}
	if elapsed := time.Since(started); elapsed > 450*time.Millisecond {
		t.Fatalf("the push, not the 500ms backoff, should have driven the re-read (took %v)", elapsed)
	}
	if got := f.reads.Load(); got != 3 {
		t.Fatalf("reads = %d, want 3 (receipt, first check, after the matching recorded fill)", got)
	}
}

func TestAccountedFallsBackToABoundedBackoffReadWhenNoPushArrives(t *testing.T) {
	f := &accountedFixture{}
	h := newSettledOrderHandle("/order", "ord_abc", f.deps(func(read int32) SimOrderWithFills {
		return terminalOrder("1", boolPtr(read >= 4))
	}, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if _, err := h.Accounted(ctx); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	// Reads: receipt, immediate check, +500ms, +1000ms.
	if elapsed < 1300*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("backoff schedule off: %v", elapsed)
	}
	if got := f.reads.Load(); got != 4 {
		t.Fatalf("reads = %d, want 4", got)
	}
}

func TestAccountedVenueReadWithoutFillsCompleteRequiresRecordedFillsToCoverExecutedSize(t *testing.T) {
	f := &accountedFixture{}
	h := newSettledOrderHandle("/order", "ord_abc", f.deps(
		func(int32) SimOrderWithFills { return terminalOrder("0.3", nil) },
		func(call int32) FillListResponse {
			fills := []Fill{{ID: "pl_1", OperationID: "op_f1", OrderID: "ord_abc", Size: "0.1"}}
			if call >= 2 {
				fills = append(fills, Fill{ID: "pl_2", OperationID: "op_f2", OrderID: "ord_abc", Size: "0.2"})
			}
			// A preview (no operation id) never counts.
			fills = append(fills, Fill{ID: "preview", OrderID: "ord_abc", Size: "9"})
			return FillListResponse{Fills: fills, Total: len(fills)}
		}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	detail, err := h.Accounted(ctx)
	if err != nil || detail.Order.FilledSize != "0.3" {
		t.Fatalf("%+v %v", detail, err)
	}
	if got := f.listCalls.Load(); got != 2 {
		t.Fatalf("listFills calls = %d, want 2 (0.1 alone does not cover 0.3; 0.1 + 0.2 does, exactly)", got)
	}
}

func TestAccountedZeroFillTerminalOrderIsAccountedTrivially(t *testing.T) {
	f := &accountedFixture{}
	h := newSettledOrderHandle("/order", "ord_abc", f.deps(func(int32) SimOrderWithFills {
		return SimOrderWithFills{Order: SimOrder{ID: "ord_abc", Status: OrderCancelled, Size: "1", FilledSize: "0"}}
	}, func(int32) FillListResponse { t.Fatal("no fills to list for a zero-fill order"); return FillListResponse{} }))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	detail, err := h.Accounted(ctx)
	if err != nil || detail.Order.Status != OrderCancelled {
		t.Fatalf("%+v %v", detail, err)
	}
}

func TestAccountedHonoursTheDeadlineWhenAccountingNeverCompletes(t *testing.T) {
	f := &accountedFixture{}
	h := newSettledOrderHandle("/order", "ord_abc", f.deps(func(int32) SimOrderWithFills { return terminalOrder("1", boolPtr(false)) }, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := h.Accounted(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline", err)
	}
	if got := f.nudges(); len(got) != 0 {
		t.Fatalf("no nudge without completion, got %v", got)
	}
}

func TestRecordedSizesCoverIsExactDecimalArithmetic(t *testing.T) {
	for _, tc := range []struct {
		sizes    []string
		executed string
		want     bool
	}{
		{[]string{"0.1", "0.2"}, "0.3", true},
		{[]string{"0.1", "0.2"}, "0.30000", true},
		{[]string{"0.1"}, "0.3", false},
		{[]string{"0.1", "0.2", "0.000000001"}, "0.3", false},
		{nil, "0", true},
		{nil, "0.0", true},
		{nil, "1", false},
		{[]string{"1e-1"}, "0.1", false},
		{[]string{"-0.1", "0.4"}, "0.3", false},
		{[]string{""}, "0", false},
	} {
		if got := recordedSizesCover(tc.sizes, tc.executed); got != tc.want {
			t.Errorf("recordedSizesCover(%v, %q) = %v, want %v", tc.sizes, tc.executed, got, tc.want)
		}
	}
}

func TestRecordedFillMatchesByOrderIDOrPlacementOperation(t *testing.T) {
	if !recordedFillMatches(&SimFill{OrderID: "ord_abc"}, "ord_abc", "op_x") {
		t.Fatal("venue order id must match")
	}
	if recordedFillMatches(&SimFill{OrderID: "ord_other"}, "ord_abc", "op_x") {
		t.Fatal("foreign order must not match")
	}
	if !recordedFillMatches(&SimFill{OrderOperationID: "op_place"}, "ord_abc", "op_place") {
		t.Fatal("placement operation id must match a pending bracket child's fill")
	}
	if recordedFillMatches(&SimFill{OrderOperationID: "op_place"}, "ord_abc", "op_other") {
		t.Fatal("foreign placement must not match")
	}
}
