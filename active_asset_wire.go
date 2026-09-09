package arca

import "encoding/json"

// UnmarshalJSON retains the venue's scalar or side-specific availability wire
// value. AvailableToTrade is populated only for scalar responses; consumers
// needing directional availability use AvailableToTradeRaw.
func (a *ActiveAssetData) UnmarshalJSON(data []byte) error {
	type alias ActiveAssetData
	var wire struct {
		*alias
		Available json.RawMessage `json:"availableToTrade"`
	}
	wire.alias = (*alias)(a)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	a.AvailableToTradeRaw = wire.Available
	a.AvailableToTrade = ""
	if len(wire.Available) == 0 || string(wire.Available) == "null" {
		return nil
	}
	if wire.Available[0] == '[' {
		var sides [2]string
		return json.Unmarshal(wire.Available, &sides)
	}
	return json.Unmarshal(wire.Available, &a.AvailableToTrade)
}
