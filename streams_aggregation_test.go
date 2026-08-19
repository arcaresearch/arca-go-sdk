package arca

import (
	"context"
	"testing"
	"time"
)

// A standalone aggregation watch lives on the connection it was created on: the
// server destroys it when that connection closes, and the manager's post-auth
// resubscribe does not cover it. Without re-creating it the stream goes
// permanently silent — with no error, which is what made it hard to spot.

// newAggTestArca builds a client against the test server with the realm already
// in id form, so no HTTP lookup is issued.
func newAggTestArca(t *testing.T, s *wsTestServer, lifetime time.Duration) *Arca {
	t.Helper()
	a, err := New(Config{
		APIKey:             "arca_test_key",
		Realm:              "rlm_01h2xcejqtf2nbrexx3vqjhp41",
		BaseURL:            s.rootURL(),
		ConnectionLifetime: &lifetime,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(a.Dispose)
	return a
}

// answerAggregationWatch replies to a create_aggregation_watch request with the
// given watch id.
func answerAggregationWatch(c *wsTestConn, watchID string) {
	req := c.waitFor("create_aggregation_watch")
	reqID, _ := req["requestId"].(string)
	c.send(map[string]any{
		"type":        "aggregation_watch_created",
		"requestId":   reqID,
		"watchId":     watchID,
		"aggregation": map[string]any{"prefix": "/", "totalEquityUsd": "0", "departingUsd": "0", "breakdown": []any{}},
	})
}

// aggregationUpdate is a server broadcast for a specific watch id.
func aggregationUpdate(watchID, equity string) map[string]any {
	return map[string]any{
		"type":     string(EventAggregationUpdated),
		"entityId": watchID,
		"aggregation": map[string]any{
			"prefix": "/", "totalEquityUsd": equity, "departingUsd": "0", "breakdown": []any{},
		},
	}
}

// openAggregationWatch starts a stream and returns it plus the connection it
// was created on.
func openAggregationWatch(t *testing.T, s *wsTestServer, a *Arca, lifetimeSec float64) (*AggregationWatchStream, *wsTestConn) {
	t.Helper()
	type result struct {
		s   *AggregationWatchStream
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := a.WatchAggregation(context.Background(), []AggregationSource{NewPrefixSource("/users")})
		done <- result{st, err}
	}()

	c := s.accept()
	c.handshake(lifetimeSec)
	answerAggregationWatch(c, "watch-1")

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("WatchAggregation: %v", r.err)
		}
		t.Cleanup(r.s.Close)
		return r.s, c
	case <-time.After(wsTestWait):
		t.Fatal("WatchAggregation never returned")
		return nil, nil
	}
}

// collectEquity reports whether the stream has emitted a given equity value.
func collectEquity(s *AggregationWatchStream) func(string) bool {
	var got []string
	ch := make(chan string, 32)
	s.OnUpdate(func(agg PathAggregation) { ch <- agg.TotalEquityUsd })
	return func(want string) bool {
		for {
			select {
			case v := <-ch:
				got = append(got, v)
			default:
				for _, v := range got {
					if v == want {
						return true
					}
				}
				return false
			}
		}
	}
}

// 16. A rotation retires the connection the watch lived on, so the watch has to
// be re-created against the replacement — and none of it may look like an
// outage to the consumer.
func TestWatchAggregation_RecreatedOnRotation(t *testing.T) {
	s := newWSTestServer(t)
	compressRotationTimings(t, 5*time.Second, time.Hour, 20*time.Millisecond)
	a := newAggTestArca(t, s, 0)

	stream, first := openAggregationWatch(t, s, a, 0)
	sawEquity := collectEquity(stream)

	var status statusRecorder
	a.ws.OnStatus(status.add)

	if !a.ws.RotateConnection() {
		t.Fatal("RotateConnection refused")
	}
	second := s.accept()
	second.warmup(0)
	second.promote()
	first.waitGone("retired connection")

	// The re-create lands on the new connection and gets a new id.
	answerAggregationWatch(second, "watch-2")
	waitFor(t, "the new watch id to be adopted", func() bool { return stream.WatchID() == "watch-2" })

	second.send(aggregationUpdate("watch-2", "42"))
	waitFor(t, "an update on the re-created watch", func() bool { return sawEquity("42") })

	if status.contains(StatusDisconnected) || status.contains(StatusConnecting) {
		t.Fatalf("rotation surfaced as a reconnect: %s", status.String())
	}
}

// 17. Regression: the same re-create must happen after an ordinary reconnect.
// Before the fix the stream went silent here forever, with no error.
func TestWatchAggregation_RecreatedAfterReconnect(t *testing.T) {
	s := newWSTestServer(t)
	a := newAggTestArca(t, s, 0)

	stream, first := openAggregationWatch(t, s, a, 0)
	sawEquity := collectEquity(stream)

	// Updates flow on the original connection.
	first.send(aggregationUpdate("watch-1", "10"))
	waitFor(t, "an update before the reconnect", func() bool { return sawEquity("10") })

	first.close()

	second := s.accept()
	second.handshake(0)
	// The server assigns a fresh id; a stream that kept filtering on the old
	// one would ignore every update from here on.
	answerAggregationWatch(second, "watch-2")
	waitFor(t, "the new watch id to be adopted", func() bool { return stream.WatchID() == "watch-2" })

	second.send(aggregationUpdate("watch-2", "77"))
	waitFor(t, "an update after the reconnect", func() bool { return sawEquity("77") })
}
