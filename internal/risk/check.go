package risk

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// Check runs the risk check on o against s. It reads nothing else and changes
// nothing, so the same inputs always give the same verdict.
func Check(o Order, s Snapshot) Verdict {
	if problems := details(o, s); len(problems) > 0 {
		return Verdict{Results: []Result{refuse(CheckDetails, strings.Join(problems, " "))}}
	}
	c := checker{o: o, s: s}
	c.value, c.valueErr = c.orderValue()

	var rs []Result
	switch o.Kind {
	case Buy:
		rs = []Result{
			c.stockShare(),
			c.orderValueLimit(),
			c.orderCount(),
			c.dailyLoss(),
			c.holdings(),
			c.sectorShare(),
			c.cash(),
			c.watchlist(),
			c.limitPrice(),
			c.sameDay(),
			c.switchAndBroker(),
			c.marketOpen(),
		}
	case Sell:
		rs = []Result{
			c.orderValueLimit(),
			c.orderCount(),
			c.dailyLoss(),
			c.sharesHeld(),
			c.sameDay(),
			c.switchAndBroker(),
			c.marketOpen(),
		}
	case ProtectiveSell:
		rs = []Result{
			c.orderValueLimit(),
			c.sharesHeld(),
			c.marketOpen(),
		}
	}
	return Verdict{Results: rs}
}

// details lists what is missing or malformed in the order or the snapshot,
// so nothing below works on a value that was never set.
func details(o Order, s Snapshot) []string {
	var p []string
	if o.Symbol == "" {
		p = append(p, "The order names no stock.")
	}
	switch o.Kind {
	case Buy, Sell, ProtectiveSell:
	default:
		p = append(p, fmt.Sprintf("The order's kind (%s) is not a buy, a sell or a protective sell.", o.Kind))
	}
	if o.Quantity.IsZero() {
		p = append(p, "The order is for zero shares.")
	} else if err := money.PolicyFor(s.Limits.FractionalShares).Check(o.Quantity); err != nil {
		p = append(p, fmt.Sprintf("The order is for %s shares, but fractional shares are off.", o.Quantity))
	}
	switch {
	case o.Kind == Buy && o.Price.IsZero():
		p = append(p, "The buy has no limit price.")
	case o.Kind == ProtectiveSell && o.Price.IsZero():
		p = append(p, "The protective sell has no stop price.")
	}
	if o.Session.IsZero() {
		p = append(p, "The order names no trading date.")
	}
	if s.Now.IsZero() {
		p = append(p, "The risk check was given no time.")
	}
	if s.Calendar == nil {
		p = append(p, "The risk check was given no exchange calendar.")
	}
	return p
}

// checker holds one check's inputs and the order's value.
type checker struct {
	o        Order
	s        Snapshot
	value    money.Money
	valueErr string
}

func pass(check, detail string) Result   { return Result{Check: check, Passed: true, Detail: detail} }
func refuse(check, detail string) Result { return Result{Check: check, Passed: false, Detail: detail} }

// orderValue is shares times the limit or stop price, or times the last close
// for an ordinary sell. The second result explains why it could not be found.
func (c checker) orderValue() (money.Money, string) {
	if c.o.Kind != Sell {
		return c.o.Price.Mul(c.o.Quantity), ""
	}
	closePrice, ok := c.close(c.o.Symbol)
	if !ok {
		return money.Money{}, fmt.Sprintf("There is no last close for %s to value the sell by.", c.o.Symbol)
	}
	return closePrice.Mul(c.o.Quantity), ""
}

// close returns a stock's last close, if there is a usable one.
func (c checker) close(symbol string) (money.Price, bool) {
	p, ok := c.s.LastCloses[symbol]
	return p, ok && !p.IsZero()
}

// held returns the shares held of a stock.
func (c checker) held(symbol string) money.Quantity { return c.s.Held[symbol] }

// heldSymbols lists the stocks held, sorted, so any text built from them is
// the same on every run.
func (c checker) heldSymbols() []string {
	var out []string
	for sym, q := range c.s.Held {
		if !q.IsZero() {
			out = append(out, sym)
		}
	}
	slices.Sort(out)
	return out
}

// holdingValue values a holding at its last close.
func (c checker) holdingValue(symbol string) (money.Money, bool) {
	q := c.held(symbol)
	if q.IsZero() {
		return money.Money{}, true
	}
	p, ok := c.close(symbol)
	if !ok {
		return money.Money{}, false
	}
	return p.Mul(q), true
}

// openBuys sums the session's open buys that match keep.
func (c checker) openBuys(keep func(symbol string) bool) money.Money {
	var sum money.Money
	for _, d := range c.s.DayOrders {
		if d.Kind == Buy && d.Open && keep(d.Symbol) {
			sum = sum.Add(d.Value)
		}
	}
	return sum
}

// portfolioValue returns the portfolio's value, refusing if it is unknown.
func (c checker) portfolioValue(check string) (money.Money, *Result) {
	if !c.s.Value.IsPositive() {
		r := refuse(check, "The portfolio's value is not known, so its share cannot be measured.")
		return money.Money{}, &r
	}
	return c.s.Value, nil
}

// stockShare: the largest share of the portfolio one stock may hold after the
// buy fills. Applies to buys.
func (c checker) stockShare() Result {
	const name = CheckStockShare
	if c.valueErr != "" {
		return refuse(name, c.valueErr)
	}
	limit, ok := ratio(c.s.Limits.MaxStockShare)
	if !ok {
		return refuse(name, "The limit (max_stock_share) is not set.")
	}
	total, r := c.portfolioValue(name)
	if r != nil {
		return *r
	}
	held, ok := c.holdingValue(c.o.Symbol)
	if !ok {
		return refuse(name, fmt.Sprintf("There is no last close for %s to value the shares already held.", c.o.Symbol))
	}
	after := held.Add(c.openBuys(func(sym string) bool { return sym == c.o.Symbol })).Add(c.value)
	share := shareOf(after, total)
	if after.Cmp(times(total, limit)) > 0 {
		return refuse(name, fmt.Sprintf("After this buy %s would be %s of the portfolio (%s of %s), over the %s limit.",
			c.o.Symbol, percent(share), dollars(after), dollars(total), percent(limit)))
	}
	return pass(name, fmt.Sprintf("After this buy %s would be %s of the portfolio; the limit is %s.",
		c.o.Symbol, percent(share), percent(limit)))
}

// orderValueLimit: the largest value of a single order. Applies to every
// order.
func (c checker) orderValueLimit() Result {
	const name = CheckOrderValue
	if c.valueErr != "" {
		return refuse(name, c.valueErr)
	}
	limit := c.s.Limits.MaxOrderValue
	if !limit.IsPositive() {
		return refuse(name, "The limit (max_order_value) is not set.")
	}
	if c.value.Cmp(limit) > 0 {
		return refuse(name, fmt.Sprintf("The order is worth %s, over the %s limit for one order.", dollars(c.value), dollars(limit)))
	}
	return pass(name, fmt.Sprintf("The order is worth %s; the limit is %s.", dollars(c.value), dollars(limit)))
}

// orderCount: the most orders sent in one trading day. Protective sells
// neither count nor are blocked.
func (c checker) orderCount() Result {
	const name = CheckOrderCount
	limit := c.s.Limits.MaxOrdersPerDay
	if limit < 1 {
		return refuse(name, "The limit (max_orders_per_day) is not set.")
	}
	n := 0
	for _, d := range c.s.DayOrders {
		if d.Kind != ProtectiveSell {
			n++
		}
	}
	if n+1 > limit {
		return refuse(name, fmt.Sprintf("%s already sent or approved today; the limit is %d a day.", countOrders(n), limit))
	}
	return pass(name, fmt.Sprintf("This would be order %d of at most %d today.", n+1, limit))
}

// dailyLoss: when the portfolio has lost this much since the previous close,
// all trading stops for the rest of the day. Protective sells are not
// blocked.
func (c checker) dailyLoss() Result {
	const name = CheckDailyLoss
	limit := c.s.Limits.DailyLossLimit
	if !limit.IsPositive() {
		return refuse(name, "The limit (daily_loss_limit) is not set.")
	}
	if !c.s.PreviousCloseValue.IsPositive() {
		return refuse(name, "The portfolio's value at the previous close is not known, so the day's loss cannot be measured.")
	}
	loss := c.s.PreviousCloseValue.Sub(c.s.Value)
	if loss.Cmp(limit) >= 0 {
		return refuse(name, fmt.Sprintf("The portfolio has lost %s since the previous close, which reaches the %s daily loss limit, so trading has stopped for the rest of the day.",
			dollars(loss), dollars(limit)))
	}
	if !loss.IsPositive() {
		return pass(name, fmt.Sprintf("The portfolio has not lost money since the previous close; the limit is %s.", dollars(limit)))
	}
	return pass(name, fmt.Sprintf("The portfolio has lost %s since the previous close, under the %s limit.", dollars(loss), dollars(limit)))
}

// holdings: at most the limit (and never more than 20) holdings after the
// buy fills. Applies to buys.
func (c checker) holdings() Result {
	const name = CheckHoldings
	limit := min(c.s.Limits.MaxHoldings, config.MaxHoldings)
	if limit < 1 {
		return refuse(name, "The limit (max_holdings) is not set.")
	}
	stocks := make(map[string]bool)
	for _, sym := range c.heldSymbols() {
		stocks[sym] = true
	}
	for _, d := range c.s.DayOrders {
		if d.Kind == Buy && d.Open {
			stocks[d.Symbol] = true
		}
	}
	stocks[c.o.Symbol] = true
	n := len(stocks)
	if n > limit {
		return refuse(name, fmt.Sprintf("After this buy there would be %d holdings, over the limit of %d.", n, limit))
	}
	return pass(name, fmt.Sprintf("After this buy there would be %d holdings; the limit is %d.", n, limit))
}

// sectorShare: the largest share of the portfolio in one sector, when the
// limit is set. Applies to buys.
func (c checker) sectorShare() Result {
	const name = CheckSectorShare
	if c.s.Limits.MaxSectorShare == nil {
		return pass(name, "No sector limit is set.")
	}
	if c.valueErr != "" {
		return refuse(name, c.valueErr)
	}
	limit, ok := ratio(*c.s.Limits.MaxSectorShare)
	if !ok {
		return refuse(name, "The limit (max_sector_share) is not a number.")
	}
	sector := c.s.Sectors[c.o.Symbol]
	if sector == "" {
		return refuse(name, fmt.Sprintf("The strategy file gives no sector for %s.", c.o.Symbol))
	}
	total, r := c.portfolioValue(name)
	if r != nil {
		return *r
	}
	inSector := func(sym string) bool { return c.s.Sectors[sym] == sector }
	var after money.Money
	for _, sym := range c.heldSymbols() {
		if !inSector(sym) {
			continue
		}
		v, ok := c.holdingValue(sym)
		if !ok {
			return refuse(name, fmt.Sprintf("There is no last close for %s to value the %s holdings by.", sym, sector))
		}
		after = after.Add(v)
	}
	after = after.Add(c.openBuys(inSector)).Add(c.value)
	share := shareOf(after, total)
	if after.Cmp(times(total, limit)) > 0 {
		return refuse(name, fmt.Sprintf("After this buy the %s sector would be %s of the portfolio (%s of %s), over the %s limit.",
			sector, percent(share), dollars(after), dollars(total), percent(limit)))
	}
	return pass(name, fmt.Sprintf("After this buy the %s sector would be %s of the portfolio; the limit is %s.",
		sector, percent(share), percent(limit)))
}

// cash: a buy may not cost more than the cash not already promised to the
// session's other buys. No margin. Applies to buys.
func (c checker) cash() Result {
	const name = CheckCash
	free := c.s.Cash.Sub(c.openBuys(func(string) bool { return true }))
	if c.value.Cmp(free) > 0 {
		return refuse(name, fmt.Sprintf("The buy costs %s, more than the %s of cash free after today's other buys. Sherwood never borrows to buy.",
			dollars(c.value), dollars(free)))
	}
	return pass(name, fmt.Sprintf("The buy costs %s; %s of cash is free after today's other buys.", dollars(c.value), dollars(free)))
}

// watchlist: the stock is on the watchlist or already held. Applies to buys.
func (c checker) watchlist() Result {
	const name = CheckWatchlist
	switch {
	case !c.held(c.o.Symbol).IsZero():
		return pass(name, fmt.Sprintf("%s is already held.", c.o.Symbol))
	case slices.Contains(c.s.Watchlist, c.o.Symbol):
		return pass(name, fmt.Sprintf("%s is on the watchlist.", c.o.Symbol))
	default:
		return refuse(name, fmt.Sprintf("%s is neither on the watchlist nor held.", c.o.Symbol))
	}
}

// limitPrice: the buy's limit price is within the configured distance of the
// last close. Applies to buys.
func (c checker) limitPrice() Result {
	const name = CheckLimitPrice
	limit, ok := ratio(c.s.Limits.MaxLimitDistance)
	if !ok {
		return refuse(name, "The limit (max_limit_distance) is not set.")
	}
	closePrice, ok := c.close(c.o.Symbol)
	if !ok {
		return refuse(name, fmt.Sprintf("There is no last close for %s to measure the limit price against.", c.o.Symbol))
	}
	gap := c.o.Price.Decimal().Sub(closePrice.Decimal()).Abs()
	distance := gap.DivRound(closePrice.Decimal(), 6)
	if gap.Cmp(closePrice.Decimal().Mul(limit)) > 0 {
		return refuse(name, fmt.Sprintf("The limit price %s is %s from the last close of %s, further than the %s allowed.",
			c.o.Price.Money().String(), percent(distance), closePrice.String(), percent(limit)))
	}
	return pass(name, fmt.Sprintf("The limit price %s is %s from the last close of %s; at most %s is allowed.",
		c.o.Price.Money().String(), percent(distance), closePrice.String(), percent(limit)))
}

// sharesHeld: a sell is of shares held, at no more than the shares held.
// Applies to every sell, protective or not.
func (c checker) sharesHeld() Result {
	const name = CheckSharesHeld
	held := c.held(c.o.Symbol)
	if held.IsZero() {
		return refuse(name, fmt.Sprintf("%s is not held, and sherwood never sells short.", c.o.Symbol))
	}
	if c.o.Quantity.Cmp(held) > 0 {
		return refuse(name, fmt.Sprintf("The order sells %s, but the holding is only %s.", shares(c.o.Quantity), shares(held)))
	}
	return pass(name, fmt.Sprintf("The order sells %s of the %s held.", shares(c.o.Quantity), shares(held)))
}

// sameDay: no buying and selling the same stock on the same day. Applies to
// buys and ordinary sells.
func (c checker) sameDay() Result {
	const name = CheckSameDay
	for _, d := range c.s.DayOrders {
		if d.Symbol == c.o.Symbol && d.Kind.IsSell() != c.o.Kind.IsSell() {
			return refuse(name, fmt.Sprintf("%s already has a %s today, so this %s would trade it both ways in one day.",
				c.o.Symbol, d.Kind, c.o.Kind))
		}
	}
	return pass(name, fmt.Sprintf("%s has no opposite order today.", c.o.Symbol))
}

// switchAndBroker: the stop switch is off and the broker is healthy. Protective
// sells are not blocked.
func (c checker) switchAndBroker() Result {
	const name = CheckSwitch
	switch {
	case !c.s.SwitchOff:
		return refuse(name, "The stop switch is on, so only protective sells go through until you reset it.")
	case !c.s.BrokerHealthy:
		return refuse(name, "The broker is not answering, so only protective sells go through.")
	default:
		return pass(name, "The stop switch is off and the broker is answering.")
	}
}

// marketOpen: the market is open for the order's session, or the order is a
// resting stop. At preview the session only has to be still ahead.
func (c checker) marketOpen() Result {
	const name = CheckMarketOpen
	if c.o.Kind == ProtectiveSell {
		return pass(name, "A protective sell rests at the broker as a stop order, so it may be placed at any hour.")
	}
	date := c.o.Session.In(clock.Eastern).Format(clock.DateLayout)
	sess, err := c.s.Calendar.Session(c.o.Session)
	switch {
	case errors.Is(err, clock.ErrClosed):
		return refuse(name, fmt.Sprintf("The order is for %s, when the exchange is closed.", date))
	case errors.Is(err, clock.ErrNotCovered):
		return refuse(name, fmt.Sprintf("The order is for %s, outside the exchange calendar, so sherwood cannot tell whether the market is open.", date))
	case err != nil:
		return refuse(name, fmt.Sprintf("The session for %s could not be read: %v.", date, err))
	}
	closes := sess.Close.Format("3:04 PM") + " ET"
	if !c.s.Now.Before(sess.Close) {
		return refuse(name, fmt.Sprintf("The order was for the %s session, which closed at %s.", date, closes))
	}
	if c.s.Preview {
		return pass(name, fmt.Sprintf("The order is for the %s session, which has not closed yet; this is checked again when it is sent.", date))
	}
	if c.s.Now.Before(sess.Open) {
		return refuse(name, fmt.Sprintf("The market opens at %s ET on %s; it is not open yet.", sess.Open.Format("3:04 PM"), date))
	}
	return pass(name, fmt.Sprintf("The market is open until %s.", closes))
}

// ratio reads a share from the strategy file exactly. It is false when the
// share is unset or not above zero.
func ratio(d config.Decimal) (decimal.Decimal, bool) {
	r, err := decimal.NewFromString(d.String())
	if err != nil || !r.IsPositive() {
		return decimal.Decimal{}, false
	}
	return r, true
}

// times is m times a ratio, exactly.
func times(m money.Money, r decimal.Decimal) money.Money {
	return money.FromDecimal(m.Decimal().Mul(r))
}

// shareOf is part over whole, to six places, for display only; the limits
// are compared by multiplying, never by this rounded figure.
func shareOf(part, whole money.Money) decimal.Decimal {
	return part.Decimal().DivRound(whole.Decimal(), 6)
}

var hundred = decimal.NewFromInt(100)

// percent writes a share as a percentage to at most one place: 0.10 is 10%.
func percent(r decimal.Decimal) string {
	return r.Mul(hundred).Round(1).String() + "%"
}

// dollars writes an amount rounded to the cent: $1,234.50 is written $1234.50.
func dollars(m money.Money) string {
	r := m.RoundCents()
	if r.IsNegative() {
		return "-$" + r.Neg().String()
	}
	return "$" + r.String()
}

// shares writes a share count with its noun.
func shares(q money.Quantity) string {
	if q.Equal(money.MustParseQuantity("1")) {
		return "1 share"
	}
	return q.String() + " shares"
}

// countOrders writes a count of orders with its noun.
func countOrders(n int) string {
	if n == 1 {
		return "1 order was"
	}
	return fmt.Sprintf("%d orders were", n)
}
