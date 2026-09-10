package arca

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOrderHandleLifecycleFactoryWaitsForCommittedFillsWithoutResubmission(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "lost_http_response"}[lostResponse], func(t *testing.T) {
			var posts, detailReads, pathReads atomic.Int32
			view := lifecycleFixture(t)
			requested, remaining, average := view.Intent.RequestedSize, view.RemainingSize, view.AveragePrice
			view.ExecutionReceipt = &OrderExecutionReceipt{ObjectID: "account", OperationID: "original", Leg: "0", Market: "gllt:3", OrderID: "3:order", Status: "FILLED", FilledSize: view.ExecutedSize, RequestedSize: &requested, RemainingSize: &remaining, ExecutionState: "partial", FulfillmentState: "partial", RemainingDisposition: "cancelled", AvgFillPrice: &average, AveragePriceSource: "venue_aggregate"}
			initialView := view
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "POST" && r.URL.Path == "/api/v1/objects/account/exchange/orders":
					posts.Add(1)
					if lostResponse {
						writeError(w, 504, "GATEWAY_TIMEOUT", "submission response was lost", nil)
						return
					}
					writeEnvelope(w, 200, OrderOperationResponse{Operation: Operation{ID: "original", State: OpCompleted}})
				case r.Method == "GET" && r.URL.Path == "/api/v1/objects/account/exchange/order-lifecycle":
					pathReads.Add(1)
					if r.URL.Query().Get("operationPath") != "/alice/original" {
						t.Errorf("recovery changed original path: %s", r.URL)
					}
					writeEnvelope(w, 200, map[string]any{"lifecycle": initialView})
				case r.Method == "GET" && r.URL.Path == "/api/v1/objects/account/exchange/orders/3:order":
					detailReads.Add(1)
					complete := true
					writeEnvelope(w, 200, SimOrderWithFills{Order: SimOrder{ID: "3:order", Market: "gllt:3", Status: OrderFilled, FilledSize: "3.123456789"}, FillsComplete: &complete})
				default:
					t.Errorf("unexpected HTTP %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer httpServer.Close()
			a := newTestArca(t, httpServer.URL)
			sockets := newWSTestServer(t)
			a.ws = newTestWSManager(t, sockets, 0)
			conn := connectManager(t, sockets, a.ws, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			handle := a.PlaceOrder(ctx, PlaceOrderOptions{ObjectID: "account", Path: "/alice/original", Market: "gllt:3", Side: "buy", OrderType: "MARKET", Size: requested})
			type result struct {
				receipt OrderExecutionReceipt
				err     error
			}
			receipts := make(chan result, 1)
			go func() { r, e := handle.ExecutionReceipt(ctx); receipts <- result{r, e} }()
			request := conn.waitFor("watch_order_lifecycle")
			lifecyclePush(conn, request, view, 1)
			r := <-receipts
			if r.err != nil || r.receipt.FilledSize != view.ExecutedSize || r.receipt.FillsComplete || r.receipt.AveragePriceFinal {
				t.Fatalf("receipt: %+v", r)
			}
			if detailReads.Load() != 0 {
				t.Fatal("prompt receipt read full history")
			}
			conn.waitFor("unwatch_order_lifecycle")
			filled := make(chan error, 1)
			go func() { _, e := handle.Filled(ctx); filled <- e }()
			request = conn.waitFor("watch_order_lifecycle")
			lifecyclePush(conn, request, view, 2)
			select {
			case e := <-filled:
				t.Fatalf("filled returned before accounting: %v", e)
			case <-time.After(50 * time.Millisecond):
			}
			if detailReads.Load() != 0 {
				t.Fatal("accounting-pending snapshot triggered history polling")
			}
			completeReceipt := *view.ExecutionReceipt
			completeReceipt.FillsComplete, completeReceipt.AveragePriceFinal = true, true
			completeReceipt.AveragePriceSource = "ledger_vwap"
			view.ExecutionReceipt = &completeReceipt
			view.AccountedSize, view.AccountingComplete, view.AveragePriceFinal = view.ExecutedSize, true, true
			lifecyclePush(conn, request, view, 3)
			if e := <-filled; e != nil {
				t.Fatal(e)
			}
			conn.waitFor("unwatch_order_lifecycle")
			if posts.Load() != 1 || detailReads.Load() != 1 {
				t.Fatalf("posts=%d details=%d", posts.Load(), detailReads.Load())
			}
			if lostResponse && pathReads.Load() != 2 {
				t.Fatalf("each disposed watch should recover its same original path: %d", pathReads.Load())
			}
		})
	}
}

func TestOrderHandleLifecycleOnFillRecoversCanonicalSnapshotAfterLostHTTP(t *testing.T) {
	var posts, reads atomic.Int32
	view := lifecycleFixture(t)
	initial := view
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			writeError(w, 504, "GATEWAY_TIMEOUT", "lost response", nil)
			return
		}
		if r.URL.Path == "/api/v1/objects/account/exchange/order-lifecycle" {
			writeEnvelope(w, 200, map[string]any{"lifecycle": initial})
			return
		}
		reads.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	a := newTestArca(t, server.URL)
	sockets := newWSTestServer(t)
	a.ws = newTestWSManager(t, sockets, 0)
	conn := connectManager(t, sockets, a.ws, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handle := a.PlaceOrder(ctx, PlaceOrderOptions{ObjectID: "account", Path: "/alice/original", Market: "gllt:3", Side: "buy", OrderType: "MARKET", Size: "10"})
	received := make(chan SimFill, 3)
	stop := handle.OnFill(ctx, func(f SimFill) { received <- f })
	defer stop()
	request := conn.waitFor("watch_order_lifecycle")
	fill := func(id, size string) OrderLifecycleFill {
		return OrderLifecycleFill{ObjectID: "account", OriginalOperationID: "original", Leg: "0", SimFill: SimFill{ID: id, OrderID: "3:order", RealmID: view.Intent.RealmID, AccountID: "123", Market: "gllt:3", Side: "buy", Size: size, Price: "2000.000000001", Fee: "0.123456789"}}
	}
	view.CommittedFills = []OrderLifecycleFill{fill("a", "1")}
	view.AccountedSize = "1"
	lifecyclePush(conn, request, view, 1)
	select {
	case f := <-received:
		if f.ID != "a" || f.Fee != "0.123456789" {
			t.Fatalf("%+v", f)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lifecyclePush(conn, request, view, 2)
	view.CommittedFills = append(view.CommittedFills, fill("b", "1"), fill("c", "1.123456789"))
	view.AccountedSize = view.ExecutedSize
	view.AccountingComplete = true
	view.AveragePriceFinal = true
	lifecyclePush(conn, request, view, 3)
	for _, id := range []string{"b", "c"} {
		select {
		case f := <-received:
			if f.ID != id {
				t.Fatalf("wanted %s got %s", id, f.ID)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	conn.waitFor("unwatch_order_lifecycle")
	if posts.Load() != 1 || reads.Load() != 0 {
		t.Fatalf("posts=%d history=%d", posts.Load(), reads.Load())
	}
	select {
	case f := <-received:
		t.Fatalf("duplicate %+v", f)
	default:
	}
}
