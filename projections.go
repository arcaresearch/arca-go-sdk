package arca

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// ProjectionSpec defines the permitted fields and resource/label selectors.
type ProjectionSpec struct {
	Fields        []string          `json:"fields"`
	Resources     []string          `json:"resources"`
	IncludeLabels map[string]string `json:"includeLabels,omitempty"`
	ExcludeLabels map[string]string `json:"excludeLabels,omitempty"`
}

// ProjectionValuationsOptions narrows a registered projection; it cannot expand it.
type ProjectionValuationsOptions struct {
	Limit                int
	Cursor, Prefix, Path string
	Paths                []string
}

// ProjectionValuationsResponse preserves the configured field set without
// imposing a fixed financial schema on arbitrary registered projections.
type ProjectionValuationsResponse struct {
	Valuations []json.RawMessage `json:"valuations"`
	Cursor     string            `json:"cursor"`
	NextCursor string            `json:"nextCursor"`
}

func (a *Arca) UpsertProjection(ctx context.Context, name string, spec ProjectionSpec) error {
	rid, err := a.realmID(ctx)
	if err != nil {
		return err
	}
	return a.client.put(ctx, "/realms/"+url.PathEscape(rid)+"/projections/"+url.PathEscape(name), spec, nil)
}

func (a *Arca) GetProjectionValuations(ctx context.Context, name string, opts ProjectionValuationsOptions) (ProjectionValuationsResponse, error) {
	var out ProjectionValuationsResponse
	rid, err := a.realmID(ctx)
	if err != nil {
		return out, err
	}
	q := url.Values{"realmId": {rid}}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Prefix != "" {
		q.Set("prefix", opts.Prefix)
	}
	if opts.Path != "" {
		q.Set("path", opts.Path)
	}
	if len(opts.Paths) > 0 {
		q.Set("paths", strings.Join(opts.Paths, ","))
	}
	err = a.client.get(ctx, "/projections/"+url.PathEscape(name)+"/valuations", q, &out)
	return out, err
}
