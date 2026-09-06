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
	Revision              string                               `json:"revision"`
	Preferences           map[string]TradingLeveragePreference `json:"preferences"`
	Projection            *TradingAllocationProjection         `json:"projection,omitempty"`
	ProjectionUnavailable bool                                 `json:"projectionUnavailable"`
}
