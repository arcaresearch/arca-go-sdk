package arca

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// EnsureDenominatedArca creates (or returns) a denominated Arca object (a user
// wallet) at the given path. Idempotent.
func (a *Arca) EnsureDenominatedArca(ctx context.Context, opts EnsureDenominatedArcaOptions) *OperationHandle[EnsureArcaObjectResponse] {
	return op(a, ctx, func() (EnsureArcaObjectResponse, error) {
		var resp EnsureArcaObjectResponse
		err := a.client.post(ctx, "/objects", map[string]any{
			"realmId":       a.currentRealmID(),
			"path":          opts.Ref,
			"type":          "denominated",
			"metadata":      nilIfEmpty(opts.Metadata),
			"labels":        opts.Labels,
			"operationPath": nilIfEmpty(opts.OperationPath),
		}, &resp)
		return resp, err
	}, EnsureArcaObjectResponse.op, (*EnsureArcaObjectResponse).setOp, nil, 0)
}

// EnsureArca creates (or returns) an Arca object of any type at the given path.
func (a *Arca) EnsureArca(ctx context.Context, opts EnsureArcaOptions) *OperationHandle[EnsureArcaObjectResponse] {
	return op(a, ctx, func() (EnsureArcaObjectResponse, error) {
		var resp EnsureArcaObjectResponse
		err := a.client.post(ctx, "/objects", map[string]any{
			"realmId":       a.currentRealmID(),
			"path":          opts.Ref,
			"type":          opts.Type,
			"metadata":      nilIfEmpty(opts.Metadata),
			"labels":        opts.Labels,
			"operationPath": nilIfEmpty(opts.OperationPath),
		}, &resp)
		return resp, err
	}, EnsureArcaObjectResponse.op, (*EnsureArcaObjectResponse).setOp, nil, 0)
}

// EnsureInfo creates (or returns) an info object at the given directory path
// (/.info is appended automatically).
func (a *Arca) EnsureInfo(ctx context.Context, opts EnsureInfoOptions) *OperationHandle[EnsureArcaObjectResponse] {
	ref := opts.Ref
	if !strings.HasSuffix(ref, "/.info") {
		ref = ref + "/.info"
	}
	return op(a, ctx, func() (EnsureArcaObjectResponse, error) {
		var resp EnsureArcaObjectResponse
		err := a.client.post(ctx, "/objects", map[string]any{
			"realmId":       a.currentRealmID(),
			"path":          ref,
			"type":          "info",
			"metadata":      nil,
			"labels":        opts.Labels,
			"operationPath": nilIfEmpty(opts.OperationPath),
		}, &resp)
		return resp, err
	}, EnsureArcaObjectResponse.op, (*EnsureArcaObjectResponse).setOp, nil, 0)
}

// EnsureDeleted deletes an Arca object by path. If SweepTo is set, remaining
// funds are transferred there before deletion.
func (a *Arca) EnsureDeleted(ctx context.Context, opts EnsureDeletedOptions) *OperationHandle[DeleteArcaObjectResponse] {
	return op(a, ctx, func() (DeleteArcaObjectResponse, error) {
		var resp DeleteArcaObjectResponse
		err := a.client.post(ctx, "/objects/delete", map[string]any{
			"realmId":            a.currentRealmID(),
			"path":               opts.Ref,
			"sweepToPath":        nilIfEmpty(opts.SweepTo),
			"liquidatePositions": opts.LiquidatePositions,
			"operationPath":      nilIfEmpty(opts.OperationPath),
		}, &resp)
		return resp, err
	}, DeleteArcaObjectResponse.op, (*DeleteArcaObjectResponse).setOp, nil, 0)
}

// GetObject fetches an Arca object by path.
func (a *Arca) GetObject(ctx context.Context, path string) (ArcaObject, error) {
	var out ArcaObject
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/objects/by-path", url.Values{"realmId": {rid}, "path": {path}}, &out)
	return out, err
}

// GetObjectDetail fetches an Arca object's full detail by id.
func (a *Arca) GetObjectDetail(ctx context.Context, objectID string) (ArcaObjectDetailResponse, error) {
	var out ArcaObjectDetailResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/objects/"+objectID, nil, &out)
	return out, err
}

// ListObjects lists Arca objects, optionally filtered by path.
func (a *Arca) ListObjects(ctx context.Context, opts *ListObjectsOptions) (ArcaObjectListResponse, error) {
	var out ArcaObjectListResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}}
	if opts != nil {
		if opts.Path != "" {
			if err := validatePath(opts.Path); err != nil {
				return out, err
			}
			params.Set("prefix", opts.Path)
		}
		if opts.IncludeDeleted {
			params.Set("includeDeleted", "true")
		}
		if opts.Limit > 0 {
			params.Set("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Cursor != "" {
			params.Set("cursor", opts.Cursor)
		}
	}
	err = a.client.get(ctx, "/objects", params, &out)
	return out, err
}

// GetBalances fetches balances for an Arca object by id.
func (a *Arca) GetBalances(ctx context.Context, objectID string) ([]ArcaBalance, error) {
	if err := a.ensureReady(ctx); err != nil {
		return nil, err
	}
	var out ArcaBalanceListResponse
	if err := a.client.get(ctx, "/objects/"+objectID+"/balances", nil, &out); err != nil {
		return nil, err
	}
	return out.Balances, nil
}

// GetBalancesByPath resolves a path to an object id, then fetches balances.
func (a *Arca) GetBalancesByPath(ctx context.Context, path string) ([]ArcaBalance, error) {
	obj, err := a.GetObject(ctx, path)
	if err != nil {
		return nil, err
	}
	return a.GetBalances(ctx, obj.ID)
}

// BrowseObjects browses Arca objects in a folder-like structure.
func (a *Arca) BrowseObjects(ctx context.Context, opts *BrowseObjectsOptions) (ArcaObjectBrowseResponse, error) {
	var out ArcaObjectBrowseResponse
	p := "/"
	includeDeleted := false
	if opts != nil {
		if opts.Path != "" {
			p = opts.Path
		}
		includeDeleted = opts.IncludeDeleted
	}
	if err := validatePath(p); err != nil {
		return out, err
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	params := url.Values{"realmId": {rid}, "prefix": {p}}
	if includeDeleted {
		params.Set("includeDeleted", "true")
	}
	err = a.client.get(ctx, "/objects/browse", params, &out)
	return out, err
}

// CreateIsolationZone declares a path as an isolation zone root.
func (a *Arca) CreateIsolationZone(ctx context.Context, opts CreateIsolationZoneOptions) (IsolationZone, error) {
	var out IsolationZone
	if err := validatePath(opts.Path); err != nil {
		return out, err
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	path := opts.Path
	if path != "/" {
		path = trimTrailingSlash(path)
	}
	if path == "/" {
		return out, &ValidationError{newArcaError("VALIDATION_ERROR", "Isolation zones must be created at a non-root path.", "")}
	}
	err = a.client.post(ctx, "/isolation-zones", map[string]any{"realmId": rid, "path": path}, &out)
	return out, err
}

// UpdateLabels updates labels on an Arca object. A nil value pointer removes a key.
func (a *Arca) UpdateLabels(ctx context.Context, opts UpdateLabelsOptions) (ArcaObject, error) {
	var out ArcaObject
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.patch(ctx, "/objects/"+opts.ObjectID+"/labels",
		url.Values{"realmId": {rid}}, map[string]any{"labels": opts.Labels}, &out)
	return out, err
}

// GetFolderLabels reads folder-level labels for a path.
func (a *Arca) GetFolderLabels(ctx context.Context, path string) (FolderLabelsResponse, error) {
	var out FolderLabelsResponse
	if err := validatePath(path); err != nil {
		return out, err
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/folders/labels", url.Values{"realmId": {rid}, "path": {path}}, &out)
	return out, err
}

// UpdateFolderLabels replaces the label set on a folder path (full overwrite).
func (a *Arca) UpdateFolderLabels(ctx context.Context, opts UpdateFolderLabelsOptions) (FolderLabelsResponse, error) {
	var out FolderLabelsResponse
	if err := validatePath(opts.Path); err != nil {
		return out, err
	}
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.put(ctx, "/folders/labels", map[string]any{
		"realmId": rid,
		"path":    opts.Path,
		"labels":  opts.Labels,
	}, &out)
	return out, err
}

// GetObjectVersions returns version history for an Arca object.
func (a *Arca) GetObjectVersions(ctx context.Context, objectID string) (ArcaObjectVersionsResponse, error) {
	var out ArcaObjectVersionsResponse
	if err := a.ensureReady(ctx); err != nil {
		return out, err
	}
	err := a.client.get(ctx, "/objects/"+objectID+"/versions", nil, &out)
	return out, err
}

// GetSnapshotBalances returns point-in-time balances for an Arca object.
func (a *Arca) GetSnapshotBalances(ctx context.Context, objectID, asOf string) (SnapshotBalancesResponse, error) {
	var out SnapshotBalancesResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/objects/"+objectID+"/snapshot", url.Values{"realmId": {rid}, "asOf": {asOf}}, &out)
	return out, err
}

// GetObjectValuation returns the valuation for a single Arca object.
func (a *Arca) GetObjectValuation(ctx context.Context, path string) (ObjectValuation, error) {
	var out ObjectValuation
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	err = a.client.get(ctx, "/objects/valuation", url.Values{"realmId": {rid}, "path": {path}}, &out)
	return out, err
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// itoa helper for time-based ids etc.
func msNow() int64 { return time.Now().UnixMilli() }
