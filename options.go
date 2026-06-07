package arca

// Request option structs for Arca methods. Fields map 1:1 to the TypeScript
// SDK's *Options interfaces.

type EnsureDenominatedArcaOptions struct {
	// Ref is the full Arca path (e.g. /users/u123/usd/main).
	Ref string
	// Metadata is optional opaque metadata.
	Metadata string
	// Labels are optional key/value labels.
	Labels map[string]string
	// OperationPath is an optional idempotency key.
	OperationPath string
}

type EnsureArcaOptions struct {
	Ref           string
	Type          ArcaObjectType
	Metadata      string
	Labels        map[string]string
	OperationPath string
}

type EnsureInfoOptions struct {
	// Ref is a directory path; /.info is appended automatically if absent.
	Ref           string
	Labels        map[string]string
	OperationPath string
}

type EnsureDeletedOptions struct {
	Ref                string
	SweepTo            string
	LiquidatePositions bool
	OperationPath      string
}

type TransferOptions struct {
	Path   string
	From   string
	To     string
	Amount string
	// FeeOverride overrides the transfer fee (e.g. "0"); non-production realms only.
	FeeOverride *string
}

type FeeEstimateParams struct {
	Action      string // "transfer" or "order"
	Amount      string
	SourcePath  string
	TargetPath  string
	FeeOverride *string
}

type FundAccountOptions struct {
	ArcaRef         string
	Amount          string
	Path            string
	SenderAddress   string
	DurationSeconds *int
	WillSucceed     *bool
}

type DefundAccountOptions struct {
	ArcaPath           string
	Amount             string
	DestinationAddress string
	Path               string
}

type CreatePaymentLinkOptions struct {
	Type               string // "deposit" or "withdrawal"
	ArcaRef            string
	Amount             string
	ReturnURL          string
	ReturnStrategy     ReturnStrategy
	ExpiresInMinutes   *int
	Metadata           map[string]any
	DestinationAddress string
}

type ListPaymentLinksOptions struct {
	Type   string
	Status string
}

type UpdateLabelsOptions struct {
	ObjectID string
	// Labels maps key -> value. Use a nil pointer value to remove a key.
	Labels map[string]*string
}

type UpdateFolderLabelsOptions struct {
	Path   string
	Labels map[string]string
}

type ListObjectsOptions struct {
	Path           string
	IncludeDeleted bool
	Limit          int
	Cursor         string
}

type BrowseObjectsOptions struct {
	Path           string
	IncludeDeleted bool
}

type CreateIsolationZoneOptions struct {
	Path string
}

type ListOperationsOptions struct {
	Type           OperationType
	Types          []OperationType
	ArcaPath       string
	ArcaID         string
	Path           string
	IncludeContext bool
	Limit          int
	Cursor         string
}

type GetOperationOptions struct {
	IncludeEvidence bool
}

type ExportOperationEvidenceOptions struct {
	From     string
	To       string
	Type     OperationType
	Types    []OperationType
	ArcaPath string
	ArcaID   string
	Path     string
	Limit    int
	Cursor   string
}

type ListEventsOptions struct {
	ArcaPath string
	Path     string
	Limit    int
	Cursor   string
}

// ---- Exchange ----

type CreatePerpsExchangeOptions struct {
	Ref string
	// Venue the exchange object trades against: "hl-sim" (default)
	// provisions a simulated Hyperliquid account for paper realms; "hl"
	// provisions a live Hyperliquid account. The legacy long forms
	// "sim-exchange" / "hyperliquid" are no longer accepted and are
	// rejected with a validation error; use the canonical "hl-sim" / "hl".
	Venue string
	// Deprecated: use Venue. ExchangeType carried no venue information (it
	// was always "hyperliquid") and is ignored. Removed in a future release.
	ExchangeType  string
	OperationPath string
}

type PlaceOrderOptions struct {
	Path        string
	ObjectID    string
	Market      string
	Side        OrderSide
	OrderType   string // "MARKET" or "LIMIT"
	Size        string
	Price       string
	Leverage    *int
	ReduceOnly  bool
	Isolated    bool
	TimeInForce string // "GTC" | "IOC" | "ALO"
	// ApplicationFeeTenthsBps is the application's fee on this order in tenths
	// of a basis point.
	ApplicationFeeTenthsBps *int
	FeeTargets              []FeeTarget
	IsTrigger               bool
	TriggerPx               string
	IsMarket                *bool
	Tpsl                    string // "tp" | "sl"
	// SizeToMax marks an unsized ("size to max") TP/SL: it carries no fixed
	// quantity and, when triggered, closes the entire current position
	// regardless of size (the trump card). Leave false for a sized TP/SL,
	// which closes its fixed Size (reduce-only caps it at the live position).
	// Either way, no TP/SL outlives the position.
	SizeToMax bool
	// OcoGroupID links this order to the other legs of a TP/SL bracket so a
	// fill on one leg cancels its siblings (one-cancels-the-other). Advisory
	// and unsigned — forwarded to the venue but never part of the signed order
	// digest. Usually left empty for a standalone order; SetPositionTpsl sets
	// it automatically for its two legs.
	OcoGroupID    string
	UseMax        bool
	SizeTolerance *float64
}

type ClosePositionOptions struct {
	Path        string
	ObjectID    string
	Market      string
	Size        string
	TimeInForce string
	// ApplicationFeeTenthsBps is the application's fee on this order in tenths
	// of a basis point.
	ApplicationFeeTenthsBps *int
	FeeTargets              []FeeTarget
	Isolated                *bool
	Leverage                *int
}

// SetPositionTriggerOptions parameterizes SetStopLoss / SetTakeProfit. The
// trigger is attached to the open position for Coin: the closing side is
// inferred (long → sell, short → buy), SizeToMax is set (unsized), and
// ReduceOnly is set. When the trigger fires it closes the entire live position
// regardless of size, and it is cancelled when the position closes.
type SetPositionTriggerOptions struct {
	Path     string
	ObjectID string
	Market   string
	// TriggerPx is the mark-price threshold that activates the order (required).
	TriggerPx string
	// IsMarket controls execution when the trigger fires: market (default) or
	// limit. Nil means market.
	IsMarket *bool
	// LimitPrice is the resting limit price used when IsMarket is false. Required
	// in that case; ignored for market triggers.
	LimitPrice string
	// Replace, when nil or true, cancels any existing unsized (sizeToMax) order
	// of the same tp/sl type for the coin before placing the new one. Set to a
	// pointer to false to stack multiple triggers.
	Replace *bool
	// Leverage overrides the position's leverage on the order body.
	Leverage *int
	// Isolated overrides the isolated-margin inference (defaults from market meta).
	Isolated *bool
	// Size, when set to a positive base-unit quantity, places a SIZED partial
	// reduce-only trigger that closes only that quantity when it fires
	// (reduce-only caps it at the live position). Empty (the default) places an
	// unsized (sizeToMax) trigger that closes the ENTIRE live position. Use a
	// sized trigger to scale out of a position — e.g. take profit on half.
	Size string
	// TimeInForce defaults to GTC.
	TimeInForce string
	// ApplicationFeeTenthsBps is the application's fee on this order in tenths
	// of a basis point.
	ApplicationFeeTenthsBps *int
	FeeTargets              []FeeTarget
	// OcoGroupID links this trigger to the other legs of a TP/SL bracket so a
	// fill on one leg cancels its siblings (one-cancels-the-other). Advisory
	// and unsigned. Normally left empty when calling SetStopLoss/SetTakeProfit
	// directly; SetPositionTpsl sets it on both legs it places.
	OcoGroupID string
}

// SetPositionTpslOptions parameterizes SetPositionTpsl, which attaches a
// stop-loss and/or take-profit to an open position in one call. At least one of
// StopLossPx / TakeProfitPx is required.
type SetPositionTpslOptions struct {
	Path         string
	ObjectID     string
	Market       string
	StopLossPx   string
	TakeProfitPx string
	IsMarket     *bool
	Replace      *bool
	// ApplicationFeeTenthsBps is the application's fee on this order in tenths
	// of a basis point.
	ApplicationFeeTenthsBps *int
	FeeTargets              []FeeTarget
	// StopLossSz / TakeProfitSz, when set to a positive base-unit quantity,
	// make the corresponding leg a SIZED partial reduce-only trigger (closing
	// only that quantity) instead of an unsized whole-position trigger. Empty
	// keeps the whole-position (sizeToMax) behavior for that leg.
	//
	// When EITHER leg is sized, the two legs are NOT auto-linked as
	// one-cancels-the-other — otherwise a PARTIAL fill of the sized leg would
	// cancel the sibling (e.g. scaling out half via the TP would drop the SL
	// protecting the remainder). Pass an explicit OcoGroupID if you do want the
	// two sized legs to cancel each other. Two unsized legs keep the auto-OCO.
	StopLossSz   string
	TakeProfitSz string
	// OcoGroupID overrides the auto-generated bracket id that links the SL and
	// TP legs as one-cancels-the-other. Leave empty to let SetPositionTpsl mint
	// a fresh opaque id (the common case for two unsized legs); set it only to
	// reuse a known group or to force-link sized legs.
	OcoGroupID string
}

// SetPositionTpslResult holds the handles for the legs placed by
// SetPositionTpsl. A leg is nil when its trigger price was not provided.
type SetPositionTpslResult struct {
	StopLoss   *OrderHandle
	TakeProfit *OrderHandle
}

// OpenBracketOptions parameterizes OpenWithBracket, which opens a position and
// attaches reduce-only TP/SL triggers in ONE atomic batch (Hyperliquid
// normalTpsl parity). The entry and its triggers are submitted as a single
// signed batch to one operation: the whole bracket validates and commits at the
// venue, or none of it does. The trigger legs arm only when the entry fills, and
// the venue links them with a shared one-cancels-the-other group. At least one
// of TakeProfitPx / StopLossPx is required.
type OpenBracketOptions struct {
	// Path is the operation idempotency key; it owns the whole bracket.
	Path     string
	ObjectID string
	Market   string
	// Side is the entry order side.
	Side OrderSide
	// OrderType is the entry order type ("MARKET" or "LIMIT"; default "MARKET").
	OrderType string
	// Size is the entry size in base-asset units.
	Size string
	// Price is the entry limit price (required when OrderType is "LIMIT").
	Price string
	// Leverage optionally overrides the entry leverage (required with Isolated).
	Leverage *int
	// Isolated targets the asset's isolated-margin bucket.
	Isolated bool
	// TimeInForce defaults to "GTC".
	TimeInForce string
	// ApplicationFeeTenthsBps is applied to every leg, in tenths of a bp.
	ApplicationFeeTenthsBps *int
	// TakeProfitPx is the take-profit trigger (mark) price. Empty skips the TP leg.
	TakeProfitPx string
	// StopLossPx is the stop-loss trigger (mark) price. Empty skips the SL leg.
	StopLossPx string
	// TakeProfitSz / StopLossSz, when set to a positive base-unit quantity, make
	// the corresponding trigger leg a SIZED partial reduce-only close (that
	// quantity only) instead of the default whole-position (sizeToMax) close.
	// Empty keeps the whole-position behavior.
	//
	// NOTE: the venue links a bracket's TP and SL legs as one-cancels-the-other,
	// and a fill on either — including a PARTIAL fill of a sized leg — cancels
	// the sibling. So a partial TP combined with an SL in the SAME bracket will
	// cancel that SL when the TP fills. To scale out and keep a stop on the
	// remainder, place the partial TP separately (SetTakeProfit with a Size) and
	// keep the stop unlinked, rather than in one bracket.
	TakeProfitSz string
	StopLossSz   string
	// TriggersAreMarket fires the TP/SL legs as market orders when triggered.
	// Nil defaults to true.
	TriggersAreMarket *bool
	// Grouping is the batch grouping ("normalTpsl" default, or "positionTpsl").
	Grouping string
}

// OpenBracketResult holds one OrderHandle per leg placed by OpenWithBracket.
// All handles are backed by the single bracket operation, so Wait on any of
// them resolves when the batch is placed; each handle's Filled/OnFill/Cancel
// target that specific leg's order. A leg is nil when its trigger price was not
// provided.
type OpenBracketResult struct {
	Entry      *OrderHandle
	TakeProfit *OrderHandle
	StopLoss   *OrderHandle
}

// ClearPositionTpslOptions parameterizes ClearPositionTpsl. Tpsl ("tp" or "sl")
// narrows the clear to a single leg; empty clears both.
type ClearPositionTpslOptions struct {
	Path     string
	ObjectID string
	Market   string
	Tpsl     string
}

type CancelOrderOptions struct {
	Path     string
	ObjectID string
	OrderID  string
}

// ModifyOrderOptions resizes a resting order. Only sized orders can be resized
// (resting limit orders and sized TP/SL triggers); unsized ("size to max")
// TP/SL triggers are rejected by the venue. NewSize is the new TOTAL size and
// must exceed the order's already-filled quantity. Path is a per-resize
// idempotency key.
type ModifyOrderOptions struct {
	Path     string
	ObjectID string
	OrderID  string
	NewSize  string
}

type UpdateLeverageOptions struct {
	ObjectID string
	Market   string
	Leverage int
}

// UpdateIsolatedMarginOptions parameterizes UpdateIsolatedMargin. Amount is a
// signed decimal USD string: positive adds collateral to the isolated position
// (lowering its liquidation price), negative removes it (raising it).
type UpdateIsolatedMarginOptions struct {
	ObjectID string
	Market   string
	Amount   string
}

// SetMarginModeOptions parameterizes SetMarginMode. MarginMode switches the
// asset to cross or isolated margin.
type SetMarginModeOptions struct {
	ObjectID   string
	Market     string
	MarginMode MarginMode
}

type PlaceTwapOptions struct {
	Path            string
	ExchangeID      string
	Market          string
	Side            OrderSide
	TotalSize       string
	DurationMinutes int
	IntervalSeconds *int
	Randomize       *bool
	ReduceOnly      *bool
	Leverage        *int
	SlippageBps     *int
}

type ListFillsOptions struct {
	Market    string
	StartTime string
	EndTime   string
	Limit     int
	Cursor    string
}

type TradeSummaryOptions struct {
	StartTime string
	EndTime   string
}

type GetCandlesOptions struct {
	StartTime    *int64
	EndTime      *int64
	SkipBackfill bool
}
