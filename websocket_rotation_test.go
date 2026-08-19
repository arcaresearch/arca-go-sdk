package arca

import (
	"context"
	"testing"
	"time"
)

// 1. The replacement is warmed alongside the live connection: the live one is
// not closed and consumers see no status movement.
func TestRotateConnection_WarmsSecondConnectionWithoutClosingFirst(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)

	var status statusRecorder
	first := connectManager(t, s, m, 0)
	m.OnStatus(status.add)

	if !m.RotateConnection() {
		t.Fatal("RotateConnection refused on a healthy connection")
	}
	second := s.accept()
	second.waitFor("auth")

	first.stillOpen("live connection during warmup")
	if got := status.snapshot(); len(got) != 0 {
		t.Fatalf("status moved during warmup: %v", got)
	}
	if m.Status() != StatusConnected {
		t.Fatalf("status = %q, want connected", m.Status())
	}
}

// 2. Every subscription is re-issued on the replacement before it takes over.
func TestRotateConnection_ReissuesSubscriptions(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	m.acquireMids("sim", []string{"hl:0:BTC"})
	m.acquireCandles([]string{"hl:0:BTC"}, []CandleInterval{"1m"})
	m.acquireOI([]string{"hl:0:BTC"}, []CandleInterval{"1m"})
	m.acquireTrades([]string{"hl:0:BTC"})
	go func() { _, _ = m.watchPath(context.Background(), "/users") }()
	first.waitFor("watch")

	if !m.RotateConnection() {
		t.Fatal("RotateConnection refused")
	}
	second := s.accept()
	reissued := second.warmup(0)

	want := []string{"subscribe_mids", "subscribe_candles", "subscribe_oi", "subscribe_trades", "watch"}
	for _, w := range want {
		found := false
		for _, got := range reissued {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("replacement never received %q; got %v", w, reissued)
		}
	}
}

// 3. The old connection is retired at promotion and delivery continues on the
// new one.
func TestRotateConnection_RetiresOldConnectionAndDeliversFromNew(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)

	m.RotateConnection()
	second := s.accept()
	second.warmup(0)
	second.promote()

	first.waitGone("retired connection")

	second.send(pathEvent("/after", 0))
	waitFor(t, "event from the new connection", func() bool { return events.len() == 1 })
	if got := events.paths(); got[0] != "/after" {
		t.Fatalf("delivered %v, want /after", got)
	}
	if m.Status() != StatusConnected {
		t.Fatalf("status = %q, want connected", m.Status())
	}
}

// 4. Events on the replacement before it takes over are dropped: the live
// connection is carrying the same stream, so dispatching them would double up.
func TestRotateConnection_DropsDuplicateEventsOnWarmingConnection(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)

	m.RotateConnection()
	second := s.accept()
	second.waitFor("auth")
	second.sendAuthenticated(0)

	// Broadcast the same event down both connections while the second is still
	// warming; only the live one should reach consumers.
	first.send(pathEvent("/dup", 0))
	second.send(pathEvent("/dup", 0))
	waitFor(t, "event from the live connection", func() bool { return events.len() >= 1 })
	time.Sleep(80 * time.Millisecond)

	if got := events.paths(); len(got) != 1 {
		t.Fatalf("delivered %v, want exactly one copy", got)
	}
}

// 5. Traffic that arrives on the retired connection after the swap never
// reaches consumers.
func TestRotateConnection_IgnoresLateTrafficFromRetiredConnection(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)

	m.RotateConnection()
	second := s.accept()
	second.warmup(0)
	second.promote()
	first.waitGone("retired connection")

	// Whether these lose the race to the close or land in the retired read
	// loop, the contract is the same: nothing from a retired connection is
	// delivered.
	for i := 0; i < 5; i++ {
		first.send(pathEvent("/late", 0))
	}
	time.Sleep(100 * time.Millisecond)

	second.send(pathEvent("/live", 0))
	waitFor(t, "event from the new connection", func() bool { return events.len() >= 1 })
	for _, p := range events.paths() {
		if p == "/late" {
			t.Fatalf("late traffic from the retired connection was delivered: %v", events.paths())
		}
	}
}

// 6. The handoff must not look like a gap. A new connection restarts the
// delivery sequence, so the cursor has to be reset with it.
func TestRotateConnection_DoesNotReportDeliveryGap(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)
	gaps := make(chan int64, 8)
	m.OnGap(func(missed int64) { gaps <- missed })

	first.send(pathEvent("/before", 1))
	waitFor(t, "pre-rotation event", func() bool { return events.len() == 1 })

	m.RotateConnection()
	second := s.accept()
	second.warmup(0)
	second.promote()
	first.waitGone("retired connection")

	// Ahead of the old cursor by more than one: carrying it across would read
	// as three missed events.
	second.send(pathEvent("/after", 5))
	waitFor(t, "post-rotation event", func() bool { return events.len() == 2 })

	select {
	case missed := <-gaps:
		t.Fatalf("reported a gap of %d across the handoff", missed)
	case <-time.After(150 * time.Millisecond):
	}
}

// 7. A rotation is not a reconnect: OnRotated fires, OnAuthenticated does not.
func TestRotateConnection_FiresOnRotatedNotOnAuthenticated(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	rotated := make(chan struct{}, 4)
	authed := make(chan struct{}, 4)
	m.OnRotated(func() { rotated <- struct{}{} })
	m.OnAuthenticated(func() { authed <- struct{}{} })

	m.RotateConnection()
	second := s.accept()
	second.warmup(0)
	second.promote()
	first.waitGone("retired connection")

	select {
	case <-rotated:
	case <-time.After(wsTestWait):
		t.Fatal("OnRotated never fired")
	}
	select {
	case <-authed:
		t.Fatal("OnAuthenticated fired for a rotation")
	case <-time.After(100 * time.Millisecond):
	}
}

// 8. A replacement that dies before taking over leaves the live connection
// exactly as it was.
func TestRotateConnection_KeepsOriginalWhenReplacementDies(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 2*time.Second, time.Hour, 20*time.Millisecond)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	var status statusRecorder
	var events eventCollector
	m.OnStatus(status.add)
	m.On(string(EventObjectUpdated), events.add)

	m.RotateConnection()
	second := s.accept()
	second.waitFor("auth")
	second.sendAuthenticated(0)
	second.close()

	waitFor(t, "handoff to be abandoned", func() bool { return !m.handoffActive() })
	first.stillOpen("live connection after a failed handoff")

	first.send(pathEvent("/still-here", 0))
	waitFor(t, "delivery on the original connection", func() bool { return events.len() == 1 })
	if got := status.snapshot(); len(got) != 0 {
		t.Fatalf("status moved for a failed handoff: %v", got)
	}
}

// 9. A replacement that never completes the handoff is abandoned on the
// timeout, and a retry is armed.
func TestRotateConnection_AbandonsReplacementOnTimeout(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 120*time.Millisecond, 120*time.Millisecond, 20*time.Millisecond)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	m.RotateConnection()
	second := s.accept()
	second.waitFor("auth")
	second.sendAuthenticated(0)
	second.waitFor("ping")
	// Deliberately never pong: the barrier is never crossed.

	second.waitGone("replacement abandoned on timeout")
	first.stillOpen("live connection after an abandoned handoff")

	// The retry is what keeps a rotation from being a one-shot: another
	// replacement must be attempted before the lifetime it is racing expires.
	third := s.accept()
	third.waitFor("auth")
}

// 10. Promotion waits for in-flight requests rather than rejecting them by
// retiring the connection they are waiting on.
func TestRotateConnection_WaitsForInFlightRequests(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 5*time.Second, time.Hour, 20*time.Millisecond)
	m := newTestWSManager(t, s, 0)
	first := connectManager(t, s, m, 0)

	type result struct {
		snap *WatchSnapshot
		err  error
	}
	done := make(chan result, 1)
	go func() {
		snap, err := m.watchPath(context.Background(), "/users")
		done <- result{snap, err}
	}()
	req := first.waitFor("watch")
	reqID, _ := req["requestId"].(string)

	m.RotateConnection()
	second := s.accept()
	second.warmup(0)
	second.promote()

	// Several promote attempts must come and go without retiring the
	// connection the reply is owed on.
	time.Sleep(120 * time.Millisecond)
	first.stillOpen("connection with an in-flight request")

	first.send(map[string]any{"type": "watch_snapshot", "requestId": reqID, "objects": []any{}})
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("in-flight request failed across the rotation: %v", r.err)
		}
	case <-time.After(wsTestWait):
		t.Fatal("in-flight request never completed")
	}

	first.waitGone("retired once the request settled")
}

// 11. Nothing to hand off from means nothing happens.
func TestRotateConnection_NoOpWhenDisconnected(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)

	if m.RotateConnection() {
		t.Fatal("RotateConnection accepted while disconnected")
	}
	s.expectNoConn(150*time.Millisecond, "rotation while disconnected")
}

// 12. A second handoff cannot start while one is under way.
func TestRotateConnection_NoParallelHandoff(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 5*time.Second, time.Hour, 20*time.Millisecond)
	m := newTestWSManager(t, s, 0)
	connectManager(t, s, m, 0)

	if !m.RotateConnection() {
		t.Fatal("first RotateConnection refused")
	}
	second := s.accept()
	second.waitFor("auth")

	if m.RotateConnection() {
		t.Fatal("second RotateConnection started a parallel handoff")
	}
	s.expectNoConn(150*time.Millisecond, "parallel handoff")
}

// 13. Rotation is armed automatically from the configured lifetime.
func TestRotateConnection_AutoRotatesOnLifetimeSchedule(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 5*time.Second, time.Hour, 20*time.Millisecond)
	m := newTestWSManager(t, s, 150*time.Millisecond)
	first := connectManager(t, s, m, 0)

	second := s.accept()
	second.warmup(0)
	second.promote()
	first.waitGone("retired by the scheduled rotation")
}

// 14. A zero lifetime opts out: the connection runs until something else ends
// it.
func TestRotateConnection_NoRotationWhenLifetimeZero(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	connectManager(t, s, m, 0)

	s.expectNoConn(300*time.Millisecond, "rotation with lifetime disabled")
}

// 14b. Opting out stays opted out even where the server advertises a cap.
// Production does advertise one, so a server value that overrode a configured 0
// would leave the documented escape hatch inoperative on the only fleet where
// anyone would reach for it.
func TestRotateConnection_LifetimeZeroIgnoresServerReportedLifetime(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 5*time.Second, time.Hour, 20*time.Millisecond)
	m := newTestWSManager(t, s, 0)
	connectManager(t, s, m, 0.2)

	s.expectNoConn(400*time.Millisecond, "rotation from the server lifetime despite being disabled")
}

// 15. The server sits behind the proxy enforcing the cap, so its number wins
// over the configured one.
func TestRotateConnection_PrefersServerReportedLifetime(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 5*time.Second, time.Hour, 20*time.Millisecond)
	// Configured lifetime would not rotate for hours; the server says 0.2s.
	m := newTestWSManager(t, s, time.Hour)
	first := connectManager(t, s, m, 0.2)

	second := s.accept()
	second.warmup(0.2)
	second.promote()
	first.waitGone("retired on the server-reported lifetime")
}

// ---- test-only accessors ----

func (m *WebSocketManager) handoffActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handoffState != nil
}
