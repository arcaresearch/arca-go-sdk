package arca

import (
	"context"
	"sync"
)

// WatchState is the lifecycle state of a watch stream. Streams never terminally
// error — they retry forever, transitioning loading → connected ⇄ reconnecting.
type WatchState string

const (
	WatchLoading      WatchState = "loading"
	WatchConnected    WatchState = "connected"
	WatchReconnecting WatchState = "reconnecting"
)

const watchChanBuffer = 256

// WatchStream is the generic base for all watch streams. Consume updates via
// OnUpdate callbacks or the Updates channel; call Close when done.
type WatchStream[T any] struct {
	mu        sync.Mutex
	state     WatchState
	value     T
	hasValue  bool
	updateCbs map[int]func(T)
	stateCbs  map[int]func(WatchState)
	nextID    int
	ch        chan T
	closed    bool
	unsubs    []func()
	readyCh   chan struct{}
	readyOnce sync.Once
}

func newWatchStream[T any]() *WatchStream[T] {
	return &WatchStream[T]{
		state:     WatchLoading,
		updateCbs: map[int]func(T){},
		stateCbs:  map[int]func(WatchState){},
		ch:        make(chan T, watchChanBuffer),
		readyCh:   make(chan struct{}),
	}
}

func (s *WatchStream[T]) emit(v T) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.value = v
	s.hasValue = true
	if s.state != WatchConnected {
		s.state = WatchConnected
		s.notifyStateLocked(WatchConnected)
	}
	cbs := make([]func(T), 0, len(s.updateCbs))
	for _, cb := range s.updateCbs {
		cbs = append(cbs, cb)
	}
	// Non-blocking channel send; drop oldest on overflow.
	select {
	case s.ch <- v:
	default:
		select {
		case <-s.ch:
		default:
		}
		select {
		case s.ch <- v:
		default:
		}
	}
	s.mu.Unlock()
	s.readyOnce.Do(func() { close(s.readyCh) })
	for _, cb := range cbs {
		cb(v)
	}
}

func (s *WatchStream[T]) setState(st WatchState) {
	s.mu.Lock()
	if s.closed || s.state == st {
		s.mu.Unlock()
		return
	}
	s.state = st
	s.notifyStateLocked(st)
	s.mu.Unlock()
}

func (s *WatchStream[T]) notifyStateLocked(st WatchState) {
	cbs := make([]func(WatchState), 0, len(s.stateCbs))
	for _, cb := range s.stateCbs {
		cbs = append(cbs, cb)
	}
	go func() {
		for _, cb := range cbs {
			cb(st)
		}
	}()
}

// OnUpdate registers an update callback; returns an unsubscribe func.
func (s *WatchStream[T]) OnUpdate(cb func(T)) func() {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.updateCbs[id] = cb
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.updateCbs, id)
		s.mu.Unlock()
	}
}

// OnStateChange registers a state-change callback; returns an unsubscribe func.
func (s *WatchStream[T]) OnStateChange(cb func(WatchState)) func() {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.stateCbs[id] = cb
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.stateCbs, id)
		s.mu.Unlock()
	}
}

// Updates returns a channel of updates. The channel is closed on Close.
func (s *WatchStream[T]) Updates() <-chan T { return s.ch }

// State returns the current stream state.
func (s *WatchStream[T]) State() WatchState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Value returns the latest value and whether one has been received.
func (s *WatchStream[T]) Value() (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.hasValue
}

// Ready blocks until the first value arrives or ctx is done.
func (s *WatchStream[T]) Ready(ctx context.Context) error {
	s.mu.Lock()
	if s.hasValue {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	select {
	case <-s.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// IsClosed reports whether the stream has been closed.
func (s *WatchStream[T]) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *WatchStream[T]) addUnsub(fn func()) {
	s.mu.Lock()
	s.unsubs = append(s.unsubs, fn)
	s.mu.Unlock()
}

// Close releases the stream's subscriptions and closes the Updates channel.
func (s *WatchStream[T]) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	unsubs := s.unsubs
	s.unsubs = nil
	close(s.ch)
	s.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
}

// ---- Concrete update payloads ----

// BalanceUpdate is delivered by BalanceWatchStream.
type BalanceUpdate struct {
	EntityID   string
	EntityPath string
	Balances   []ArcaBalance
}

// CandleUpdate is delivered by CandleWatchStream.
type CandleUpdate struct {
	Coin     string
	Interval CandleInterval
	Candle   Candle
}

// ---- Concrete streams ----

// PriceWatchStream streams mid prices. Read Get/Prices any time after Ready.
type PriceWatchStream struct {
	*WatchStream[map[string]string]
	pmu    sync.Mutex
	prices map[string]string
}

// Get returns the current price for a coin (decimal string) and whether it is
// known.
func (s *PriceWatchStream) Get(coin string) (string, bool) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	v, ok := s.prices[coin]
	return v, ok
}

// Prices returns a copy of the current price map.
func (s *PriceWatchStream) Prices() map[string]string {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	out := make(map[string]string, len(s.prices))
	for k, v := range s.prices {
		out[k] = v
	}
	return out
}

func (s *PriceWatchStream) merge(mids map[string]string) {
	s.pmu.Lock()
	for k, v := range mids {
		s.prices[k] = v
	}
	out := make(map[string]string, len(s.prices))
	for k, v := range s.prices {
		out[k] = v
	}
	s.pmu.Unlock()
	s.emit(out)
}

// WatchPricesOptions configures WatchPrices.
type WatchPricesOptions struct {
	Exchange string   // default "sim"
	Coins    []string // nil/empty subscribes to all
}

// WatchPrices subscribes to real-time mid prices. Open once and read Get/Prices
// any time. Ready resolves after the initial snapshot.
func (a *Arca) WatchPrices(ctx context.Context, opts *WatchPricesOptions) (*PriceWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	exchange := "sim"
	var coins []string
	if opts != nil {
		if opts.Exchange != "" {
			exchange = opts.Exchange
		}
		coins = opts.Coins
	}
	s := &PriceWatchStream{WatchStream: newWatchStream[map[string]string](), prices: map[string]string{}}
	a.ws.acquireMids(exchange, coins)
	unsub := a.ws.OnMidsUpdated(func(mids map[string]string) { s.merge(mids) })
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.releaseMids(coins) })
	if err := s.Ready(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// OperationWatchStream streams operation lifecycle changes.
type OperationWatchStream struct {
	*WatchStream[Operation]
}

// WatchOperations streams operation events under a path prefix.
func (a *Arca) WatchOperations(ctx context.Context, path string) (*OperationWatchStream, error) {
	if path == "" {
		path = "/"
	}
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &OperationWatchStream{WatchStream: newWatchStream[Operation]()}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), path) }()
	handler := func(op *Operation, _ RealmEvent) {
		if op != nil {
			s.emit(*op)
		}
	}
	u1 := a.ws.On(EventOperationCreated, func(ev RealmEvent) { handler(ev.Operation, ev) })
	u2 := a.ws.OnOperationUpdated(handler)
	s.addUnsub(u1)
	s.addUnsub(u2)
	s.addUnsub(func() { a.ws.unwatchPath(path) })
	return s, nil
}

// BalanceWatchStream streams balance changes under a path prefix.
type BalanceWatchStream struct {
	*WatchStream[BalanceUpdate]
}

// WatchBalances streams balance updates under a path prefix.
func (a *Arca) WatchBalances(ctx context.Context, path string) (*BalanceWatchStream, error) {
	if path == "" {
		path = "/"
	}
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &BalanceWatchStream{WatchStream: newWatchStream[BalanceUpdate]()}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), path) }()
	unsub := a.ws.OnBalanceUpdated(func(entityID string, ev RealmEvent) {
		s.emit(BalanceUpdate{EntityID: entityID, EntityPath: ev.EntityPath, Balances: ev.Balances})
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.unwatchPath(path) })
	return s, nil
}

// AggregationWatchStream streams aggregated valuation updates for a set of
// sources.
type AggregationWatchStream struct {
	*WatchStream[PathAggregation]
	watchID string
}

// WatchID returns the server-side watch id backing this stream.
func (s *AggregationWatchStream) WatchID() string { return s.watchID }

// WatchAggregation subscribes to real-time aggregation updates for a set of
// sources.
func (a *Arca) WatchAggregation(ctx context.Context, sources []AggregationSource) (*AggregationWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	a.ws.EnsureConnected()
	watchID, agg, err := a.ws.createAggregationWatch(ctx, sources, "")
	if err != nil {
		return nil, err
	}
	s := &AggregationWatchStream{WatchStream: newWatchStream[PathAggregation](), watchID: watchID}
	unsub := a.ws.OnAggregationUpdated(func(wid string, updated *PathAggregation, _ RealmEvent) {
		if wid == watchID && updated != nil {
			s.emit(*updated)
		}
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.destroyAggregationWatch(watchID) })
	s.emit(agg)
	return s, nil
}

// ObjectWatchStream streams single-object valuation updates.
type ObjectWatchStream struct {
	*WatchStream[ObjectValuation]
	path string
}

// WatchObject streams valuation updates for a single Arca object.
func (a *Arca) WatchObject(ctx context.Context, path string) (*ObjectWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &ObjectWatchStream{WatchStream: newWatchStream[ObjectValuation](), path: path}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), path) }()
	unsub := a.ws.OnObjectValuation(func(p string, val *ObjectValuation, _ string, _ RealmEvent) {
		if p == path && val != nil {
			s.emit(*val)
		}
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.unwatchPath(path) })
	// Seed with an initial REST valuation.
	if v, err := a.GetObjectValuation(ctx, path); err == nil {
		s.emit(v)
	}
	return s, nil
}

// ObjectsWatchStream streams a merged map of valuations for multiple objects.
type ObjectsWatchStream struct {
	*WatchStream[map[string]ObjectValuation]
	mu       sync.Mutex
	byPath   map[string]ObjectValuation
	children []*ObjectWatchStream
}

// WatchObjects streams valuations for multiple Arca objects, emitting a merged
// map keyed by path.
func (a *Arca) WatchObjects(ctx context.Context, paths []string) (*ObjectsWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &ObjectsWatchStream{WatchStream: newWatchStream[map[string]ObjectValuation](), byPath: map[string]ObjectValuation{}}
	for _, p := range paths {
		child, err := a.WatchObject(ctx, p)
		if err != nil {
			s.Close()
			return nil, err
		}
		s.children = append(s.children, child)
		unsub := child.OnUpdate(func(v ObjectValuation) {
			s.mu.Lock()
			s.byPath[v.Path] = v
			out := make(map[string]ObjectValuation, len(s.byPath))
			for k, val := range s.byPath {
				out[k] = val
			}
			s.mu.Unlock()
			s.emit(out)
		})
		s.addUnsub(unsub)
	}
	for _, c := range s.children {
		child := c
		s.addUnsub(func() { child.Close() })
	}
	return s, nil
}

// ExchangeWatchStream streams exchange-account state updates.
type ExchangeWatchStream struct {
	*WatchStream[ExchangeState]
	objectID string
}

// WatchExchangeState streams exchange state (positions, orders, pending
// intents) for an exchange object.
func (a *Arca) WatchExchangeState(ctx context.Context, objectID string) (*ExchangeWatchStream, error) {
	detail, err := a.GetObjectDetail(ctx, objectID)
	if err != nil {
		return nil, err
	}
	objectPath := detail.Object.Path
	s := &ExchangeWatchStream{WatchStream: newWatchStream[ExchangeState](), objectID: objectID}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), objectPath) }()
	unsub := a.ws.OnExchangeNotification(func(ev RealmEvent) {
		if ev.EntityID != objectID && ev.EntityPath != objectPath {
			return
		}
		if ev.ExchangeState != nil {
			s.emit(*ev.ExchangeState)
			return
		}
		if st, e := a.GetExchangeState(context.Background(), objectID); e == nil {
			s.emit(st)
		}
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.unwatchPath(objectPath) })
	if st, e := a.GetExchangeState(ctx, objectID); e == nil {
		s.emit(st)
	}
	return s, nil
}

// FillWatchStream streams fills (preview + recorded) for an exchange object.
type FillWatchStream struct {
	*WatchStream[Fill]
	objectID   string
	objectPath string
}

// WatchFills streams fills for an exchange object.
func (a *Arca) WatchFills(ctx context.Context, objectID string, opts *ListFillsOptions) (*FillWatchStream, error) {
	detail, err := a.GetObjectDetail(ctx, objectID)
	if err != nil {
		return nil, err
	}
	s := &FillWatchStream{WatchStream: newWatchStream[Fill](), objectID: objectID, objectPath: detail.Object.Path}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), s.objectPath) }()
	emitFromEvent := func(ev RealmEvent) {
		if ev.EntityID != objectID && ev.EntityPath != s.objectPath {
			return
		}
		if ev.Fill != nil {
			s.emit(Fill{
				ID: ev.Fill.ID, OrderID: ev.Fill.OrderID, Market: ev.Fill.Coin,
				Side: ev.Fill.Side, Size: ev.Fill.Size, Price: ev.Fill.Price,
				Fee: ev.Fill.Fee, IsLiquidation: ev.Fill.IsLiquidation,
				CreatedAt: ev.Fill.CreatedAt, IsOptimistic: ev.Fill.IsOptimistic,
			})
		}
	}
	u1 := a.ws.OnExchangeFill(func(_ *SimFill, ev RealmEvent) { emitFromEvent(ev) })
	u2 := a.ws.OnFillRecorded(func(ev RealmEvent) { emitFromEvent(ev) })
	s.addUnsub(u1)
	s.addUnsub(u2)
	s.addUnsub(func() { a.ws.unwatchPath(s.objectPath) })
	return s, nil
}

// FundingWatchStream streams funding payment events for an exchange object.
type FundingWatchStream struct {
	*WatchStream[FundingPayment]
	objectID   string
	objectPath string
}

// WatchFunding streams funding payment events for an exchange object.
func (a *Arca) WatchFunding(ctx context.Context, objectID string) (*FundingWatchStream, error) {
	detail, err := a.GetObjectDetail(ctx, objectID)
	if err != nil {
		return nil, err
	}
	s := &FundingWatchStream{WatchStream: newWatchStream[FundingPayment](), objectID: objectID, objectPath: detail.Object.Path}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), s.objectPath) }()
	unsub := a.ws.OnExchangeFunding(func(f *FundingPayment, ev RealmEvent) {
		if ev.EntityID != objectID && ev.EntityPath != s.objectPath {
			return
		}
		if f != nil {
			s.emit(*f)
		}
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.unwatchPath(s.objectPath) })
	return s, nil
}

// CandleWatchStream streams candle updates.
type CandleWatchStream struct {
	*WatchStream[CandleUpdate]
	coins     []string
	intervals []CandleInterval
}

// WatchCandles streams candle updates for the given coins and intervals.
func (a *Arca) WatchCandles(ctx context.Context, coins []string, intervals []CandleInterval) (*CandleWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &CandleWatchStream{WatchStream: newWatchStream[CandleUpdate](), coins: coins, intervals: intervals}
	a.ws.acquireCandles(coins, intervals)
	unsub := a.ws.OnCandleUpdated(func(ev RealmEvent) {
		if ev.Candle != nil {
			s.emit(CandleUpdate{Coin: ev.Coin, Interval: ev.Interval, Candle: *ev.Candle})
		}
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.releaseCandles(coins, intervals) })
	return s, nil
}

// TradeWatchStream streams market trades.
type TradeWatchStream struct {
	*WatchStream[MarketTrade]
	coins []string
}

// WatchTrades streams market trades for the given coins.
func (a *Arca) WatchTrades(ctx context.Context, coins []string) (*TradeWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &TradeWatchStream{WatchStream: newWatchStream[MarketTrade](), coins: coins}
	a.ws.acquireTrades(coins)
	unsub := a.ws.OnTradeExecuted(func(t MarketTrade) { s.emit(t) })
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.releaseTrades(coins) })
	return s, nil
}

// TwapWatchStream streams the latest Twap state for a single TWAP.
type TwapWatchStream struct {
	*WatchStream[Twap]
	operationID string
}

// WatchTwap streams a single TWAP by parent operation id. The first snapshot is
// fetched eagerly via REST.
func (a *Arca) WatchTwap(ctx context.Context, exchangeID, operationID string) (*TwapWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &TwapWatchStream{WatchStream: newWatchStream[Twap](), operationID: operationID}
	a.ws.EnsureConnected()
	emit := func(ev RealmEvent) {
		if ev.TwapID != "" && ev.TwapID != operationID && ev.EntityID != operationID {
			return
		}
		if ev.Twap != nil {
			s.emit(*ev.Twap)
		}
	}
	for _, t := range []string{EventTwapStarted, EventTwapProgress, EventTwapCompleted, EventTwapCancelled, EventTwapFailed} {
		s.addUnsub(a.ws.On(t, emit))
	}
	if initial, err := a.GetTwap(ctx, exchangeID, operationID); err == nil {
		s.emit(initial.Twap)
	}
	return s, nil
}
