package arca

import (
	"context"
	"sync"
	"time"
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
	Market   string
	Interval CandleInterval
	Candle   Candle
}

// OIUpdate is delivered by OIWatchStream.
type OIUpdate struct {
	Market   string
	Interval CandleInterval
	Bar      OIBar
	IsClosed bool
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

	widMu      sync.Mutex
	watchID    string
	recreating bool
	redo       bool
}

// WatchID returns the server-side watch id backing this stream. It changes
// whenever the stream re-establishes its watch on a new connection.
func (s *AggregationWatchStream) WatchID() string {
	s.widMu.Lock()
	defer s.widMu.Unlock()
	return s.watchID
}

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
		// The id changes every time the watch is re-established, so this has to
		// read the current one — a captured id stops matching at the first
		// reconnect and the stream goes quiet.
		if updated != nil && wid == s.WatchID() {
			s.emit(*updated)
		}
	})
	s.addUnsub(unsub)
	// A standalone aggregation watch lives on the connection it was created on:
	// the server destroys it when that connection closes, and the manager's
	// post-auth resubscribe does not cover it. Re-establishing it on both hooks
	// is what keeps the stream alive — a reconnect surfaces as OnAuthenticated,
	// a rotation only as OnRotated, since it emits no status change. Off the
	// delivery goroutine, because creating a watch waits for a reply that only
	// that goroutine can deliver.
	s.addUnsub(a.ws.OnAuthenticated(func() { go s.recreate(a, sources) }))
	s.addUnsub(a.ws.OnRotated(func() { go s.recreate(a, sources) }))
	s.addUnsub(func() { a.ws.destroyAggregationWatch(s.WatchID()) })
	s.emit(agg)
	return s, nil
}

// recreate re-establishes the server-side watch on the current connection. One
// runs at a time; a trigger that lands during one is honoured afterwards rather
// than dropped, since the in-flight attempt may already have been written to
// the connection that just went away.
func (s *AggregationWatchStream) recreate(a *Arca, sources []AggregationSource) {
	s.widMu.Lock()
	if s.recreating {
		s.redo = true
		s.widMu.Unlock()
		return
	}
	s.recreating = true
	s.widMu.Unlock()

	for {
		s.recreateOnce(a, sources)
		s.widMu.Lock()
		again := s.redo && !s.IsClosed()
		s.redo = false
		s.recreating = again
		s.widMu.Unlock()
		if !again {
			return
		}
	}
}

func (s *AggregationWatchStream) recreateOnce(a *Arca, sources []AggregationSource) {
	if s.IsClosed() {
		return
	}
	s.widMu.Lock()
	previous := s.watchID
	s.widMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), wsRequestTimeout)
	defer cancel()
	watchID, agg, err := a.ws.createAggregationWatch(ctx, sources, "")
	if err != nil || s.IsClosed() {
		return
	}
	s.widMu.Lock()
	s.watchID = watchID
	s.widMu.Unlock()
	if previous != "" && previous != watchID {
		// Best effort: after a reconnect the connection it belonged to is gone,
		// which destroyed it server-side anyway.
		a.ws.destroyAggregationWatch(previous)
	}
	s.emit(agg)
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
				ID: ev.Fill.ID, OrderID: ev.Fill.OrderID, Market: ev.Fill.Market,
				Side: ev.Fill.Side, Size: ev.Fill.Size, Price: ev.Fill.Price,
				Fee: ev.Fill.Fee, IsLiquidation: ev.Fill.IsLiquidation,
				CreatedAt: ev.Fill.CreatedAt, IsOptimistic: ev.Fill.IsOptimistic,
			})
		}
	}
	u1 := a.ws.OnFillPreviewed(func(_ *SimFill, ev RealmEvent) { emitFromEvent(ev) })
	u2 := a.ws.OnFillRecorded(func(ev RealmEvent) { emitFromEvent(ev) })
	s.addUnsub(u1)
	s.addUnsub(u2)
	s.addUnsub(func() { a.ws.unwatchPath(s.objectPath) })
	return s, nil
}

// ---- Realm-wide fill stream (catch-up-then-tail) ----

const (
	// realmFillsPageLimit is the page size used for durable-log replay. It
	// matches the server's maximum, so "partial page" reliably means "head
	// of the log reached".
	realmFillsPageLimit = 500
	// realmFillsDedupeWindow bounds the recent-fill-id LRU used to drop
	// duplicates in the catch-up/tail overlap window. Best-effort only: the
	// stream's contract is at-least-once, so a duplicate escaping the
	// window is allowed, and consumers must process idempotently by fill id.
	realmFillsDedupeWindow = 512
)

// encodeRealmFillCursor mints the opaque resume cursor for a fill from its
// (createdAt, id) keyset position — the same encoding the server's realm-wide
// fills listing returns. CreatedAt on both REST fills and fill.recorded
// events is the position_ledger row's commit timestamp in the canonical
// layout, which is what makes a client-minted cursor byte-identical to a
// server-minted one.
func encodeRealmFillCursor(createdAt, id string) string {
	if createdAt == "" || id == "" {
		return ""
	}
	return createdAt + "|" + id
}

// RealmFillUpdate is one delivery from RealmFillWatchStream.
type RealmFillUpdate struct {
	Fill Fill
	// Cursor is the stream's durable resume position after this fill.
	// Persist it after processing; passing it back as
	// WatchRealmFillsOptions.FromCursor resumes with no loss. It can lag
	// the fill itself (live fills delivered while a replay is in flight
	// keep the replay's position), which only ever causes re-delivery —
	// never a skip.
	Cursor string
	// Replayed is true when the fill came from a durable-log replay
	// (catch-up after open, gap recovery, reconnect) rather than the live
	// tail.
	Replayed bool
}

// RealmFillWatchStream streams every recorded fill in the realm with
// at-least-once delivery. See WatchRealmFills.
type RealmFillWatchStream struct {
	*WatchStream[RealmFillUpdate]

	arca *Arca

	replayCh chan struct{}
	stopCh   chan struct{}

	fmu          sync.Mutex
	started      bool
	cursor       string
	replaying    bool
	replayQueued bool
	seen         map[string]struct{}
	seenOrder    []string
}

// Cursor returns the stream's current durable resume position.
func (s *RealmFillWatchStream) Cursor() string {
	s.fmu.Lock()
	defer s.fmu.Unlock()
	return s.cursor
}

// start begins delivery on the first consumer attach. Deferring the initial
// catch-up (and live-tail delivery) until a consumer exists is what makes
// OnUpdate loss-free: an emission before the callback attaches would be
// dropped by the base stream, and because it advances the internal cursor,
// the next persisted position would silently skip past it — an at-least-once
// violation. Pre-start live fills are deliberately ignored; they are in the
// durable log, and the first replay (which starts here, from a cursor no
// delivery has advanced) re-covers them.
func (s *RealmFillWatchStream) start() {
	s.fmu.Lock()
	if s.started {
		s.fmu.Unlock()
		return
	}
	s.started = true
	s.fmu.Unlock()
	go s.replayLoop()
	s.scheduleReplay()
}

// OnUpdate registers an update callback and starts delivery on first attach.
func (s *RealmFillWatchStream) OnUpdate(cb func(RealmFillUpdate)) func() {
	unsub := s.WatchStream.OnUpdate(cb)
	s.start()
	return unsub
}

// Updates returns the update channel and starts delivery on first use.
func (s *RealmFillWatchStream) Updates() <-chan RealmFillUpdate {
	s.start()
	return s.WatchStream.Updates()
}

// Ready blocks until the first delivery, starting the stream if needed.
func (s *RealmFillWatchStream) Ready(ctx context.Context) error {
	s.start()
	return s.WatchStream.Ready(ctx)
}

// WatchRealmFills streams every recorded fill across ALL exchange objects in
// the realm, with at-least-once delivery and downtime replay.
//
// Delivery model — catch-up-then-tail:
//  1. Catch-up: fills after FromCursor are replayed in order from the
//     durable log (the realm-wide fills listing).
//  2. Tail: live fill.recorded events are delivered as they happen.
//  3. Self-heal: on a detected delivery gap, a server resync marker, or a
//     reconnect, the stream silently re-runs catch-up from its last durable
//     position.
//
// Consumer contract: process each fill idempotently keyed on Fill.ID, then
// persist Cursor. Duplicates are possible by design (a small recent-id
// window drops most catch-up/tail overlap); missing a fill is not, as long
// as the consumer resumes from its persisted cursor. Fill.ObjectID carries
// the owning exchange object.
//
// Delivery begins when the first consumer attaches (OnUpdate, Updates, or
// Ready) — the initial catch-up is deferred until then so no replayed fill
// can be emitted before anyone is listening.
//
// Requires a credential with realm-wide read (arca:ReadObject on "*") and
// arca:Subscribe.
func (a *Arca) WatchRealmFills(ctx context.Context, opts *WatchRealmFillsOptions) (*RealmFillWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	fromCursor := ""
	if opts != nil {
		fromCursor = opts.FromCursor
	}
	s := &RealmFillWatchStream{
		WatchStream: newWatchStream[RealmFillUpdate](),
		arca:        a,
		cursor:      fromCursor,
		seen:        map[string]struct{}{},
		replayCh:    make(chan struct{}, 1),
		stopCh:      make(chan struct{}),
	}
	if fromCursor == "" {
		// "Start from now": seed the head-of-log position so gap recovery
		// has a floor to replay from before the first live fill arrives. An
		// empty realm leaves the cursor empty, and replay-from-empty is
		// still correct — the whole (empty-at-open) history is after us.
		head, err := a.ListRealmFills(ctx, &ListRealmFillsOptions{Order: "desc", Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(head.Fills) > 0 {
			s.cursor = encodeRealmFillCursor(head.Fills[0].CreatedAt, head.Fills[0].ID)
		}
	}

	a.ws.EnsureConnected()
	// Subscribe by event TYPE, not by watching "/". A realm-root path watch
	// makes the server assemble a full-realm snapshot (every balance, every
	// operation, a valuations map) and hold a realm-wide valuation watch
	// that re-derives on every balance change in the realm — all of which
	// this stream discards, since it wants fill.recorded and nothing else.
	// Type-routed delivery is not an authorization widening: the server
	// still applies the per-event scope backstop.
	a.ws.subscribeEvents([]string{string(EventFillRecorded)})
	s.addUnsub(a.ws.OnFillRecorded(s.onLiveFill))
	// Both loss signals funnel into the same recovery: replay the durable
	// log from the last cursor. OnGap covers observed deliverySeq holes and
	// server-announced resync markers; OnAuthenticated covers reconnects
	// (everything during the outage was lost without a gap to observe).
	s.addUnsub(a.ws.OnGap(func(int64) { s.scheduleReplay() }))
	s.addUnsub(a.ws.OnAuthenticated(func() { s.scheduleReplay() }))
	s.addUnsub(func() { a.ws.unsubscribeEvents([]string{string(EventFillRecorded)}) })
	s.addUnsub(func() { close(s.stopCh) })
	// The initial catch-up (which also closes the seam between the
	// head-seed read above and the live subscription) is scheduled by
	// start() on the first consumer attach — see the delivery-start note
	// in the method doc.
	return s, nil
}

func (s *RealmFillWatchStream) onLiveFill(ev RealmEvent) {
	s.fmu.Lock()
	started := s.started
	s.fmu.Unlock()
	if !started {
		// No consumer yet: ignore rather than deliver-to-nobody. The fill
		// is in the durable log and the cursor has not moved, so the
		// first replay after start() re-covers it.
		return
	}
	if ev.Fill == nil || ev.Fill.ID == "" {
		return
	}
	f := fillFromRecordedEvent(ev)
	// CreatedAt is empty on events from workers predating the enrichment;
	// the fill is still delivered, the cursor just doesn't advance (the
	// next replay re-covers it — duplicates over loss).
	s.deliver(f, encodeRealmFillCursor(f.CreatedAt, f.ID), false)
}

// deliver is the single serialization point for both the live tail and the
// replay loop: dedupe by fill id, advance the durable cursor, emit.
//
// The cursor only advances from a live fill when no replay is active or
// queued. A live fill can arrive ahead of rows a replay hasn't scanned yet;
// letting it advance the cursor mid-replay would let a crash-then-resume
// skip those rows — the one failure the at-least-once contract forbids.
func (s *RealmFillWatchStream) deliver(f Fill, cursor string, replayed bool) {
	s.fmu.Lock()
	_, dup := s.seen[f.ID]
	if !dup {
		s.seen[f.ID] = struct{}{}
		s.seenOrder = append(s.seenOrder, f.ID)
		if len(s.seenOrder) > realmFillsDedupeWindow {
			delete(s.seen, s.seenOrder[0])
			s.seenOrder = s.seenOrder[1:]
		}
	}
	if cursor != "" && (replayed || (!s.replaying && !s.replayQueued)) {
		s.cursor = cursor
	}
	cur := s.cursor
	s.fmu.Unlock()
	if dup {
		return
	}
	s.emit(RealmFillUpdate{Fill: f, Cursor: cur, Replayed: replayed})
}

func (s *RealmFillWatchStream) scheduleReplay() {
	s.fmu.Lock()
	s.replayQueued = true
	s.fmu.Unlock()
	select {
	case s.replayCh <- struct{}{}:
	default:
	}
}

func (s *RealmFillWatchStream) replayLoop() {
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.replayCh:
			s.runReplay()
		}
	}
}

// runReplay drains the durable log from the stream's cursor to the head,
// delivering every row. REST failures retry with capped backoff — streams
// never terminally error. Loops while further replays were queued during
// the run.
func (s *RealmFillWatchStream) runReplay() {
	for {
		s.fmu.Lock()
		s.replayQueued = false
		s.replaying = true
		local := s.cursor
		s.fmu.Unlock()

		backoff := time.Second
		for !s.IsClosed() {
			resp, err := s.arca.ListRealmFills(context.Background(), &ListRealmFillsOptions{
				Order:  "asc",
				Cursor: local,
				Limit:  realmFillsPageLimit,
			})
			if err != nil {
				s.setState(WatchReconnecting)
				select {
				case <-s.stopCh:
					return
				case <-time.After(backoff):
				}
				if backoff *= 2; backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
				continue
			}
			backoff = time.Second
			for i := range resp.Fills {
				f := resp.Fills[i]
				s.deliver(f, encodeRealmFillCursor(f.CreatedAt, f.ID), true)
			}
			if resp.Cursor != "" {
				// Ascending pages always carry the resume position (echoed
				// on an empty page), which also skips over trailing
				// non-fill ledger rows the server filtered out.
				local = resp.Cursor
			}
			if len(resp.Fills) < realmFillsPageLimit {
				break // head of the log reached
			}
		}

		s.fmu.Lock()
		if local != "" {
			s.cursor = local
		}
		s.replaying = false
		again := s.replayQueued && !s.IsClosed()
		s.fmu.Unlock()
		if !again {
			return
		}
	}
}

// fillFromRecordedEvent maps a fill.recorded event payload to the Fill shape
// the REST listings return, so live-tail and replayed deliveries are
// interchangeable. The event's EntityID is the owning exchange object.
func fillFromRecordedEvent(ev RealmEvent) Fill {
	sf := ev.Fill
	realized := ""
	if sf.RealizedPnl != nil {
		realized = *sf.RealizedPnl
	}
	return Fill{
		ID:                sf.ID,
		OperationID:       sf.OperationID,
		FillID:            sf.FillID,
		ObjectID:          ev.EntityID,
		OrderOperationID:  sf.OrderOperationID,
		OrderID:           sf.OrderID,
		Market:            sf.Market,
		Side:              sf.Side,
		Size:              sf.Size,
		Price:             sf.Price,
		Direction:         sf.Direction,
		StartPosition:     sf.StartPosition,
		Fee:               sf.Fee,
		ExchangeFee:       sf.ExchangeFee,
		PlatformFee:       sf.PlatformFee,
		BuilderFee:        sf.BuilderFee,
		RealizedPnl:       realized,
		ResultingPosition: sf.ResultingPosition,
		IsLiquidation:     sf.IsLiquidation,
		CreatedAt:         sf.CreatedAt,
		IsTrigger:         sf.IsTrigger,
		Tpsl:              sf.Tpsl,
		TriggerPx:         sf.TriggerPx,
		LiquidationKind:   sf.LiquidationKind,
	}
}

// ---- Realm-wide exchange.updated stream (live-tail) ----

// RealmExchangeUpdate is one delivery from RealmExchangeWatchStream.
type RealmExchangeUpdate struct {
	ObjectID   string
	ObjectPath string
	// State is the serve-path open book (positions include liquidationPrice).
	// Nil when the event arrived without a snapshot — treat as degraded for
	// that one object (one GET if you want; this stream will not do it).
	State *ExchangeState
	// Resync is true on a delivery gap or reconnect. There is no durable
	// log for exchange.updated; re-seed the books you already hold. State
	// is nil on a resync marker.
	Resync bool
}

// RealmExchangeWatchStream tails every exchange.updated in the realm.
// See WatchRealmExchange.
type RealmExchangeWatchStream struct {
	*WatchStream[RealmExchangeUpdate]

	emu     sync.Mutex
	started bool
	haveAuth bool
}

// start begins delivery on the first consumer attach so a live event cannot
// be emitted before anyone is listening (the base stream drops those).
func (s *RealmExchangeWatchStream) start() {
	s.emu.Lock()
	s.started = true
	s.emu.Unlock()
}

// OnUpdate registers an update callback and starts delivery on first attach.
func (s *RealmExchangeWatchStream) OnUpdate(cb func(RealmExchangeUpdate)) func() {
	unsub := s.WatchStream.OnUpdate(cb)
	s.start()
	return unsub
}

// Updates returns the update channel and starts delivery on first use.
func (s *RealmExchangeWatchStream) Updates() <-chan RealmExchangeUpdate {
	s.start()
	return s.WatchStream.Updates()
}

// Ready blocks until the first delivery, starting the stream if needed.
func (s *RealmExchangeWatchStream) Ready(ctx context.Context) error {
	s.start()
	return s.WatchStream.Ready(ctx)
}

// WatchRealmExchange tails exchange.updated across every exchange object
// in the realm. The wire is the same type-routed subscribe WatchRealmFills
// uses for fills:
//
//	{"action":"subscribe_events","types":["exchange.updated"]}
//
// No path watch, no realm snapshot, no valuation firehose.
// WatchExchangeState is per-object — N of those is a walk with a websocket.
//
// Delivery model — live-tail, not catch-up-then-tail:
//  1. Live events carry the serve-path open book (State), including
//     liquidationPrice. Rewrite every open row on that account.
//  2. There is no durable log. A delivery gap or reconnect emits one
//     update with Resync=true; re-seed the books you already hold
//     (one GET per object, not a realm walk). This stream never fans
//     out GetExchangeState.
//  3. A name-only event (State=nil, Resync=false) is degraded for that
//     one object — GET it if you want; place/cancel with an unchanged
//     book can be ignored.
//
// Delivery begins when the first consumer attaches (OnUpdate, Updates, or
// Ready). Events before that attach are dropped.
//
// Requires a credential with realm-wide read (arca:ReadObject on "*") and
// arca:Subscribe.
func (a *Arca) WatchRealmExchange(ctx context.Context) (*RealmExchangeWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	s := &RealmExchangeWatchStream{
		WatchStream: newWatchStream[RealmExchangeUpdate](),
	}
	a.ws.EnsureConnected()
	// Subscribe by event TYPE, not by watching "/". A realm-root path
	// watch makes the server assemble a full-realm snapshot and hold a
	// realm-wide valuation watch this stream discards.
	a.ws.subscribeEvents([]string{string(EventExchangeUpdated)})
	s.addUnsub(a.ws.OnExchangeNotification(s.onEvent))
	s.addUnsub(a.ws.OnGap(func(int64) { s.onLoss() }))
	s.addUnsub(a.ws.OnAuthenticated(s.onAuthenticated))
	s.addUnsub(func() { a.ws.unsubscribeEvents([]string{string(EventExchangeUpdated)}) })
	return s, nil
}

func (s *RealmExchangeWatchStream) onEvent(ev RealmEvent) {
	s.emu.Lock()
	started := s.started
	s.emu.Unlock()
	if !started {
		return
	}
	s.emit(RealmExchangeUpdate{
		ObjectID:   ev.EntityID,
		ObjectPath: ev.EntityPath,
		State:      ev.ExchangeState,
	})
}

func (s *RealmExchangeWatchStream) onAuthenticated() {
	s.emu.Lock()
	started := s.started
	first := !s.haveAuth
	s.haveAuth = true
	s.emu.Unlock()
	if !started || first {
		return
	}
	s.emit(RealmExchangeUpdate{Resync: true})
}

func (s *RealmExchangeWatchStream) onLoss() {
	s.emu.Lock()
	started := s.started
	s.emu.Unlock()
	if !started {
		return
	}
	s.emit(RealmExchangeUpdate{Resync: true})
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
			s.emit(CandleUpdate{Market: ev.Market, Interval: ev.Interval, Candle: *ev.Candle})
		}
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.releaseCandles(coins, intervals) })
	return s, nil
}

// OIWatchStream streams open-interest + 24h-notional bar updates.
type OIWatchStream struct {
	*WatchStream[OIUpdate]
	coins     []string
	intervals []CandleInterval
}

// WatchOI streams live open-interest bars for the given coins and intervals.
// Tier 3 ambient: open interest moves slowly and the stream is self-correcting
// (no gap recovery), so the practical intervals are the fine-grained 1m/5m
// buckets — coarser history (1h/1d) is served by GetOIHistory. When intervals
// is empty it defaults to ["1m", "5m"].
func (a *Arca) WatchOI(ctx context.Context, coins []string, intervals []CandleInterval) (*OIWatchStream, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	if len(intervals) == 0 {
		intervals = []CandleInterval{Interval1m, Interval5m}
	}
	s := &OIWatchStream{WatchStream: newWatchStream[OIUpdate](), coins: coins, intervals: intervals}
	a.ws.acquireOI(coins, intervals)
	unsub := a.ws.OnOIUpdated(func(ev RealmEvent) {
		if ev.Bar != nil {
			s.emit(OIUpdate{Market: ev.Market, Interval: ev.Interval, Bar: *ev.Bar, IsClosed: ev.IsClosed})
		}
	})
	s.addUnsub(unsub)
	s.addUnsub(func() { a.ws.releaseOI(coins, intervals) })
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
