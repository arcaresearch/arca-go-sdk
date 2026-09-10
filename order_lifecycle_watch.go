package arca

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type OrderLifecycleUpdate struct {
	Lifecycle   *OrderLifecycle
	Unavailable bool
	Recoverable bool
	Reason      string
}

// OrderLifecycleWatch owns an attachment to an existing operation. Updates are
// complete server views; a slow reader may see only the latest view. It never
// places orders or derives fills/balances from client-side observations.
type OrderLifecycleWatch struct {
	Updates <-chan OrderLifecycleUpdate
	cancel  context.CancelFunc
}

func (w *OrderLifecycleWatch) Stop() { w.cancel() }

type lifecycleFrame struct {
	OrderLifecycleKey
	Type        string          `json:"type"`
	WatchID     string          `json:"watchId"`
	RequestID   string          `json:"requestId"`
	Lifecycle   *OrderLifecycle `json:"lifecycle"`
	Unavailable bool            `json:"unavailable"`
	Recoverable bool            `json:"recoverable"`
	Reason      string          `json:"reason"`
	Message     string          `json:"message"`
}
type lifecycleRegistration struct {
	requestID string
	receive   func(lifecycleFrame)
	stop      context.CancelFunc
}

func (m *WebSocketManager) deliverOrderLifecycle(data []byte, kind, requestID string, seq *int64) bool {
	if kind != "order.lifecycle.updated" && kind != "order_lifecycle_watch_created" && !(kind == "error" && strings.HasPrefix(requestID, "order-lifecycle-")) {
		return false
	}
	if seq != nil {
		m.checkGap(*seq)
	}
	m.mu.Lock()
	var receive func(lifecycleFrame)
	for _, r := range m.lifecycleReaders {
		if r.requestID != "" && r.requestID == requestID {
			receive = r.receive
			break
		}
	}
	m.mu.Unlock()
	if receive == nil {
		return true // Stale replies cannot fail the current watch or connection.
	}
	var frame lifecycleFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		receive(lifecycleFrame{Type: "error", RequestID: requestID, Message: "invalid_order_evidence"})
	} else {
		receive(frame)
	}
	return true
}

// WatchOrderLifecycle resolves an original path when necessary, then subscribes
// by immutable operation ID and leg. Gaps/reattachment never repeat placement.
func (a *Arca) WatchOrderLifecycle(ctx context.Context, ref OriginalOrderReference) (*OrderLifecycleWatch, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	if ref.OperationID == "" {
		view, err := a.GetOrderLifecycle(ctx, ref)
		if err != nil {
			return nil, err
		}
		ref.OperationID, ref.OperationPath = view.Intent.OperationID, ""
	}
	if ref.OperationPath != "" || ref.ObjectID == "" || len(ref.ObjectID) > 128 || len(ref.OperationID) > 128 || ref.Leg < 0 {
		return nil, fmt.Errorf("an account and original operation identity are required")
	}
	key := OrderLifecycleKey{RealmID: a.currentRealmID(), ObjectID: ref.ObjectID, OperationID: ref.OperationID, Leg: strconv.Itoa(ref.Leg)}
	return newOrderLifecycleWatch(ctx, a.ws, key, 45*time.Second), nil
}

func newOrderLifecycleWatch(ctx context.Context, ws *WebSocketManager, key OrderLifecycleKey, snapshotTimeout time.Duration) *OrderLifecycleWatch {
	ctx, cancel := context.WithCancel(ctx)
	updates := make(chan OrderLifecycleUpdate, 1)
	w := &OrderLifecycleWatch{Updates: updates, cancel: cancel}
	frames := make(chan lifecycleFrame, 32)
	refresh := make(chan struct{}, 1)
	signal := func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}
	registration := lifecycleRegistration{stop: cancel, receive: func(f lifecycleFrame) {
		select {
		case frames <- f:
		default:
			signal()
		}
	}}
	id := "order-lifecycle-" + ws.newRequestID()
	ws.mu.Lock()
	if ws.lifecycleReaders == nil {
		ws.lifecycleReaders = map[string]lifecycleRegistration{}
	}
	ws.lifecycleReaders[id] = registration
	ws.mu.Unlock()
	off := []func(){ws.OnAuthenticated(signal), ws.OnRotated(signal), ws.OnGap(func(int64) { signal() })}
	go func() {
		var deadline, retry *time.Timer
		var deadlineC, retryC <-chan time.Time
		clearDeadline := func() {
			if deadline != nil {
				deadline.Stop()
			}
			deadline = nil
			deadlineC = nil
		}
		clearRetry := func() {
			if retry != nil {
				retry.Stop()
			}
			retry = nil
			retryC = nil
		}
		defer func() {
			cancel()
			clearDeadline()
			clearRetry()
			for _, stop := range off {
				stop()
			}
			ws.mu.Lock()
			delete(ws.lifecycleReaders, id)
			ws.mu.Unlock()
			ws.send(map[string]any{"action": "unwatch_order_lifecycle", "watchId": id})
			close(updates)
		}()
		publish := func(u OrderLifecycleUpdate) {
			select {
			case updates <- u:
			default:
				select {
				case <-updates:
				default:
				}
				select {
				case updates <- u:
				case <-ctx.Done():
				}
			}
		}
		delay := 250 * time.Millisecond
		unavailable := func(reason string, recoverable bool) {
			clearDeadline()
			publish(OrderLifecycleUpdate{Unavailable: true, Recoverable: recoverable, Reason: reason})
			if recoverable && retry == nil {
				retry = time.NewTimer(delay)
				retryC = retry.C
				delay *= 2
				if delay > 10*time.Second {
					delay = 10 * time.Second
				}
			}
		}
		requestID := ""
		attach := func() {
			if ctx.Err() != nil {
				return
			}
			clearDeadline()
			clearRetry()
			requestID = id + "/" + ws.newRequestID()
			registration.requestID = requestID
			ws.mu.Lock()
			if ctx.Err() != nil {
				ws.mu.Unlock()
				return
			}
			if ws.lifecycleReaders == nil {
				ws.lifecycleReaders = map[string]lifecycleRegistration{}
			}
			ws.lifecycleReaders[id] = registration
			conn, connected := ws.conn, ws.status == StatusConnected
			ws.mu.Unlock()
			deadline = time.NewTimer(snapshotTimeout)
			deadlineC = deadline.C
			if conn == nil || !connected {
				return
			}
			if err := ws.writeJSON(conn, map[string]any{"action": "watch_order_lifecycle", "watchId": id, "requestId": requestID, "objectId": key.ObjectID, "operationId": key.OperationID, "leg": key.Leg}); err != nil {
				unavailable("connection_unavailable", true)
				return
			}
		}
		var original *OrderLifecycleIntent
		ws.EnsureConnected()
		attach()
		for {
			select {
			case <-ctx.Done():
				return
			case <-refresh:
				attach()
			case <-retryC:
				retry = nil
				retryC = nil
				attach()
			case <-deadlineC:
				unavailable("snapshot_timeout", true)
			case frame := <-frames:
				if frame.RequestID != requestID {
					continue
				}
				if frame.Type == "order_lifecycle_watch_created" {
					continue
				}
				if frame.Type == "error" {
					unavailable(frame.Message, false)
					return
				}
				if frame.WatchID != id || frame.OrderLifecycleKey != key {
					continue
				}
				if frame.Unavailable {
					unavailable(frame.Reason, frame.Recoverable)
					if !frame.Recoverable {
						return
					}
					continue
				}
				if frame.Lifecycle == nil || validateLifecycle(*frame.Lifecycle, key) != nil {
					unavailable("invalid_order_evidence", false)
					return
				}
				if original != nil && *original != frame.Lifecycle.Intent {
					unavailable("original_intent_changed", false)
					return
				}
				copyIntent := frame.Lifecycle.Intent
				original = &copyIntent
				clearDeadline()
				delay = 250 * time.Millisecond
				// A good frame never cancels pending gap repair.
				publish(OrderLifecycleUpdate{Lifecycle: frame.Lifecycle})
			}
		}
	}()
	return w
}
