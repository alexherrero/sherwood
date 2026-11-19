package proposal

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/rules"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Config is what a Proposer needs. Every field is required.
type Config struct {
	Store    *store.Store
	Clock    clock.Clock
	Calendar *clock.Calendar
	// Strategy is the strategy file loaded and validated at start-up.
	Strategy *config.Strategy
	// Checker is the risk check, risk.Standard{} outside tests.
	Checker risk.Checker
	// Switch is the stop switch: every order move goes through its
	// OrderError, so an illegal transition halts trading.
	Switch *halt.Switch
	// Notifier says when buys are waiting. Pass notify.Nowhere when
	// notifications are off, never nil.
	Notifier notify.Notifier
	// BrokerHealthy says whether the broker adapter is answering, for the
	// risk check's preview. The check is run again just before sending.
	BrokerHealthy func(context.Context) bool
	Logger        *slog.Logger
}

// Proposer makes orders from signals and expires the ones left unapproved.
type Proposer struct {
	cfg    Config
	margin decimal.Decimal
	policy money.QuantityPolicy
	defs   map[string]strategyRules
}

type strategyRules struct {
	set   rules.Set
	share decimal.Decimal
}

// New checks cfg and parses the strategies' rules.
func New(cfg Config) (*Proposer, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("proposal: no store")
	case cfg.Clock == nil:
		return nil, errors.New("proposal: no clock")
	case cfg.Calendar == nil:
		return nil, errors.New("proposal: no calendar")
	case cfg.Strategy == nil:
		return nil, errors.New("proposal: no strategy file")
	case cfg.Checker == nil:
		return nil, errors.New("proposal: no risk check")
	case cfg.Switch == nil:
		return nil, errors.New("proposal: no stop switch")
	case cfg.Notifier == nil:
		return nil, errors.New("proposal: no notifier; pass notify.Nowhere when notifications are off")
	case cfg.BrokerHealthy == nil:
		return nil, errors.New("proposal: no broker health")
	case cfg.Logger == nil:
		return nil, errors.New("proposal: no logger")
	}
	margin, err := decimal.NewFromString(cfg.Strategy.Limits.BuyLimitMargin.String())
	if err != nil {
		return nil, fmt.Errorf("proposal: buy_limit_margin %q: %w", cfg.Strategy.Limits.BuyLimitMargin, err)
	}
	p := &Proposer{cfg: cfg, margin: margin, policy: money.PolicyFor(cfg.Strategy.Limits.FractionalShares),
		defs: map[string]strategyRules{}}
	for _, def := range cfg.Strategy.Strategies {
		set, err := rules.ParseSet(def)
		if err != nil {
			return nil, err
		}
		share, err := decimal.NewFromString(def.PortfolioShare.String())
		if err != nil {
			return nil, fmt.Errorf("proposal: strategy %q: portfolio_share %q: %w", def.Name, def.PortfolioShare, err)
		}
		p.defs[def.Name] = strategyRules{set: set, share: share}
	}
	return p, nil
}

// Day is one trading date's proposal.
type Day struct {
	// Date is the trading date whose signals become orders; only its Eastern
	// calendar date is read.
	Date time.Time
	// CatchUp is true when the run is for a date whose own day has gone: no
	// buy or add is proposed.
	CatchUp bool
	// Holdings and Cash are what the rules ran with: the holdings and cash
	// in force at the date's first after-close attempt. Cash is nil when no
	// cash reading had been recorded.
	Holdings holdings.Set
	Cash     *holdings.Cash
}

// Result says what a proposal did.
type Result struct {
	// Session is the trading date the orders are for.
	Session time.Time
	// Waiting counts the new orders waiting for approval, and BuysWaiting
	// the buys and adds among them.
	Waiting, BuysWaiting int
	// Protective counts the new protective sells, approved at once.
	Protective int
	// Blocked counts the new orders the risk check refused.
	Blocked int
	// Existing counts signals an earlier run had already made orders for.
	Existing int
	// Skipped says, for each signal that made no order, why.
	Skipped []string
}

// plan is one order to make, before it is created.
type plan struct {
	order orders.New
	risk  risk.Order
}

// Propose turns the date's recorded signals into orders for the next
// session. It is safe to run again for the same date: orders already made
// are found by key and left as they are.
func (p *Proposer) Propose(ctx context.Context, d Day) (Result, error) {
	date := clock.DateOf(d.Date)
	sessionDate, err := p.cfg.Calendar.NextTradingDay(date)
	if err != nil {
		return Result{}, err
	}
	session, err := p.cfg.Calendar.Session(sessionDate)
	if err != nil {
		return Result{}, err
	}
	res := Result{Session: sessionDate}
	log := p.cfg.Logger.With("trading_date", sessionDate.Format(clock.DateLayout))

	in, err := p.read(ctx, date, d)
	if err != nil {
		return res, err
	}
	if len(in.signals) == 0 {
		log.Info("no orders proposed", "reason", "no signals were recorded for "+date.Format(clock.DateLayout))
		return res, nil
	}

	if in.unvaluedWhy != "" {
		log.Warn("portfolio not valued for the risk check", "reason", in.unvaluedWhy)
	}
	now := p.cfg.Clock.Now()
	opened := !now.Before(session.Open)
	healthy := p.cfg.BrokerHealthy(ctx)
	var plans []plan
	for _, s := range in.signals {
		pl, why, err := p.plan(ctx, s, in, sessionDate)
		switch {
		case err != nil:
			return res, err
		case why == "" && s.Kind.Side() == signals.SideBuy && d.CatchUp:
			why = "this run is catching up a date whose day has gone, so it proposes no buy"
		case why == "" && s.Kind != signals.KindStopSell && opened:
			why = "the session of " + sessionDate.Format(clock.DateLayout) + " has already opened"
		}
		if why != "" {
			res.Skipped = append(res.Skipped, s.Symbol+" "+string(s.Kind)+": "+why)
			log.Info("no order for a signal", "strategy", s.Strategy, "symbol", s.Symbol,
				"signal", string(s.Kind), "order_key", s.Key, "reason", why)
			continue
		}
		plans = append(plans, pl)
	}
	// Sells go before buys, protective sells first, as the back-tester
	// works them; otherwise the signals' own order is kept.
	slices.SortStableFunc(plans, func(a, b plan) int { return cmp.Compare(rank(a.order.Kind), rank(b.order.Kind)) })

	var made []orders.Order
	err = orders.Update(ctx, p.cfg.Store, func(tx *sql.Tx) error {
		made = made[:0]
		res.Waiting, res.BuysWaiting, res.Protective, res.Blocked, res.Existing = 0, 0, 0, 0, 0
		sw, err := halt.Active(ctx, tx)
		if err != nil {
			return err
		}
		// The limits in force: the file's, tightened from the phone app.
		lim, err := limits.Effective(ctx, tx, p.cfg.Strategy)
		if err != nil {
			return err
		}
		for _, pl := range plans {
			o, created, err := orders.Create(ctx, tx, pl.order, orders.Proposer, now)
			if err != nil {
				return err
			}
			if !created {
				res.Existing++
				continue
			}
			day, err := orders.ForDate(ctx, tx, sessionDate)
			if err != nil {
				return err
			}
			snap := p.snapshot(now, in, lim, sw, healthy, dayOrders(day, o.ID, in.closes))
			v := p.cfg.Checker.Check(pl.risk, snap)
			ch := orders.Change{Actor: orders.Risk, Verdict: v.String(), At: now}
			switch {
			case !v.Passed():
				ch.To, ch.Reason = orders.Blocked, v.Reason()
				if ch.Reason == "" {
					ch.Reason = "The risk check did not pass the order."
				}
				res.Blocked++
			case o.Protective():
				ch.To, ch.Reason = orders.Approved, "The risk check passed. A protective sell goes without approval."
				res.Protective++
			default:
				ch.To, ch.Reason = orders.AwaitingApproval, "The risk check passed. It waits for your approval."
				res.Waiting++
				if o.Side == signals.SideBuy {
					res.BuysWaiting++
				}
			}
			if o, err = orders.Move(ctx, tx, o.ID, ch); err != nil {
				return err
			}
			made = append(made, o)
		}
		return nil
	})
	if err = p.cfg.Switch.OrderError(ctx, err); err != nil {
		return res, err
	}

	for _, o := range made {
		decision := "allowed"
		if o.State == orders.Blocked {
			decision = "blocked"
		}
		attrs := []any{"strategy", o.Strategy, "symbol", o.Symbol, "signal", string(o.Kind), "side", string(o.Side),
			"order_key", o.Key, "quantity", o.Quantity, "state", string(o.State), "decision", decision}
		if o.Price != nil {
			attrs = append(attrs, "price", *o.Price)
		}
		log.Info("order proposed", attrs...)
	}
	log.Info("orders proposed", "reason", fmt.Sprintf(
		"%d waiting for approval (%d buys), %d protective sells approved, %d blocked, %d already proposed, %d signals made no order",
		res.Waiting, res.BuysWaiting, res.Protective, res.Blocked, res.Existing, len(res.Skipped)))

	if res.BuysWaiting > 0 {
		if err := p.cfg.Notifier.Notify(ctx, notify.BuysWaiting(res.BuysWaiting)); err != nil {
			log.Warn("waiting buys not notified", "reason", err.Error())
		}
	}
	return res, nil
}

// rank orders kinds: protective sells, exits, trims, then buys and adds.
func rank(k signals.Kind) int {
	switch k {
	case signals.KindStopSell:
		return 0
	case signals.KindSell:
		return 1
	case signals.KindTrim:
		return 2
	default:
		return 3
	}
}

// inputs is what a proposal reads besides the orders.
type inputs struct {
	signals     []signals.Signal
	stops       map[string]*money.Price // "strategy\x00symbol" → the stop rules' stop
	closes      map[string]money.Price  // the date's checked closes
	primary     string                  // the date's primary price source
	held        map[string]money.Quantity
	cash        money.Money
	value       money.Money // cash plus holdings at the date's close; zero when not valued
	date        time.Time
	unvaluedWhy string
}

func stopKey(strategy, symbol string) string { return strategy + "\x00" + symbol }

func (p *Proposer) read(ctx context.Context, date time.Time, d Day) (*inputs, error) {
	q := p.cfg.Store.Reader()
	in := &inputs{date: date, stops: map[string]*money.Price{}, closes: map[string]money.Price{},
		held: map[string]money.Quantity{}}
	var err error
	if in.signals, err = signals.Load(ctx, q, date); err != nil {
		return nil, err
	}
	if len(in.signals) == 0 {
		return in, nil
	}
	sugg, err := signals.LoadSuggestions(ctx, q, date)
	if err != nil {
		return nil, err
	}
	for _, s := range sugg {
		in.stops[stopKey(s.Strategy, s.Symbol)] = s.Stop
	}
	day, err := prices.LoadDay(ctx, q, date)
	if err != nil {
		return nil, err
	}
	if !day.Usable {
		return nil, fmt.Errorf("proposal: the prices for %s are not usable: %s", day.DateString(), day.Detail)
	}
	in.primary = day.PrimarySource
	bars, err := prices.UsableBars(ctx, q, date)
	if err != nil {
		return nil, err
	}
	for _, b := range bars {
		if !day.Excluded(b.Symbol) {
			in.closes[b.Symbol] = b.Close
		}
	}

	if d.Cash != nil {
		in.cash = d.Cash.Amount
	}
	total := in.cash
	var unpriced []string
	for _, h := range d.Holdings.Holdings {
		in.held[h.Symbol] = h.Quantity
		c, ok := in.closes[h.Symbol]
		if !ok {
			unpriced = append(unpriced, h.Symbol)
			continue
		}
		total = total.Add(h.Value(c))
	}
	switch {
	case len(unpriced) > 0:
		in.unvaluedWhy = fmt.Sprintf("no agreed close for %v", unpriced)
	case d.Cash == nil:
		in.unvaluedWhy = "no cash reading has been recorded"
	default:
		in.value = total
	}
	return in, nil
}

// plan works out one signal's order. A non-empty why says why the signal
// makes no order.
func (p *Proposer) plan(ctx context.Context, s signals.Signal, in *inputs, session time.Time) (plan, string, error) {
	def, ok := p.defs[s.Strategy]
	if !ok {
		return plan{}, "its strategy is no longer in the strategy file", nil
	}
	pl := plan{
		order: orders.New{Strategy: s.Strategy, Symbol: s.Symbol, Date: session, Kind: s.Kind,
			Reason: s.Reason, StrategyHash: s.StrategyHash},
		risk: risk.Order{Strategy: s.Strategy, Symbol: s.Symbol, Session: session},
	}
	held := in.held[s.Symbol]

	switch s.Kind {
	case signals.KindStopSell:
		stop := in.stops[stopKey(s.Strategy, s.Symbol)]
		if stop == nil {
			// A stop rule only sells at a stop it set, so this is a bug.
			return plan{}, "", fmt.Errorf("proposal: %s's protective sell for %s on %s has no stop recorded",
				s.Strategy, s.Symbol, in.date.Format(clock.DateLayout))
		}
		if held.IsZero() {
			return plan{}, "the stock is not held", nil
		}
		price := *stop
		pl.order.Quantity, pl.order.Price = held, &price
		pl.risk.Kind, pl.risk.Quantity, pl.risk.Price = risk.ProtectiveSell, held, price
		pl.order.Reason = fmt.Sprintf("%s; stop %s for all %s shares", s.Reason, price, held)
		return pl, "", nil

	case signals.KindSell, signals.KindTrim:
		if held.IsZero() {
			return plan{}, "the stock is not held", nil
		}
		q := held
		if s.Kind == signals.KindTrim {
			half, err := p.policy.Round(held.Decimal().Div(decimal.NewFromInt(2)))
			if err != nil {
				return plan{}, "", err
			}
			if half.IsZero() {
				return plan{}, fmt.Sprintf("half of %s shares rounds down to none", held), nil
			}
			q = half
			pl.order.Reason = fmt.Sprintf("%s; sells half the holding, %s of %s shares", s.Reason, q, held)
		} else {
			pl.order.Reason = fmt.Sprintf("%s; sells all %s shares", s.Reason, q)
		}
		pl.order.Quantity = q
		pl.risk.Kind, pl.risk.Quantity = risk.Sell, q
		return pl, "", nil
	}

	// A buy or an add.
	if def.set.Sizing == nil {
		return plan{}, "the strategy has no sizing rule", nil
	}
	closePrice, ok := in.closes[s.Symbol]
	if !ok {
		return plan{}, "the stock has no checked close on the signal's date", nil
	}
	limit, err := money.RoundPrice(closePrice.Decimal().Mul(decimal.NewFromInt(1).Add(p.margin)))
	if err != nil {
		return plan{}, "", err
	}
	from := in.date.AddDate(0, 0, -signals.DefaultHistoryDays)
	stored, err := prices.History(ctx, p.cfg.Store.Reader(), in.primary, s.Symbol, from, in.date)
	if err != nil {
		return plan{}, "", err
	}
	bars := make([]rules.Bar, 0, len(stored))
	for _, b := range stored {
		bars = append(bars, rules.Bar{Date: b.Date, High: b.High, Low: b.Low, Close: b.Close})
	}
	stop, err := def.set.InitialStop(rules.Input{Symbol: s.Symbol, Bars: bars}, limit)
	if err != nil {
		return plan{}, "", err
	}
	capital := money.FromDecimal(in.value.Decimal().Mul(def.share).Truncate(money.CentPlaces))
	size, err := def.set.Sizing.Size(rules.SizeInput{Symbol: s.Symbol, Price: limit, Stop: stop,
		Capital: capital, Policy: p.policy})
	if err != nil {
		if errors.Is(err, rules.ErrNoStop) {
			return plan{}, "its stop rules give no initial stop, which its sizing rule needs", nil
		}
		return plan{}, "", err
	}
	if size.Quantity.IsZero() {
		why := "the sizing rule buys no shares"
		if size.Reason != "" {
			why += ": " + size.Reason
		}
		if in.unvaluedWhy != "" {
			why += " (the portfolio could not be valued: " + in.unvaluedWhy + ")"
		}
		return plan{}, why, nil
	}
	reason := fmt.Sprintf("%s; %s; limit %s is the close of %s plus %s", s.Reason, size.Reason, limit, closePrice,
		percent(p.margin))
	if stop != nil {
		reason += fmt.Sprintf("; initial stop %s", *stop)
	}
	pl.order.Quantity, pl.order.Price, pl.order.Reason = size.Quantity, &limit, reason
	pl.risk.Kind, pl.risk.Quantity, pl.risk.Price = risk.Buy, size.Quantity, limit
	return pl, "", nil
}

// percent writes a fraction as a percentage, such as 0.005 as "0.5%".
func percent(f decimal.Decimal) string {
	return f.Mul(decimal.NewFromInt(100)).String() + "%"
}

// snapshot is the risk check's view at proposal: a preview for the order's
// session, from the holdings, cash and closes the rules ran with.
func (p *Proposer) snapshot(now time.Time, in *inputs, lim config.Limits, sw halt.State, healthy bool,
	day []risk.DayOrder) risk.Snapshot {
	return risk.Snapshot{
		Now:       now,
		Calendar:  p.cfg.Calendar,
		Preview:   true,
		Limits:    lim,
		Sectors:   p.cfg.Strategy.Sectors,
		Watchlist: p.cfg.Strategy.Watchlist,
		Held:      in.held,
		Cash:      in.cash,
		Value:     in.value,
		// The session's previous close is the signal's date's close.
		PreviousCloseValue: in.value,
		LastCloses:         in.closes,
		DayOrders:          day,
		SwitchOff:          !sw.On(),
		BrokerHealthy:      healthy,
	}
}

// dayOrders is the session's other orders that count against its limits, as
// the risk check's DayOrder describes them: approved, sent, filled or
// unknown, leaving out the order being checked. Orders waiting for approval
// do not count until they are approved, so you choose among them, and the
// check before sending sees every order approved by then. A protective stop
// counts only once it has filled.
func dayOrders(list []orders.Order, except int64, closes map[string]money.Price) []risk.DayOrder {
	var out []risk.DayOrder
	for _, o := range list {
		if o.ID == except {
			continue
		}
		switch o.State {
		case orders.Approved, orders.Submitted, orders.Accepted, orders.Unknown:
			if o.Protective() {
				continue
			}
		case orders.PartiallyFilled, orders.Filled:
		default:
			continue
		}
		d := risk.DayOrder{Symbol: o.Symbol, Kind: riskKind(o.Kind)}
		switch {
		case o.Price != nil:
			d.Value = o.Price.Mul(o.Quantity)
		default:
			if c, ok := closes[o.Symbol]; ok {
				d.Value = c.Mul(o.Quantity)
			}
		}
		d.Open = d.Kind == risk.Buy && o.State != orders.Filled
		out = append(out, d)
	}
	return out
}

func riskKind(k signals.Kind) risk.Kind {
	switch k {
	case signals.KindBuy, signals.KindAdd:
		return risk.Buy
	case signals.KindStopSell:
		return risk.ProtectiveSell
	default:
		return risk.Sell
	}
}
