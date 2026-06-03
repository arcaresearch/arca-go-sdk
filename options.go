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
	Ref           string
	ExchangeType  string // currently only "hyperliquid"
	OperationPath string
}

type PlaceOrderOptions struct {
	Path          string
	ObjectID      string
	Coin          string
	Side          OrderSide
	OrderType     string // "MARKET" or "LIMIT"
	Size          string
	Price         string
	Leverage      *int
	ReduceOnly    bool
	Isolated      bool
	TimeInForce   string // "GTC" | "IOC" | "ALO"
	BuilderFeeBps *int
	FeeTargets    []FeeTarget
	IsTrigger     bool
	TriggerPx     string
	IsMarket      *bool
	Tpsl          string // "tp" | "sl"
	Grouping      string // "na" | "normalTpsl" | "positionTpsl"
	UseMax        bool
	SizeTolerance *float64
}

type ClosePositionOptions struct {
	Path          string
	ObjectID      string
	Coin          string
	Size          string
	TimeInForce   string
	BuilderFeeBps *int
	FeeTargets    []FeeTarget
	Isolated      *bool
	Leverage      *int
}

// SetPositionTriggerOptions parameterizes SetStopLoss / SetTakeProfit. The
// trigger is attached to the open position for Coin: the closing side is
// inferred (LONG → SELL, SHORT → BUY), Grouping is positionTpsl, ReduceOnly is
// set, and Size is "0" so the venue fills the trigger from — and resizes it
// with — the live position.
type SetPositionTriggerOptions struct {
	Path     string
	ObjectID string
	Coin     string
	// TriggerPx is the mark-price threshold that activates the order (required).
	TriggerPx string
	// IsMarket controls execution when the trigger fires: market (default) or
	// limit. Nil means market.
	IsMarket *bool
	// LimitPrice is the resting limit price used when IsMarket is false. Required
	// in that case; ignored for market triggers.
	LimitPrice string
	// Replace, when nil or true, cancels any existing positionTpsl order of the
	// same tp/sl type for the coin before placing the new one. Set to a pointer
	// to false to stack multiple triggers.
	Replace *bool
	// Leverage overrides the position's leverage on the order body.
	Leverage *int
	// Isolated overrides the isolated-margin inference (defaults from market meta).
	Isolated *bool
	// TimeInForce defaults to GTC.
	TimeInForce   string
	BuilderFeeBps *int
	FeeTargets    []FeeTarget
}

// SetPositionTpslOptions parameterizes SetPositionTpsl, which attaches a
// stop-loss and/or take-profit to an open position in one call. At least one of
// StopLossPx / TakeProfitPx is required.
type SetPositionTpslOptions struct {
	Path          string
	ObjectID      string
	Coin          string
	StopLossPx    string
	TakeProfitPx  string
	IsMarket      *bool
	Replace       *bool
	BuilderFeeBps *int
	FeeTargets    []FeeTarget
}

// SetPositionTpslResult holds the handles for the legs placed by
// SetPositionTpsl. A leg is nil when its trigger price was not provided.
type SetPositionTpslResult struct {
	StopLoss   *OrderHandle
	TakeProfit *OrderHandle
}

// ClearPositionTpslOptions parameterizes ClearPositionTpsl. Tpsl ("tp" or "sl")
// narrows the clear to a single leg; empty clears both.
type ClearPositionTpslOptions struct {
	Path     string
	ObjectID string
	Coin     string
	Tpsl     string
}

type CancelOrderOptions struct {
	Path     string
	ObjectID string
	OrderID  string
}

type UpdateLeverageOptions struct {
	ObjectID string
	Coin     string
	Leverage int
}

// UpdateIsolatedMarginOptions parameterizes UpdateIsolatedMargin. Amount is a
// signed decimal USD string: positive adds collateral to the isolated position
// (lowering its liquidation price), negative removes it (raising it).
type UpdateIsolatedMarginOptions struct {
	ObjectID string
	Coin     string
	Amount   string
}

// SetMarginModeOptions parameterizes SetMarginMode. MarginMode switches the
// asset to cross or isolated margin.
type SetMarginModeOptions struct {
	ObjectID   string
	Coin       string
	MarginMode MarginMode
}

type PlaceTwapOptions struct {
	Path            string
	ExchangeID      string
	Coin            string
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
