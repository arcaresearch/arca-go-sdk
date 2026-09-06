package arca

// LeveragePreferenceMode describes Arca allocation intent, not margin mode.
type LeveragePreferenceMode string

const (
	LeverageVenueDefault LeveragePreferenceMode = "venue-default"
	LeverageFixed        LeveragePreferenceMode = "fixed"
)

type TradingLeveragePreference struct {
	Mode     LeveragePreferenceMode `json:"mode"`
	Leverage *int                   `json:"leverage,omitempty"`
}

type PositionAllocation struct {
	Market             string                    `json:"market"`
	Preference         TradingLeveragePreference `json:"preference"`
	EffectiveLeverage  int                       `json:"effectiveLeverage"`
	VenueInitialMargin string                    `json:"venueInitialMargin"`
	AllocatedMargin    string                    `json:"allocatedMargin"`
	ExtraMargin        string                    `json:"extraMargin"`
	ReservedMargin     string                    `json:"reservedMargin"`
	ReservedExtra      string                    `json:"reservedExtra"`
}

type TradingAllocationProjection struct {
	Revision                string                        `json:"revision"`
	Positions               map[string]PositionAllocation `json:"positions"`
	VenueInitialMargin      string                        `json:"venueInitialMargin"`
	PositionAllocatedMargin string                        `json:"positionAllocatedMargin"`
	AllocatedMargin         string                        `json:"allocatedMargin"`
	ExtraMargin             string                        `json:"extraMargin"`
	PendingMargin           string                        `json:"pendingMargin"`
	PendingCosts            string                        `json:"pendingCosts"`
	ReservedExtra           string                        `json:"reservedExtra"`
	AvailableToTrade        string                        `json:"availableToTrade"`
}

type TradingAllocationState struct {
	AsOf                  string                               `json:"asOf,omitempty"`
	ValidUntil            string                               `json:"validUntil,omitempty"`
	Revision              string                               `json:"revision"`
	Preferences           map[string]TradingLeveragePreference `json:"preferences"`
	Projection            *TradingAllocationProjection         `json:"projection,omitempty"`
	ProjectionUnavailable bool                                 `json:"projectionUnavailable"`
}

// TradingLeverageSelection updates intent; an empty selection keeps it.
type TradingLeverageSelection struct {
	Mode     LeveragePreferenceMode `json:"mode,omitempty"`
	Leverage *int                   `json:"leverage,omitempty"`
}

type TradingAllocationRead struct {
	Enabled           bool                   `json:"enabled"`
	InputID           string                 `json:"inputId"`
	Allocation        TradingAllocationState `json:"allocation"`
	UnavailableReason string                 `json:"unavailableReason,omitempty"`
}

type TradingAllocationQuoteRequest struct {
	Market      string                   `json:"market"`
	Side        OrderSide                `json:"side"`
	OrderType   string                   `json:"orderType"` // "market" or "limit"
	Price       *string                  `json:"price,omitempty"`
	Size        *string                  `json:"size,omitempty"`
	SlippageBps *int                     `json:"slippageBps,omitempty"`
	ReduceOnly  bool                     `json:"reduceOnly,omitempty"`
	Selection   TradingLeverageSelection `json:"selection"`
}

type TradingAllocationMaximum struct {
	Revision    string                       `json:"revision"`
	MaxSize     string                       `json:"maxSize"`
	MaxNotional string                       `json:"maxNotional"`
	Projection  *TradingAllocationProjection `json:"projection,omitempty"`
}

type TradingAllocationQuote struct {
	InputID         string                       `json:"inputId"`
	Market          string                       `json:"market"`
	ReferencePrice  string                       `json:"referencePrice"`
	LimitPrice      string                       `json:"limitPrice"`
	Allocation      TradingAllocationState       `json:"allocation"`
	Maximum         TradingAllocationMaximum     `json:"maximum"`
	Affordable      *bool                        `json:"affordable,omitempty"`
	OrderProjection *TradingAllocationProjection `json:"orderProjection,omitempty"`
}
