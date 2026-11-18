package backtest

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/fill"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/rules"
)

// divPlaces is how many decimal places a division keeps.
const divPlaces = 12

// Config is what one back-test reads.
type Config struct {
	// Strategy is a strategy file that has passed rules.Validate.
	Strategy *config.Strategy
	// Bars are each stock's daily bars, oldest first, keyed by symbol.
	Bars map[string][]prices.Bar
	// Capital is the starting capital the strategies' shares are taken of.
	Capital money.Money
	// From and To bound the dates traded, both inclusive, read as Eastern
	// dates. The zero time leaves that end open. Bars before From are still
	// read by the rules as history.
	From, To time.Time
}

// ErrConfig is wrapped by the error Run returns for a bad Config.
var ErrConfig = errors.New("backtest: bad input")

// Side is a trade's direction.
type Side string

// The sides.
const (
	Bought Side = "buy"
	Sold   Side = "sell"
)

// Trade is one filled order.
type Trade struct {
	Date     time.Time
	Symbol   string
	Side     Side
	Quantity money.Quantity
	Price    money.Price
	Value    money.Money
	// Profit is the sale's value less the average cost of the shares sold.
	// It is zero for a buy.
	Profit money.Money
	// Why is the rule's reason for the order, then the fill's reason.
	Why string
}

// Result is one strategy's back-test.
type Result struct {
	Name           string
	PortfolioShare string
	Capital        money.Money
	// First and Last are the first and last trading dates replayed. Days is
	// how many there were.
	First, Last time.Time
	Days        int
	Final       money.Money
	// Return, MaxDrawdown, WinRate and Turnover are ratios (0.05 is 5%).
	// WinRate is meaningless when Sells is zero.
	Return, MaxDrawdown, WinRate, Turnover decimal.Decimal
	AverageValue                           money.Money
	BoughtValue, SoldValue                 money.Money
	Buys, Sells, StopSells, Wins           int
	Unfilled, BlockedCash, BlockedHoldings int
	NoSizing                               int
	HeldAtEnd                              int
	Trades                                 []Trade
}

// Report is a back-test of every strategy in a strategy file.
type Report struct {
	StrategyHash string
	Capital      money.Money
	Results      []Result
}

// Run back-tests every strategy in cfg.Strategy, in the file's order.
func Run(cfg Config) (*Report, error) {
	if cfg.Strategy == nil {
		return nil, fmt.Errorf("%w: no strategy file", ErrConfig)
	}
	if !cfg.Capital.IsPositive() {
		return nil, fmt.Errorf("%w: starting capital must be above zero", ErrConfig)
	}
	if err := rules.Validate(cfg.Strategy); err != nil {
		return nil, err
	}
	rep := &Report{StrategyHash: cfg.Strategy.Hash, Capital: cfg.Capital}
	for _, def := range cfg.Strategy.Strategies {
		res, err := runStrategy(cfg, def)
		if err != nil {
			return nil, fmt.Errorf("strategy %q: %w", def.Name, err)
		}
		rep.Results = append(rep.Results, *res)
	}
	return rep, nil
}

// series is one stock's bars, in both shapes the replay needs.
type series struct {
	bars  []prices.Bar
	rules []rules.Bar
	// at maps a date key to the bar's index.
	at map[int]int
}

// position is a holding during the replay.
type position struct {
	qty    money.Quantity
	basis  money.Money // what the shares held cost in all
	opened time.Time
	stop   *money.Price
}

// cost is the average cost per share, rounded to the cent.
func (p *position) cost() (money.Price, error) {
	return money.RoundPrice(p.basis.Decimal().DivRound(p.qty.Decimal(), divPlaces))
}

// order is an order made after one date's close, worked on the next.
type order struct {
	symbol string
	kind   fill.Kind
	qty    money.Quantity
	limit  money.Price
	stop   bool // the sell came from a stop rule
	why    string
}

// strategyRules is a strategy's parsed rules by kind, in the file's order.
type strategyRules struct {
	signal  []rules.SignalRule
	ranking []rules.RankingRule
	stop    []rules.StopRule
	sizing  rules.SizingRule
}

func parseRules(def config.StrategyDef) (strategyRules, error) {
	var sr strategyRules
	for _, cr := range def.Rules {
		r, err := rules.Parse(cr)
		if err != nil {
			return sr, err
		}
		switch x := r.(type) {
		case rules.SignalRule:
			sr.signal = append(sr.signal, x)
		case rules.RankingRule:
			sr.ranking = append(sr.ranking, x)
		case rules.StopRule:
			sr.stop = append(sr.stop, x)
		case rules.SizingRule:
			sr.sizing = x
		}
	}
	return sr, nil
}

// dayKey turns a date into a number that orders like the date, reading its
// Eastern year, month and day.
func dayKey(t time.Time) int {
	y, m, d := t.In(clock.Eastern).Date()
	return y*10000 + int(m)*100 + d
}

func ratio(a, b decimal.Decimal) decimal.Decimal {
	if b.IsZero() {
		return decimal.Zero
	}
	return a.DivRound(b, divPlaces)
}

// sim is one strategy's replay.
type sim struct {
	cfg     Config
	def     config.StrategyDef
	rules   strategyRules
	policy  money.QuantityPolicy
	margin  decimal.Decimal
	maxHeld int
	symbols []string // sorted
	series  map[string]*series

	cash    money.Money
	held    map[string]*position
	pending []order
	res     *Result

	peak, sumValue money.Money
}

func runStrategy(cfg Config, def config.StrategyDef) (*Result, error) {
	sr, err := parseRules(def)
	if err != nil {
		return nil, err
	}
	share, err := decimal.NewFromString(def.PortfolioShare.String())
	if err != nil {
		return nil, fmt.Errorf("%w: portfolio_share %q", ErrConfig, def.PortfolioShare)
	}
	margin, err := decimal.NewFromString(cfg.Strategy.Limits.BuyLimitMargin.String())
	if err != nil {
		return nil, fmt.Errorf("%w: buy_limit_margin %q", ErrConfig, cfg.Strategy.Limits.BuyLimitMargin)
	}
	capital := money.FromDecimal(cfg.Capital.Decimal().Mul(share).Truncate(money.CentPlaces))
	s := &sim{
		cfg:     cfg,
		def:     def,
		rules:   sr,
		policy:  money.PolicyFor(cfg.Strategy.Limits.FractionalShares),
		margin:  margin,
		maxHeld: min(cfg.Strategy.Limits.MaxHoldings, config.MaxHoldings),
		symbols: slices.Sorted(slices.Values(def.Stocks)),
		series:  map[string]*series{},
		cash:    capital,
		held:    map[string]*position{},
		res:     &Result{Name: def.Name, PortfolioShare: def.PortfolioShare.String(), Capital: capital},
	}
	s.symbols = slices.Compact(s.symbols)

	var dates []int
	for _, sym := range s.symbols {
		bars, ok := cfg.Bars[sym]
		if !ok || len(bars) == 0 {
			return nil, fmt.Errorf("%w: no bars for %s", ErrConfig, sym)
		}
		se := &series{bars: bars, at: map[int]int{}}
		for i, b := range bars {
			k := dayKey(b.Date)
			if i > 0 && k <= dayKey(bars[i-1].Date) {
				return nil, fmt.Errorf("%w: %s bars are not in date order at %s", ErrConfig, sym, b.DateString())
			}
			if err := b.Check(); err != nil {
				return nil, fmt.Errorf("%w: %w", ErrConfig, err)
			}
			se.at[k] = i
			se.rules = append(se.rules, rules.Bar{Date: b.Date, High: b.High, Low: b.Low, Close: b.Close})
			dates = append(dates, k)
		}
		s.series[sym] = se
	}
	slices.Sort(dates)
	dates = slices.Compact(dates)

	from, to := 0, 99991231
	if !cfg.From.IsZero() {
		from = dayKey(cfg.From)
	}
	if !cfg.To.IsZero() {
		to = dayKey(cfg.To)
	}
	for _, d := range dates {
		if d < from || d > to {
			continue
		}
		if err := s.day(d); err != nil {
			return nil, err
		}
	}
	if s.res.Days == 0 {
		return nil, fmt.Errorf("%w: no trading dates in the range", ErrConfig)
	}
	s.finish()
	return s.res, nil
}

// barOn returns sym's bar on date d.
func (s *sim) barOn(sym string, d int) (prices.Bar, int, bool) {
	se := s.series[sym]
	i, ok := se.at[d]
	if !ok {
		return prices.Bar{}, 0, false
	}
	return se.bars[i], i, true
}

// lastClose is sym's latest close on or before date d.
func (s *sim) lastClose(sym string, d int) money.Price {
	se := s.series[sym]
	i, _ := slices.BinarySearchFunc(se.bars, d, func(b prices.Bar, k int) int { return dayKey(b.Date) - k })
	if i < len(se.bars) && dayKey(se.bars[i].Date) == d {
		return se.bars[i].Close
	}
	return se.bars[i-1].Close // the caller only asks for held stocks, which have an earlier bar
}

func (s *sim) day(d int) error {
	if s.res.Days == 0 {
		s.res.First = s.anyBar(d).Date
	}
	s.res.Days++
	s.res.Last = s.anyBar(d).Date
	if err := s.work(d); err != nil {
		return err
	}
	s.value(d)
	return s.decide(d)
}

// anyBar is the first stock's bar on date d, for its date.
func (s *sim) anyBar(d int) prices.Bar {
	for _, sym := range s.symbols {
		if b, _, ok := s.barOn(sym, d); ok {
			return b
		}
	}
	return prices.Bar{}
}

// work fills the pending orders against date d's bars: sells at the open,
// then resting stops, then buys.
func (s *sim) work(d int) error {
	var keep []order
	for _, o := range s.pending {
		if o.kind != fill.SellAtOpen {
			continue
		}
		b, _, ok := s.barOn(o.symbol, d)
		if !ok {
			keep = append(keep, o) // the sell waits for the stock's next bar
			continue
		}
		if err := s.apply(o, b); err != nil {
			return err
		}
	}
	for _, sym := range s.symbols {
		p, ok := s.held[sym]
		if !ok || p.stop == nil {
			continue
		}
		b, _, ok := s.barOn(sym, d)
		if !ok {
			continue
		}
		o := order{symbol: sym, kind: fill.SellStop, qty: p.qty, limit: *p.stop, stop: true, why: "its resting stop"}
		if err := s.apply(o, b); err != nil {
			return err
		}
	}
	for _, o := range s.pending {
		if o.kind != fill.BuyLimit {
			continue
		}
		b, _, ok := s.barOn(o.symbol, d)
		if !ok {
			s.res.Unfilled++ // a day order for a day the stock did not trade
			continue
		}
		if err := s.apply(o, b); err != nil {
			return err
		}
	}
	s.pending = keep
	return nil
}

// apply works one order against a bar and books the fill.
func (s *sim) apply(o order, b prices.Bar) error {
	p := s.held[o.symbol]
	if o.kind != fill.BuyLimit {
		if p == nil {
			return nil // already sold earlier today
		}
		if o.qty.Cmp(p.qty) > 0 {
			o.qty = p.qty
		}
	}
	f, ok, err := fill.Apply(fill.Order{Kind: o.kind, Quantity: o.qty, Price: o.limit},
		fill.Day{Open: b.Open, High: b.High, Low: b.Low}, s.policy)
	if err != nil {
		return fmt.Errorf("%s on %s: %w", o.symbol, b.DateString(), err)
	}
	if !ok {
		if o.kind == fill.BuyLimit {
			s.res.Unfilled++
		}
		return nil
	}
	t := Trade{Date: b.Date, Symbol: o.symbol, Quantity: f.Quantity, Price: f.Price, Value: f.Value,
		Why: o.why + "; " + f.Reason}
	if o.kind == fill.BuyLimit {
		if f.Value.Cmp(s.cash) > 0 {
			return fmt.Errorf("%s on %s: a buy of %s is more than the cash of %s", o.symbol, b.DateString(), f.Value, s.cash)
		}
		s.cash = s.cash.Sub(f.Value)
		if p == nil {
			p = &position{opened: b.Date}
			s.held[o.symbol] = p
		}
		p.qty = p.qty.Add(f.Quantity)
		p.basis = p.basis.Add(f.Value)
		t.Side = Bought
		s.res.Buys++
		s.res.BoughtValue = s.res.BoughtValue.Add(f.Value)
	} else {
		basis := p.basis
		if !f.Quantity.Equal(p.qty) {
			basis = money.FromDecimal(p.basis.Decimal().Mul(f.Quantity.Decimal()).DivRound(p.qty.Decimal(), divPlaces))
		}
		left, err := p.qty.Sub(f.Quantity)
		if err != nil {
			return err
		}
		s.cash = s.cash.Add(f.Value)
		if left.IsZero() {
			delete(s.held, o.symbol)
		} else {
			p.qty = left
			p.basis = p.basis.Sub(basis)
		}
		t.Side = Sold
		t.Profit = f.Value.Sub(basis)
		s.res.Sells++
		if o.stop {
			s.res.StopSells++
		}
		if t.Profit.IsPositive() {
			s.res.Wins++
		}
		s.res.SoldValue = s.res.SoldValue.Add(f.Value)
	}
	s.res.Trades = append(s.res.Trades, t)
	return nil
}

// value books the day's value: cash plus each holding at its latest close.
func (s *sim) value(d int) {
	v := s.holdingsValue(d).Add(s.cash)
	s.sumValue = s.sumValue.Add(v)
	if v.Cmp(s.peak) > 0 {
		s.peak = v
	}
	if dd := ratio(s.peak.Sub(v).Decimal(), s.peak.Decimal()); dd.Cmp(s.res.MaxDrawdown) > 0 {
		s.res.MaxDrawdown = dd
	}
	s.res.Final = v
}

func (s *sim) holdingsValue(d int) money.Money {
	var v money.Money
	for _, sym := range s.symbols {
		if p, ok := s.held[sym]; ok {
			v = v.Add(s.lastClose(sym, d).Mul(p.qty))
		}
	}
	return v
}

// action is a stock's combined outcome, in priority order: a lower value
// wins.
type action int

const (
	stopSell action = iota
	sell
	trim
	entry
	none
)

func actionOf(o rules.Outcome) action {
	switch o {
	case rules.Sell:
		return sell
	case rules.Trim:
		return trim
	case rules.Buy, rules.Add:
		return entry
	default:
		return none
	}
}

type pick struct {
	act action
	why string
}

func (p *pick) offer(a action, ruleType, reason string) {
	if a < p.act {
		p.act, p.why = a, ruleType+": "+reason
	}
}

func (s *sim) holding(sym string) (*rules.Holding, error) {
	p, ok := s.held[sym]
	if !ok {
		return nil, nil
	}
	cost, err := p.cost()
	if err != nil {
		return nil, err
	}
	return &rules.Holding{Quantity: p.qty, Cost: cost, Opened: p.opened, Stop: p.stop}, nil
}

// decide runs the rules after date d's close and makes the next date's
// orders.
func (s *sim) decide(d int) error {
	picks := map[string]*pick{}
	inputs := make([]rules.Input, 0, len(s.symbols))
	for _, sym := range s.symbols {
		se := s.series[sym]
		end, _ := slices.BinarySearchFunc(se.bars, d+1, func(b prices.Bar, k int) int { return dayKey(b.Date) - k })
		end-- // the last bar on or before d
		h, err := s.holding(sym)
		if err != nil {
			return err
		}
		inputs = append(inputs, rules.Input{Symbol: sym, Bars: se.rules[:end+1], Holding: h})
	}
	for i, sym := range s.symbols {
		if _, _, ok := s.barOn(sym, d); !ok {
			continue
		}
		pk := &pick{act: none}
		picks[sym] = pk
		in := inputs[i]
		if in.Holding != nil {
			var top *money.Price
			for _, r := range s.rules.stop {
				dec, err := r.Stop(in)
				if err != nil {
					return err
				}
				if dec.Stop != nil && (top == nil || dec.Stop.Cmp(*top) > 0) {
					stop := *dec.Stop
					top = &stop
				}
				if dec.Outcome == rules.Sell {
					pk.offer(stopSell, r.Type(), dec.Reason)
				}
			}
			if top != nil {
				s.held[sym].stop = top
			}
		}
		for _, r := range s.rules.signal {
			dec, err := r.Decide(in)
			if err != nil {
				return err
			}
			pk.offer(actionOf(dec.Outcome), r.Type(), dec.Reason)
		}
	}
	for _, r := range s.rules.ranking {
		decs, err := r.Rank(inputs)
		if err != nil {
			return err
		}
		for i, dec := range decs {
			if pk, ok := picks[s.symbols[i]]; ok {
				pk.offer(actionOf(dec.Outcome), r.Type(), dec.Reason)
			}
		}
	}

	// Sells first, so they are worked before buys on the next date.
	var buys []string
	for _, sym := range s.symbols {
		pk, ok := picks[sym]
		if !ok {
			continue
		}
		switch pk.act {
		case stopSell, sell:
			s.pending = append(s.pending, order{symbol: sym, kind: fill.SellAtOpen, qty: s.held[sym].qty,
				stop: pk.act == stopSell, why: pk.why})
		case trim:
			half, err := s.policy.Round(s.held[sym].qty.Decimal().DivRound(decimal.NewFromInt(2), divPlaces))
			if err != nil {
				return err
			}
			if !half.IsZero() {
				s.pending = append(s.pending, order{symbol: sym, kind: fill.SellAtOpen, qty: half, why: pk.why})
			}
		case entry:
			buys = append(buys, sym)
		}
	}
	return s.placeBuys(d, buys, picks, inputs)
}

// placeBuys sizes each buy and admits it if the cash and the holdings limit
// allow, in symbol order.
func (s *sim) placeBuys(d int, buys []string, picks map[string]*pick, inputs []rules.Input) error {
	promised := money.Money{}
	count := len(s.held)
	for _, sym := range buys {
		if s.rules.sizing == nil {
			s.res.NoSizing++
			continue
		}
		b, _, _ := s.barOn(sym, d)
		limit, err := money.RoundPrice(b.Close.Decimal().Mul(decimal.NewFromInt(1).Add(s.margin)))
		if err != nil {
			return err
		}
		stop, err := s.initialStop(sym, limit, inputs)
		if err != nil {
			return err
		}
		size, err := s.rules.sizing.Size(rules.SizeInput{Symbol: sym, Price: limit, Stop: stop,
			Capital: s.res.Capital, Policy: s.policy})
		if err != nil {
			return err
		}
		if size.Quantity.IsZero() {
			continue
		}
		_, held := s.held[sym]
		if !held && count+1 > s.maxHeld {
			s.res.BlockedHoldings++
			continue
		}
		cost := limit.Mul(size.Quantity)
		if promised.Add(cost).Cmp(s.cash) > 0 {
			s.res.BlockedCash++
			continue
		}
		promised = promised.Add(cost)
		if !held {
			count++
		}
		s.pending = append(s.pending, order{symbol: sym, kind: fill.BuyLimit, qty: size.Quantity, limit: limit,
			why: picks[sym].why + "; " + size.Reason})
	}
	return nil
}

// initialStop is the highest stop the strategy's stop rules give a holding of
// sym bought at limit on the trading date, or nil if none gives one.
func (s *sim) initialStop(sym string, limit money.Price, inputs []rules.Input) (*money.Price, error) {
	var in rules.Input
	for _, x := range inputs {
		if x.Symbol == sym {
			in = x
		}
	}
	in.Holding = &rules.Holding{Cost: limit, Opened: in.Bars[len(in.Bars)-1].Date}
	var top *money.Price
	for _, r := range s.rules.stop {
		dec, err := r.Stop(in)
		if err != nil {
			return nil, err
		}
		if dec.Stop != nil && (top == nil || dec.Stop.Cmp(*top) > 0) {
			stop := *dec.Stop
			top = &stop
		}
	}
	return top, nil
}

// finish works out the figures.
func (s *sim) finish() {
	r := s.res
	r.HeldAtEnd = len(s.held)
	r.Return = ratio(r.Final.Sub(r.Capital).Decimal(), r.Capital.Decimal())
	r.WinRate = ratio(decimal.NewFromInt(int64(r.Wins)), decimal.NewFromInt(int64(r.Sells)))
	r.AverageValue = money.FromDecimal(ratio(s.sumValue.Decimal(), decimal.NewFromInt(int64(r.Days))))
	traded := r.BoughtValue.Add(r.SoldValue).Decimal().DivRound(decimal.NewFromInt(2), divPlaces)
	r.Turnover = ratio(traded, r.AverageValue.Decimal())
}
