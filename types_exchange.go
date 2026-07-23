package arca

// ---- Exchange / Perps ----

type OrderSide string

const (
	Buy  OrderSide = "buy"
	Sell OrderSide = "sell"
)

type PositionSide string

const (
	Long  PositionSide = "long"
	Short PositionSide = "short"
)

type OrderStatus string

const (
	OrderPending         OrderStatus = "PENDING"
	OrderOpen            OrderStatus = "OPEN"
	OrderPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	OrderFilled          OrderStatus = "FILLED"
	OrderCancelled       OrderStatus = "CANCELLED"
	OrderFailed          OrderStatus = "FAILED"
	OrderWaitingTrigger  OrderStatus = "WAITING_FOR_TRIGGER"
	OrderTriggered       OrderStatus = "TRIGGERED"
)

type LeverageType string

const (
	LeverageCross    LeverageType = "cross"
	LeverageIsolated LeverageType = "isolated"
)

type MarginMode string

const (
	MarginModeCross    MarginMode = "cross"
	MarginModeIsolated MarginMode = "isolated"
)

type FeeTarget struct {
	ArcaPath   string `json:"arcaPath"`
	Percentage int    `json:"percentage"`
}

type OrderOperationResponse struct {
	Operation Operation           `json:"operation"`
	Fee       *AmountDenomination `json:"fee,omitempty"`
}

func (r OrderOperationResponse) op() *Operation { return &r.Operation }
func (r *OrderOperationResponse) setOp(o *Operation) {
	if o != nil {
		r.Operation = *o
	}
}

type SimAccount struct {
	ID        string `json:"id"`
	RealmID   string `json:"realmId"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type SimMarginSummary struct {
	Equity                    string `json:"equity"`
	InitialMarginUsed         string `json:"initialMarginUsed"`
	MaintenanceMarginRequired string `json:"maintenanceMarginRequired"`
	AvailableToWithdraw       string `json:"availableToWithdraw"`
	TotalNtlPos               string `json:"totalNtlPos"`
	TotalUnrealizedPnl        string `json:"totalUnrealizedPnl"`
	TotalRawUsd               string `json:"totalRawUsd,omitempty"`
}

type SimPosition struct {
	ID         string       `json:"id"`
	AccountID  string       `json:"accountId"`
	RealmID    string       `json:"realmId"`
	Market     string       `json:"market"`
	Side       PositionSide `json:"side"`
	Size       string       `json:"size"`
	EntryPrice string       `json:"entryPrice"`
	Leverage   int          `json:"leverage"`
	MarginUsed string       `json:"marginUsed"`
	// MarginMode is "cross" or "isolated". Isolated positions carry their own
	// dedicated collateral (IsolatedMargin) and are liquidated independently of
	// the cross pool.
	MarginMode MarginMode `json:"marginMode"`
	// IsolatedMargin is the locked collateral for an isolated position (decimal
	// string). May exceed the leverage-implied margin after UpdateIsolatedMargin.
	// Nil for cross positions.
	IsolatedMargin        *string `json:"isolatedMargin,omitempty"`
	LiquidationPrice      *string `json:"liquidationPrice"`
	UnrealizedPnl         *string `json:"unrealizedPnl"`
	ReturnOnEquity        *string `json:"returnOnEquity"`
	PositionValue         *string `json:"positionValue"`
	CumulativeFunding     *string `json:"cumulativeFunding,omitempty"`
	CumulativeFee         *string `json:"cumulativeFee,omitempty"`
	CumulativeExchangeFee *string `json:"cumulativeExchangeFee,omitempty"`
	CumulativePlatformFee *string `json:"cumulativePlatformFee,omitempty"`
	CumulativeBuilderFee  *string `json:"cumulativeBuilderFee,omitempty"`
	Error                 *string `json:"error,omitempty"`
	CreatedAt             string  `json:"createdAt"`
	UpdatedAt             string  `json:"updatedAt"`
}

type PositionListResponse struct {
	Positions []SimPosition `json:"positions"`
	Total     int           `json:"total"`
}

type SimOrder struct {
	ID           string      `json:"id"`
	AccountID    string      `json:"accountId"`
	RealmID      string      `json:"realmId"`
	Market       string      `json:"market"`
	Side         OrderSide   `json:"side"`
	OrderType    string      `json:"orderType"`
	Price        *string     `json:"price"`
	Size         string      `json:"size"`
	FilledSize   string      `json:"filledSize"`
	AvgFillPrice *string     `json:"avgFillPrice"`
	Status       OrderStatus `json:"status"`
	ReduceOnly   bool        `json:"reduceOnly"`
	TimeInForce  string      `json:"timeInForce"`
	Leverage     int         `json:"leverage"`
	// BuilderFeeBps echoes the venue's builder-code fee on the order (the
	// value configured via the order's applicationFeeTenthsBps).
	BuilderFeeBps *int    `json:"builderFeeBps,omitempty"`
	IsTrigger     bool    `json:"isTrigger,omitempty"`
	TriggerPx     *string `json:"triggerPx,omitempty"`
	IsMarket      bool    `json:"isMarket,omitempty"`
	Tpsl          string  `json:"tpsl,omitempty"`
	// SizeToMax is true for an unsized ("size to max") TP/SL that closes the
	// entire position when fired.
	SizeToMax bool `json:"sizeToMax,omitempty"`
	// OcoGroupID links the legs of a TP/SL bracket so that when one leg fills
	// (even partially) the venue cancels the sibling legs sharing this id
	// (one-cancels-the-other). Empty for a standalone order. SetPositionTpsl
	// assigns one id to both legs; it is advisory and never part of the signed
	// order digest.
	OcoGroupID string `json:"ocoGroupId,omitempty"`
	// CancelReason explains why a CANCELLED order was cancelled — one of
	// user_requested, sibling_filled, position_closed, position_flipped,
	// liquidated, position_gone. Empty unless Status == "CANCELLED".
	CancelReason string `json:"cancelReason,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type OrderListResponse struct {
	Orders []SimOrder `json:"orders"`
	Total  int        `json:"total"`
}

type SimFill struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"orderId"`
	AccountID     string    `json:"accountId,omitempty"`
	RealmID       string    `json:"realmId,omitempty"`
	Market        string    `json:"market"`
	Side          OrderSide `json:"side"`
	Price         string    `json:"price"`
	Size          string    `json:"size"`
	Fee           string    `json:"fee"`
	BuilderFee    string    `json:"builderFee,omitempty"`
	Cloid         string    `json:"cloid,omitempty"`
	IsMaker       bool      `json:"isMaker,omitempty"`
	PlatformFee   string    `json:"platformFee,omitempty"`
	RealizedPnl   *string   `json:"realizedPnl"`
	IsLiquidation bool      `json:"isLiquidation"`
	CreatedAt     string    `json:"createdAt,omitempty"`
	IsOptimistic  bool      `json:"isOptimistic,omitempty"`
}

type SimOrderWithFills struct {
	Order SimOrder  `json:"order"`
	Fills []SimFill `json:"fills"`
}

type FundingPayment struct {
	AccountID   string `json:"accountId"`
	Market      string `json:"market"`
	Side        string `json:"side"`
	Size        string `json:"size"`
	Price       string `json:"price"`
	FundingRate string `json:"fundingRate"`
	Payment     string `json:"payment"`
}

type FillResultingPosition struct {
	Side     PositionSide `json:"side"`
	Size     string       `json:"size"`
	EntryPx  string       `json:"entryPx,omitempty"`
	Leverage int          `json:"leverage"`
}

type Fill struct {
	ID                string                 `json:"id"`
	OperationID       string                 `json:"operationId,omitempty"`
	FillID            string                 `json:"fillId,omitempty"`
	OrderOperationID  string                 `json:"orderOperationId,omitempty"`
	OrderID           string                 `json:"orderId,omitempty"`
	Market            string                 `json:"market"`
	Side              OrderSide              `json:"side,omitempty"`
	Size              string                 `json:"size,omitempty"`
	Price             string                 `json:"price,omitempty"`
	Direction         string                 `json:"direction,omitempty"`
	StartPosition     string                 `json:"startPosition,omitempty"`
	Fee               string                 `json:"fee,omitempty"`
	ExchangeFee       string                 `json:"exchangeFee,omitempty"`
	PlatformFee       string                 `json:"platformFee,omitempty"`
	BuilderFee        string                 `json:"builderFee,omitempty"`
	RealizedPnl       string                 `json:"realizedPnl,omitempty"`
	ResultingPosition *FillResultingPosition `json:"resultingPosition,omitempty"`
	IsLiquidation     bool                   `json:"isLiquidation,omitempty"`
	CreatedAt         string                 `json:"createdAt"`
	IsOptimistic      bool                   `json:"isOptimistic,omitempty"`
}

type FillListResponse struct {
	Fills  []Fill `json:"fills"`
	Total  int    `json:"total"`
	Cursor string `json:"cursor,omitempty"`
}

type OpenPositionCosts struct {
	ExchangeFees    string `json:"exchangeFees"`
	PlatformFees    string `json:"platformFees"`
	BuilderFees     string `json:"builderFees"`
	FundingPaid     string `json:"fundingPaid"`
	FundingReceived string `json:"fundingReceived"`
	Total           string `json:"total"`
}

type MarketTradeSummary struct {
	Market               string             `json:"market"`
	TotalRealizedPnl     string             `json:"totalRealizedPnl"`
	TotalFees            string             `json:"totalFees"`
	TotalExchangeFees    string             `json:"totalExchangeFees,omitempty"`
	TotalPlatformFees    string             `json:"totalPlatformFees,omitempty"`
	TotalBuilderFees     string             `json:"totalBuilderFees,omitempty"`
	TotalFundingPaid     string             `json:"totalFundingPaid,omitempty"`
	TotalFundingReceived string             `json:"totalFundingReceived,omitempty"`
	TradeCount           int                `json:"tradeCount"`
	TotalVolume          string             `json:"totalVolume"`
	OpenPositionCosts    *OpenPositionCosts `json:"openPositionCosts,omitempty"`
}

type TradeSummaryTotals struct {
	TotalRealizedPnl     string `json:"totalRealizedPnl"`
	TotalFees            string `json:"totalFees"`
	TotalExchangeFees    string `json:"totalExchangeFees,omitempty"`
	TotalPlatformFees    string `json:"totalPlatformFees,omitempty"`
	TotalBuilderFees     string `json:"totalBuilderFees,omitempty"`
	TotalFundingPaid     string `json:"totalFundingPaid,omitempty"`
	TotalFundingReceived string `json:"totalFundingReceived,omitempty"`
	TradeCount           int    `json:"tradeCount"`
	TotalVolume          string `json:"totalVolume"`
}

type TradeSummaryResponse struct {
	Markets []MarketTradeSummary `json:"markets"`
	Totals  TradeSummaryTotals   `json:"totals"`
}

type SimFeeTierEntry struct {
	Tier         int    `json:"tier"`
	Label        string `json:"label"`
	MinVolume14d int64  `json:"minVolume14d"`
	TakerBps     int    `json:"takerBps"`
	MakerBps     int    `json:"makerBps"`
}

type SimFeeRates struct {
	Taker       string            `json:"taker"`
	Maker       string            `json:"maker"`
	PlatformFee string            `json:"platformFee,omitempty"`
	Tier        int               `json:"tier,omitempty"`
	TierLabel   string            `json:"tierLabel,omitempty"`
	Volume14d   string            `json:"volume14d,omitempty"`
	Schedule    []SimFeeTierEntry `json:"schedule,omitempty"`
}

type ExchangeIntent struct {
	OperationID   string    `json:"operationId"`
	OperationPath string    `json:"operationPath"`
	Market        string    `json:"market"`
	Side          OrderSide `json:"side"`
	Size          string    `json:"size"`
	OrderType     string    `json:"orderType"`
	ReduceOnly    bool      `json:"reduceOnly"`
	CreatedAt     string    `json:"createdAt"`
}

type ExchangeState struct {
	Account                    SimAccount        `json:"account"`
	MarginSummary              SimMarginSummary  `json:"marginSummary"`
	CrossMarginSummary         *SimMarginSummary `json:"crossMarginSummary,omitempty"`
	CrossMaintenanceMarginUsed string            `json:"crossMaintenanceMarginUsed,omitempty"`
	Positions                  []SimPosition     `json:"positions"`
	OpenOrders                 []SimOrder        `json:"openOrders"`
	FeeRates                   *SimFeeRates      `json:"feeRates,omitempty"`
	PendingIntents             []ExchangeIntent  `json:"pendingIntents"`
}

type AssetFeeEntry struct {
	Market       string `json:"market"`
	TakerFeeRate string `json:"takerFeeRate"`
	MakerFeeRate string `json:"makerFeeRate"`
}

type LeverageInfo struct {
	Type  LeverageType `json:"type"`
	Value int          `json:"value"`
}

type MarginTier struct {
	LowerBound  string `json:"lowerBound"`
	MaxLeverage int    `json:"maxLeverage"`
}

type MarginTable struct {
	Description string       `json:"description"`
	MarginTiers []MarginTier `json:"marginTiers"`
}

type ActiveAssetData struct {
	Market                string       `json:"market"`
	Leverage              LeverageInfo `json:"leverage"`
	MaxBuySize            string       `json:"maxBuySize"`
	MaxSellSize           string       `json:"maxSellSize"`
	MaxBuyUsd             string       `json:"maxBuyUsd"`
	MaxSellUsd            string       `json:"maxSellUsd"`
	AvailableToTrade      string       `json:"availableToTrade"`
	MarkPx                string       `json:"markPx"`
	FeeRate               string       `json:"feeRate"`
	MaintenanceMarginRate string       `json:"maintenanceMarginRate"`
	MarginTiers           []MarginTier `json:"marginTiers,omitempty"`
	// BidPx / AskPx are the top-of-book best bid / best ask at snapshot time.
	// Market sells are margin-checked at the bid, buys at the ask, so these are
	// the directional execution prices behind MaxSellSize / MaxBuySize. Equal to
	// MarkPx when no order book is available.
	BidPx string `json:"bidPx"`
	AskPx string `json:"askPx"`
}

type UpdateLeverageResponse struct {
	AccountID        string `json:"accountId"`
	Market           string `json:"market"`
	Leverage         int    `json:"leverage"`
	PreviousLeverage int    `json:"previousLeverage"`
}

type LeverageSetting struct {
	Market     string     `json:"market"`
	Leverage   int        `json:"leverage"`
	MarginMode MarginMode `json:"marginMode"`
}

// UpdateIsolatedMarginResponse is returned by UpdateIsolatedMargin: the
// resulting locked isolated collateral and recomputed liquidation price.
type UpdateIsolatedMarginResponse struct {
	AccountID        string `json:"accountId"`
	Market           string `json:"market"`
	IsolatedMargin   string `json:"isolatedMargin"`
	LiquidationPrice string `json:"liquidationPrice"`
}

// SetMarginModeResponse is returned by SetMarginMode with the asset's new
// margin mode.
type SetMarginModeResponse struct {
	AccountID  string     `json:"accountId"`
	Market     string     `json:"market"`
	MarginMode MarginMode `json:"marginMode"`
}

// ---- TWAP ----

type TwapStatus string

const (
	TwapActive    TwapStatus = "active"
	TwapCompleted TwapStatus = "completed"
	TwapCancelled TwapStatus = "cancelled"
	TwapFailed    TwapStatus = "failed"
)

type Twap struct {
	TwapID              string     `json:"twapId"`
	RealmID             string     `json:"realmId"`
	OperationID         string     `json:"operationId"`
	ExchangeObjectID    string     `json:"exchangeObjectId"`
	ExchangeObjectPath  string     `json:"exchangeObjectPath"`
	SimAccountID        string     `json:"simAccountId"`
	Type                string     `json:"type"`
	Market              string     `json:"market"`
	Side                OrderSide  `json:"side"`
	TotalSize           *string    `json:"totalSize"`
	ExecutedSize        string     `json:"executedSize"`
	ExecutedNotional    string     `json:"executedNotional"`
	SliceCount          int        `json:"sliceCount"`
	ExpectedSliceCount  int        `json:"expectedSliceCount"`
	FilledSlices        int        `json:"filledSlices"`
	FailedSlices        int        `json:"failedSlices"`
	IntervalSeconds     int        `json:"intervalSeconds"`
	DurationMinutes     int        `json:"durationMinutes"`
	StartTime           string     `json:"startTime"`
	EndTime             *string    `json:"endTime"`
	Status              TwapStatus `json:"status"`
	CancelReason        *string    `json:"cancelReason"`
	FailureReason       *string    `json:"failureReason"`
	TargetPrice         *string    `json:"targetPrice"`
	ReduceOnly          bool       `json:"reduceOnly"`
	Leverage            *int       `json:"leverage"`
	SlippageBps         int        `json:"slippageBps"`
	Randomize           bool       `json:"randomize"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	CreatedAt           string     `json:"createdAt"`
	UpdatedAt           string     `json:"updatedAt"`
}

type TwapOperationResponse struct {
	Twap      Twap       `json:"twap"`
	Operation *Operation `json:"operation"`
}

func (r TwapOperationResponse) op() *Operation      { return r.Operation }
func (r *TwapOperationResponse) setOp(o *Operation) { r.Operation = o }

type TwapLimitsConfig struct {
	MinTotalSize           string `json:"minTotalSize"`
	MaxDurationMinutes     int    `json:"maxDurationMinutes"`
	MinIntervalSeconds     int    `json:"minIntervalSeconds"`
	MaxIntervalSeconds     int    `json:"maxIntervalSeconds"`
	MinSlippageBps         int    `json:"minSlippageBps"`
	MaxSlippageBps         int    `json:"maxSlippageBps"`
	DefaultIntervalSeconds int    `json:"defaultIntervalSeconds"`
	DefaultSlippageBps     int    `json:"defaultSlippageBps"`
	MaxConcurrentPerObject int    `json:"maxConcurrentPerObject"`
}

type TwapRecommendationBucket struct {
	MaxDurationMinutes         int `json:"maxDurationMinutes"`
	RecommendedIntervalSeconds int `json:"recommendedIntervalSeconds"`
}

type TwapRecommendationCurve struct {
	Buckets []TwapRecommendationBucket `json:"buckets"`
}

type TwapLimits struct {
	Limits         TwapLimitsConfig        `json:"limits"`
	Recommendation TwapRecommendationCurve `json:"recommendation"`
}

type OrderLimits struct {
	MinOrderNotionalUsd float64 `json:"minOrderNotionalUsd"`
}

// MinOrderSizeOptions is the input to (*Arca).MinOrderSize. Provide either a
// resolved Market (no fetch) or a MarketID (looked up via Market()).
type MinOrderSizeOptions struct {
	// Market, when non-nil, is used directly without a metadata fetch.
	Market *Market
	// MarketID is the canonical id (e.g. "hl:0:BTC"), used when Market is nil.
	MarketID string
	// Price is the reference price for the notional→size conversion (mark price
	// for market orders, limit price for limit orders), as a decimal string.
	Price string
	// ReduceOnly orders are exempt from the minimum so dust positions can be closed.
	ReduceOnly bool
	// IsTrigger marks stop / take-profit trigger orders.
	IsTrigger bool
	// SizeToMax marks unsized trigger orders, which are exempt from the minimum.
	SizeToMax bool
}

// MinOrderSizeResult is the output of (*Arca).MinOrderSize.
type MinOrderSizeResult struct {
	// MinSize is the minimum order size in base-asset units (decimal string),
	// rounded up to the market's szDecimals precision. For exempt orders it is
	// a single size tick (10^-szDecimals).
	MinSize string `json:"minSize"`
	// MinNotionalUsd is the USD notional floor applied. Zero for exempt orders.
	MinNotionalUsd float64 `json:"minNotionalUsd"`
}

// ValidateOrderSizeOptions is the input to (*Arca).ValidateOrderSize.
type ValidateOrderSizeOptions struct {
	Market     *Market
	MarketID   string
	Price      string
	Size       string
	ReduceOnly bool
	IsTrigger  bool
	SizeToMax  bool
}

// OrderSizeValidation is the output of (*Arca).ValidateOrderSize.
type OrderSizeValidation struct {
	OK             bool    `json:"ok"`
	Reason         string  `json:"reason,omitempty"`
	MinSize        string  `json:"minSize"`
	MinNotionalUsd float64 `json:"minNotionalUsd"`
}

// ---- Market data ----

type CandleInterval string

const (
	Interval15s CandleInterval = "15s"
	Interval1m  CandleInterval = "1m"
	Interval5m  CandleInterval = "5m"
	Interval15m CandleInterval = "15m"
	Interval1h  CandleInterval = "1h"
	Interval4h  CandleInterval = "4h"
	Interval1d  CandleInterval = "1d"
)

type CandleHistoryBounds struct {
	EarliestMs   int64 `json:"earliestMs"`
	HlEarliestMs int64 `json:"hlEarliestMs"`
}

type LogoSource struct {
	URL    string `json:"url"`
	Format string `json:"format"`
	Width  int    `json:"width"`
}

// Market is a single tradable market's metadata. The Name field is the
// canonical market id in the readable {exchange}:{dexIndex}:{symbol} form
// (e.g. "hl:0:BTC", "hl:1:TSLA") and is the value to pass to Market,
// trading, and market-data APIs. Symbol is display-only.
type Market struct {
	Name                string       `json:"name"`
	Dex                 string       `json:"dex,omitempty"`
	Symbol              string       `json:"symbol"`
	VenueSymbol         string       `json:"venueSymbol,omitempty"`
	DisplayName         string       `json:"displayName,omitempty"`
	LogoURL             string       `json:"logoUrl,omitempty"`
	LogoSources         []LogoSource `json:"logoSources,omitempty"`
	Exchange            string       `json:"exchange"`
	AssetType           string       `json:"assetType,omitempty"`
	CategoryLabel       string       `json:"categoryLabel,omitempty"`
	Mapped              bool         `json:"mapped"`
	HasDisplayName      bool         `json:"hasDisplayName"`
	HasLogo             bool         `json:"hasLogo"`
	DescriptionStatus   string       `json:"descriptionStatus,omitempty"`
	IsHip3              bool         `json:"isHip3,omitempty"`
	DeployerDisplayName string       `json:"deployerDisplayName,omitempty"`
	Index               int          `json:"index"`
	SzDecimals          int          `json:"szDecimals"`
	MaxLeverage         int          `json:"maxLeverage"`
	// MinOrderNotionalUsd is the minimum order notional in USD (size*price)
	// for this market. Use MinOrderSize to convert it into a minimum order
	// size in base-asset units, or ValidateOrderSize to check a size before
	// PlaceOrder. Reduce-only orders and unsized (SizeToMax) triggers are
	// exempt. May be zero when served by an older backend; clients fall back
	// to the venue-wide GetOrderLimits default.
	MinOrderNotionalUsd float64 `json:"minOrderNotionalUsd,omitempty"`
	// OnlyIsolated is Hyperliquid-specific and deprecated in favor of
	// MarginModes. OnlyIsolated==true is equivalent to MarginModes
	// being ["isolated"]. Independent of HIP-3 — some HIP-3 markets
	// (e.g. hl:1:TSLA) are cross-eligible.
	OnlyIsolated bool `json:"onlyIsolated"`
	// MarginModes is the explicit, exchange-agnostic set of margin modes
	// the asset supports: ["isolated"] for isolated-only markets,
	// ["cross","isolated"] otherwise. Read this instead of inferring from
	// OnlyIsolated or IsHip3. May be nil when served by an older backend.
	MarginModes   []string             `json:"marginModes,omitempty"`
	FeeScale      float64              `json:"feeScale,omitempty"`
	MarginTableID int                  `json:"marginTableId,omitempty"`
	CandleHistory *CandleHistoryBounds `json:"candleHistory,omitempty"`
}

type SimMetaResponse struct {
	Universe     []Market               `json:"universe"`
	MarginTables map[string]MarginTable `json:"marginTables,omitempty"`
}

type SimMidsResponse struct {
	Mids map[string]string `json:"mids"`
}

type MarketTicker struct {
	Market            string  `json:"market"`
	Dex               string  `json:"dex,omitempty"`
	Symbol            string  `json:"symbol"`
	Exchange          string  `json:"exchange"`
	MarkPx            string  `json:"markPx"`
	MidPx             string  `json:"midPx"`
	PrevDayPx         string  `json:"prevDayPx"`
	DayNtlVlm         string  `json:"dayNtlVlm"`
	PriceChange24hPct string  `json:"priceChange24hPct"`
	OpenInterest      string  `json:"openInterest"`
	Funding           string  `json:"funding"`
	NextFundingTime   int64   `json:"nextFundingTime,omitempty"`
	FeeScale          float64 `json:"feeScale"`
	IsDelisted        bool    `json:"isDelisted"`
}

type MarketTickersResponse struct {
	Tickers []MarketTicker `json:"tickers"`
}

type SimBookLevel struct {
	Price      string `json:"price"`
	Size       string `json:"size"`
	OrderCount int    `json:"orderCount"`
}

type SimBookResponse struct {
	Market string         `json:"market"`
	Bids   []SimBookLevel `json:"bids"`
	Asks   []SimBookLevel `json:"asks"`
	Time   int64          `json:"time"`
}

type Candle struct {
	T int64  `json:"t"`
	O string `json:"o"`
	H string `json:"h"`
	L string `json:"l"`
	C string `json:"c"`
	V string `json:"v"`
	N int    `json:"n"`
	S string `json:"s,omitempty"`
}

type CandlesResponse struct {
	Market   string   `json:"market"`
	Interval string   `json:"interval"`
	Candles  []Candle `json:"candles"`
}

// OIBar is a single open-interest / 24h-notional bar. The OHLC values track
// open interest (base-asset units) over the bucket; NtlVlm is the rolling 24h
// notional volume (USD) at bucket close; Mark is the last mark price in the
// bucket (USD OI ~= OIClose * Mark). S is the data source ("" self-recorded,
// "0xa" 0xArchive backfill).
type OIBar struct {
	T       int64  `json:"t"`
	OIOpen  string `json:"oiOpen"`
	OIHigh  string `json:"oiHigh"`
	OILow   string `json:"oiLow"`
	OIClose string `json:"oiClose"`
	NtlVlm  string `json:"ntlVlm"`
	Mark    string `json:"mark,omitempty"`
	S       string `json:"s,omitempty"`
}

type OIHistoryResponse struct {
	Market   string  `json:"market"`
	Interval string  `json:"interval"`
	Bars     []OIBar `json:"bars"`
}

// FundingObservation is a single SETTLED funding-rate observation for a market.
// Unlike OIBar / Candle it is not interval-bucketed — it is a raw event at the
// venue's real settlement timestamp (T, Unix ms), so a market's true funding
// schedule is preserved. FundingRate/Premium are settled historical rates,
// never predicted (use the ticker's Funding + NextFundingTime for the
// current/predicted rate). S is the data source ("hl").
type FundingObservation struct {
	T           int64  `json:"t"`
	FundingRate string `json:"fundingRate"`
	Premium     string `json:"premium,omitempty"`
	S           string `json:"s,omitempty"`
}

type FundingHistoryResponse struct {
	Market  string               `json:"market"`
	Funding []FundingObservation `json:"funding"`
}

// OIEvent is delivered by WatchOI on each live open-interest bar update.
type OIEvent struct {
	Market   string         `json:"market"`
	Interval CandleInterval `json:"interval"`
	Bar      OIBar          `json:"bar"`
	IsClosed bool           `json:"isClosed,omitempty"`
}

type MarketTrade struct {
	Market string `json:"market"`
	Px     string `json:"px"`
	Sz     string `json:"sz"`
	Side   string `json:"side"`
	Time   string `json:"time"`
	Hash   string `json:"hash,omitempty"`
}

type SparklinesResponse struct {
	Sparklines map[string][]float64 `json:"sparklines"`
}

// ---- Predicted effect ----

type PredictedBalanceChange struct {
	Departing string `json:"departing,omitempty"`
	Arriving  string `json:"arriving,omitempty"`
}

type PredictedOrderIntent struct {
	Market     string `json:"market"`
	Side       string `json:"side"`
	Size       string `json:"size"`
	ReduceOnly bool   `json:"reduceOnly"`
}

type PredictedEffect struct {
	Type           string                            `json:"type"`
	BalanceChanges map[string]PredictedBalanceChange `json:"balanceChanges,omitempty"`
	OrderIntent    *PredictedOrderIntent             `json:"orderIntent,omitempty"`
}
