package rules

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// stopAt is price * (1 - f), rounded to the cent.
func stopAt(price money.Price, f decimal.Decimal) (money.Price, error) {
	return money.RoundPrice(price.Decimal().Mul(decimal.NewFromInt(1).Sub(f)))
}

// stopped is the decision for a holding whose stop is stop: a sell if the last
// close is at or below it, otherwise no change. Either way it carries stop.
func stopped(in Input, stop money.Price, name, hold string) Decision {
	closed := in.Bars[len(in.Bars)-1].Close
	d := Decision{Symbol: in.Symbol, Outcome: NoChange, Stop: &stop}
	if closed.Cmp(stop) <= 0 {
		d.Outcome = Sell
		d.Reason = fmt.Sprintf("closed at %s, at or below its %s of %s", closed, name, stop)
		return d
	}
	d.Reason = fmt.Sprintf("its %s is %s, %s", name, stop, hold)
	return d
}

// FixedStop sets a holding's stop a fixed share below its average cost, and
// sells the holding when it closes at or below that stop.
type FixedStop struct {
	Distance decimal.Decimal
}

var fixedStopSettings = []Setting{
	{Name: "distance", Type: Fraction, Doc: "how far below the average cost the stop sits"},
}

func newFixedStop(v *values) Rule { return FixedStop{Distance: v.fraction["distance"]} }

// Type returns "fixed_stop".
func (FixedStop) Type() string { return config.RuleFixedStop }

// Kind returns KindStop.
func (FixedStop) Kind() Kind { return KindStop }

// Stop gives the holding's stop and its outcome on the last bar. A stock not
// held gets no change and no stop.
func (r FixedStop) Stop(in Input) (Decision, error) {
	if err := checkBars(in); err != nil {
		return Decision{}, err
	}
	if in.Holding == nil {
		return noChange(in, "not held"), nil
	}
	if len(in.Bars) == 0 {
		return tooFew(in, 1), nil
	}
	stop, err := stopAt(in.Holding.Cost, r.Distance)
	if err != nil {
		return Decision{}, err
	}
	return stopped(in, stop, "stop", fmt.Sprintf("%s under its cost of %s", percent(r.Distance), in.Holding.Cost)), nil
}

// TrailingStop sets a holding's stop a fixed share below its highest close
// since it was bought, never lowering it, and sells the holding when it
// closes at or below that stop.
type TrailingStop struct {
	Distance decimal.Decimal
}

var trailingStopSettings = []Setting{
	{Name: "distance", Type: Fraction, Doc: "how far below the highest close since buying the stop sits"},
}

func newTrailingStop(v *values) Rule { return TrailingStop{Distance: v.fraction["distance"]} }

// Type returns "trailing_stop".
func (TrailingStop) Type() string { return config.RuleTrailingStop }

// Kind returns KindStop.
func (TrailingStop) Kind() Kind { return KindStop }

// Stop gives the holding's stop and its outcome on the last bar. The stop is
// worked out from the closes on or after the holding's Opened date, and is
// never below the holding's Stop already in force. A stock not held gets no
// change and no stop.
func (r TrailingStop) Stop(in Input) (Decision, error) {
	if err := checkBars(in); err != nil {
		return Decision{}, err
	}
	h := in.Holding
	if h == nil {
		return noChange(in, "not held"), nil
	}
	opened := dayKey(h.Opened)
	from := len(in.Bars)
	for from > 0 && dayKey(in.Bars[from-1].Date) >= opened {
		from--
	}
	if from == len(in.Bars) {
		reason := "has no prices since it was bought on " + h.Opened.Format(time.DateOnly)
		if h.Stop == nil {
			return noChange(in, reason), nil
		}
		stop := *h.Stop
		return Decision{Symbol: in.Symbol, Outcome: NoChange, Stop: &stop,
			Reason: fmt.Sprintf("%s; its trailing stop stays at %s", reason, stop)}, nil
	}
	high := highestClose(in.Bars, from, len(in.Bars))
	stop, err := stopAt(high, r.Distance)
	if err != nil {
		return Decision{}, err
	}
	hold := fmt.Sprintf("%s under its highest close since it was bought, %s", percent(r.Distance), high)
	if h.Stop != nil && h.Stop.Cmp(stop) > 0 {
		stop = *h.Stop
		hold = "kept, as a trailing stop is never lowered"
	}
	return stopped(in, stop, "trailing stop", hold), nil
}
