package risk

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// Every symbol, price and amount below is made up. Tuesday 17 November 2026
// is a regular trading day; Saturday 21 November is not.

var (
	tradingDay = time.Date(2026, time.November, 17, 0, 0, 0, 0, clock.Eastern)
	saturday   = time.Date(2026, time.November, 21, 0, 0, 0, 0, clock.Eastern)
)

func at(day time.Time, hour, minute int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, clock.Eastern)
}

func m(s string) money.Money      { return money.MustParseMoney(s) }
func p(s string) money.Price      { return money.MustParsePrice(s) }
func q(s string) money.Quantity   { return money.MustParseQuantity(s) }
func dec(s string) config.Decimal { return config.MustDecimal(s) }

func ptr(d config.Decimal) *config.Decimal { return &d }

// snapshot is a portfolio worth $1000.00 with $500.00 cash, 2 AAA (tech, at
// $20.00) and 3 BBB (energy, at $50.00), on the trading day at 10:00 ET with
// nothing else sent today.
func snapshot() Snapshot {
	return Snapshot{
		Now:      at(tradingDay, 10, 0),
		Calendar: clock.NYSE(),
		Limits: config.Limits{
			MaxStockShare:    dec("0.10"),
			MaxOrderValue:    m("100.00"),
			MaxOrdersPerDay:  3,
			DailyLossLimit:   m("50.00"),
			MaxHoldings:      5,
			MaxSectorShare:   ptr(dec("0.30")),
			MaxLimitDistance: dec("0.05"),
			BuyLimitMargin:   dec("0.01"),
		},
		Sectors:            map[string]string{"AAA": "tech", "BBB": "energy", "CCC": "tech", "DDD": "health"},
		Watchlist:          []string{"CCC", "DDD"},
		Held:               map[string]money.Quantity{"AAA": q("2"), "BBB": q("3")},
		Cash:               m("500.00"),
		Value:              m("1000.00"),
		PreviousCloseValue: m("1000.00"),
		LastCloses:         map[string]money.Price{"AAA": p("20.00"), "BBB": p("50.00"), "CCC": p("10.00"), "DDD": p("30.00")},
		SwitchOff:          true,
		BrokerHealthy:      true,
	}
}

// buy is 5 CCC at a $10.10 limit: $50.50.
func buy() Order {
	return Order{Strategy: "test", Symbol: "CCC", Kind: Buy, Quantity: q("5"), Price: p("10.10"), Session: tradingDay}
}

// sell is 1 BBB at the open, valued at its $50.00 close.
func sell() Order {
	return Order{Strategy: "test", Symbol: "BBB", Kind: Sell, Quantity: q("1"), Session: tradingDay}
}

// protective is a stop at $18.00 for the 2 AAA held: $36.00.
func protective() Order {
	return Order{Strategy: "test", Symbol: "AAA", Kind: ProtectiveSell, Quantity: q("2"), Price: p("18.00"), Session: tradingDay}
}

func names(v Verdict) []string {
	var out []string
	for _, r := range v.Results {
		out = append(out, r.Check)
	}
	return out
}

func find(t *testing.T, v Verdict, check string) Result {
	t.Helper()
	for _, r := range v.Results {
		if r.Check == check {
			return r
		}
	}
	t.Fatalf("verdict has no %q check:\n%s", check, v)
	return Result{}
}

func TestBaseOrdersPassEveryCheck(t *testing.T) {
	for _, o := range []Order{buy(), sell(), protective()} {
		v := Check(o, snapshot())
		if !v.Passed() {
			t.Errorf("%s refused:\n%s", o.Kind, v)
		}
		if v.Reason() != "" {
			t.Errorf("%s: Reason = %q on a pass", o.Kind, v.Reason())
		}
	}
}

func TestEachKindRunsItsOwnList(t *testing.T) {
	want := map[Kind][]string{
		Buy: {CheckStockShare, CheckOrderValue, CheckOrderCount, CheckDailyLoss, CheckHoldings, CheckSectorShare,
			CheckCash, CheckWatchlist, CheckLimitPrice, CheckSameDay, CheckSwitch, CheckMarketOpen},
		Sell:           {CheckOrderValue, CheckOrderCount, CheckDailyLoss, CheckSharesHeld, CheckSameDay, CheckSwitch, CheckMarketOpen},
		ProtectiveSell: {CheckOrderValue, CheckSharesHeld, CheckMarketOpen},
	}
	for _, o := range []Order{buy(), sell(), protective()} {
		if got := names(Check(o, snapshot())); !slices.Equal(got, want[o.Kind]) {
			t.Errorf("%s checks = %v, want %v", o.Kind, got, want[o.Kind])
		}
	}
}

// TestEachLimit gives every check at least one passing and one refusing case.
func TestEachLimit(t *testing.T) {
	cases := []struct {
		name   string
		order  func() Order
		change func(*Order, *Snapshot)
		check  string
		pass   bool
		detail string
	}{
		// One stock's share of the portfolio.
		{"stock share under the limit", buy, nil, CheckStockShare, true, "CCC would be 5.1% of the portfolio; the limit is 10%"},
		{"stock share exactly at the limit", buy, func(o *Order, _ *Snapshot) {
			o.Quantity, o.Price = q("10"), p("10.00")
		}, CheckStockShare, true, "10%"},
		{"stock share over the limit with shares held", buy, func(_ *Order, s *Snapshot) {
			s.Held["CCC"] = q("8")
		}, CheckStockShare, false, "CCC would be 13.1% of the portfolio ($130.50 of $1000.00), over the 10% limit"},
		{"stock share counts today's open buys", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "CCC", Kind: Buy, Value: m("60.00"), Open: true}}
		}, CheckStockShare, false, "over the 10% limit"},
		{"stock share with no portfolio value", buy, func(_ *Order, s *Snapshot) { s.Value = m("0") },
			CheckStockShare, false, "portfolio's value is not known"},

		// Value of one order.
		{"order value under the limit", buy, nil, CheckOrderValue, true, "worth $50.50; the limit is $100.00"},
		{"order value over the limit", buy, func(o *Order, _ *Snapshot) {
			o.Symbol, o.Quantity, o.Price = "DDD", q("4"), p("30.30")
		}, CheckOrderValue, false, "worth $121.20, over the $100.00 limit"},
		{"sell valued at the last close", sell, func(o *Order, _ *Snapshot) { o.Quantity = q("3") },
			CheckOrderValue, false, "worth $150.00, over the $100.00 limit"},
		{"sell with no close", sell, func(_ *Order, s *Snapshot) { delete(s.LastCloses, "BBB") },
			CheckOrderValue, false, "no last close for BBB"},
		{"protective sell over the limit", protective, func(o *Order, _ *Snapshot) { o.Price = p("60.00") },
			CheckOrderValue, false, "worth $120.00, over the $100.00 limit"},
		{"protective sell under the limit", protective, nil, CheckOrderValue, true, "worth $36.00"},

		// Orders in one day.
		{"third order of three", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "AAA", Kind: Buy, Value: m("1")}, {Symbol: "DDD", Kind: Buy, Value: m("1")}}
		}, CheckOrderCount, true, "order 3 of at most 3"},
		{"fourth order of three", sell, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "AAA", Kind: Sell}, {Symbol: "DDD", Kind: Buy}, {Symbol: "CCC", Kind: Buy}}
		}, CheckOrderCount, false, "3 orders were already sent or approved today; the limit is 3 a day"},
		{"protective sells do not count", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "AAA", Kind: ProtectiveSell}, {Symbol: "BBB", Kind: ProtectiveSell},
				{Symbol: "EEE", Kind: ProtectiveSell}, {Symbol: "DDD", Kind: Buy}}
		}, CheckOrderCount, true, "order 2 of at most 3"},

		// Daily loss limit.
		{"loss under the limit", buy, func(_ *Order, s *Snapshot) { s.Value = m("960.00") },
			CheckDailyLoss, true, "lost $40.00 since the previous close, under the $50.00 limit"},
		{"loss reaching the limit", buy, func(_ *Order, s *Snapshot) { s.Value = m("950.00") },
			CheckDailyLoss, false, "lost $50.00 since the previous close, which reaches the $50.00 daily loss limit"},
		{"a gain", sell, func(_ *Order, s *Snapshot) { s.Value = m("1020.00") },
			CheckDailyLoss, true, "has not lost money"},
		{"no previous close value", sell, func(_ *Order, s *Snapshot) { s.PreviousCloseValue = m("0") },
			CheckDailyLoss, false, "value at the previous close is not known"},

		// Number of holdings.
		{"holdings at the limit", buy, func(_ *Order, s *Snapshot) { s.Limits.MaxHoldings = 3 },
			CheckHoldings, true, "3 holdings; the limit is 3"},
		{"holdings over the limit", buy, func(_ *Order, s *Snapshot) { s.Limits.MaxHoldings = 2 },
			CheckHoldings, false, "3 holdings, over the limit of 2"},
		{"adding to a holding keeps the count", buy, func(o *Order, s *Snapshot) {
			s.Limits.MaxHoldings = 2
			o.Symbol, o.Price = "AAA", p("20.20")
		}, CheckHoldings, true, "2 holdings; the limit is 2"},
		{"holdings count today's open buys", buy, func(_ *Order, s *Snapshot) {
			s.Limits.MaxHoldings = 3
			s.DayOrders = []DayOrder{{Symbol: "DDD", Kind: Buy, Value: m("30.00"), Open: true}}
		}, CheckHoldings, false, "4 holdings, over the limit of 3"},
		{"holdings never above 20", buy, func(_ *Order, s *Snapshot) {
			s.Limits.MaxHoldings = 25
			for i := range 20 {
				s.Held[string(rune('E'+i))+"X"] = q("1")
			}
		}, CheckHoldings, false, "over the limit of 20"},

		// One sector's share of the portfolio.
		{"sector under the limit", buy, nil, CheckSectorShare, true, "the tech sector would be 9.1% of the portfolio; the limit is 30%"},
		{"sector over the limit", buy, func(_ *Order, s *Snapshot) { s.Limits.MaxSectorShare = ptr(dec("0.08")) },
			CheckSectorShare, false, "the tech sector would be 9.1% of the portfolio ($90.50 of $1000.00), over the 8% limit"},
		{"sector counts today's open buys", buy, func(_ *Order, s *Snapshot) {
			s.Limits.MaxSectorShare = ptr(dec("0.10"))
			s.DayOrders = []DayOrder{{Symbol: "AAA", Kind: Buy, Value: m("20.00"), Open: true}}
		}, CheckSectorShare, false, "over the 10% limit"},
		{"stock with no sector", buy, func(_ *Order, s *Snapshot) { delete(s.Sectors, "CCC") },
			CheckSectorShare, false, "gives no sector for CCC"},
		{"no sector limit", buy, func(_ *Order, s *Snapshot) {
			s.Limits.MaxSectorShare = nil
			s.Sectors = nil
		}, CheckSectorShare, true, "No sector limit is set"},

		// Cash only.
		{"cash covers the buy exactly", buy, func(_ *Order, s *Snapshot) { s.Cash = m("50.50") },
			CheckCash, true, "costs $50.50; $50.50 of cash is free"},
		{"cash short", buy, func(_ *Order, s *Snapshot) { s.Cash = m("50.49") },
			CheckCash, false, "more than the $50.49 of cash free"},
		{"cash promised to today's open buys", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "DDD", Kind: Buy, Value: m("460.00"), Open: true}}
		}, CheckCash, false, "more than the $40.00 of cash free after today's other buys"},
		{"a settled buy no longer holds cash", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "DDD", Kind: Buy, Value: m("460.00")}}
		}, CheckCash, true, "$500.00 of cash is free"},
		{"negative cash", buy, func(_ *Order, s *Snapshot) { s.Cash = m("-5.00") },
			CheckCash, false, "-$5.00 of cash free"},

		// On the watchlist or held.
		{"on the watchlist", buy, nil, CheckWatchlist, true, "CCC is on the watchlist"},
		{"already held", buy, func(o *Order, _ *Snapshot) { o.Symbol, o.Price = "BBB", p("50.50") },
			CheckWatchlist, true, "BBB is already held"},
		{"neither", buy, func(o *Order, s *Snapshot) {
			o.Symbol = "EEE"
			s.LastCloses["EEE"] = p("10.00")
		}, CheckWatchlist, false, "EEE is neither on the watchlist nor held"},

		// Limit price near the last close.
		{"limit at the distance", buy, func(o *Order, _ *Snapshot) { o.Price = p("10.50") },
			CheckLimitPrice, true, "10.50 is 5% from the last close of 10.00; at most 5% is allowed"},
		{"limit too far above", buy, func(o *Order, _ *Snapshot) { o.Price = p("10.51") },
			CheckLimitPrice, false, "10.51 is 5.1% from the last close of 10.00, further than the 5% allowed"},
		{"limit too far below", buy, func(o *Order, _ *Snapshot) { o.Price = p("9.49") },
			CheckLimitPrice, false, "further than the 5% allowed"},
		{"no close for the limit", buy, func(_ *Order, s *Snapshot) { delete(s.LastCloses, "CCC") },
			CheckLimitPrice, false, "no last close for CCC"},

		// Shares held.
		{"sell all held", sell, func(o *Order, _ *Snapshot) { o.Quantity = q("2") },
			CheckSharesHeld, true, "sells 2 shares of the 3 shares held"},
		{"sell more than held", sell, func(o *Order, _ *Snapshot) { o.Quantity = q("4") },
			CheckSharesHeld, false, "sells 4 shares, but the holding is only 3 shares"},
		{"sell of a stock not held", sell, func(o *Order, s *Snapshot) {
			o.Symbol = "CCC"
		}, CheckSharesHeld, false, "CCC is not held, and sherwood never sells short"},
		{"protective sell of more than held", protective, func(o *Order, _ *Snapshot) { o.Quantity = q("3") },
			CheckSharesHeld, false, "sells 3 shares, but the holding is only 2 shares"},
		{"protective sell of a stock not held", protective, func(o *Order, _ *Snapshot) { o.Symbol = "DDD" },
			CheckSharesHeld, false, "DDD is not held"},

		// No buy and sell of one stock in one day.
		{"no opposite order", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "BBB", Kind: Sell}, {Symbol: "CCC", Kind: Buy}}
		}, CheckSameDay, true, "CCC has no opposite order today"},
		{"buy after a sell", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "CCC", Kind: Sell}}
		}, CheckSameDay, false, "CCC already has a sell today, so this buy would trade it both ways"},
		{"buy after a filled stop", buy, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "CCC", Kind: ProtectiveSell}}
		}, CheckSameDay, false, "already has a protective sell today"},
		{"sell after a buy", sell, func(_ *Order, s *Snapshot) {
			s.DayOrders = []DayOrder{{Symbol: "BBB", Kind: Buy, Value: m("50.00")}}
		}, CheckSameDay, false, "BBB already has a buy today, so this sell would trade it both ways"},

		// Stop switch off and broker healthy.
		{"switch off, broker answering", sell, nil, CheckSwitch, true, "stop switch is off"},
		{"switch on", buy, func(_ *Order, s *Snapshot) { s.SwitchOff = false }, CheckSwitch, false, "stop switch is on"},
		{"broker down", sell, func(_ *Order, s *Snapshot) { s.BrokerHealthy = false }, CheckSwitch, false, "broker is not answering"},

		// Market open.
		{"market open", buy, nil, CheckMarketOpen, true, "market is open until 4:00 PM ET"},
		{"before the open", buy, func(_ *Order, s *Snapshot) { s.Now = at(tradingDay, 9, 29) },
			CheckMarketOpen, false, "opens at 9:30 AM ET on 2026-11-17; it is not open yet"},
		{"previewed the evening before", buy, func(_ *Order, s *Snapshot) {
			s.Now = at(tradingDay.AddDate(0, 0, -1), 18, 0)
			s.Preview = true
		}, CheckMarketOpen, true, "has not closed yet; this is checked again when it is sent"},
		{"after the close", sell, func(_ *Order, s *Snapshot) { s.Now = at(tradingDay, 16, 0) },
			CheckMarketOpen, false, "the 2026-11-17 session, which closed at 4:00 PM ET"},
		{"previewed after its session closed", buy, func(_ *Order, s *Snapshot) {
			s.Now = at(tradingDay, 18, 0)
			s.Preview = true
		}, CheckMarketOpen, false, "which closed at 4:00 PM ET"},
		{"a later day sending an old order", buy, func(_ *Order, s *Snapshot) { s.Now = at(tradingDay.AddDate(0, 0, 1), 9, 35) },
			CheckMarketOpen, false, "which closed"},
		{"a day the exchange is closed", buy, func(o *Order, s *Snapshot) {
			o.Session = saturday
			s.Now = at(saturday, 10, 0)
		}, CheckMarketOpen, false, "for 2026-11-21, when the exchange is closed"},
		{"a year the calendar does not cover", sell, func(o *Order, s *Snapshot) {
			o.Session = time.Date(2099, time.March, 3, 0, 0, 0, 0, clock.Eastern)
		}, CheckMarketOpen, false, "outside the exchange calendar"},
		{"a resting stop on a Saturday night", protective, func(_ *Order, s *Snapshot) { s.Now = at(saturday, 22, 0) },
			CheckMarketOpen, true, "rests at the broker"},
	}
	covered := map[string][2]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, s := c.order(), snapshot()
			if c.change != nil {
				c.change(&o, &s)
			}
			v := Check(o, s)
			r := find(t, v, c.check)
			if r.Passed != c.pass {
				t.Errorf("%s passed = %v, want %v: %s", c.check, r.Passed, c.pass, r.Detail)
			}
			if !strings.Contains(r.Detail, c.detail) {
				t.Errorf("%s detail = %q, want it to contain %q", c.check, r.Detail, c.detail)
			}
			if !c.pass && v.Passed() {
				t.Error("the verdict passed with a refused check")
			}
			if !c.pass && !strings.Contains(v.Reason(), r.Detail) {
				t.Errorf("Reason %q leaves out %q", v.Reason(), r.Detail)
			}
		})
		got := covered[c.check]
		if c.pass {
			got[0] = true
		} else {
			got[1] = true
		}
		covered[c.check] = got
	}
	for _, check := range []string{CheckStockShare, CheckOrderValue, CheckOrderCount, CheckDailyLoss, CheckHoldings,
		CheckSectorShare, CheckCash, CheckWatchlist, CheckLimitPrice, CheckSharesHeld, CheckSameDay, CheckSwitch, CheckMarketOpen} {
		if got := covered[check]; !got[0] || !got[1] {
			t.Errorf("%s needs a passing and a refusing case (have pass=%v, refuse=%v)", check, got[0], got[1])
		}
	}
}

// TestProtectiveSellsGoThroughOnABadDay: the daily loss limit is reached, the
// day's orders are used up, the switch is on, the broker is down and the
// session has closed. Buys and ordinary sells are refused; a protective sell
// of shares held still passes.
func TestProtectiveSellsGoThroughOnABadDay(t *testing.T) {
	s := snapshot()
	s.Value = m("900.00")
	s.DayOrders = []DayOrder{{Symbol: "DDD", Kind: Buy}, {Symbol: "EEE", Kind: Sell}, {Symbol: "FFF", Kind: Buy}}
	s.SwitchOff = false
	s.BrokerHealthy = false
	s.Now = at(tradingDay, 20, 0)

	for _, o := range []Order{buy(), sell()} {
		v := Check(o, s)
		if v.Passed() {
			t.Fatalf("%s passed on a bad day:\n%s", o.Kind, v)
		}
		for _, check := range []string{CheckDailyLoss, CheckOrderCount, CheckSwitch} {
			if find(t, v, check).Passed {
				t.Errorf("%s: %s passed", o.Kind, check)
			}
		}
	}
	if v := Check(protective(), s); !v.Passed() {
		t.Errorf("protective sell refused on a bad day:\n%s", v)
	}

	// Each blocker on its own, too.
	blockers := map[string]func(*Snapshot){
		"loss limit":  func(s *Snapshot) { s.Value = m("900.00") },
		"order count": func(s *Snapshot) { s.DayOrders = make([]DayOrder, 3) },
		"switch":      func(s *Snapshot) { s.SwitchOff = false },
		"broker":      func(s *Snapshot) { s.BrokerHealthy = false },
	}
	for name, block := range blockers {
		s := snapshot()
		block(&s)
		if Check(buy(), s).Passed() {
			t.Errorf("%s: buy passed", name)
		}
		if v := Check(protective(), s); !v.Passed() {
			t.Errorf("%s: protective sell refused:\n%s", name, v)
		}
	}
}

func TestSellOfMoreThanHeldIsRefused(t *testing.T) {
	for _, o := range []Order{sell(), protective()} {
		o.Quantity = o.Quantity.Add(q("5"))
		s := snapshot()
		s.Limits.MaxOrderValue = m("10000.00")
		v := Check(o, s)
		if v.Passed() {
			t.Errorf("%s of more than held passed:\n%s", o.Kind, v)
		}
		if r := find(t, v, CheckSharesHeld); r.Passed {
			t.Errorf("%s: shares held check passed: %s", o.Kind, r.Detail)
		}
	}
}

func TestMissingDetailsRefuseWithoutRunningTheChecks(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Order, *Snapshot)
		want   string
	}{
		{"no stock", func(o *Order, _ *Snapshot) { o.Symbol = "" }, "names no stock"},
		{"no kind", func(o *Order, _ *Snapshot) { o.Kind = 0 }, "is not a buy, a sell or a protective sell"},
		{"zero shares", func(o *Order, _ *Snapshot) { o.Quantity = money.Quantity{} }, "zero shares"},
		{"a fraction with whole shares", func(o *Order, _ *Snapshot) { o.Quantity = q("1.5") }, "1.5 shares, but fractional shares are off"},
		{"no limit price", func(o *Order, _ *Snapshot) { o.Price = money.Price{} }, "no limit price"},
		{"no trading date", func(o *Order, _ *Snapshot) { o.Session = time.Time{} }, "names no trading date"},
		{"no time", func(_ *Order, s *Snapshot) { s.Now = time.Time{} }, "given no time"},
		{"no calendar", func(_ *Order, s *Snapshot) { s.Calendar = nil }, "no exchange calendar"},
	}
	for _, c := range cases {
		o, s := buy(), snapshot()
		c.change(&o, &s)
		v := Check(o, s)
		if v.Passed() || len(v.Results) != 1 || v.Results[0].Check != CheckDetails || !strings.Contains(v.Results[0].Detail, c.want) {
			t.Errorf("%s: verdict = %s, want one refused %q mentioning %q", c.name, v, CheckDetails, c.want)
		}
	}
	o := protective()
	o.Price = money.Price{}
	if v := Check(o, snapshot()); v.Passed() || !strings.Contains(v.Reason(), "no stop price") {
		t.Errorf("protective sell with no stop: %s", v)
	}
	s := snapshot()
	s.Limits.FractionalShares = true
	o = buy()
	o.Quantity = q("1.5")
	if v := Check(o, s); !v.Passed() {
		t.Errorf("a fractional buy with fractional shares on refused:\n%s", v)
	}
}

// A snapshot left at its zero value, or zero limits, never lets an order
// through.
func TestZeroValuesRefuse(t *testing.T) {
	s := Snapshot{Now: at(tradingDay, 10, 0), Calendar: clock.NYSE()}
	for _, o := range []Order{buy(), sell(), protective()} {
		if v := Check(o, s); v.Passed() {
			t.Errorf("%s passed against an empty snapshot:\n%s", o.Kind, v)
		}
	}
	s = snapshot()
	s.Limits = config.Limits{}
	for _, o := range []Order{buy(), sell(), protective()} {
		v := Check(o, s)
		if v.Passed() {
			t.Errorf("%s passed with no limits:\n%s", o.Kind, v)
		}
		if !strings.Contains(v.Reason(), "is not set") {
			t.Errorf("%s: reason %q does not say a limit is not set", o.Kind, v.Reason())
		}
	}
	if (Verdict{}).Passed() {
		t.Error("an empty verdict passed")
	}
	if !strings.Contains((Verdict{}).String(), "Refused") || (Verdict{}).Reason() == "" {
		t.Error("an empty verdict does not read as refused")
	}
}

func TestVerdictText(t *testing.T) {
	s := snapshot()
	s.SwitchOff = false
	v := Check(sell(), s)
	text := v.String()
	lines := strings.Split(text, "\n")
	if len(lines) != len(v.Results) {
		t.Fatalf("%d lines for %d checks:\n%s", len(lines), len(v.Results), text)
	}
	if lines[0] != "Passed: Value of one order. The order is worth $50.00; the limit is $100.00." {
		t.Errorf("first line = %q", lines[0])
	}
	if want := "Refused: Stop switch off and broker healthy. The stop switch is on, so only protective sells go through until you reset it."; !slices.Contains(lines, want) {
		t.Errorf("no line %q in:\n%s", want, text)
	}
	if got, want := v.Reason(), "Stop switch off and broker healthy: The stop switch is on, so only protective sells go through until you reset it."; got != want {
		t.Errorf("Reason = %q, want %q", got, want)
	}
	if len(v.Refusals()) != 1 {
		t.Errorf("refusals = %v, want one", v.Refusals())
	}
}

func TestCheckIsRepeatableAndChangesNothing(t *testing.T) {
	s := snapshot()
	s.Held["CCC"] = q("8")
	s.DayOrders = []DayOrder{{Symbol: "AAA", Kind: Buy, Value: m("20.00"), Open: true}}
	first := Check(buy(), s).String()
	for range 20 {
		if got := Check(buy(), s).String(); got != first {
			t.Fatalf("verdict changed between runs:\n%s\n---\n%s", first, got)
		}
	}
	if len(s.Held) != 3 || !s.Held["AAA"].Equal(q("2")) || len(s.DayOrders) != 1 || len(s.LastCloses) != 4 {
		t.Error("the check changed its snapshot")
	}
}

func TestStandardIsTheCheck(t *testing.T) {
	var c Checker = Standard{}
	o, s := buy(), snapshot()
	if got, want := c.Check(o, s).String(), Check(o, s).String(); got != want {
		t.Errorf("Standard gave\n%s\nwant\n%s", got, want)
	}
}

func TestKindNames(t *testing.T) {
	for k, want := range map[Kind]string{Buy: "buy", Sell: "sell", ProtectiveSell: "protective sell", 0: "Kind(0)"} {
		if k.String() != want {
			t.Errorf("Kind(%d) = %q, want %q", int(k), k.String(), want)
		}
	}
}
