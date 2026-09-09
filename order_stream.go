package arca

import (
	"context"
	"encoding/json"
	"sync"
)

// orderStream owns a subscription independently of operation settlement. Local
// capture starts before submission. A terminal HTTP receipt can finish without
// waiting for its transport; nonterminal observers wait for ready then seed once.
// The replay buffer is bounded; it is progress received so far, not fill history.
type orderStream struct {
	mu           sync.Mutex
	replay       []RealmEvent
	listeners    map[int]func(RealmEvent)
	next         int
	ready        chan struct{}
	readyErr     error
	stopOnce     sync.Once
	cancel       context.CancelFunc
	off          []func()
	ws           *WebSocketManager
	objectID     string
	operationID  string
	orderID      string
	stopped      bool
	onStop       func()
	watchStarted bool
}

// captureOrderSubmission is constructed before newOperationHandle starts its
// eager HTTP goroutine. Every early validation/transport error releases capture.
func (a *Arca) captureOrderSubmission(ctx context.Context, objectID string, call func() (OrderOperationResponse, error)) (func() (OrderOperationResponse, error), orderHandleDeps) {
	stream := a.newOrderStream(ctx, objectID)
	deps := a.orderHandleDeps()
	deps.releaseExecution = stream.stop
	deps.onExecutionEvent = stream.subscribe
	deps.awaitExecutionReady = stream.awaitReady
	liveFills := deps.onFillEvent
	deps.onFillEvent = func(handler func(RealmEvent)) func() {
		live, replay := liveFills(handler), stream.subscribe(handler)
		return func() { live(); replay() }
	}
	return func() (OrderOperationResponse, error) {
		response, err := call()
		if err != nil {
			stream.stop()
		} else {
			stream.submitted(response.Operation)
		}
		return response, err
	}, deps
}

func (a *Arca) newOrderStream(ctx context.Context, objectID string) *orderStream {
	ws := a.ws
	ctx, cancel := context.WithCancel(ctx)
	s := &orderStream{listeners: map[int]func(RealmEvent){}, ready: make(chan struct{}), cancel: cancel, ws: ws, objectID: objectID}
	a.orderStreamsMu.Lock()
	if a.orderStreams == nil {
		a.orderStreams = map[*orderStream]struct{}{}
	}
	a.orderStreams[s] = struct{}{}
	a.orderStreamsMu.Unlock()
	s.onStop = func() { a.orderStreamsMu.Lock(); delete(a.orderStreams, s); a.orderStreamsMu.Unlock() }
	for _, kind := range []string{EventOperationUpdated, EventOrderUpdated, EventFillPreviewed, EventFillRecorded} {
		s.off = append(s.off, ws.On(kind, s.receive))
	}
	go func() {
		if err := a.ensureReady(ctx); err != nil {
			s.readyErr = err
		} else {
			s.watchStarted = true
			_, s.readyErr = ws.watchPath(ctx, "/")
		}

		close(s.ready)
		// A failed ACK is a transport gap, not the end of the order. Keep
		// capture and its acquired path lease for acknowledged recovery.
		<-ctx.Done()
		s.stop()
	}()
	return s
}

func (s *orderStream) stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()
		s.cancel()
		if s.onStop != nil {
			s.onStop()
		}
		for _, off := range s.off {
			off()
		}
		// watchPath has acquired its reference before ready closes. Wait before
		// release so cancellation cannot race with and leak the path reference.
		go func() {
			<-s.ready
			if s.watchStarted {
				s.ws.unwatchPath("/")
			}
		}()
	})
}

func (s *orderStream) receive(e RealmEvent) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if e.Order != nil && e.EntityID != s.objectID {
		s.mu.Unlock()
		return
	}
	if s.operationID != "" && e.Operation != nil && e.Operation.ID != s.operationID {
		s.mu.Unlock()
		return
	}
	if s.orderID != "" && e.Fill != nil && e.Fill.OrderID != s.orderID {
		s.mu.Unlock()
		return
	}
	if len(s.replay) == 256 {
		copy(s.replay, s.replay[1:])
		s.replay = s.replay[:255]
	}
	s.replay = append(s.replay, e)
	listeners := make([]func(RealmEvent), 0, len(s.listeners))
	for _, f := range s.listeners {
		listeners = append(listeners, f)
	}
	terminal := e.Order != nil && e.Order.Order.ID == s.orderID && isTerminalOrderStatus(e.Order.Order.Status)
	if terminal {
		state, _ := executionDisposition(e.Order.Order)
		terminal = state != "unknown"
	}
	if e.Operation != nil && s.operationID != "" && e.Operation.ID == s.operationID {
		_, operationTerminal := operationExecution(*e.Operation, s.objectID)
		terminal = terminal || operationTerminal || e.Operation.State == OpFailed || e.Operation.State == OpExpired
	}
	s.mu.Unlock()
	for _, f := range listeners {
		f(e)
	}
	if terminal {
		s.stop()
	}
}

func (s *orderStream) subscribe(f func(RealmEvent)) func() {
	s.mu.Lock()
	s.next++
	id := s.next
	s.listeners[id] = f
	batch := append([]RealmEvent(nil), s.replay...)
	s.mu.Unlock()
	for _, e := range batch {
		f(e)
	}
	return func() { s.mu.Lock(); delete(s.listeners, id); s.mu.Unlock() }
}
func (s *orderStream) awaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ready:
		return s.readyErr
	}
}
func (s *orderStream) submitted(op Operation) {
	receipt, terminal := operationExecution(op, s.objectID)
	s.mu.Lock()
	s.operationID = op.ID
	if terminal {
		s.orderID = receipt.Order.ID
	} else if op.Outcome != nil {
		var parsed struct {
			OrderID string `json:"orderId"`
		}
		if json.Unmarshal([]byte(*op.Outcome), &parsed) == nil {
			s.orderID = parsed.OrderID
		}
	}
	// An authoritative update may have arrived before HTTP supplied identity.
	// Recheck that bounded capture now, so an unobserved handle also releases
	// its lease instead of waiting for a duplicate terminal event.
	if !terminal {
		for _, event := range s.replay {
			if event.Operation != nil && event.Operation.ID == op.ID {
				_, terminal = operationExecution(*event.Operation, s.objectID)
				terminal = terminal || event.Operation.State == OpFailed || event.Operation.State == OpExpired
			}
			if !terminal && event.Order != nil && event.EntityID == s.objectID && s.orderID != "" && event.Order.Order.ID == s.orderID {
				_, terminal = executionUpdate(op, s.objectID, event.Order.Order)
			}
			if terminal {
				break
			}
		}
	}
	s.mu.Unlock()
	if terminal || op.State == OpFailed || op.State == OpExpired {
		s.stop()
	}
}
