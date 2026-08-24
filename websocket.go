package arca

import (
	"context"
	"encoding/json"
	"math"
	"math/rand/v2"
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
	wsReadLimit      = 16 * 1024 * 1024

	// DefaultConnectionLifetime is how long a connection is kept before it is
	// rotated. It sits below the cap the production load balancer imposes, so
	// a handoff that fails still has room for a retry or two before the cap
	// severs the connection anyway.
	DefaultConnectionLifetime = 50 * time.Minute
	// Fraction of the known lifetime at which to rotate.
	wsRotateAt = 0.85
	// Rotations are spread out by ±this fraction. Every rotation costs a
	// resubscribe, and a resubscribe costs the server a full mids snapshot — so
	// a fleet that rotated on a shared schedule would arrive as a thundering
	// herd. The jitter is what keeps the cost flat instead of spiky.
	wsRotateJitter = 0.1
	// Attempts to promote past in-flight requests before giving up and
	// retiring the connection anyway.
	wsPromoteMaxAttempts = 20
)

// Rotation timings. Vars rather than consts so tests can compress them;
// nothing in the SDK reassigns them at runtime.
var (
	// A warming connection that has not taken over within this budget is
	// abandoned.
	wsHandoffTimeout = 10 * time.Second
	// Delay before retrying a failed handoff.
	wsHandoffRetry = 60 * time.Second
	// Wait between attempts to promote past in-flight requests.
	wsPromoteRetry = 250 * time.Millisecond
)

type wsConfig struct {
	baseURL    string
	credential string
	credType   credentialType
	getRealmID func() string
	getToken   func(ctx context.Context) (string, error)
	// lifetime is the configured connection lifetime; 0 disables rotation.
	lifetime time.Duration
}

// handoff tracks a warming connection: one opened alongside the live one that
// takes over only once the server has confirmed its subscriptions are live.
// All fields are guarded by WebSocketManager.mu.
type handoff struct {
	conn *websocket.Conn
	// promoted is closed when this connection becomes the primary one.
	promoted chan struct{}
	// newGen is the generation this connection owns after promotion.
	newGen   int
	timeout  *time.Timer
	promote  *time.Timer
	attempts int
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
	rotatedList    map[int]func()
	errorList      map[int]func(error)
	nextListenerID int

	pathRefs   map[string]int
	midsAll    int
	midsCoins  map[string]int
	midsExch   string
	candleRefs map[string]map[CandleInterval]bool
	oiRefs     map[string]map[CandleInterval]bool
	tradeRefs  map[string]int

	chartWatches map[string]chartWatchReq

	// attachedWatches holds watches created out-of-band (REST
	// POST /aggregations/watch) that this connection registered for
	// delivery. Delivery is ownership-gated server-side, so these must be
	// re-attached on every reconnect or the watch goes silent.
	attachedWatches map[string]struct{}

	// eventTypeSubs holds realm event types subscribed by TYPE rather than
	// by path, so a consumer of one event class does not have to watch "/"
	// (which drags a full-realm snapshot and a realm-wide valuation watch
	// behind it). Re-issued on every reconnect.
	eventTypeSubs map[string]struct{}

	pending   map[string]pendingRequest
	nextReqID int

	lastDeliverySeq int64

	handoffState  *handoff
	rotationTimer *time.Timer
	// serverLifetime is the lifetime reported at auth. It outranks the
	// configured one: the server sits behind the proxy enforcing the cap, so it
	// is the only party that knows the real number. Reading it means retuning
	// the cap is a server-side config change rather than an SDK release.
	serverLifetime time.Duration

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
		rotatedList:  map[int]func(){},
		errorList:    map[int]func(error){},
		pathRefs:     map[string]int{},
		midsCoins:    map[string]int{},
		midsExch:     "sim",
		candleRefs:   map[string]map[CandleInterval]bool{},
		oiRefs:       map[string]map[CandleInterval]bool{},
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

// Reconnect forces the socket to drop and immediately re-establish. The new
// connection re-runs auth (fetching a fresh token via getToken when
// configured) and re-issues every active subscription, so this is the way to
// move a live session onto a new credential/identity. No-op when the manager
// is disconnected with no reconnect intent. Mirrors the Swift/Kotlin SDKs'
// reconnect().
func (m *WebSocketManager) Reconnect() {
	m.mu.Lock()
	if !m.shouldConnect {
		m.mu.Unlock()
		return
	}
	m.gen++
	gen := m.gen
	conn := m.conn
	m.conn = nil
	m.connecting = true
	m.cancelRotationLocked()
	warming := m.takeHandoffLocked()
	m.mu.Unlock()
	closeConn(warming, "reconnecting")
	closeConn(conn, "credential changed")
	go m.connectLoop(gen)
}

// Disconnect closes the connection and stops reconnecting.
func (m *WebSocketManager) Disconnect() {
	m.mu.Lock()
	m.shouldConnect = false
	m.connecting = false
	m.gen++
	conn := m.conn
	m.conn = nil
	m.cancelRotationLocked()
	warming := m.takeHandoffLocked()
	m.setStatusLocked(StatusDisconnected)
	m.mu.Unlock()
	closeConn(warming, "client disconnect")
	closeConn(conn, "client disconnect")
}

func (m *WebSocketManager) connectLoop(gen int) {
	attempt := 0
	for {
		if !m.beginAttempt(gen) {
			return
		}
		_ = m.dialAndServe(gen)
		if !m.endAttempt(gen, &attempt) {
			return
		}
	}
}

// beginAttempt reports whether this generation should dial.
func (m *WebSocketManager) beginAttempt(gen int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.shouldConnect || m.gen != gen {
		m.clearConnectingLocked(gen)
		return false
	}
	m.setStatusLocked(StatusConnecting)
	return true
}

// endAttempt records the loss of the connection this generation was serving and
// sleeps out the reconnect backoff. It reports whether the loop should keep
// going; false means another generation has taken over (a rotation, an explicit
// reconnect, or a disconnect) and this one must exit without touching state.
func (m *WebSocketManager) endAttempt(gen int, attempt *int) bool {
	m.mu.Lock()
	if !m.shouldConnect || m.gen != gen {
		m.clearConnectingLocked(gen)
		m.mu.Unlock()
		return false
	}
	// The live connection is gone, so a warming replacement for it is moot —
	// the reconnect below supersedes it.
	m.cancelRotationLocked()
	warming := m.takeHandoffLocked()
	m.setStatusLocked(StatusDisconnected)
	m.rejectPendingLocked()
	m.mu.Unlock()
	closeConn(warming, "primary disconnected")

	delay := time.Duration(math.Min(float64(time.Second)*math.Pow(2, float64(*attempt)), float64(wsMaxReconnect)))
	*attempt++
	time.Sleep(delay)
	return true
}

// clearConnectingLocked releases the connect-loop slot, but only if this
// generation still owns it — a superseding generation has its own loop running
// and clearing the flag under it would let a second one start alongside.
func (m *WebSocketManager) clearConnectingLocked(gen int) {
	if m.gen == gen {
		m.connecting = false
	}
}

func closeConn(conn *websocket.Conn, reason string) {
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, reason)
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
	conn.SetReadLimit(wsReadLimit)

	m.mu.Lock()
	if !m.shouldConnect || m.gen != gen {
		m.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "superseded")
		return nil
	}
	m.conn = conn
	m.mu.Unlock()

	if err := m.writeJSON(conn, m.authMessage()); err != nil {
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
		m.mu.Lock()
		current := m.gen == gen && m.conn == conn
		m.mu.Unlock()
		if !current {
			// A rotation (or an explicit reconnect) has already moved delivery
			// onto another connection, and that one is carrying the same
			// stream — anything still buffered here would be a second copy.
			_ = conn.Close(websocket.StatusNormalClosure, "superseded")
			return nil
		}
		m.handleMessage(data)
	}
}

// authMessage builds the auth frame, refreshing the credential first when a
// token provider is configured.
func (m *WebSocketManager) authMessage() map[string]any {
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
	return auth
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
	case "stream.resync":
		// The server announced that events for this connection were dropped
		// BEFORE they were sequenced (delivery-queue overflow under
		// backpressure), so no deliverySeq gap will ever reveal the loss —
		// this marker is the only signal. Run the same recovery as a
		// detected gap; the count is a floor of 1 (the server knows events
		// were lost, not how many). The marker carries its own deliverySeq,
		// so the sequence check runs first and stays contiguous for
		// subsequent messages. A control message: never dispatched to event
		// listeners.
		if head.DeliverySeq != nil {
			m.checkGap(*head.DeliverySeq)
		}
		m.emitGap(1)
		return
	case "authenticated":
		m.onAuthenticated(data)
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
				Market   string         `json:"market"`
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
	case "oi.updated":
		if head.DeliverySeq != nil {
			m.checkGap(*head.DeliverySeq)
		}
		var msg struct {
			Market   string         `json:"market"`
			Interval CandleInterval `json:"interval"`
			OI       *OIBar         `json:"oi"`
			IsClosed bool           `json:"isClosed"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.OI != nil {
			m.dispatch(RealmEvent{Type: EventOIUpdated, Market: msg.Market, Interval: msg.Interval, Bar: msg.OI, IsClosed: msg.IsClosed})
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

func (m *WebSocketManager) onAuthenticated(data []byte) {
	m.readServerLifetime(data)

	m.mu.Lock()
	m.lastDeliverySeq = 0
	m.setStatusLocked(StatusConnected)
	conn := m.conn
	authCbs := make([]func(), 0, len(m.authList))
	for _, cb := range m.authList {
		authCbs = append(authCbs, cb)
	}
	m.mu.Unlock()

	if conn != nil {
		m.resubscribe(conn)
	}
	m.scheduleRotation(0)

	// Notified after every subscription is re-issued so any chart-history watch
	// ids they depend on are already registered.
	for _, cb := range authCbs {
		cb()
	}
}

// resubscribe re-issues every active subscription on conn. It targets an
// explicit connection rather than the current one so a warming connection can
// be brought fully up to date before it takes over.
func (m *WebSocketManager) resubscribe(conn *websocket.Conn) {
	m.mu.Lock()
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
	var oiCoins []string
	oiIntervals := map[CandleInterval]bool{}
	for c, ivs := range m.oiRefs {
		oiCoins = append(oiCoins, c)
		for iv := range ivs {
			oiIntervals[iv] = true
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
	var attached []string
	for id := range m.attachedWatches {
		attached = append(attached, id)
	}
	var eventTypes []string
	for t := range m.eventTypeSubs {
		eventTypes = append(eventTypes, t)
	}
	m.mu.Unlock()

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
	if len(oiCoins) > 0 {
		ivs := make([]CandleInterval, 0, len(oiIntervals))
		for iv := range oiIntervals {
			ivs = append(ivs, iv)
		}
		_ = m.writeJSON(conn, map[string]any{"action": "subscribe_oi", "coins": oiCoins, "intervals": ivs})
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
	// Re-register watches created out-of-band over REST. The registry is
	// per-pod, so a reconnect that lands elsewhere answers "unknown watch"
	// — the watch is genuinely gone there and the owner recreates it.
	for _, watchID := range attached {
		_ = m.writeJSON(conn, map[string]any{"action": "attach_aggregation_watch", "watchId": watchID})
	}
	if len(eventTypes) > 0 {
		_ = m.writeJSON(conn, map[string]any{"action": "subscribe_events", "types": eventTypes})
	}
}

// ---- Gapless rotation ----
//
// Infrastructure in front of the server caps how long any connection may stay
// open, and for a WebSocket that cap is a maximum lifetime rather than an idle
// timeout — a busy connection is severed on schedule. Reaching it means an
// unplanned reconnect (backoff, TCP, TLS, auth, resubscribe) during which a
// price display holds its last value and appears frozen. Rotating first turns
// that into a handoff between two live connections.

// RotateConnection replaces the current connection with a fresh one without
// interrupting delivery.
//
// The replacement authenticates and re-issues every subscription while the
// current connection keeps streaming. Only once the server confirms those
// subscriptions are live does it take over, and only then is the old one
// closed — so there is no window in which nothing is subscribed. A failure
// anywhere along the way leaves the current connection untouched and serving,
// which makes the worst case "nothing happened".
//
// Returns false when there is no healthy connection to hand off from, or when a
// handoff is already under way.
func (m *WebSocketManager) RotateConnection() bool {
	m.mu.Lock()
	if !m.shouldConnect || m.handoffState != nil || m.status != StatusConnected || m.conn == nil {
		m.mu.Unlock()
		return false
	}
	h := &handoff{promoted: make(chan struct{})}
	m.handoffState = h
	gen := m.gen
	m.mu.Unlock()

	// Armed before the connection is dialed so a half-built one can never be
	// left hanging around unnoticed.
	t := time.AfterFunc(wsHandoffTimeout, func() {
		if m.abortHandoffIf(h) {
			m.scheduleRotation(wsHandoffRetry)
		}
	})
	m.mu.Lock()
	if m.handoffState == h {
		h.timeout = t
	} else {
		t.Stop()
	}
	m.mu.Unlock()

	go m.serveHandoff(gen, h)
	return true
}

// OnRotated fires when delivery has moved to a new connection without an
// outage (see RotateConnection). Returns an unsubscribe func.
//
// This is not a reconnect: no status change is emitted, nothing was missed, and
// there is no gap to recover. It exists for state the server holds
// per-connection and therefore cannot survive the swap — a standalone
// aggregation watch has to be re-created against the new connection, because
// the old one died with the connection it was registered on. Anything the
// manager re-issues itself (mids, candles, OI, trades, path watches,
// chart-history watches) is already handled and needs no hook.
//
// Do NOT use this to refetch history or run gap recovery; OnAuthenticated is
// the hook for that. Rotations are routine, so a refetch here multiplies into
// steady background load across every connected client. Handlers run on the
// delivery goroutine, so anything that waits on the server belongs in its own
// goroutine.
func (m *WebSocketManager) OnRotated(handler func()) func() {
	m.mu.Lock()
	id := m.nextListenerID
	m.nextListenerID++
	m.rotatedList[id] = handler
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.rotatedList, id)
		m.mu.Unlock()
	}
}

// serveHandoff warms a second connection and, if it is promoted, keeps serving
// on it as the primary read loop until it too drops.
func (m *WebSocketManager) serveHandoff(gen int, h *handoff) {
	promotedGen := m.warmAndServe(gen, h)
	if promotedGen == 0 {
		return
	}
	// This connection took over and has now dropped. It owns its generation, so
	// from here it runs the same reconnect cycle connectLoop does.
	attempt := 0
	if !m.endAttempt(promotedGen, &attempt) {
		return
	}
	m.connectLoop(promotedGen)
}

// warmAndServe dials the replacement, brings it up to date, and serves it. It
// returns the generation it was promoted under, or 0 if it never took over.
func (m *WebSocketManager) warmAndServe(gen int, h *handoff) int {
	dialCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	conn, _, err := websocket.Dial(dialCtx, m.wsURL(), nil)
	cancel()
	if err != nil {
		if m.abortHandoffIf(h) {
			m.scheduleRotation(wsHandoffRetry)
		}
		return 0
	}
	conn.SetReadLimit(wsReadLimit)

	m.mu.Lock()
	if m.handoffState != h || !m.shouldConnect || m.gen != gen {
		m.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "handoff superseded")
		return 0
	}
	h.conn = conn
	m.mu.Unlock()

	if err := m.writeJSON(conn, m.authMessage()); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "auth write failed")
		if m.abortHandoffIf(h) {
			m.scheduleRotation(wsHandoffRetry)
		}
		return 0
	}

	// The heartbeat belongs to whichever connection consumers are reading from;
	// while warming, the promotion barrier is the only ping that goes out.
	pingStop := make(chan struct{})
	defer close(pingStop)
	go func() {
		select {
		case <-pingStop:
		case <-h.promoted:
			m.heartbeat(conn, pingStop)
		}
	}()

	promotedGen := 0
	for {
		readCtx, readCancel := context.WithTimeout(context.Background(), wsStaleThreshold)
		_, data, rerr := conn.Read(readCtx)
		readCancel()

		// Resolved under the same lock promotion takes, so a message is either
		// decided before the swap (dropped — the retiring connection is still
		// carrying the same stream) or after it (dispatched — the retiring
		// connection is already closed and silenced).
		m.mu.Lock()
		promoted := m.conn == conn
		if promoted {
			promotedGen = h.newGen
		}
		mine := m.handoffState == h
		m.mu.Unlock()

		if rerr != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "read error")
			if !promoted {
				// A warming connection died before taking over. The live one
				// never stopped serving, so consumers see nothing; try again
				// later rather than escalating to the reconnect path.
				if m.abortHandoffIf(h) {
					m.scheduleRotation(wsHandoffRetry)
				}
				return 0
			}
			m.mu.Lock()
			if m.conn == conn {
				m.conn = nil
			}
			m.mu.Unlock()
			return promotedGen
		}

		if promoted {
			m.handleMessage(data)
			continue
		}
		if !mine {
			_ = conn.Close(websocket.StatusNormalClosure, "handoff superseded")
			return 0
		}
		m.handleWarmMessage(h, conn, data)
	}
}

// handleWarmMessage processes a message on a connection that has not taken over
// yet. It takes no part in event delivery or gap tracking.
func (m *WebSocketManager) handleWarmMessage(h *handoff, conn *websocket.Conn, data []byte) {
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &head) != nil {
		return
	}
	switch head.Type {
	case "error":
		if m.abortHandoffIf(h) {
			m.scheduleRotation(wsHandoffRetry)
		}
	case "authenticated":
		m.readServerLifetime(data)
		m.resubscribe(conn)
		// Queued behind the batch above; its reply is the barrier this
		// connection takes over on.
		_ = m.writeJSON(conn, map[string]any{"action": "ping"})
	case "pong":
		// The server reads one connection's messages in order, so a reply to a
		// ping queued behind the resubscribe batch proves every subscription in
		// that batch is registered — from here on, live broadcasts reach this
		// connection. That is what makes this the point where it can take over
		// without leaving a gap.
		//
		// Any snapshot those subscriptions trigger is sent asynchronously and
		// may well arrive after this pong, so it is not part of the barrier —
		// and it is not needed, because the connection being retired has been
		// delivering the same stream right up to this moment, leaving consumer
		// state current at the swap.
		m.clearHandoffTimeout(h)
		m.promoteHandoff(h)
	default:
		// The live connection is carrying this same stream, so anything
		// arriving here before the takeover duplicates something consumers
		// already have. Dropping it avoids a double dispatch and keeps gap
		// detection on a single sequence space.
	}
}

// promoteHandoff hands delivery over to the warmed connection and retires the
// current one.
func (m *WebSocketManager) promoteHandoff(h *handoff) {
	m.mu.Lock()
	// Still being the current handoff is the liveness test: the read loop
	// clears it the moment the warming connection fails, and there is no
	// cheaper "is it open" question to ask a *websocket.Conn.
	if m.handoffState != h || h.conn == nil {
		m.mu.Unlock()
		return
	}
	// Retiring the current connection rejects anything still awaiting a reply
	// on it. A rotation has slack by construction — it runs well before the
	// lifetime it exists to avoid — so wait for the reply instead of turning it
	// into a spurious failure.
	if len(m.pending) > 0 && h.attempts < wsPromoteMaxAttempts {
		h.attempts++
		if h.promote != nil {
			h.promote.Stop()
		}
		h.promote = time.AfterFunc(wsPromoteRetry, func() { m.promoteHandoff(h) })
		m.mu.Unlock()
		return
	}

	retired := m.conn
	m.conn = h.conn
	// A fresh generation retires the loop that was serving: its read failure
	// must not be read as a disconnect, and it must not schedule a competing
	// reconnect against the connection that just took over. Ownership of the
	// reconnect cycle moves with it, hence the connecting flag.
	m.gen++
	m.connecting = true
	h.newGen = m.gen
	// New connection, new sequence space. Carrying the old cursor across would
	// report a gap that did not happen.
	m.lastDeliverySeq = 0
	m.stopHandoffTimersLocked(h)
	m.handoffState = nil
	rotatedCbs := make([]func(), 0, len(m.rotatedList))
	for _, cb := range m.rotatedList {
		rotatedCbs = append(rotatedCbs, cb)
	}
	m.mu.Unlock()

	close(h.promoted)
	closeConn(retired, "rotated")
	m.scheduleRotation(0)

	// Status deliberately does not move. Delivery never stopped, so emitting
	// StatusDisconnected would put consumers into a reconnecting state and run
	// gap recovery for a gap that did not happen. State the swap genuinely
	// cannot carry over is re-established through OnRotated instead.
	for _, cb := range rotatedCbs {
		cb()
	}
}

// abortHandoffIf abandons h if it is still the current handoff, reporting
// whether it did. The live connection is left exactly as it was.
func (m *WebSocketManager) abortHandoffIf(h *handoff) bool {
	m.mu.Lock()
	if m.handoffState != h {
		m.mu.Unlock()
		return false
	}
	m.handoffState = nil
	m.stopHandoffTimersLocked(h)
	conn := h.conn
	m.mu.Unlock()
	closeConn(conn, "handoff abandoned")
	return true
}

// takeHandoffLocked detaches any warming connection and returns it for the
// caller to close once it has released the lock.
func (m *WebSocketManager) takeHandoffLocked() *websocket.Conn {
	h := m.handoffState
	if h == nil {
		return nil
	}
	m.handoffState = nil
	m.stopHandoffTimersLocked(h)
	return h.conn
}

func (m *WebSocketManager) stopHandoffTimersLocked(h *handoff) {
	if h.timeout != nil {
		h.timeout.Stop()
		h.timeout = nil
	}
	if h.promote != nil {
		h.promote.Stop()
		h.promote = nil
	}
}

func (m *WebSocketManager) clearHandoffTimeout(h *handoff) {
	m.mu.Lock()
	if m.handoffState == h && h.timeout != nil {
		h.timeout.Stop()
		h.timeout = nil
	}
	m.mu.Unlock()
}

// scheduleRotation arms the next rotation. A non-zero delay overrides the
// schedule, which is how a failed handoff retries before the lifetime it is
// racing runs out.
func (m *WebSocketManager) scheduleRotation(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelRotationLocked()
	if delay <= 0 {
		// A configured 0 is an opt-out and outranks the server's figure. The
		// server reports a real constraint, so it wins over any other
		// configured value — but it must not resurrect rotation for a caller
		// who turned it off, or the documented escape hatch would be
		// inoperative on the one fleet that advertises a cap.
		lifetime := m.cfg.lifetime
		if lifetime != 0 && m.serverLifetime > 0 {
			lifetime = m.serverLifetime
		}
		if lifetime <= 0 {
			return
		}
		base := float64(lifetime) * wsRotateAt
		spread := base * wsRotateJitter
		delay = time.Duration(base - spread + rand.Float64()*spread*2)
	}
	m.rotationTimer = time.AfterFunc(delay, func() { m.RotateConnection() })
}

func (m *WebSocketManager) cancelRotationLocked() {
	if m.rotationTimer != nil {
		m.rotationTimer.Stop()
		m.rotationTimer = nil
	}
}

// readServerLifetime adopts the connection lifetime the server reports at auth,
// which outranks the configured one.
func (m *WebSocketManager) readServerLifetime(data []byte) {
	var msg struct {
		MaxConnectionLifetimeSec *float64 `json:"maxConnectionLifetimeSec"`
	}
	if json.Unmarshal(data, &msg) != nil || msg.MaxConnectionLifetimeSec == nil {
		return
	}
	sec := *msg.MaxConnectionLifetimeSec
	if sec <= 0 || math.IsInf(sec, 0) || math.IsNaN(sec) {
		return
	}
	m.mu.Lock()
	m.serverLifetime = time.Duration(sec * float64(time.Second))
	m.mu.Unlock()
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
//
// Fires for two kinds of loss: a hole the client observed in the
// server-assigned deliverySeq (missed is exact), and a server-announced
// stream.resync marker — events dropped before they were sequenced, a loss
// no sequence check can see (missed is a floor of 1). Both mean the same
// thing for recovery: refetch.
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
	m.mu.Unlock()
	if last > 0 && seq > last+1 {
		m.emitGap(seq - last - 1)
	}
}

// emitGap fires every gap listener with the given missed count. checkGap uses
// it for detected deliverySeq holes; the stream.resync handler uses it for
// server-announced loss, where the count is a floor of 1.
func (m *WebSocketManager) emitGap(missed int64) {
	m.mu.Lock()
	cbs := make([]func(int64), 0, len(m.gapList))
	for _, cb := range m.gapList {
		cbs = append(cbs, cb)
	}
	m.mu.Unlock()
	for _, cb := range cbs {
		cb(missed)
	}
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
	m.onAuthenticatedOnce(func() { m.send(msg) })
}

// onAuthenticatedOnce runs handler on the next authenticated event and then
// removes itself. It deregisters by the id it holds rather than by an unsub
// closure the handler would have to read back, which the delivery goroutine can
// reach before the registering one has stored it.
func (m *WebSocketManager) onAuthenticatedOnce(handler func()) {
	m.mu.Lock()
	id := m.nextListenerID
	m.nextListenerID++
	m.authList[id] = func() {
		m.mu.Lock()
		_, live := m.authList[id]
		delete(m.authList, id)
		m.mu.Unlock()
		if live {
			handler()
		}
	}
	m.mu.Unlock()
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

func (m *WebSocketManager) acquireOI(coins []string, intervals []CandleInterval) {
	m.mu.Lock()
	for _, c := range coins {
		if m.oiRefs[c] == nil {
			m.oiRefs[c] = map[CandleInterval]bool{}
		}
		for _, iv := range intervals {
			m.oiRefs[c][iv] = true
		}
	}
	m.mu.Unlock()
	m.EnsureConnected()
	m.syncOI()
}

func (m *WebSocketManager) releaseOI(coins []string, intervals []CandleInterval) {
	m.mu.Lock()
	for _, c := range coins {
		if ivs := m.oiRefs[c]; ivs != nil {
			for _, iv := range intervals {
				delete(ivs, iv)
			}
			if len(ivs) == 0 {
				delete(m.oiRefs, c)
			}
		}
	}
	m.mu.Unlock()
	m.syncOI()
}

func (m *WebSocketManager) syncOI() {
	m.mu.Lock()
	if len(m.oiRefs) == 0 {
		m.mu.Unlock()
		m.send(map[string]any{"action": "unsubscribe_oi"})
		return
	}
	var coins []string
	ivSet := map[CandleInterval]bool{}
	for c, ivs := range m.oiRefs {
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
	m.send(map[string]any{"action": "subscribe_oi", "coins": coins, "intervals": ivs})
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
	m.mu.Lock()
	delete(m.attachedWatches, watchID)
	m.mu.Unlock()
	m.send(map[string]any{"action": "destroy_aggregation_watch", "watchId": watchID})
}

// attachAggregationWatch registers a watch created out-of-band (REST
// POST /aggregations/watch) for delivery on this connection.
//
// Delivery is ownership-gated server-side: a connection receives
// aggregation.updated only for watches it registered. Without this call a
// REST-created watch produces no events. The server re-authorizes the
// watch's sources against this connection's credential, so attaching can
// never widen access.
func (m *WebSocketManager) attachAggregationWatch(watchID string) {
	if watchID == "" {
		return
	}
	m.mu.Lock()
	if m.attachedWatches == nil {
		m.attachedWatches = map[string]struct{}{}
	}
	m.attachedWatches[watchID] = struct{}{}
	m.mu.Unlock()
	m.EnsureConnected()
	m.sendWhenConnected(map[string]any{"action": "attach_aggregation_watch", "watchId": watchID})
}

// detachAggregationWatch stops delivery on this connection without
// destroying the watch.
func (m *WebSocketManager) detachAggregationWatch(watchID string) {
	if watchID == "" {
		return
	}
	m.mu.Lock()
	delete(m.attachedWatches, watchID)
	m.mu.Unlock()
	m.send(map[string]any{"action": "detach_aggregation_watch", "watchId": watchID})
}

// subscribeEvents asks for realm events by TYPE, with no path watch. The
// server still applies the per-event scope backstop, so this widens which
// events arrive, never whose.
func (m *WebSocketManager) subscribeEvents(types []string) {
	if len(types) == 0 {
		return
	}
	m.mu.Lock()
	if m.eventTypeSubs == nil {
		m.eventTypeSubs = map[string]struct{}{}
	}
	for _, t := range types {
		m.eventTypeSubs[t] = struct{}{}
	}
	m.mu.Unlock()
	m.EnsureConnected()
	m.sendWhenConnected(map[string]any{"action": "subscribe_events", "types": types})
}

// unsubscribeEvents drops type-routed delivery for the given types.
func (m *WebSocketManager) unsubscribeEvents(types []string) {
	if len(types) == 0 {
		return
	}
	m.mu.Lock()
	for _, t := range types {
		delete(m.eventTypeSubs, t)
	}
	m.mu.Unlock()
	m.send(map[string]any{"action": "unsubscribe_events", "types": types})
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

// OnDepositDetected fires when money is seen arriving at a watched deposit
// address. The funds are not credited yet — OnBalanceUpdated is what says
// that — so treat this as "money is on its way", which is exactly what a
// user staring at an unchanged balance needs to be told.
func (m *WebSocketManager) OnDepositDetected(cb func(deposit *DetectedDeposit, ev RealmEvent)) func() {
	return m.On(EventDepositDetected, func(ev RealmEvent) {
		if ev.Deposit != nil {
			cb(ev.Deposit, ev)
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

func (m *WebSocketManager) OnOIUpdated(cb func(RealmEvent)) func() {
	return m.On(EventOIUpdated, cb)
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
