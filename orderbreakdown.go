package arca

import (
	"math"
	"strconv"
	"strings"
)

// OrderBreakdownAmountType controls how OrderBreakdownOptions.Amount is read:
// "spend" (total deducted = margin + fee), "notional" (USD exposure), or
// "tokens" (base-asset quantity).
type OrderBreakdownAmountType = string

// OrderBreakdownExistingPosition describes an open position in the same coin to
// merge with the hypothetical fill (same-side blends entry, opposite reduces or
// flips), matching backend PositionService.ApplyFill semantics.
type OrderBreakdownExistingPosition struct {
	Side       PositionSide
	Size       string
	EntryPrice string
}

// OrderBreakdownAccountContext supplies account-wide state for the cross-margin
// liquidation estimate.
type OrderBreakdownAccountContext struct {
	Equity                 string
	OtherMaintenanceMargin string
	ExistingPosition       *OrderBreakdownExistingPosition
}

// OrderBreakdownOptions are the inputs to ComputeOrderBreakdown. When
// MaintenanceMarginRate or MarginTiers is set, AccountContext must also be
// provided for the liquidation estimate.
type OrderBreakdownOptions struct {
	Amount                string
	AmountType            OrderBreakdownAmountType
	Leverage              int
	FeeRate               string
	Price                 string
	Side                  OrderSide
	SzDecimals            int
	MarginTiers           []MarginTier
	MaintenanceMarginRate string
	AccountContext        *OrderBreakdownAccountContext
}

// OrderBreakdown is the result of ComputeOrderBreakdown.
type OrderBreakdown struct {
	Tokens                         string `json:"tokens"`
	NotionalUsd                    string `json:"notionalUsd"`
	MarginRequired                 string `json:"marginRequired"`
	EstimatedFee                   string `json:"estimatedFee"`
	TotalSpend                     string `json:"totalSpend"`
	Price                          string `json:"price"`
	FeeRate                        string `json:"feeRate"`
	EffectiveLeverage              string `json:"effectiveLeverage,omitempty"`
	EffectiveMaintenanceMarginRate string `json:"effectiveMaintenanceMarginRate,omitempty"`
	NextTierThreshold              string `json:"nextTierThreshold,omitempty"`
	EstimatedLiquidationPrice      string `json:"estimatedLiquidationPrice,omitempty"`
}

func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func fmtNum(v float64, d int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	s := strconv.FormatFloat(v, 'f', d, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// ComputeOrderBreakdown converts between spend/notional/token representations
// of an order and estimates margin, fee, and (with account context) the
// cross-margin liquidation price. Pure: no network call.
func ComputeOrderBreakdown(opts OrderBreakdownOptions) OrderBreakdown {
	price := parseF(opts.Price)
	feeRate := parseF(opts.FeeRate)
	leverage := float64(opts.Leverage)
	szDecimals := opts.SzDecimals
	if szDecimals == 0 {
		szDecimals = 5
	}
	amount := parseF(opts.Amount)

	zero := OrderBreakdown{Tokens: "0", NotionalUsd: "0", MarginRequired: "0", EstimatedFee: "0", TotalSpend: "0", Price: opts.Price, FeeRate: opts.FeeRate}
	if !(price > 0) || !(leverage > 0) || feeRate < 0 || !(amount > 0) {
		return zero
	}

	var notional float64
	switch opts.AmountType {
	case "spend":
		if len(opts.MarginTiers) > 0 {
			targetSpend := amount
			deduction := 0.0
			tierMaxLev := float64(opts.MarginTiers[0].MaxLeverage)
			effLev := leverage
			if tierMaxLev < effLev {
				effLev = tierMaxLev
			}
			activeRate := 1 / effLev
			prevRate := activeRate
			prevDeduction := 0.0
			for _, tier := range opts.MarginTiers {
				lowerBound := parseF(tier.LowerBound)
				lev := leverage
				if float64(tier.MaxLeverage) < lev {
					lev = float64(tier.MaxLeverage)
				}
				rate := 1 / lev
				nextDeduction := prevDeduction + lowerBound*(rate-prevRate)
				spendAtBound := lowerBound*rate - nextDeduction + lowerBound*feeRate
				if targetSpend < spendAtBound {
					break
				}
				activeRate = rate
				prevRate = rate
				prevDeduction = nextDeduction
				deduction = nextDeduction
			}
			notional = (targetSpend + deduction) / (activeRate + feeRate)
		} else {
			notional = amount / (1/leverage + feeRate)
		}
	case "notional":
		notional = amount
	case "tokens":
		notional = amount * price
	default:
		return zero
	}

	scale := math.Pow(10, float64(szDecimals))
	tokens := math.Floor(notional/price*scale) / scale
	actualNotional := tokens * price

	marginRequired := 0.0
	var nextTierThreshold float64
	haveNextTier := false
	var mmRequired float64
	haveMM := false

	if len(opts.MarginTiers) > 0 {
		deduction := 0.0
		tierMaxLev := float64(opts.MarginTiers[0].MaxLeverage)
		effLev := leverage
		if tierMaxLev < effLev {
			effLev = tierMaxLev
		}
		activeRate := 1 / effLev
		prevRate := activeRate
		prevDeduction := 0.0
		for _, tier := range opts.MarginTiers {
			lowerBound := parseF(tier.LowerBound)
			lev := leverage
			if float64(tier.MaxLeverage) < lev {
				lev = float64(tier.MaxLeverage)
			}
			rate := 1 / lev
			if actualNotional < lowerBound {
				nextTierThreshold = lowerBound
				haveNextTier = true
				break
			}
			deduction = prevDeduction + lowerBound*(rate-prevRate)
			activeRate = rate
			prevRate = rate
			prevDeduction = deduction
		}
		marginRequired = actualNotional*activeRate - deduction

		mmDeduction := 0.0
		mmActiveRate := 0.5 / float64(opts.MarginTiers[0].MaxLeverage)
		mmPrevRate := mmActiveRate
		mmPrevDeduction := 0.0
		for _, tier := range opts.MarginTiers {
			lowerBound := parseF(tier.LowerBound)
			rate := 0.5 / float64(tier.MaxLeverage)
			if actualNotional < lowerBound {
				break
			}
			mmDeduction = mmPrevDeduction + lowerBound*(rate-mmPrevRate)
			mmActiveRate = rate
			mmPrevRate = rate
			mmPrevDeduction = mmDeduction
		}
		mmRequired = actualNotional*mmActiveRate - mmDeduction
		haveMM = true
	} else {
		marginRequired = actualNotional / leverage
	}

	estimatedFee := actualNotional * feeRate
	totalSpend := marginRequired + estimatedFee

	result := OrderBreakdown{
		Tokens:         fmtNum(tokens, szDecimals),
		NotionalUsd:    fmtNum(actualNotional, 8),
		MarginRequired: fmtNum(marginRequired, 8),
		EstimatedFee:   fmtNum(estimatedFee, 8),
		TotalSpend:     fmtNum(totalSpend, 8),
		Price:          opts.Price,
		FeeRate:        opts.FeeRate,
	}

	if (opts.MaintenanceMarginRate != "" || len(opts.MarginTiers) > 0) && opts.AccountContext != nil {
		ctx := opts.AccountContext
		equity := parseF(ctx.Equity)
		otherMM := parseF(ctx.OtherMaintenanceMargin)
		if !math.IsNaN(equity) && !math.IsNaN(otherMM) {
			newSide := Long
			if opts.Side == Sell {
				newSide = Short
			}
			if merged, ok := mergeOrderWithPosition(newSide, tokens, price, ctx.ExistingPosition); ok && merged.size > 0 {
				mergedNotional := merged.size * merged.entry
				mmMerged := 0.0
				if len(opts.MarginTiers) > 0 {
					mmDeduction := 0.0
					mmActiveRate := 0.5 / float64(opts.MarginTiers[0].MaxLeverage)
					mmPrevRate := mmActiveRate
					mmPrevDeduction := 0.0
					for _, tier := range opts.MarginTiers {
						lowerBound := parseF(tier.LowerBound)
						rate := 0.5 / float64(tier.MaxLeverage)
						if mergedNotional < lowerBound {
							break
						}
						mmDeduction = mmPrevDeduction + lowerBound*(rate-mmPrevRate)
						mmActiveRate = rate
						mmPrevRate = rate
						mmPrevDeduction = mmDeduction
					}
					mmMerged = mergedNotional*mmActiveRate - mmDeduction
				} else if opts.MaintenanceMarginRate != "" {
					mmMerged = parseF(opts.MaintenanceMarginRate) * mergedNotional
				}
				equityPost := equity - estimatedFee
				marginAvail := equityPost - (otherMM + mmMerged)
				if marginAvail > 0 {
					perUnit := marginAvail / merged.size
					var liq float64
					if merged.side == Long {
						liq = price - perUnit
					} else {
						liq = price + perUnit
					}
					if liq > 0 {
						result.EstimatedLiquidationPrice = fmtNum(liq, 8)
					}
				}
			}
		}
	}

	if len(opts.MarginTiers) > 0 {
		if actualNotional > 0 {
			result.EffectiveLeverage = fmtNum(actualNotional/marginRequired, 8)
		} else {
			result.EffectiveLeverage = fmtNum(leverage, 8)
		}
		if haveMM && actualNotional > 0 {
			result.EffectiveMaintenanceMarginRate = fmtNum(mmRequired/actualNotional, 8)
		}
	}
	if haveNextTier {
		result.NextTierThreshold = fmtNum(nextTierThreshold, 8)
	}

	return result
}

type mergedPosition struct {
	size  float64
	entry float64
	side  PositionSide
}

func mergeOrderWithPosition(newSide PositionSide, newSize, fillPrice float64, existing *OrderBreakdownExistingPosition) (mergedPosition, bool) {
	if !(newSize > 0) || !(fillPrice > 0) {
		return mergedPosition{}, false
	}
	if existing == nil {
		return mergedPosition{size: newSize, entry: fillPrice, side: newSide}, true
	}
	exSize := parseF(existing.Size)
	exEntry := parseF(existing.EntryPrice)
	if !(exSize > 0) || !(exEntry > 0) {
		return mergedPosition{size: newSize, entry: fillPrice, side: newSide}, true
	}
	if existing.Side == newSide {
		mergedSize := exSize + newSize
		mergedEntry := (exSize*exEntry + newSize*fillPrice) / mergedSize
		return mergedPosition{size: mergedSize, entry: mergedEntry, side: newSide}, true
	}
	if newSize < exSize {
		return mergedPosition{size: exSize - newSize, entry: exEntry, side: existing.Side}, true
	}
	if newSize == exSize {
		return mergedPosition{}, false
	}
	return mergedPosition{size: newSize - exSize, entry: fillPrice, side: newSide}, true
}
