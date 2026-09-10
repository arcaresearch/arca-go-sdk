package arca

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func lifecycleFixture(t *testing.T) OrderLifecycle {
	t.Helper()
	var v OrderLifecycle
	if err := json.Unmarshal([]byte(lifecycleReadFixture), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func lifecyclePush(c *wsTestConn, request map[string]any, view OrderLifecycle, seq int) {
	c.send(map[string]any{"type": "order.lifecycle.updated", "watchId": request["watchId"], "requestId": request["requestId"], "realmId": view.Intent.RealmID, "objectId": "account", "operationId": "original", "leg": "0", "lifecycle": view, "deliverySeq": seq})
}
func lifecycleNext(t *testing.T, w *OrderLifecycleWatch) OrderLifecycleUpdate {
	t.Helper()
	select {
	case u, ok := <-w.Updates:
		if !ok {
			t.Fatal("watch ended")
		}
		return u
	case <-time.After(wsTestWait):
		t.Fatal("missing lifecycle update")
		return OrderLifecycleUpdate{}
	}
}

func TestOrderLifecycleWatchQuietGapRotationAndOriginalIdentity(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	conn := connectManager(t, s, m, 0)
	view := lifecycleFixture(t)
	w := newOrderLifecycleWatch(context.Background(), m, view.Intent.OrderLifecycleKey, time.Second)
	defer w.Stop()
	request := conn.waitFor("watch_order_lifecycle")
	conn.send(map[string]any{"type": "order_lifecycle_watch_created", "requestId": request["requestId"], "watchId": request["watchId"]})
	lifecyclePush(conn, request, view, 1)
	if u := lifecycleNext(t, w); u.Lifecycle == nil || u.Lifecycle.ExecutedSize != "3.123456789" || u.Lifecycle.AveragePriceFinal {
		t.Fatalf("lost partial evidence: %+v", u)
	}
	conn.expectNoAction("watch_order_lifecycle", 70*time.Millisecond)
	// The healthy frame that reveals a gap cannot cancel reconstruction.
	lifecyclePush(conn, request, view, 3)
	fresh := conn.waitFor("watch_order_lifecycle")
	if fresh["requestId"] == request["requestId"] {
		t.Fatal("reattachment did not fence old replies")
	}
	view.AccountedSize = view.ExecutedSize
	view.AccountingComplete = true
	view.AveragePriceFinal = true
	view.AveragePrice = "2001.000000001"
	lifecyclePush(conn, fresh, view, 4)
	for {
		u := lifecycleNext(t, w)
		if u.Lifecycle != nil && u.Lifecycle.AccountingComplete {
			break
		}
	}
	conn.send(map[string]any{"type": "error", "requestId": request["requestId"], "message": "stale"})
	conn.expectNoAction("watch_order_lifecycle", 70*time.Millisecond)
	if !m.RotateConnection() {
		t.Fatal("rotation refused")
	}
	replacement := s.accept()
	replacement.warmup(0)
	replacement.promote()
	request = replacement.waitFor("watch_order_lifecycle")
	if request["operationId"] != "original" || request["objectId"] != "account" || request["leg"] != "0" {
		t.Fatalf("original identity changed: %+v", request)
	}
	lifecyclePush(replacement, request, view, 1)
	if u := lifecycleNext(t, w); u.Lifecycle == nil || !u.Lifecycle.AveragePriceFinal {
		t.Fatalf("lost final evidence on replacement: %+v", u)
	}
	replacement.expectNoAction("watch_order_lifecycle", 70*time.Millisecond)
	// A new attachment may not rewrite the original requested quantity/type.
	replacement.sendAuthenticated(0)
	request = replacement.waitFor("watch_order_lifecycle")
	view.Intent.RequestedSize = "3.123456789"
	view.Intent.OrderType = "LIMIT"
	lifecyclePush(replacement, request, view, 1)
	if u := lifecycleNext(t, w); !u.Unavailable || u.Recoverable || u.Reason != "original_intent_changed" {
		t.Fatalf("changed original accepted: %+v", u)
	}
}

func TestOrderLifecycleWatchAckDeadlineRecoveryAndDispose(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	conn := connectManager(t, s, m, 0)
	view := lifecycleFixture(t)
	w := newOrderLifecycleWatch(context.Background(), m, view.Intent.OrderLifecycleKey, 100*time.Millisecond)
	request := conn.waitFor("watch_order_lifecycle")
	conn.send(map[string]any{"type": "order_lifecycle_watch_created", "requestId": request["requestId"], "watchId": request["watchId"]})
	if u := lifecycleNext(t, w); !u.Unavailable || !u.Recoverable || u.Reason != "snapshot_timeout" {
		t.Fatalf("ACK was treated as evidence: %+v", u)
	}
	request = conn.waitFor("watch_order_lifecycle")
	lifecyclePush(conn, request, view, 1)
	if u := lifecycleNext(t, w); u.Lifecycle == nil {
		t.Fatalf("failed recovery: %+v", u)
	}
	conn.expectNoAction("watch_order_lifecycle", 150*time.Millisecond)
	conn.send(map[string]any{"type": "order.lifecycle.updated", "watchId": request["watchId"], "requestId": request["requestId"], "realmId": view.Intent.RealmID, "objectId": "account", "operationId": "original", "leg": "0", "deliverySeq": 2, "unavailable": true, "recoverable": true, "reason": "source_unavailable"})
	if u := lifecycleNext(t, w); !u.Unavailable || !u.Recoverable || u.Reason != "source_unavailable" {
		t.Fatalf("lost source failure: %+v", u)
	}
	request = conn.waitFor("watch_order_lifecycle")
	lifecyclePush(conn, request, view, 3)
	if u := lifecycleNext(t, w); u.Lifecycle == nil {
		t.Fatalf("failed source recovery: %+v", u)
	}
	m.Disconnect()
	select {
	case _, ok := <-w.Updates:
		if ok {
			t.Fatal("expected closed watch")
		}
	case <-time.After(wsTestWait):
		t.Fatal("dispose leaked watch")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.lifecycleReaders) != 0 {
		t.Fatal("dispose leaked registration")
	}
}

func TestOrderLifecycleWatchRejectsForeignAndMissingFinality(t *testing.T) {
	for _, bad := range []string{"account", "finality"} {
		t.Run(bad, func(t *testing.T) {
			s := newWSTestServer(t)
			m := newTestWSManager(t, s, 0)
			conn := connectManager(t, s, m, 0)
			view := lifecycleFixture(t)
			w := newOrderLifecycleWatch(context.Background(), m, view.Intent.OrderLifecycleKey, time.Second)
			defer w.Stop()
			r := conn.waitFor("watch_order_lifecycle")
			var raw map[string]any
			if err := json.Unmarshal([]byte(lifecycleReadFixture), &raw); err != nil {
				t.Fatal(err)
			}
			if bad == "account" {
				raw["intent"].(map[string]any)["objectId"] = "foreign"
			} else {
				delete(raw, "executionQuantityFinal")
			}
			conn.send(map[string]any{"type": "order.lifecycle.updated", "watchId": r["watchId"], "requestId": r["requestId"], "realmId": view.Intent.RealmID, "objectId": "account", "operationId": "original", "leg": "0", "lifecycle": raw})
			if u := lifecycleNext(t, w); !u.Unavailable || u.Recoverable || u.Reason != "invalid_order_evidence" {
				t.Fatalf("invalid evidence accepted: %+v", u)
			}
		})
	}
}
