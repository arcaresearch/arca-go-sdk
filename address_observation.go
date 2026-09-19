package arca

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Address observations: read-only watching of external wallets for ERC-20
// Transfer logs on a configured chain/token, with immediate receipts,
// durable per-realm replay and explicit corrections.
//
// Wire contract: documents/contracts/address-observations.md. Every chain id,
// block number, amount, sequence and revision is a decimal STRING — parse
// amounts with math/big, never float64. Addresses are lowercase 20-byte hex;
// hashes lowercase 32-byte hex.
//
// An observation is a fact about a public chain; it is never a spending
// authorisation and never mints an Arca ledger credit.

// AddressObservationSchemaVersion is the event schema this SDK understands.
const AddressObservationSchemaVersion = 1

// Address-watch lifecycle values.
const (
	AddressWatchRegistered   = "registered"
	AddressWatchInitializing = "initializing"
	AddressWatchActive       = "active"
	AddressWatchDegraded     = "degraded"
	AddressWatchSuspended    = "suspended"
)

// Address-watch health values (see the contract for semantics).
const (
	AddressHealthAwaitingObserver = "awaiting_observer"
	AddressHealthInitializing     = "initializing"
	AddressHealthLive             = "live"
	AddressHealthRecovering       = "recovering"
	AddressHealthReconciling      = "reconciling"
	AddressHealthDegraded         = "degraded"
	AddressHealthSuspended        = "suspended"
)

// Address-observation event types.
const (
	AddressEventWatchRegistered    = "watch.registered"
	AddressEventWatchInitialized   = "watch.initialized"
	AddressEventTransferObserved   = "transfer.observed"
	AddressEventTransferRemoved    = "transfer.removed"
	AddressEventTransferFinalized  = "transfer.finalized"
	AddressEventWatchRebased       = "watch.rebased"
	AddressEventWatchHealthChanged = "watch.health_changed"
)

// AddressObservationBalance is a watch's projected balance.
type AddressObservationBalance struct {
	// ObservedRaw is nil when no baseline exists or the projection is
	// contradictory — never "0" as a stand-in for unknown.
	ObservedRaw          *string `json:"observedRaw"`
	CompleteThroughBlock string  `json:"completeThroughBlock,omitempty"`
	Health               string  `json:"health"`
}

// AddressObservationEffect is one watched address's side of a transfer.
type AddressObservationEffect struct {
	WatchID   string `json:"watchId"`
	Address   string `json:"address"`
	Direction string `json:"direction"`
	// DeltaRaw is signed; FundingDelta is the receipt amount (incoming
	// only) and "0" otherwise — never trigger forwarding on DeltaRaw.
	DeltaRaw      string                    `json:"deltaRaw"`
	FundingDelta  string                    `json:"fundingDelta"`
	WatchRevision string                    `json:"watchRevision"`
	Balance       AddressObservationBalance `json:"balance"`
}

// AddressObservationFinality is separate finality metadata.
type AddressObservationFinality struct {
	Status    string `json:"status"`
	Depth     string `json:"depth,omitempty"`
	Block     string `json:"block,omitempty"`
	BlockHash string `json:"blockHash,omitempty"`
}

// AddressObservationWatchData carries watch.* event bodies.
type AddressObservationWatchData struct {
	WatchID              string  `json:"watchId"`
	Address              string  `json:"address"`
	Lifecycle            string  `json:"lifecycle"`
	Health               string  `json:"health"`
	WatchRevision        string  `json:"watchRevision"`
	StartBlock           string  `json:"startBlock,omitempty"`
	StartBlockHash       string  `json:"startBlockHash,omitempty"`
	BaselineRaw          *string `json:"baselineRaw,omitempty"`
	BaselineBlock        string  `json:"baselineBlock,omitempty"`
	BaselineBlockHash    string  `json:"baselineBlockHash,omitempty"`
	CoverageThroughBlock string  `json:"coverageThroughBlock,omitempty"`
	Reason               string  `json:"reason,omitempty"`
}

// AddressObservationEvent is one event on a realm's observation feed.
type AddressObservationEvent struct {
	SchemaVersion   int                          `json:"schemaVersion"`
	EventID         string                       `json:"eventId"`
	RealmID         string                       `json:"realmId"`
	Sequence        string                       `json:"sequence"`
	Cursor          string                       `json:"cursor"`
	Type            string                       `json:"type"`
	ObservedAt      string                       `json:"observedAt"`
	CommitGroup     string                       `json:"commitGroup,omitempty"`
	ChainID         string                       `json:"chainId,omitempty"`
	TokenAddress    string                       `json:"tokenAddress,omitempty"`
	AmountRaw       string                       `json:"amountRaw,omitempty"`
	From            string                       `json:"from,omitempty"`
	To              string                       `json:"to,omitempty"`
	TransactionHash string                       `json:"transactionHash,omitempty"`
	BlockNumber     string                       `json:"blockNumber,omitempty"`
	BlockHash       string                       `json:"blockHash,omitempty"`
	LogIndex        string                       `json:"logIndex,omitempty"`
	Finality        *AddressObservationFinality  `json:"finality,omitempty"`
	Effects         []AddressObservationEffect   `json:"effects,omitempty"`
	Watch           *AddressObservationWatchData `json:"watch,omitempty"`
}

// RegisterAddressWatchRequest registers one external wallet. RealmID may be
// empty to use the client's configured realm.
type RegisterAddressWatchRequest struct {
	RealmID      string `json:"realmId"`
	ChainID      string `json:"chainId"`
	TokenAddress string `json:"tokenAddress"`
	Address      string `json:"address"`
}

// AddressBaseline is the pinned balanceOf read a watch was initialised from.
type AddressBaseline struct {
	Raw       string `json:"raw"`
	Block     string `json:"block"`
	BlockHash string `json:"blockHash"`
}

// AddressWatch is the durable watch snapshot.
type AddressWatch struct {
	WatchID        string                    `json:"watchId"`
	RealmID        string                    `json:"realmId"`
	ChainID        string                    `json:"chainId"`
	TokenAddress   string                    `json:"tokenAddress"`
	Address        string                    `json:"address"`
	Lifecycle      string                    `json:"lifecycle"`
	Revision       string                    `json:"revision"`
	StartBlock     string                    `json:"startBlock,omitempty"`
	StartBlockHash string                    `json:"startBlockHash,omitempty"`
	Baseline       *AddressBaseline          `json:"baseline,omitempty"`
	Balance        AddressObservationBalance `json:"balance"`
	LastError      *string                   `json:"lastError,omitempty"`
	RegisteredAt   string                    `json:"registeredAt"`
	UpdatedAt      string                    `json:"updatedAt"`
}

// RegisterAddressWatchResponse is the registration result. Created is true
// for a new watch (HTTP 201), false for an existing one (HTTP 200). Cursor
// is the realm's current feed cursor.
type RegisterAddressWatchResponse struct {
	Watch   AddressWatch `json:"watch"`
	Created bool         `json:"created"`
	Cursor  string       `json:"cursor"`
}

// AddressObservationSnapshot is one page of watches at one read timestamp.
type AddressObservationSnapshot struct {
	RealmID       string         `json:"realmId"`
	ReadAt        string         `json:"readAt"`
	Cursor        string         `json:"cursor"`
	Watches       []AddressWatch `json:"watches"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

// AddressObservationChanges is one bounded replay page.
type AddressObservationChanges struct {
	RealmID           string                    `json:"realmId"`
	Events            []AddressObservationEvent `json:"events"`
	OldestValidCursor string                    `json:"oldestValidCursor"`
	CurrentCursor     string                    `json:"currentCursor"`
	NextCursor        string                    `json:"nextCursor"`
	HasMore           bool                      `json:"hasMore"`
}

// AddressObservationHealth is the observer's health for the realm's scope.
//
// OwnerHolder is the observer replica that published the scope's most
// recent checkpoint and OwnerLeaseLive is true while that checkpoint is
// fresh (younger than three head polls); LastCheckpointAt is its
// timestamp. A live observer with `fault` set is stopped by design and
// waits for an operator.
type AddressObservationHealth struct {
	RealmID              string  `json:"realmId"`
	Scope                string  `json:"scope"`
	ChainID              string  `json:"chainId"`
	TokenAddress         string  `json:"tokenAddress"`
	OwnerHolder          string  `json:"ownerHolder,omitempty"`
	OwnerLeaseLive       bool    `json:"ownerLeaseLive"`
	LastCheckpointAt     string  `json:"lastCheckpointAt,omitempty"`
	CompleteThroughBlock *string `json:"completeThroughBlock"`
	ObservedHead         *string `json:"observedHead"`
	FinalityStatus       string  `json:"finalityStatus"`
	Fault                *string `json:"fault"`
	FeedEpoch            string  `json:"feedEpoch"`
	LastSequence         string  `json:"lastSequence"`
	PublishedSequence    string  `json:"publishedSequence"`
	UnpublishedEvents    string  `json:"unpublishedEvents"`
}

// AddressObservationBatch is one delivered SSE frame: events in sequence
// order and the cursor to persist once they are applied.
type AddressObservationBatch struct {
	Events []AddressObservationEvent `json:"events"`
	Cursor string                    `json:"cursor"`
}

// ---- typed errors ----

// ObservationCursorExpiredError: the cursor predates retained history or
// names a reset feed epoch. Take a fresh snapshot and mark the gap in your
// own history; the platform never fabricates the missing receipts.
type ObservationCursorExpiredError struct {
	*ArcaError
	// Cursor is the cursor that was refused (empty when unknown).
	Cursor string
}

// ObservationSchemaError: an event carried a schema version this SDK does
// not understand. Upgrade rather than guess at fields.
type ObservationSchemaError struct {
	Version int
}

func (e *ObservationSchemaError) Error() string {
	return fmt.Sprintf("arca: address observation schema version %d is not supported (this SDK understands %d)", e.Version, AddressObservationSchemaVersion)
}

// ObservationStreamDisconnectedError: the connection ended without a
// terminal verdict. LastCursor is the last cursor whose batch the callback
// accepted; resume from it.
type ObservationStreamDisconnectedError struct {
	LastCursor string
	Cause      error
}

func (e *ObservationStreamDisconnectedError) Error() string {
	return fmt.Sprintf("arca: address observation stream disconnected (resume from %q): %v", e.LastCursor, e.Cause)
}

func (e *ObservationStreamDisconnectedError) Unwrap() error { return e.Cause }

// ObservationStreamResetError: the server sent a `reset_required` control
// frame (retention passed, epoch reset, or authorisation lost mid-stream).
type ObservationStreamResetError struct {
	Code    string
	Message string
	Cursor  string
}

func (e *ObservationStreamResetError) Error() string {
	return fmt.Sprintf("arca: address observation stream reset required (%s): %s", e.Code, e.Message)
}

// ---- REST ----

// RegisterAddressWatch idempotently registers an external wallet. The same
// (realm, chain, token, address) always returns the same watch.
func (a *Arca) RegisterAddressWatch(ctx context.Context, req RegisterAddressWatchRequest) (*RegisterAddressWatchResponse, error) {
	if req.RealmID == "" {
		realm, err := a.realmID(ctx)
		if err != nil {
			return nil, err
		}
		req.RealmID = realm
	}
	var out RegisterAddressWatchResponse
	if err := a.client.post(ctx, "/address-observations/watches", req, &out); err != nil {
		return nil, translateObservationError(err, "")
	}
	return &out, nil
}

// GetAddressWatch returns one watch by id.
func (a *Arca) GetAddressWatch(ctx context.Context, watchID string) (*AddressWatch, error) {
	realm, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	var out AddressWatch
	if err := a.client.get(ctx, "/address-observations/watches/"+url.PathEscape(watchID), url.Values{"realmId": {realm}}, &out); err != nil {
		return nil, translateObservationError(err, "")
	}
	return &out, nil
}

// ListAddressWatchSnapshot returns one consistent page of the realm's
// watches. Pass the returned NextPageToken to continue at the same read
// timestamp; an expired token is an ObservationCursorExpiredError and the
// snapshot must be restarted from an empty token.
func (a *Arca) ListAddressWatchSnapshot(ctx context.Context, pageToken string, limit int) (*AddressObservationSnapshot, error) {
	realm, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	params := url.Values{"realmId": {realm}}
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var out AddressObservationSnapshot
	if err := a.client.get(ctx, "/address-observations/snapshot", params, &out); err != nil {
		return nil, translateObservationError(err, pageToken)
	}
	return &out, nil
}

// ListAddressObservationChanges returns a bounded replay page after a
// cursor ("" = from the oldest retained event).
func (a *Arca) ListAddressObservationChanges(ctx context.Context, after string, limit int) (*AddressObservationChanges, error) {
	realm, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	params := url.Values{"realmId": {realm}}
	if after != "" {
		params.Set("after", after)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var out AddressObservationChanges
	if err := a.client.get(ctx, "/address-observations/changes", params, &out); err != nil {
		return nil, translateObservationError(err, after)
	}
	for _, ev := range out.Events {
		if ev.SchemaVersion != AddressObservationSchemaVersion {
			return nil, &ObservationSchemaError{Version: ev.SchemaVersion}
		}
	}
	return &out, nil
}

// GetAddressObservationHealth reports the observer's health for the realm.
func (a *Arca) GetAddressObservationHealth(ctx context.Context) (*AddressObservationHealth, error) {
	realm, err := a.realmID(ctx)
	if err != nil {
		return nil, err
	}
	var out AddressObservationHealth
	if err := a.client.get(ctx, "/address-observations/health", url.Values{"realmId": {realm}}, &out); err != nil {
		return nil, translateObservationError(err, "")
	}
	return &out, nil
}

func translateObservationError(err error, cursor string) error {
	var ae *ArcaError
	if errors.As(err, &ae) && ae.Code == "OBSERVATION_CURSOR_EXPIRED" {
		return &ObservationCursorExpiredError{ArcaError: ae, Cursor: cursor}
	}
	return err
}

// ---- SSE ----

// StreamAddressObservations opens one SSE connection to the realm's feed
// from `after` ("" = oldest retained) and calls accept for every batch in
// sequence order. The callback must persist the batch's Cursor
// transactionally with its own state; a callback error stops delivery and
// is returned, and the cursor is NOT advanced past that batch.
//
// Returns *ObservationStreamDisconnectedError (with the last accepted
// cursor) when the connection ends, *ObservationCursorExpiredError when the
// cursor cannot be served, *ObservationStreamResetError on a server reset
// frame, and *ObservationSchemaError on an unknown schema version. EOF is a
// disconnect, never a successful completion.
func (a *Arca) StreamAddressObservations(ctx context.Context, after string, accept func(AddressObservationBatch) error) error {
	if accept == nil {
		return fmt.Errorf("arca: address observation callback required")
	}
	realm, err := a.realmID(ctx)
	if err != nil {
		return err
	}
	endpoint, err := a.client.buildURL("/address-observations/events", url.Values{"realmId": {realm}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+a.client.getCredential())
	if after != "" {
		req.Header.Set("Last-Event-ID", after)
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
		return &ObservationStreamDisconnectedError{LastCursor: after, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return translateObservationError(a.client.unwrap(response, nil), after)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("arca: address observation stream unavailable (content-type %q)", response.Header.Get("Content-Type"))
	}

	last := after
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var (
		event string
		id    string
		data  []string
	)
	dispatch := func() error {
		defer func() { event, id, data = "", "", nil }()
		if len(data) == 0 {
			return nil // heartbeat comment or empty frame
		}
		payload := strings.Join(data, "\n")
		switch event {
		case "changes", "":
			var batch AddressObservationBatch
			if err := json.Unmarshal([]byte(payload), &batch); err != nil {
				return fmt.Errorf("arca: address observation frame: %w", err)
			}
			for _, ev := range batch.Events {
				if ev.SchemaVersion != AddressObservationSchemaVersion {
					return &ObservationSchemaError{Version: ev.SchemaVersion}
				}
				if ev.RealmID != realm {
					return fmt.Errorf("arca: address observation frame for realm %s on a %s stream", ev.RealmID, realm)
				}
			}
			if batch.Cursor == "" {
				batch.Cursor = id
			}
			if len(batch.Events) == 0 {
				return nil
			}
			if err := accept(batch); err != nil {
				return err
			}
			last = batch.Cursor
			return nil
		case "control":
			var ctl struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
				Cursor  string `json:"cursor"`
			}
			if err := json.Unmarshal([]byte(payload), &ctl); err != nil {
				return fmt.Errorf("arca: address observation control frame: %w", err)
			}
			return &ObservationStreamResetError{Code: ctl.Code, Message: ctl.Message, Cursor: ctl.Cursor}
		default:
			return nil // unknown frame types are ignored, never applied
		}
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
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
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
	return &ObservationStreamDisconnectedError{LastCursor: last, Cause: cause}
}

// RunAddressObservationStream keeps a feed open until ctx ends, reconnecting
// with jittered backoff (250ms–30s) from the last accepted cursor after a
// disconnect. It stops and returns on cancellation, on a callback error, on
// a cursor-expired / reset / schema / authorisation error — those need the
// caller's decision, not a retry.
func (a *Arca) RunAddressObservationStream(ctx context.Context, after string, accept func(AddressObservationBatch) error) error {
	cursor := after
	backoff := time.Duration(0)
	for {
		err := a.StreamAddressObservations(ctx, cursor, accept)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var disc *ObservationStreamDisconnectedError
		if !errors.As(err, &disc) {
			return err
		}
		if disc.LastCursor != "" {
			cursor = disc.LastCursor
			backoff = 0
		}
		if backoff < 250*time.Millisecond {
			backoff = 250 * time.Millisecond
		} else if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		wait := backoff + time.Duration(rand.Int63n(int64(backoff)/4+1))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
