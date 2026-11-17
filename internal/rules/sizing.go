package rules

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// ErrNoStop is returned by size_by_risk when it is given no stop price.
var ErrNoStop = errors.New("rules: size_by_risk needs a stop price")

// ErrNoPrice is returned by a sizing rule given a zero price.
var ErrNoPrice = errors.New("rules: sizing needs a price above zero")

// shares writes a share count with its noun.
func shares(q money.Quantity) string {
	if q.String() == "1" {
		return "1 share"
	}
	return q.String() + " shares"
}

// SizeFixedAmount buys a fixed dollar amount's worth of shares, rounded down.
type SizeFixedAmount struct {
	Amount money.Money
}

var sizeFixedAmountSettings = []Setting{
	{Name: "amount", Type: Amount, Doc: "the dollar amount each buy spends at most"},
}

func newSizeFixedAmount(v *values) Rule { return SizeFixedAmount{Amount: v.amount["amount"]} }

// Type returns "size_fixed_amount".
func (SizeFixedAmount) Type() string { return config.RuleSizeFixedAmount }

// Kind returns KindSizing.
func (SizeFixedAmount) Kind() Kind { return KindSizing }

// Size gives the most shares the amount buys at the price under the policy.
func (r SizeFixedAmount) Size(in SizeInput) (Size, error) {
	if in.Price.IsZero() {
		return Size{}, ErrNoPrice
	}
	q, err := in.Policy.SharesFor(r.Amount, in.Price)
	if err != nil {
		return Size{}, err
	}
	return Size{Quantity: q, Reason: fmt.Sprintf("%s at %s a share buys %s", r.Amount, in.Price, shares(q))}, nil
}

// SizeByRisk sizes a buy so that falling from its price to its stop loses
// at most RiskPerTrade of the strategy's capital, rounded down.
type SizeByRisk struct {
	RiskPerTrade decimal.Decimal
}

var sizeByRiskSettings = []Setting{
	{Name: "risk_per_trade", Type: Fraction, Doc: "the share of the strategy's capital a buy may lose if it falls to its stop"},
}

func newSizeByRisk(v *values) Rule { return SizeByRisk{RiskPerTrade: v.fraction["risk_per_trade"]} }

// Type returns "size_by_risk".
func (SizeByRisk) Type() string { return config.RuleSizeByRisk }

// Kind returns KindSizing.
func (SizeByRisk) Kind() Kind { return KindSizing }

// Size gives the most shares whose loss from the price down to the stop is
// within RiskPerTrade of the capital, under the policy. A stop at or above
// the price, or no capital, gives zero shares.
func (r SizeByRisk) Size(in SizeInput) (Size, error) {
	if in.Stop == nil {
		return Size{}, ErrNoStop
	}
	if in.Price.IsZero() {
		return Size{}, ErrNoPrice
	}
	if in.Stop.Cmp(in.Price) >= 0 {
		return Size{Reason: fmt.Sprintf("its stop of %s is not below the price of %s, so it buys no shares", in.Stop, in.Price)}, nil
	}
	// Both are whole cents, so the difference is exact and RoundPrice
	// changes nothing.
	perShare, err := money.RoundPrice(in.Price.Decimal().Sub(in.Stop.Decimal()))
	if err != nil {
		return Size{}, err
	}
	// The capital and the share are exact decimals, so the risk is
	// exact too; SharesFor divides it exactly and rounds down.
	risk := money.FromDecimal(in.Capital.Decimal().Mul(r.RiskPerTrade))
	q, err := in.Policy.SharesFor(risk, perShare)
	if err != nil {
		return Size{}, err
	}
	return Size{Quantity: q, Reason: fmt.Sprintf("risking %s (%s of %s) at %s a share (price %s, stop %s) buys %s",
		risk, percent(r.RiskPerTrade), in.Capital, perShare, in.Price, in.Stop, shares(q))}, nil
}
