package arca

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ConnectionStatus is the WebSocket connection lifecycle state.
type ConnectionStatus string

const (
	StatusConnecting   ConnectionStatus = "connecting"
	StatusConnected    ConnectionStatus = "connected"
	StatusDisconnected ConnectionStatus = "disconnected"
)

const (
	wsPingInterval   = 30 * time.Second
	wsStaleThreshold = 45 * time.Second
	wsMaxReconnect   = 30 * time.Second
	wsRequestTimeout = 15 * time.Second
)

type wsConfig struct {
	baseURL    string
	credential string
	credType   credentialType
	getRealmID func() string
	getToken   func(ctx context.Context) (string, error)
}

type pendingRequest struct {
	ch chan json.RawMessage
}

// WebSocketManager manages the realtime connection. It is created and owned by
// Arca and exposed as Arca.WS. Subscriptions are reference-counted; the socket
// connects lazily and reconnects forever with exponential backoff.
type WebSocketManager struct {
	cfg wsConfig

	mu            sync.Mutex
	conn          *websocket.Conn
	status        ConnectionStatus
	shouldConnect bool
	connecting    bool
	gen           int

	listeners      map[string]map[int]func(RealmEvent)
	statusList     map[int]func(ConnectionStatus)
	gapList        map[int]func(int64)
	authList       map[int]func()
	errorList      map[int]func(error)
	nextListenerID int

	pathRefs   map[string]int
	midsAll    int
	midsCoins  map[string]int
	midsExch   string
	candleRefs map[string]map[CandleInterval]bool
	tradeRefs  map[string]int

	chartWatches map[string]chartWatchReq

	pending   map[string]pendingRequest
	nextReqID int

	lastDeliverySeq int64

	writeMu sync.Mutex
}

type chartWatchReq struct {
	target   string
	kind     string
	objectID string
}

func newWebSocketManager(cfg wsConfig) *WebSocketManager {
	return &WebSocketManager{
		cfg:          cfg,
		status:       StatusDisconnected,
		listeners:    map[string]map[int]func(RealmEvent){},
		statusList:   map[int]func(ConnectionStatus){},
		gapList:      map[int]func(int64){},
		authList:     map[int]func(){},
		errorList:    map[int]func(error){},
		pathRefs:     map[string]int{},
		midsCoins:    map[string]int{},
		midsExch:     "sim",
		candleRefs:   map[string]map[CandleInterval]bool{},
		tradeRefs:    map[string]int{},
		chartWatches: map[string]chartWatchReq{},
		pending:      map[string]pendingRequest{},
	}
}

func (m *WebSocketManager) updateToken(token string) {
	m.mu.Lock()
	m.cfg.credential = token
	m.cfg.credType = credToken
	m.mu.Unlock()
}

// EnsureConnected starts the connection if it isn't already connecting or
// connected. Safe to call repeatedly.
func (m *WebSocketManager) EnsureConnected() {
	m.mu.Lock()
	if m.shouldConnect && (m.connecting || m.status == StatusConnected) {
		m.mu.Unlock()
		return
	}
	m.shouldConnect = true
	if m.connecting {
		m.mu.Unlock()
		return
	}
	m.connecting = true
	m.gen++
	gen := m.gen
	m.mu.Unlock()
	go m.connectLoop(gen)
}

// Disconnect closes the connection and stops reconnecting.
func (m *WebSocketManager) Disconnect() {
	m.mu.Lock()
	m.shouldConnect = false
	m.gen++
	conn := m.conn
	m.conn = nil
	m.setStatusLocked(StatusDisconnected)
	m.mu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "client disconnect")
	}
}

func (m *WebSocketManager) connectLoop(gen int) {
	attempt := 0
	for {
		m.mu.Lock()
		if !m.shouldConnect || m.gen != gen {
			m.connecting = false
			m.mu.Unlock()
			return
		}
		m.setStatusLocked(StatusConnecting)
		m.mu.Unlock()

		err := m.dialAndServe(gen)
		_ = err

		m.mu.Lock()
		if !m.shouldConnect || m.gen != gen {
			m.connecting = false
			m.mu.Unlock()
			return
		}
		m.setStatusLocked(StatusDisconnected)
		m.rejectPendingLocked()
		m.mu.Unlock()

		delay := time.Duration(math.Min(float64(time.Second)*math.Pow(2, float64(attempt)), float64(wsMaxReconnect)))
		attempt++
		time.Sleep(delay)
	}
}

func (m *WebSocketManager) wsURL() string {
	u := m.cfg.baseURL
	// strip trailing /api/v1
	if len(u) >= len("/api/v1") && u[len(u)-len("/api/v1"):] == "/api/v1" {
		u = u[:len(u)-len("/api/v1")]
	}
	if len(u) >= 5 && u[:5] == "https" {
		u = "wss" + u[5:]
	} else if len(u) >= 4 && u[:4] == "http" {
		u = "ws" + u[4:]
	}
	return u + "/api/v1/ws"
}

func (m *WebSocketManager) dialAndServe(gen int) error {
	dialCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	conn, _, err := websocket.Dial(dialCtx, m.wsURL(), nil)
	cancel()
	if err != nil {
		return err
	}
	conn.SetReadLimit(16 * 1024 * 1024)

	m.mu.Lock()
	if !m.shouldConnect || m.gen != gen {
		m.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "superseded")
		return nil
	}
	m.conn = conn
	m.mu.Unlock()

	// Send auth.
	m.mu.Lock()
	cred := m.cfg.credential
	credType := m.cfg.credType
	realmID := m.cfg.getRealmID()
	getToken := m.cfg.getToken
	m.mu.Unlock()
	if getToken != nil {
		if t, terr := getToken(context.Background()); terr == nil {
			cred = t
			m.mu.Lock()
			m.cfg.credential = t
			m.mu.Unlock()
		}
	}
	auth := map[string]any{"action": "auth", "realmId": realmID}
	if credType == credAPIKey {
		auth["apiKey"] = cred
	} else {
		auth["token"] = cred
	}
	if err := m.writeJSON(conn, auth); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "auth write failed")
		return err
	}

	// Heartbeat.
	pingStop := make(chan struct{})
	go m.heartbeat(conn, pingStop)
	defer close(pingStop)

	// Read loop.
	for {
		readCtx, readCancel := context.WithTimeout(context.Background(), wsStaleThreshold)
		_, data, rerr := conn.Read(readCtx)
		readCancel()
		if rerr != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "read error")
			m.mu.Lock()
			if m.conn == conn {
				m.conn = nil
			}
			m.mu.Unlock()
			return rerr
		}
		m.handleMessage(data)
	}
}

func (m *WebSocketManager) heartbeat(conn *websocket.Conn, stop <-chan struct{}) {
	t := time.NewTicker(wsPingInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			_ = m.writeJSON(conn, map[string]any{"action": "ping"})
		}
	}
}

func (m *WebSocketManager) writeJSON(conn *websocket.Conn, msg any) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, raw)
}

// send writes a message on the current connection if connected.
func (m *WebSocketManager) send(msg any) {
	m.mu.Lock()
	conn := m.conn
	connected := m.status == StatusConnected
	m.mu.Unlock()
	if conn != nil && connected {
		_ = m.writeJSON(conn, msg)
	}
}

func (m *WebSocketManager) handleMessage(data []byte) {
	var head struct {
		Type        string `json:"type"`
		RequestID   string `json:"requestId"`
		DeliverySeq *int64 `json:"deliverySeq"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return
	}

	switch head.Type {
	case "pong":
		return
	case "authenticated":
		m.onAuthenticated()
		return
	case "error":
		if head.RequestID != "" && m.resolvePending(head.RequestID, data) {
			return
		}
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		m.emitError(&ArcaError{Code: "WS_ERROR", Message: e.Message})
		return
	case "aggregation_watch_created", "chart_snapshot_watch_created", "watch_snapshot":
		if head.RequestID != "" {
			m.resolvePending(head.RequestID, data)
		}
		if head.Type == "watch_snapshot" {
			var ev RealmEvent
			if json.Unmarshal(data, &ev) == nil {
				ev.Type = "watch_snapshot"
				m.dispatch(ev)
			}
		}
		return
	case "mids.snapshot":
		var s struct {
			Mids map[string]string `json:"mids"`
		}
		if json.Unmarshal(data, &s) == nil && s.Mids != nil {
			m.dispatch(RealmEvent{Type: EventMidsUpdated, Mids: s.Mids})
		}
		return
	case "candles.updated":
		if head.DeliverySeq != nil {
			m.checkGap(*head.DeliverySeq)
		}
		var batch struct {
			Candles []struct {
				Market     string         `json:"coin"`
				Interval CandleInterval `json:"interval"`
				Candle   *Candle        `json:"candle"`
			} `json:"candles"`
		}
		if json.Unmarshal(data, &batch) == nil {
			for _, it := range batch.Candles {
				m.dispatch(RealmEvent{Type: EventCandleUpdated, Market: it.Market, Interval: it.Interval, Candle: it.Candle})
			}
		}
		return
	case "trades.batch":
		if head.DeliverySeq != nil {
			m.checkGap(*head.DeliverySeq)
		}
		var batch struct {
			Trades []MarketTrade `json:"trades"`
		}
		if json.Unmarshal(data, &batch) == nil {
			for i := range batch.Trades {
				t := batch.Trades[i]
				m.dispatch(RealmEvent{Type: EventTradeExecuted, Market: t.Market, Trade: &t})
			}
		}
		return
	}

	if head.DeliverySeq != nil {
		m.checkGap(*head.DeliverySeq)
	}
	var ev RealmEvent
	if json.Unmarshal(data, &ev) == nil {
		m.dispatch(ev)
	}
}

func (m *WebSocketManager) onAuthenticated() {
	m.mu.Lock()
	m.lastDeliverySeq = 0
	m.setStatusLocked(StatusConnected)
	conn := m.conn
	// Resubscribe from ref state.
	desiredMids, midsOK := m.midsSubscriptionCoinsLocked()
	midsExch := m.midsExch
	var candleCoins []string
	candleIntervals := map[CandleInterval]bool{}
	for c, ivs := range m.candleRefs {
		candleCoins = append(candleCoins, c)
		for iv := range ivs {
			candleIntervals[iv] = true
		}
	}
	var tradeCoins []string
	for c := range m.tradeRefs {
		tradeCoins = append(tradeCoins, c)
	}
	var paths []string
	for p := range m.pathRefs {
		paths = append(paths, p)
	}
	chartWatches := map[string]chartWatchReq{}
	for k, v := range m.chartWatches {
		chartWatches[k] = v
	}
	authCbs := make([]func(), 0, len(m.authList))
	for _, cb := range m.authList {
		authCbs = append(authCbs, cb)
	}
	m.mu.Unlock()

	if conn != nil {
		if midsOK {
			_ = m.writeJSON(conn, map[string]any{"action": "subscribe_mids", "exchange": midsExch, "coins": desiredMids})
		}
		if len(candleCoins) > 0 {
			ivs := make([]CandleInterval, 0, len(candleIntervals))
			for iv := range candleIntervals {
				ivs = append(ivs, iv)
			}
			_ = m.writeJSON(conn, map[string]any{"action": "subscribe_candles", "coins": candleCoins, "intervals": ivs, "batch": true})
		}
		if len(tradeCoins) > 0 {
			_ = m.writeJSON(conn, map[string]any{"action": "subscribe_trades", "coins": tradeCoins})
		}
		for _, p := range paths {
			_ = m.writeJSON(conn, map[string]any{"action": "watch", "path": p, "requestId": m.newRequestID()})
		}
		for watchID, req := range chartWatches {
			_ = m.writeJSON(conn, map[string]any{"action": "watch_chart_history", "watchId": watchID, "target": req.target, "kind": req.kind, "objectId": req.objectID})
		}
	}

	for _, cb := range authCbs {
		cb()
	}
}

// ---- Listeners ----

// On registers a handler for an event type and returns an unsubscribe func.
func (m *WebSocketManager) On(eventType string, handler func(RealmEvent)) func() {
	m.mu.Lock()
	id := m.nextListenerID
	m.nextListenerID++
	if m.listeners[eventType] == nil {
		m.listeners[eventType] = map[int]func(RealmEvent){}
	}
	m.listeners[eventType][id] = handler
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if set := m.listeners[eventType]; set != nil {
			delete(set, id)
			if len(set) == 0 {
				delete(m.listeners, eventType)
			}
		}
		m.mu.Unlock()
	}
}

// OnStatus registers a connection-status listener; returns an unsubscribe func.
func (m *WebSocketManager) OnStatus(handler func(ConnectionStatus)) func() {
	m.mu.Lock()
	id := m.nextListenerID
	m.nextListenerID++
	m.statusList[id] = handler
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.statusList, id)
		m.mu.Unlock()
	}
}

// OnGap registers a delivery-sequence gap listener (receives the count of
// missed events). Watch streams use this to trigger targeted refetches.
func (m *WebSocketManager) OnGap(handler func(missed int64)) func() {
	m.mu.Lock()
	id := m.nextListenerID
	m.nextListenerID++
	m.gapList[id] = handler
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.gapList, id)
		m.mu.Unlock()
	}
}

// OnAuthenticated fires on every successful (re)authentication.
func (m *WebSocketManager) OnAuthenticated(handler func()) func() {
	m.mu.Lock()
	id := m.nextListenerID
	m.nextListenerID++
	m.authList[id] = handler
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.authList, id)
		m.mu.Unlock()
	}
}

// OnError registers an error listener.
func (m *WebSocketManager) OnError(handler func(error)) func() {
	m.mu.Lock()
	id := m.nextListenerID
	m.nextListenerID++
	m.errorList[id] = handler
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.errorList, id)
		m.mu.Unlock()
	}
}

// EmitLocal dispatches a synthetic event to local listeners (used for
// optimistic fills).
func (m *WebSocketManager) EmitLocal(event RealmEvent) { m.dispatch(event) }

func (m *WebSocketManager) dispatch(event RealmEvent) {
	m.mu.Lock()
	var handlers []func(RealmEvent)
	for _, h := range m.listeners[event.Type] {
		handlers = append(handlers, h)
	}
	for _, h := range m.listeners["*"] {
		handlers = append(handlers, h)
	}
	m.mu.Unlock()
	for _, h := range handlers {
		h(event)
	}
}

func (m *WebSocketManager) setStatusLocked(s ConnectionStatus) {
	if s == m.status {
		return
	}
	m.status = s
	cbs := make([]func(ConnectionStatus), 0, len(m.statusList))
	for _, cb := range m.statusList {
		cbs = append(cbs, cb)
	}
	go func() {
		for _, cb := range cbs {
			cb(s)
		}
	}()
}

func (m *WebSocketManager) emitError(err error) {
	m.mu.Lock()
	cbs := make([]func(error), 0, len(m.errorList))
	for _, cb := range m.errorList {
		cbs = append(cbs, cb)
	}
	m.mu.Unlock()
	for _, cb := range cbs {
		cb(err)
	}
}

func (m *WebSocketManager) checkGap(seq int64) {
	m.mu.Lock()
	last := m.lastDeliverySeq
	m.lastDeliverySeq = seq
	var cbs []func(int64)
	if last > 0 && seq > last+1 {
		missed := seq - last - 1
		for _, cb := range m.gapList {
			cbs = append(cbs, cb)
		}
		m.mu.Unlock()
		for _, cb := range cbs {
			cb(missed)
		}
		return
	}
	m.mu.Unlock()
}

// ---- Request/response ----

func (m *WebSocketManager) newRequestID() string {
	m.mu.Lock()
	m.nextReqID++
	id := strconv.Itoa(m.nextReqID)
	m.mu.Unlock()
	return id
}

func (m *WebSocketManager) registerPending(requestID string) chan json.RawMessage {
	ch := make(chan json.RawMessage, 1)
	m.mu.Lock()
	m.pending[requestID] = pendingRequest{ch: ch}
	m.mu.Unlock()
	return ch
}

func (m *WebSocketManager) resolvePending(requestID string, data []byte) bool {
	m.mu.Lock()
	p, ok := m.pending[requestID]
	if ok {
		delete(m.pending, requestID)
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	cp := make(json.RawMessage, len(data))
	copy(cp, data)
	p.ch <- cp
	return true
}

func (m *WebSocketManager) rejectPendingLocked() {
	for id, p := range m.pending {
		close(p.ch)
		delete(m.pending, id)
	}
}

// ---- Path watch ----

func (m *WebSocketManager) watchPath(ctx context.Context, path string) (*WatchSnapshot, error) {
	m.mu.Lock()
	m.pathRefs[path] = m.pathRefs[path] + 1
	m.mu.Unlock()
	m.EnsureConnected()

	reqID := "watch-" + m.newRequestID()
	ch := m.registerPending(reqID)
	m.sendWhenConnected(map[string]any{"action": "watch", "path": path, "requestId": reqID})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case raw, ok := <-ch:
		if !ok {
			return nil, &ArcaError{Code: "WS_DISCONNECTED", Message: "websocket disconnected"}
		}
		var snap WatchSnapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			return nil, err
		}
		return &snap, nil
	case <-time.After(wsRequestTimeout):
		return nil, &ArcaError{Code: "WS_TIMEOUT", Message: "timeout waiting for watch snapshot"}
	}
}

func (m *WebSocketManager) unwatchPath(path string) {
	m.mu.Lock()
	cur := m.pathRefs[path]
	if cur <= 1 {
		delete(m.pathRefs, path)
		m.mu.Unlock()
		m.send(map[string]any{"action": "unwatch", "path": path})
		return
	}
	m.pathRefs[path] = cur - 1
	m.mu.Unlock()
}

// sendWhenConnected sends immediately if connected, otherwise once on the next
// authenticated event.
func (m *WebSocketManager) sendWhenConnected(msg any) {
	m.mu.Lock()
	connected := m.status == StatusConnected
	m.mu.Unlock()
	if connected {
		m.send(msg)
		return
	}
	var unsub func()
	unsub = m.OnAuthenticated(func() {
		m.send(msg)
		if unsub != nil {
			unsub()
		}
	})
}

// ---- Mids / candles / trades subscriptions ----

func (m *WebSocketManager) acquireMids(exchange string, coins []string) {
	m.mu.Lock()
	m.midsExch = exchange
	if len(coins) == 0 {
		m.midsAll++
	} else {
		for _, c := range coins {
			m.midsCoins[c]++
		}
	}
	m.mu.Unlock()
	m.EnsureConnected()
	m.syncMids()
}

func (m *WebSocketManager) releaseMids(coins []string) {
	m.mu.Lock()
	if len(coins) == 0 {
		if m.midsAll > 0 {
			m.midsAll--
		}
	} else {
		for _, c := range coins {
			if m.midsCoins[c] <= 1 {
				delete(m.midsCoins, c)
			} else {
				m.midsCoins[c]--
			}
		}
	}
	m.mu.Unlock()
	m.syncMids()
}

func (m *WebSocketManager) midsSubscriptionCoinsLocked() ([]string, bool) {
	if m.midsAll > 0 {
		return []string{}, true
	}
	if len(m.midsCoins) > 0 {
		coins := make([]string, 0, len(m.midsCoins))
		for c := range m.midsCoins {
			coins = append(coins, c)
		}
		sort.Strings(coins)
		return coins, true
	}
	return nil, false
}

func (m *WebSocketManager) syncMids() {
	m.mu.Lock()
	coins, ok := m.midsSubscriptionCoinsLocked()
	exch := m.midsExch
	m.mu.Unlock()
	if !ok {
		m.send(map[string]any{"action": "unsubscribe_mids"})
		return
	}
	m.send(map[string]any{"action": "subscribe_mids", "exchange": exch, "coins": coins})
}

func (m *WebSocketManager) acquireCandles(coins []string, intervals []CandleInterval) {
	m.mu.Lock()
	for _, c := range coins {
		if m.candleRefs[c] == nil {
			m.candleRefs[c] = map[CandleInterval]bool{}
		}
		for _, iv := range intervals {
			m.candleRefs[c][iv] = true
		}
	}
	m.mu.Unlock()
	m.EnsureConnected()
	m.syncCandles()
}

func (m *WebSocketManager) releaseCandles(coins []string, intervals []CandleInterval) {
	m.mu.Lock()
	for _, c := range coins {
		if ivs := m.candleRefs[c]; ivs != nil {
			for _, iv := range intervals {
				delete(ivs, iv)
			}
			if len(ivs) == 0 {
				delete(m.candleRefs, c)
			}
		}
	}
	m.mu.Unlock()
	m.syncCandles()
}

func (m *WebSocketManager) syncCandles() {
	m.mu.Lock()
	if len(m.candleRefs) == 0 {
		m.mu.Unlock()
		m.send(map[string]any{"action": "unsubscribe_candles"})
		return
	}
	var coins []string
	ivSet := map[CandleInterval]bool{}
	for c, ivs := range m.candleRefs {
		coins = append(coins, c)
		for iv := range ivs {
			ivSet[iv] = true
		}
	}
	m.mu.Unlock()
	ivs := make([]CandleInterval, 0, len(ivSet))
	for iv := range ivSet {
		ivs = append(ivs, iv)
	}
	m.send(map[string]any{"action": "subscribe_candles", "coins": coins, "intervals": ivs, "batch": true})
}

func (m *WebSocketManager) acquireTrades(coins []string) {
	m.mu.Lock()
	for _, c := range coins {
		m.tradeRefs[c]++
	}
	m.mu.Unlock()
	m.EnsureConnected()
	m.syncTrades()
}

func (m *WebSocketManager) releaseTrades(coins []string) {
	m.mu.Lock()
	for _, c := range coins {
		if m.tradeRefs[c] <= 1 {
			delete(m.tradeRefs, c)
		} else {
			m.tradeRefs[c]--
		}
	}
	m.mu.Unlock()
	m.syncTrades()
}

func (m *WebSocketManager) syncTrades() {
	m.mu.Lock()
	if len(m.tradeRefs) == 0 {
		m.mu.Unlock()
		m.send(map[string]any{"action": "unsubscribe_trades"})
		return
	}
	coins := make([]string, 0, len(m.tradeRefs))
	for c := range m.tradeRefs {
		coins = append(coins, c)
	}
	m.mu.Unlock()
	m.send(map[string]any{"action": "subscribe_trades", "coins": coins})
}

// ---- Aggregation watch ----

func (m *WebSocketManager) createAggregationWatch(ctx context.Context, sources []AggregationSource, flowsSince string) (string, PathAggregation, error) {
	m.EnsureConnected()
	reqID := m.newRequestID()
	ch := m.registerPending(reqID)
	msg := map[string]any{"action": "create_aggregation_watch", "sources": sources, "requestId": reqID}
	if flowsSince != "" {
		msg["flowsSince"] = flowsSince
	}
	m.sendWhenConnected(msg)

	select {
	case <-ctx.Done():
		return "", PathAggregation{}, ctx.Err()
	case raw, ok := <-ch:
		if !ok {
			return "", PathAggregation{}, &ArcaError{Code: "WS_DISCONNECTED", Message: "websocket disconnected"}
		}
		var resp struct {
			WatchID     string          `json:"watchId"`
			Aggregation PathAggregation `json:"aggregation"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return "", PathAggregation{}, err
		}
		return resp.WatchID, resp.Aggregation, nil
	case <-time.After(10 * time.Second):
		return "", PathAggregation{}, &ArcaError{Code: "WS_TIMEOUT", Message: "timeout creating aggregation watch"}
	}
}

func (m *WebSocketManager) destroyAggregationWatch(watchID string) {
	m.send(map[string]any{"action": "destroy_aggregation_watch", "watchId": watchID})
}

func (m *WebSocketManager) createChartHistoryWatch(ctx context.Context, target, kind, objectID string) (string, error) {
	m.EnsureConnected()
	reqID := "chart-" + m.newRequestID()
	watchID := "chart-" + m.newRequestID()
	if kind == "" {
		kind = "path"
	}
	m.mu.Lock()
	m.chartWatches[watchID] = chartWatchReq{target: target, kind: kind, objectID: objectID}
	m.mu.Unlock()

	ch := m.registerPending(reqID)
	m.sendWhenConnected(map[string]any{"action": "watch_chart_history", "requestId": reqID, "watchId": watchID, "target": target, "kind": kind, "objectId": objectID})
	select {
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.chartWatches, watchID)
		m.mu.Unlock()
		return "", ctx.Err()
	case <-ch:
		return watchID, nil
	case <-time.After(10 * time.Second):
		m.mu.Lock()
		delete(m.chartWatches, watchID)
		m.mu.Unlock()
		return "", &ArcaError{Code: "WS_TIMEOUT", Message: "timeout creating chart history watch"}
	}
}

func (m *WebSocketManager) destroyChartHistoryWatch(watchID string) {
	m.mu.Lock()
	delete(m.chartWatches, watchID)
	m.mu.Unlock()
	m.send(map[string]any{"action": "unwatch_chart_history", "watchId": watchID})
}

// ---- Typed convenience listeners ----

func (m *WebSocketManager) OnOperationUpdated(cb func(*Operation, RealmEvent)) func() {
	return m.On(EventOperationUpdated, func(ev RealmEvent) {
		if ev.Operation != nil {
			cb(ev.Operation, ev)
		}
	})
}

func (m *WebSocketManager) OnBalanceUpdated(cb func(entityID string, ev RealmEvent)) func() {
	return m.On(EventBalanceUpdated, func(ev RealmEvent) {
		if ev.EntityID != "" {
			cb(ev.EntityID, ev)
		}
	})
}

func (m *WebSocketManager) OnAggregationUpdated(cb func(watchID string, agg *PathAggregation, ev RealmEvent)) func() {
	return m.On(EventAggregationUpdated, func(ev RealmEvent) {
		if ev.EntityID != "" {
			cb(ev.EntityID, ev.Aggregation, ev)
		}
	})
}

func (m *WebSocketManager) OnObjectValuation(cb func(path string, val *ObjectValuation, watchID string, ev RealmEvent)) func() {
	return m.On(EventObjectValuation, func(ev RealmEvent) {
		if ev.Path != "" && ev.WatchID != "" {
			cb(ev.Path, ev.Valuation, ev.WatchID, ev)
		}
	})
}

func (m *WebSocketManager) OnFillPreviewed(cb func(*SimFill, RealmEvent)) func() {
	return m.On(EventFillPreviewed, func(ev RealmEvent) {
		if ev.Fill != nil {
			cb(ev.Fill, ev)
		}
	})
}

// OnExchangeFill is deprecated: use OnFillPreviewed. The underlying event was
// renamed from "exchange.fill" to "fill.previewed" pre-launch.
func (m *WebSocketManager) OnExchangeFill(cb func(*SimFill, RealmEvent)) func() {
	return m.OnFillPreviewed(cb)
}

func (m *WebSocketManager) OnFillRecorded(cb func(RealmEvent)) func() {
	return m.On(EventFillRecorded, cb)
}

func (m *WebSocketManager) OnExchangeFunding(cb func(*FundingPayment, RealmEvent)) func() {
	return m.On(EventExchangeFunding, func(ev RealmEvent) {
		if ev.Funding != nil {
			cb(ev.Funding, ev)
		}
	})
}

func (m *WebSocketManager) OnExchangeNotification(cb func(RealmEvent)) func() {
	return m.On(EventExchangeUpdated, cb)
}

func (m *WebSocketManager) OnMidsUpdated(cb func(map[string]string)) func() {
	return m.On(EventMidsUpdated, func(ev RealmEvent) {
		if ev.Mids != nil {
			cb(ev.Mids)
		}
	})
}

func (m *WebSocketManager) OnCandleUpdated(cb func(RealmEvent)) func() {
	return m.On(EventCandleUpdated, cb)
}

func (m *WebSocketManager) OnTradeExecuted(cb func(MarketTrade)) func() {
	return m.On(EventTradeExecuted, func(ev RealmEvent) {
		if ev.Trade != nil {
			cb(*ev.Trade)
		}
	})
}

// Status returns the current connection status.
func (m *WebSocketManager) Status() ConnectionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}
