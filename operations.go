package arca

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GetOperation fetches operation detail by id (with correlated events and deltas).
func (a *Arca) GetOperation(ctx context.Context, operationID string, opts *GetOperationOptions) (OperationDetailResponse, error) {
	var out OperationDetailResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	params := url.Values{}
	if opts != nil && opts.IncludeEvidence {
		params.Set("includeEvidence", "true")
	}
	err := a.client.get(ctx, "/operations/"+operationID, params, &out)
	return out, err
}

// ListOperations lists operations in the realm.
func (a *Arca) ListOperations(ctx context.Context, opts *ListOperationsOptions) (OperationListResponse, error) {
	var out OperationListResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}}
	if opts != nil {
		if len(opts.Types) > 0 {
			params.Set("types", joinOpTypes(opts.Types))
		} else if opts.Type != "" {
			params.Set("type", string(opts.Type))
		}
		if opts.ArcaPath != "" {
			params.Set("arcaPath", opts.ArcaPath)
		}
		if opts.ArcaID != "" {
			params.Set("arcaId", opts.ArcaID)
		}
		if opts.Path != "" {
			params.Set("path", opts.Path)
		}
		if opts.IncludeContext {
			params.Set("includeContext", "true")
		}
		if opts.Limit > 0 {
			params.Set("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Cursor != "" {
			params.Set("cursor", opts.Cursor)
		}
	}
	err = a.client.get(ctx, "/operations", params, &out)
	return out, err
}

// ExportOperationEvidence exports realm-scoped operation audit evidence over a
// time range.
func (a *Arca) ExportOperationEvidence(ctx context.Context, opts ExportOperationEvidenceOptions) (OperationEvidenceExportResponse, error) {
	var out OperationEvidenceExportResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}, "from": {opts.From}, "to": {opts.To}}
	if len(opts.Types) > 0 {
		params.Set("types", joinOpTypes(opts.Types))
	} else if opts.Type != "" {
		params.Set("type", string(opts.Type))
	}
	if opts.ArcaPath != "" {
		params.Set("arcaPath", opts.ArcaPath)
	}
	if opts.ArcaID != "" {
		params.Set("arcaId", opts.ArcaID)
	}
	if opts.Path != "" {
		params.Set("path", opts.Path)
	}
	if opts.Limit > 0 {
		params.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		params.Set("cursor", opts.Cursor)
	}
	err = a.client.get(ctx, "/operations/export", params, &out)
	return out, err
}

// ListEvents lists events in the realm.
func (a *Arca) ListEvents(ctx context.Context, opts *ListEventsOptions) (EventListResponse, error) {
	var out EventListResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}}
	if opts != nil {
		if opts.ArcaPath != "" {
			params.Set("arcaPath", opts.ArcaPath)
		}
		if opts.Path != "" {
			params.Set("path", opts.Path)
		}
		if opts.Limit > 0 {
			params.Set("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Cursor != "" {
			params.Set("cursor", opts.Cursor)
		}
	}
	err = a.client.get(ctx, "/events", params, &out)
	return out, err
}

// GetEventDetail fetches event detail by id.
func (a *Arca) GetEventDetail(ctx context.Context, eventID string) (EventDetailResponse, error) {
	var out EventDetailResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/events/"+eventID, nil, &out)
	return out, err
}

// ListDeltas lists balance-change records (state deltas) for a path.
func (a *Arca) ListDeltas(ctx context.Context, arcaPath string) (StateDeltaListResponse, error) {
	var out StateDeltaListResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/deltas", url.Values{"realmId": {rid}, "arcaPath": {arcaPath}}, &out)
	return out, err
}

// Summary returns aggregate realm statistics.
func (a *Arca) Summary(ctx context.Context) (ExplorerSummary, error) {
	var out ExplorerSummary
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/summary", url.Values{"realmId": {rid}}, &out)
	return out, err
}

// ListReconciliationState lists venue reconciliation state entries.
func (a *Arca) ListReconciliationState(ctx context.Context) (ReconciliationStateListResponse, error) {
	var out ReconciliationStateListResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/reconciliation", url.Values{"realmId": {rid}}, &out)
	return out, err
}

// CheckInvariants runs DB-only invariant checks. Pass realmID to scope the
// scan (much faster); pass "" for a platform-wide scan.
func (a *Arca) CheckInvariants(ctx context.Context, limit int, realmID string) (InvariantCheckResponse, error) {
	var out InvariantCheckResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	params := url.Values{}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if realmID != "" {
		params.Set("realmId", realmID)
	}
	err := a.client.get(ctx, "/internal/invariant-check", params, &out)
	return out, err
}

// WaitForQuiescence blocks until every operation in the realm is terminal,
// reacting to WebSocket events with a periodic REST safety poll.
func (a *Arca) WaitForQuiescence(ctx context.Context, pollInterval time.Duration, onPoll func(pending int)) error {
	if err := a.ensureReady(ctx); err != nil {
		return err
	}
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	a.ws.EnsureConnected()
	go func() { _, _ = a.ws.watchPath(context.Background(), "/") }()
	defer a.ws.unwatchPath("/")

	checkPending := func() (int, error) {
		res, err := a.ListOperations(ctx, nil)
		if err != nil {
			return 0, err
		}
		pending := 0
		for _, o := range res.Operations {
			if o.State == OpPending {
				pending++
			}
		}
		if onPoll != nil {
			onPoll(pending)
		}
		return pending, nil
	}

	if p, err := checkPending(); err == nil && p == 0 {
		return nil
	}

	updates := make(chan struct{}, 8)
	unsub := a.ws.OnOperationUpdated(func(_ *Operation, _ RealmEvent) {
		select {
		case updates <- struct{}{}:
		default:
		}
	})
	defer unsub()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-updates:
		case <-ticker.C:
		}
		if p, err := checkPending(); err == nil && p == 0 {
			return nil
		}
	}
}

func joinOpTypes(types []OperationType) string {
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = string(t)
	}
	return strings.Join(parts, ",")
}
