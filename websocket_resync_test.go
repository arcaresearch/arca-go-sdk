package arca

import (
	"testing"
	"time"
)

// stream.resync is the server announcing that events for this connection were
// dropped BEFORE they were sequenced (delivery-queue overflow under
// backpressure), so no deliverySeq gap will ever reveal the loss — the marker
// is the only signal. The manager must run the same recovery as a detected
// gap, keep the sequence space contiguous across the marker, and never leak
// it to event listeners.

func resyncMarker(seq int64) map[string]any {
	return map[string]any{"type": "stream.resync", "reason": "event_loss", "deliverySeq": seq}
}

// 1. The marker fires gap recovery with a floor of one missed event (the
// server knows events were lost, not how many).
func TestStreamResync_FiresGapCallback(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	c := connectManager(t, s, m, 0)

	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)
	gaps := make(chan int64, 8)
	m.OnGap(func(missed int64) { gaps <- missed })

	c.send(pathEvent("/a", 1))
	waitFor(t, "first event", func() bool { return events.len() == 1 })

	c.send(resyncMarker(2))

	select {
	case missed := <-gaps:
		if missed != 1 {
			t.Fatalf("gap reported %d missed, want the floor of 1", missed)
		}
	case <-time.After(wsTestWait):
		t.Fatal("stream.resync never fired the gap callback")
	}
}

// 2. The marker's own deliverySeq must be consumed: the event after it
// continues the sequence, and misreading that as a hole would trigger a
// second, spurious recovery.
func TestStreamResync_KeepsSequenceContiguous(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	c := connectManager(t, s, m, 0)

	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)
	gaps := make(chan int64, 8)
	m.OnGap(func(missed int64) { gaps <- missed })

	c.send(pathEvent("/a", 1))
	c.send(resyncMarker(2))
	c.send(pathEvent("/b", 3))
	waitFor(t, "both events", func() bool { return events.len() == 2 })

	select {
	case <-gaps:
	case <-time.After(wsTestWait):
		t.Fatal("stream.resync never fired the gap callback")
	}
	select {
	case missed := <-gaps:
		t.Fatalf("spurious second gap of %d after the marker; its deliverySeq was not consumed", missed)
	case <-time.After(150 * time.Millisecond):
	}
}

// 3. The marker is a control message, not an event: neither a type listener
// nor the wildcard may see it.
func TestStreamResync_NotDispatchedToListeners(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	c := connectManager(t, s, m, 0)

	var typed, wild eventCollector
	m.On("stream.resync", typed.add)
	m.On("*", wild.add)
	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)

	c.send(resyncMarker(1))
	c.send(pathEvent("/after", 2))
	waitFor(t, "trailing event", func() bool { return events.len() == 1 })

	// The wildcard sees exactly the one real event; the marker reaches
	// neither it nor a listener registered for the marker's own type.
	if typed.len() != 0 || wild.len() != 1 {
		t.Fatalf("stream.resync leaked to listeners (typed=%d wildcard=%d, want 0 and 1)", typed.len(), wild.len())
	}
}

// 4. Events after the drop site can themselves be lost further down the
// write path; a marker arriving with a sequence jump means both kinds of
// loss happened, and both must be reported.
func TestStreamResync_AlsoReportsSequenceGapWhenMarkerRevealsOne(t *testing.T) {
	s := newWSTestServer(t)
	m := newTestWSManager(t, s, 0)
	c := connectManager(t, s, m, 0)

	var events eventCollector
	m.On(string(EventObjectUpdated), events.add)
	gaps := make(chan int64, 8)
	m.OnGap(func(missed int64) { gaps <- missed })

	c.send(pathEvent("/a", 1))
	waitFor(t, "first event", func() bool { return events.len() == 1 })

	// The marker jumps from seq 1 to seq 5: three events vanished after
	// sequencing, plus the pre-sequencing loss the marker itself announces.
	c.send(resyncMarker(5))

	want := []int64{3, 1}
	for i, w := range want {
		select {
		case missed := <-gaps:
			if missed != w {
				t.Fatalf("gap %d reported %d missed, want %d (sequence hole first, then the resync floor)", i, missed, w)
			}
		case <-time.After(wsTestWait):
			t.Fatalf("gap %d never fired (want %d then %d)", i, want[0], want[1])
		}
	}
}
