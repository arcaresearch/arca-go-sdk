package arca

import (
	"context"
	"encoding/json"
	"errors"
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

// ProjectionWindow is a rolling window a projection read or watch can ask for.
type ProjectionWindow string

// ProjectionWindow30d is the rolling 30-day window.
const ProjectionWindow30d ProjectionWindow = "30d"

// WindowedStatus is the readiness of one windowed figure.
type WindowedStatus string

const (
	// WindowedComplete: the value covers the whole window.
	WindowedComplete WindowedStatus = "complete"
	// WindowedIncomplete (P&L only): a mark was missing at From or now, so
	// the value is off by that position's move.
	WindowedIncomplete WindowedStatus = "incomplete"
	// WindowedPending: not computed yet. The value is nil, so it never reads
	// as zero.
	WindowedPending WindowedStatus = "pending"
)

// ProjectedPnl is an account's P&L over (From, AsOf]: A(AsOf) - A(From),
// where A(t) = equity(t) - cumInflows(t) + cumOutflows(t). Deposits,
// withdrawals and transfers are flows and never count; realized and
// unrealized gains, fees, funding and liquidations do. Equals
// GetPnlHistory's P&L for the same From. Rank or sum rows only when their
// From matches.
type ProjectedPnl struct {
	Window string `json:"window"`
	// From is the window start (an hour boundary), RFC 3339.
	From string `json:"from"`
	// AsOf is when the value was computed. Empty while pending.
	AsOf string `json:"asOf,omitempty"`
	// Coverage is "full", or "since_creation" for an account younger than
	// the window.
	Coverage string `json:"coverage,omitempty"`
	// CoveredFrom is the account's first point, set with "since_creation".
	CoveredFrom string         `json:"coveredFrom,omitempty"`
	Status      WindowedStatus `json:"status"`
	// PnlUsd is exact decimal USD. Nil while pending.
	PnlUsd *string `json:"pnlUsd,omitempty"`
}

// ProjectedVolume is an account's traded volume over (From, AsOf]: the exact
// sum of |fill size × fill price| over its fill records booked in the window,
// liquidations and partial fills included, each fill counted once. Deposits,
// transfers, fees, funding and unfilled orders never count, and a price move
// never changes it.
type ProjectedVolume struct {
	Window string `json:"window"`
	// From is the window start (an hour boundary), RFC 3339.
	From string `json:"from"`
	// AsOf is the ledger read the sum is true at. Empty while pending.
	AsOf        string         `json:"asOf,omitempty"`
	Coverage    string         `json:"coverage,omitempty"`
	CoveredFrom string         `json:"coveredFrom,omitempty"`
	Status      WindowedStatus `json:"status"`
	// VolumeUsd is exact decimal USD. Nil while pending.
	VolumeUsd *string `json:"volumeUsd,omitempty"`
}

// ProjectedValuation is the projection-filtered view of an object valuation
// delivered to arca:ReadProjection readers. Identity fields are always
// present; everything else appears only when the projection's registered
// field set includes it.
type ProjectedValuation struct {
	ObjectID string `json:"objectId"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	// MidsComplete is present (as false) only when a position's mid price was
	// missing, so the price-derived fields understate reality.
	MidsComplete *bool `json:"midsComplete,omitempty"`
	// PricingMode "server" means the server prices the row and clients must
	// not re-mark it.
	PricingMode     string          `json:"pricingMode,omitempty"`
	Equity          *string         `json:"equity,omitempty"`
	RealizedValue   *string         `json:"realizedValue,omitempty"`
	UnrealizedValue *string         `json:"unrealizedValue,omitempty"`
	Positions       []PositionValue `json:"positions,omitempty"`
	// Pnl is the windowed P&L (projection field "pnl"), present when the read
	// asked for a window.
	Pnl *ProjectedPnl `json:"pnl,omitempty"`
	// Volume is the windowed volume (projection field "volume"), present when
	// the read asked for a window.
	Volume *ProjectedVolume `json:"volume,omitempty"`
}

var errProjectedIdentity = errors.New("arca: projected valuation has no objectId, path or type")

// UnmarshalJSON decodes a row leniently past its identity: a windowed figure
// this SDK cannot read is dropped on its own rather than failing the row.
func (v *ProjectedValuation) UnmarshalJSON(data []byte) error {
	type plain ProjectedValuation
	var aux struct {
		plain
		Pnl    json.RawMessage `json:"pnl"`
		Volume json.RawMessage `json:"volume"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.ObjectID == "" || aux.Path == "" || aux.Type == "" {
		return errProjectedIdentity
	}
	*v = ProjectedValuation(aux.plain)
	v.Pnl = nil
	if p := decodeFigure[ProjectedPnl](aux.Pnl); p != nil && p.From != "" && p.Status != "" {
		v.Pnl = p
	}
	v.Volume = nil
	if vol := decodeFigure[ProjectedVolume](aux.Volume); vol != nil && vol.From != "" && vol.Status != "" {
		v.Volume = vol
	}
	return nil
}

func decodeFigure[T any](raw json.RawMessage) *T {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out T
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}

// DecodeProjectedValuations decodes projection rows one by one, skipping rows
// that do not decode, so one row this SDK cannot read never empties a page.
func DecodeProjectedValuations(raw []json.RawMessage) []ProjectedValuation {
	out := make([]ProjectedValuation, 0, len(raw))
	for _, r := range raw {
		var v ProjectedValuation
		if json.Unmarshal(r, &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// ProjectionValuationsOptions narrows a registered projection; it cannot expand it.
type ProjectionValuationsOptions struct {
	Limit                int
	Cursor, Prefix, Path string
	Paths                []string
	// Window attaches the windowed pnl and volume figures the projection
	// grants, each with its own From and status.
	Window ProjectionWindow
}

// ProjectionValuationsResponse preserves the configured field set without
// imposing a fixed financial schema on arbitrary registered projections. Use
// Rows for the typed view.
type ProjectionValuationsResponse struct {
	Projection string            `json:"projection"`
	Fields     []string          `json:"fields"`
	Window     ProjectionWindow  `json:"window,omitempty"`
	Valuations []json.RawMessage `json:"valuations"`
	Cursor     string            `json:"cursor"`
	NextCursor string            `json:"nextCursor"`
}

// Rows decodes Valuations into typed rows, skipping rows that do not decode.
func (r ProjectionValuationsResponse) Rows() []ProjectedValuation {
	return DecodeProjectedValuations(r.Valuations)
}

func (r ProjectionValuationsResponse) nextCursor() string {
	if r.Cursor != "" {
		return r.Cursor
	}
	return r.NextCursor
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
	if opts.Window != "" {
		q.Set("window", string(opts.Window))
	}
	err = a.client.get(ctx, "/projections/"+url.PathEscape(name)+"/valuations", q, &out)
	return out, err
}
