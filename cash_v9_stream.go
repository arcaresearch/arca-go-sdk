package arca

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Wallet Account stream: one SSE connection per wallet that delivers a
// complete CashV9WalletAccount snapshot on connect and after every change to
// the boundary or to the linked external address. The stream is
// complete-snapshot, not delta — a client that misses frames loses latency,
// never state. `id:` is the snapshot's Revision; a reconnect sends it as
// Last-Event-ID and receives exactly one fresh snapshot.
//
// Wire contract: documents/contracts/v9-cash-wallet-integration.md ("Streams").

// CashV9WalletAccountStreamDisconnectedError: the connection ended (EOF, a
// network error, or the ingress's periodic reset). LastRevision is the
// revision of the last snapshot delivered; resume from it.
type CashV9WalletAccountStreamDisconnectedError struct {
	LastRevision uint64
	Cause        error
}

func (e *CashV9WalletAccountStreamDisconnectedError) Error() string {
	return fmt.Sprintf("arca: wallet account stream disconnected (last revision %d): %v", e.LastRevision, e.Cause)
}

func (e *CashV9WalletAccountStreamDisconnectedError) Unwrap() error { return e.Cause }

// StreamCashV9WalletAccount opens the stream for one boundary and calls
// accept for every snapshot until the connection ends or accept returns an
// error. lastRevision (0 = none) is sent as Last-Event-ID; the server
// answers any connect with one complete snapshot, so nothing is skipped.
// Heartbeat comments are consumed silently.
//
// Returns *CashV9WalletAccountStreamDisconnectedError when the connection
// ends (EOF is a disconnect, never a completion) or the ingress answers
// 502/503/504 before the stream opens (a rolling deploy), the accept error
// verbatim, or the API error for a refused connection (*NotFoundError for an
// unknown boundary, *ForbiddenError, a 429 when the per-key stream cap is
// reached).
func (a *Arca) StreamCashV9WalletAccount(ctx context.Context, boundaryID string, lastRevision uint64, accept func(CashV9WalletAccount) error) error {
	if accept == nil {
		return fmt.Errorf("arca: wallet account stream callback required")
	}
	if strings.TrimSpace(boundaryID) == "" {
		return fmt.Errorf("arca: wallet account stream requires a boundaryId")
	}
	realm, err := a.realmID(ctx)
	if err != nil {
		return err
	}
	endpoint, err := a.client.buildURL("/custody/v9/cash/wallet-account/events", url.Values{"realmId": {realm}, "boundaryId": {boundaryID}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.client.getCredential())
	if lastRevision > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(lastRevision, 10))
	}
	if a.client.headerHook != nil {
		for k, v := range a.client.headerHook() {
			req.Header.Set(k, v)
		}
	}
	client := *a.client.http
	client.Timeout = 0
	response, err := client.Do(req)
	if err != nil {
		return &CashV9WalletAccountStreamDisconnectedError{LastRevision: lastRevision, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadGateway && response.StatusCode <= http.StatusGatewayTimeout {
		// The ingress, not the API's answer (a rolling deploy): a disconnect
		// to retry, never a refusal to stop on.
		return &CashV9WalletAccountStreamDisconnectedError{LastRevision: lastRevision, Cause: fmt.Errorf("http %d before the stream opened", response.StatusCode)}
	}
	if response.StatusCode != http.StatusOK {
		return a.client.unwrap(response, nil)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("arca: wallet account stream unavailable (content-type %q)", response.Header.Get("Content-Type"))
	}

	last := lastRevision
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var (
		event string
		data  []string
	)
	dispatch := func() error {
		defer func() { event, data = "", nil }()
		if len(data) == 0 {
			return nil // heartbeat comment or empty frame
		}
		if event != "snapshot" && event != "" {
			return nil // unknown frame types are ignored, never applied
		}
		var snapshot CashV9WalletAccount
		if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &snapshot); err != nil {
			return fmt.Errorf("arca: wallet account frame: %w", err)
		}
		if snapshot.Schema != 1 {
			return fmt.Errorf("arca: wallet account schema %d is not supported by this SDK", snapshot.Schema)
		}
		if snapshot.RealmID != realm {
			return fmt.Errorf("arca: wallet account frame for realm %s on a %s stream", snapshot.RealmID, realm)
		}
		if err := accept(snapshot); err != nil {
			return err
		}
		last = snapshot.Revision
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := dispatch(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// comment / heartbeat
		case strings.HasPrefix(line, "id:"):
			// The id is the snapshot's revision, also carried in the body.
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	cause := scanner.Err()
	if cause == nil {
		cause = io.ErrUnexpectedEOF
	}
	return &CashV9WalletAccountStreamDisconnectedError{LastRevision: last, Cause: cause}
}

// RunCashV9WalletAccountStream keeps the stream open until ctx ends,
// reconnecting after a disconnect with exponential backoff from 1 s to 30 s
// (the schedule the SDK's websocket uses) and resuming from the last
// delivered revision. The backoff resets whenever a connection delivered a
// snapshot. It stops and returns on cancellation, on an accept error, and on
// a refused connection (not found, forbidden, the per-key cap) — those need
// the caller's decision, not a retry.
func (a *Arca) RunCashV9WalletAccountStream(ctx context.Context, boundaryID string, accept func(CashV9WalletAccount) error) error {
	var revision uint64
	attempt := 0
	for {
		delivered := false
		err := a.StreamCashV9WalletAccount(ctx, boundaryID, revision, func(w CashV9WalletAccount) error {
			delivered = true
			return accept(w)
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var disc *CashV9WalletAccountStreamDisconnectedError
		if !errors.As(err, &disc) {
			return err
		}
		if disc.LastRevision > revision {
			revision = disc.LastRevision
		}
		if delivered {
			attempt = 0
		}
		wait := cashV9StreamBackoff(attempt)
		attempt++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// cashV9StreamBackoff is 1s, 2s, 4s, … capped at 30s.
func cashV9StreamBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 5 {
		return 30 * time.Second
	}
	d := time.Second << uint(attempt)
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}
