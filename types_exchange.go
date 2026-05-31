package arca

// ---- Exchange / Perps ----

type OrderSide string

const (
	Buy  OrderSide = "BUY"
	Sell OrderSide = "SELL"
)

type PositionSide string

const (
	Long  PositionSide = "LONG"
	Short PositionSide = "SHORT"
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
	Coin       string       `json:"coin"`
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
	ID            string      `json:"id"`
	AccountID     string      `json:"accountId"`
	RealmID       string      `json:"realmId"`
	Coin          string      `json:"coin"`
	Side          OrderSide   `json:"side"`
	OrderType     string      `json:"orderType"`
	Price         *string     `json:"price"`
	Size          string      `json:"size"`
	FilledSize    string      `json:"filledSize"`
	AvgFillPrice  *string     `json:"avgFillPrice"`
	Status        OrderStatus `json:"status"`
	ReduceOnly    bool        `json:"reduceOnly"`
	TimeInForce   string      `json:"timeInForce"`
	Leverage      int         `json:"leverage"`
	BuilderFeeBps *int        `json:"builderFeeBps,omitempty"`
	IsTrigger     bool        `json:"isTrigger,omitempty"`
	TriggerPx     *string     `json:"triggerPx,omitempty"`
	IsMarket      bool        `json:"isMarket,omitempty"`
	Tpsl          string      `json:"tpsl,omitempty"`
	Grouping      string      `json:"grouping,omitempty"`
	ParentOrderID string      `json:"parentOrderId,omitempty"`
	CreatedAt     string      `json:"createdAt"`
	UpdatedAt     string      `json:"updatedAt"`
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
	Coin          string    `json:"coin"`
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
	Coin        string `json:"coin"`
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
	Dir               string                 `json:"dir,omitempty"`
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
	Coin          string    `json:"coin"`
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
	Coin         string `json:"coin"`
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
	Coin                  string       `json:"coin"`
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
}

type UpdateLeverageResponse struct {
	AccountID        string `json:"accountId"`
	Coin             string `json:"coin"`
	Leverage         int    `json:"leverage"`
	PreviousLeverage int    `json:"previousLeverage"`
}

type LeverageSetting struct {
	Coin       string     `json:"coin"`
	Leverage   int        `json:"leverage"`
	MarginMode MarginMode `json:"marginMode"`
}

// UpdateIsolatedMarginResponse is returned by UpdateIsolatedMargin: the
// resulting locked isolated collateral and recomputed liquidation price.
type UpdateIsolatedMarginResponse struct {
	AccountID        string `json:"accountId"`
	Coin             string `json:"coin"`
	IsolatedMargin   string `json:"isolatedMargin"`
	LiquidationPrice string `json:"liquidationPrice"`
}

// SetMarginModeResponse is returned by SetMarginMode with the asset's new
// margin mode.
type SetMarginModeResponse struct {
	AccountID  string     `json:"accountId"`
	Coin       string     `json:"coin"`
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
	Coin                string     `json:"coin"`
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

type SimMetaAsset struct {
	Name                string               `json:"name"`
	Dex                 string               `json:"dex,omitempty"`
	Symbol              string               `json:"symbol"`
	DisplayName         string               `json:"displayName,omitempty"`
	LogoURL             string               `json:"logoUrl,omitempty"`
	LogoSources         []LogoSource         `json:"logoSources,omitempty"`
	Exchange            string               `json:"exchange"`
	AssetType           string               `json:"assetType,omitempty"`
	CategoryLabel       string               `json:"categoryLabel,omitempty"`
	Mapped              bool                 `json:"mapped"`
	HasDisplayName      bool                 `json:"hasDisplayName"`
	HasLogo             bool                 `json:"hasLogo"`
	DescriptionStatus   string               `json:"descriptionStatus,omitempty"`
	IsHip3              bool                 `json:"isHip3,omitempty"`
	DeployerDisplayName string               `json:"deployerDisplayName,omitempty"`
	Index               int                  `json:"index"`
	SzDecimals          int                  `json:"szDecimals"`
	MaxLeverage         int                  `json:"maxLeverage"`
	OnlyIsolated        bool                 `json:"onlyIsolated"`
	FeeScale            float64              `json:"feeScale,omitempty"`
	MarginTableID       int                  `json:"marginTableId,omitempty"`
	CandleHistory       *CandleHistoryBounds `json:"candleHistory,omitempty"`
}

type SimMetaResponse struct {
	Universe     []SimMetaAsset         `json:"universe"`
	MarginTables map[string]MarginTable `json:"marginTables,omitempty"`
}

type SimMidsResponse struct {
	Mids map[string]string `json:"mids"`
}

type MarketTicker struct {
	Coin              string  `json:"coin"`
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
	Coin string         `json:"coin"`
	Bids []SimBookLevel `json:"bids"`
	Asks []SimBookLevel `json:"asks"`
	Time int64          `json:"time"`
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
	Coin     string   `json:"coin"`
	Interval string   `json:"interval"`
	Candles  []Candle `json:"candles"`
}

type MarketTrade struct {
	Coin string `json:"coin"`
	Px   string `json:"px"`
	Sz   string `json:"sz"`
	Side string `json:"side"`
	Time string `json:"time"`
	Hash string `json:"hash,omitempty"`
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
	Coin       string `json:"coin"`
	Side       string `json:"side"`
	Size       string `json:"size"`
	ReduceOnly bool   `json:"reduceOnly"`
}

type PredictedEffect struct {
	Type           string                            `json:"type"`
	BalanceChanges map[string]PredictedBalanceChange `json:"balanceChanges,omitempty"`
	OrderIntent    *PredictedOrderIntent             `json:"orderIntent,omitempty"`
}
