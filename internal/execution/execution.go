package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// DefaultCallTimeout bounds one broker call when Config.CallTimeout is zero.
const DefaultCallTimeout = 30 * time.Second

// Config is what the execution layer needs. Every field but CallTimeout is
// required.
type Config struct {
	Store    *store.Store
	Clock    clock.Clock
	Calendar *clock.Calendar
	Adapter  broker.Adapter
	// Checker is the risk check every order passes before it is sent.
	Checker risk.Checker
	Switch  *halt.Switch
	// Strategy is the loaded strategy file: its sectors, watchlist and each
	// strategy's stocks. Its limits, tightened from the phone app, are read
	// afresh for every send (see limits.Effective).
	Strategy *config.Strategy
	Logger   *slog.Logger
	// CallTimeout bounds each broker call; zero means DefaultCallTimeout.
	CallTimeout time.Duration
}

// Errors.
var (
	// ErrNoChecker is New's refusal to build the layer without a risk check.
	ErrNoChecker = errors.New("execution: no risk check: every order must pass one before it is sent")
	// ErrNotOpen is Send's refusal to run outside its session's market
	// hours.
	ErrNotOpen = errors.New("execution: the market is not open for that session")
	// ErrHalted means a broker call failed or gave an answer that halts
	// trading during the run, so the run stopped.
	ErrHalted = errors.New("execution: trading halted during the send")
)

// Executor sends orders, cancels them when the stop switch is thrown and
// reconciles with the broker. One of those runs at a time: each holds mu for
// its whole run, so a cancel never misses an order a send is placing, and a
// reconciliation never compares while orders are moving.
type Executor struct {
	mu       sync.Mutex
	store    *store.Store
	clock    clock.Clock
	calendar *clock.Calendar
	adapter  broker.Adapter
	checker  risk.Checker
	sw       *halt.Switch
	strategy *config.Strategy
	log      *slog.Logger
	timeout  time.Duration
}

// New builds the execution layer. It refuses a nil risk check, and any
// other missing part.
func New(cfg Config) (*Executor, error) {
	if cfg.Checker == nil {
		return nil, ErrNoChecker
	}
	switch {
	case cfg.Store == nil:
		return nil, errors.New("execution: no store")
	case cfg.Clock == nil:
		return nil, errors.New("execution: no clock")
	case cfg.Calendar == nil:
		return nil, errors.New("execution: no calendar")
	case cfg.Adapter == nil:
		return nil, errors.New("execution: no broker adapter")
	case cfg.Switch == nil:
		return nil, errors.New("execution: no stop switch")
	case cfg.Strategy == nil:
		return nil, errors.New("execution: no strategy file")
	case cfg.Logger == nil:
		return nil, errors.New("execution: no logger")
	}
	timeout := cfg.CallTimeout
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	return &Executor{store: cfg.Store, clock: cfg.Clock, calendar: cfg.Calendar, adapter: cfg.Adapter,
		checker: cfg.Checker, sw: cfg.Switch, strategy: cfg.Strategy, log: cfg.Logger, timeout: timeout}, nil
}

// Result is what one Send did.
type Result struct {
	Session time.Time
	// Sent are the orders the broker accepted, in the order they were sent.
	Sent []orders.Order
	// Blocked are the orders the risk check refused.
	Blocked []orders.Order
	// Held are approved buys and sells the stop switch kept back.
	Held []orders.Order
	// Called are resting stops called off, and approved stops set aside
	// because another order became the holding's one stop.
	Called []orders.Order
}

// Send works the session's approved orders; see the package comment. It
// returns ErrNotOpen outside the session's market hours, and an error
// wrapping ErrHalted when a broker call halted trading; the Result says what
// was done before that.
func (e *Executor) Send(ctx context.Context, session time.Time) (*Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session = clock.DateOf(session)
	sess, err := e.calendar.Session(session)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	if now := e.clock.Now(); now.Before(sess.Open) || !now.Before(sess.Close) {
		return nil, fmt.Errorf("%w: %s", ErrNotOpen, session.Format(clock.DateLayout))
	}
	r, err := e.load(ctx, session)
	if err != nil {
		return nil, err
	}
	res := &Result{Session: session}
	r.res = res
	if err := r.work(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// run is one Send's working state.
type run struct {
	e       *Executor
	session time.Time
	prev    time.Time
	res     *Result
	snap    risk.Snapshot
	closes  map[string]money.Price
	// held is the shares held, less the session's sells sent so far.
	held map[string]money.Quantity
	// sells, stops and buys are the session's approved orders.
	sells, stops, buys []orders.Order
	// resting are each stock's stops resting at the broker.
	resting map[string][]orders.Order
	// suggested is the highest stop the strategies' stop rules gave each
	// holding at the previous close, and the strategy that gave it.
	suggested map[string]suggestion
	// noStops is set while you have cancelled the protective stops from
	// the phone app: no stop is placed or raised until the switch is reset.
	noStops bool
	// strategy is the strategy file with the watchlist in force applied,
	// so a strategy that opts in to the watchlist covers its added stocks.
	strategy *config.Strategy
}

type suggestion struct {
	price    money.Price
	strategy string
}

// load reads everything the run needs.
func (e *Executor) load(ctx context.Context, session time.Time) (*run, error) {
	q := e.store.Reader()
	prev, err := e.calendar.PreviousTradingDay(session)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	r := &run{e: e, session: session, prev: prev, held: map[string]money.Quantity{},
		resting: map[string][]orders.Order{}, suggested: map[string]suggestion{}}

	set, err := holdings.Current(ctx, q)
	if err != nil && !errors.Is(err, holdings.ErrNone) {
		return nil, fmt.Errorf("execution: %w", err)
	}
	for _, h := range set.Holdings {
		r.held[h.Symbol] = h.Quantity
	}
	var cash money.Money
	switch c, err := holdings.LatestCash(ctx, q); {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return nil, fmt.Errorf("execution: %w", err)
	default:
		cash = c.Amount
	}
	if r.closes, err = prices.LatestCloses(ctx, q, prev); err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	previous := cash
	for _, h := range set.Holdings {
		c, ok := r.closes[h.Symbol]
		if !ok {
			// Without every close the previous value is unknown; zero makes
			// the daily loss check refuse, which fails closed.
			previous = money.Money{}
			break
		}
		previous = previous.Add(h.Value(c))
	}

	day, err := orders.ForDate(ctx, q, session)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	var dayOrders []risk.DayOrder
	for _, o := range day {
		if o.State == orders.Approved {
			switch o.Type {
			case orders.SellAtOpen:
				r.sells = append(r.sells, o)
			case orders.Stop:
				r.stops = append(r.stops, o)
			default:
				r.buys = append(r.buys, o)
			}
			continue
		}
		counts, err := r.counts(ctx, q, o)
		if err != nil {
			return nil, err
		}
		if counts {
			dayOrders = append(dayOrders, r.dayOrder(o))
		}
	}
	open, err := orders.InState(ctx, q, orders.Accepted, orders.PartiallyFilled)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	for _, o := range open {
		if o.Type == orders.Stop {
			r.resting[o.Symbol] = append(r.resting[o.Symbol], o)
		}
	}
	sugg, err := signals.LoadSuggestions(ctx, q, prev)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	for _, s := range sugg {
		if s.Stop == nil {
			continue
		}
		if cur, ok := r.suggested[s.Symbol]; !ok || s.Stop.Cmp(cur.price) > 0 {
			r.suggested[s.Symbol] = suggestion{*s.Stop, s.Strategy}
		}
	}

	acct, err := call(ctx, e.timeout, e.adapter.Account)
	if err != nil {
		return nil, e.halt(ctx, halt.BrokerError, "Reading the account before the 09:35 send failed: "+err.Error(), err)
	}
	healthy := true
	if h, ok := e.adapter.(broker.Health); ok {
		healthy = h.Healthy()
	}
	lim, err := limits.Effective(ctx, q, e.strategy)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	// The watchlist in force: the file's, plus the phone's additions.
	watched, err := limits.Watchlist(ctx, q, e.strategy)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	r.strategy = e.strategy.WithWatchlist(watched)
	r.snap = risk.Snapshot{
		Calendar:           e.calendar,
		Limits:             lim,
		Sectors:            e.strategy.Sectors,
		Watchlist:          watched,
		Cash:               cash,
		Value:              acct.Value,
		PreviousCloseValue: previous,
		LastCloses:         r.closes,
		DayOrders:          dayOrders,
		BrokerHealthy:      healthy,
	}
	return r, nil
}

// counts reports whether a session order that is not part of this run counts
// against the day's limits (see risk.DayOrder).
func (r *run) counts(ctx context.Context, q orders.Querier, o orders.Order) (bool, error) {
	switch o.State {
	case orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Filled, orders.Unknown,
		orders.Cancelled, orders.Expired:
	default:
		return false, nil
	}
	mustHaveFilled := o.Protective() || o.State == orders.Cancelled || o.State == orders.Expired
	if !mustHaveFilled {
		return true, nil
	}
	fills, err := orders.Fills(ctx, q, o.ID)
	if err != nil {
		return false, fmt.Errorf("execution: %w", err)
	}
	return len(fills) > 0, nil
}

func riskKind(o orders.Order) risk.Kind {
	switch o.Type {
	case orders.DayLimitBuy:
		return risk.Buy
	case orders.Stop:
		return risk.ProtectiveSell
	default:
		return risk.Sell
	}
}

// dayOrder is o as one of the session's other orders.
func (r *run) dayOrder(o orders.Order) risk.DayOrder {
	d := risk.DayOrder{Symbol: o.Symbol, Kind: riskKind(o)}
	switch {
	case o.Price != nil:
		d.Value = o.Price.Mul(o.Quantity)
	default:
		if c, ok := r.closes[o.Symbol]; ok {
			d.Value = c.Mul(o.Quantity)
		}
	}
	if d.Kind == risk.Buy {
		switch o.State {
		case orders.Approved, orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Unknown:
			d.Open = true
		}
	}
	return d
}

// work runs the three steps.
func (r *run) work(ctx context.Context) error {
	on, err := r.switchOn(ctx)
	if err != nil {
		return err
	}
	if on {
		r.res.Held = append(r.res.Held, r.sells...)
	} else {
		for _, o := range r.sells {
			if err := r.sell(ctx, o); err != nil {
				return err
			}
		}
	}
	if r.noStops {
		r.e.log.Warn("protective stops not placed", "halt_cause", string(halt.StopsCancelled),
			"reason", "you cancelled the protective stops; none is placed until the stop switch is reset")
	} else if err := r.protect(ctx); err != nil {
		return err
	}
	for _, o := range r.buys {
		// The switch may be thrown from the phone while the run works.
		if on, err = r.switchOn(ctx); err != nil {
			return err
		}
		if on {
			r.res.Held = append(r.res.Held, o)
			continue
		}
		if err := r.buy(ctx, o); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) switchOn(ctx context.Context) (bool, error) {
	st, err := halt.Active(ctx, r.e.store.Reader())
	if err != nil {
		return false, fmt.Errorf("execution: %w", err)
	}
	r.snap.SwitchOff = !st.On()
	r.noStops = st.Has(halt.StopsCancelled)
	return st.On(), nil
}

// check runs the risk check on o now.
func (r *run) check(o orders.Order, qty money.Quantity) risk.Verdict {
	ro := risk.Order{Strategy: o.Strategy, Symbol: o.Symbol, Kind: riskKind(o), Quantity: qty, Session: o.Date}
	if o.Price != nil {
		ro.Price = *o.Price
	}
	snap := r.snap
	snap.Now = r.e.clock.Now()
	snap.Held = maps(r.held)
	snap.DayOrders = slices.Clone(r.snap.DayOrders)
	return r.e.checker.Check(ro, snap)
}

func maps(m map[string]money.Quantity) map[string]money.Quantity {
	out := make(map[string]money.Quantity, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// block moves a refused order to blocked.
func (r *run) block(ctx context.Context, o orders.Order, v risk.Verdict) error {
	o, err := r.e.move(ctx, o, orders.Change{To: orders.Blocked, Actor: orders.Risk,
		Reason: "Refused just before sending: " + v.Reason(), Verdict: v.String()})
	if err != nil {
		return err
	}
	r.e.log.Warn("order blocked by the risk check", "order_key", o.Key, "symbol", o.Symbol,
		"side", string(o.Side), "decision", "blocked", "reason", v.Reason())
	r.res.Blocked = append(r.res.Blocked, o)
	return nil
}

// sell sends one ordinary sell, resizing the holding's stop first.
func (r *run) sell(ctx context.Context, o orders.Order) error {
	v := r.check(o, o.Quantity)
	if !v.Passed() {
		return r.block(ctx, o, v)
	}
	left, err := r.held[o.Symbol].Sub(o.Quantity)
	if err != nil {
		return fmt.Errorf("execution: order %d: %w", o.ID, err) // the shares-held check passed, so this cannot happen
	}
	r.held[o.Symbol] = left
	if err := r.restop(ctx, o.Symbol); err != nil {
		return err
	}
	sent, err := r.e.send(ctx, o, v)
	if err != nil {
		return err
	}
	r.snap.DayOrders = append(r.snap.DayOrders, r.dayOrder(sent))
	r.res.Sent = append(r.res.Sent, sent)
	return nil
}

// buy sends one buy.
func (r *run) buy(ctx context.Context, o orders.Order) error {
	v := r.check(o, o.Quantity)
	if !v.Passed() {
		return r.block(ctx, o, v)
	}
	sent, err := r.e.send(ctx, o, v)
	if err != nil {
		return err
	}
	r.snap.DayOrders = append(r.snap.DayOrders, r.dayOrder(sent))
	r.res.Sent = append(r.res.Sent, sent)
	return nil
}

// protect brings every holding's resting stop up to date, and sends or sets
// aside the session's approved protective sells.
func (r *run) protect(ctx context.Context) error {
	var symbols []string
	for s, q := range r.held {
		if !q.IsZero() {
			symbols = append(symbols, s)
		}
	}
	for s := range r.resting {
		symbols = append(symbols, s)
	}
	for _, o := range r.stops {
		symbols = append(symbols, o.Symbol)
	}
	slices.Sort(symbols)
	for _, s := range slices.Compact(symbols) {
		if err := r.restop(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// restop makes the stock's resting stop cover exactly the shares held now,
// at the highest stop known, with one stop order.
func (r *run) restop(ctx context.Context, sym string) error {
	held := r.held[sym]
	rest := r.resting[sym]
	var cands, others []orders.Order
	for _, o := range r.stops {
		if o.Symbol == sym {
			cands = append(cands, o)
		} else {
			others = append(others, o)
		}
	}
	r.stops = others // each approved stop is handled once

	var target *money.Price
	raise := func(p *money.Price) {
		if p != nil && (target == nil || p.Cmp(*target) > 0) {
			v := *p
			target = &v
		}
	}
	covered := money.Quantity{}
	for _, o := range rest {
		raise(o.Price)
		covered = covered.Add(o.Quantity)
	}
	for _, o := range cands {
		raise(o.Price)
	}
	if s, ok := r.suggested[sym]; ok {
		raise(&s.price)
	}
	if target == nil {
		return nil // nothing rests and no stop is known
	}
	if len(cands) == 0 && len(rest) == 1 && rest[0].Quantity.Equal(held) && rest[0].Price.Equal(*target) {
		return nil // already right
	}

	var carrier *orders.Order
	if !held.IsZero() {
		c, err := r.carrier(ctx, sym, held, *target, rest, cands)
		if err != nil {
			return err
		}
		carrier = c
	}
	var v risk.Verdict
	if carrier != nil {
		v = r.check(*carrier, carrier.Quantity)
		if !v.Passed() {
			if err := r.block(ctx, *carrier, v); err != nil {
				return err
			}
			carrier = nil
		}
	}
	// Set aside the approved stops that are not the carrier.
	for _, o := range cands {
		if carrier != nil && o.ID == carrier.ID {
			continue
		}
		why := "Set aside: nothing is left to protect after today's sells."
		if carrier != nil {
			why = fmt.Sprintf("Set aside: order %d is now the one stop for this holding.", carrier.ID)
		} else if !held.IsZero() {
			why = "Set aside: no stop could be sized to the shares held."
		}
		o, err := r.e.move(ctx, o, orders.Change{To: orders.Cancelled, Actor: orders.Execution, Reason: why})
		if err != nil {
			return err
		}
		r.res.Called = append(r.res.Called, o)
	}
	if carrier == nil {
		// Keep what rests if it covers no more than is held; otherwise call
		// it off, because a stop must never cover shares that are not held.
		if covered.Cmp(held) <= 0 {
			if !held.IsZero() {
				r.e.log.Warn("resting stop not resized", "symbol", sym, "quantity", covered,
					"reason", "no stop order could be made for the shares held, so the one resting stays")
			}
			return nil
		}
		return r.callOff(ctx, sym, rest, "Called off: it covered more shares than will be held.")
	}
	if err := r.callOff(ctx, sym, rest, fmt.Sprintf("Replaced by order %d.", carrier.ID)); err != nil {
		return err
	}
	sent, err := r.e.send(ctx, *carrier, v)
	if err != nil {
		return err
	}
	r.resting[sym] = []orders.Order{sent}
	r.res.Sent = append(r.res.Sent, sent)
	return nil
}

// callOff cancels each resting stop at the broker, one call each.
func (r *run) callOff(ctx context.Context, sym string, rest []orders.Order, why string) error {
	for _, o := range rest {
		o, err := r.e.cancel(ctx, o, orders.Broker, why)
		if err != nil {
			return err
		}
		r.res.Called = append(r.res.Called, o)
	}
	delete(r.resting, sym)
	return nil
}

// carrier finds or makes the approved stop order that will be the holding's
// one stop: an approved stop for the session with exactly the shares and
// price, or a new one. A new order needs its own key (strategy, stock and
// session), so it takes the first strategy, of those that set or own a stop
// on the stock, whose key is free. It returns nil when no key is free.
func (r *run) carrier(ctx context.Context, sym string, held money.Quantity, target money.Price,
	rest, cands []orders.Order) (*orders.Order, error) {
	for _, o := range cands {
		if o.Quantity.Equal(held) && o.Price.Equal(target) {
			return &o, nil
		}
	}
	var names []string
	for _, o := range rest {
		names = append(names, o.Strategy)
	}
	for _, o := range cands {
		names = append(names, o.Strategy)
	}
	if s, ok := r.suggested[sym]; ok {
		names = append(names, s.strategy)
	}
	for _, def := range r.strategy.Strategies {
		if slices.Contains(def.Stocks, sym) {
			names = append(names, def.Name)
		}
	}
	tried := map[string]bool{}
	for _, name := range names {
		if name == "" || tried[name] {
			continue
		}
		tried[name] = true
		var made orders.Order
		var created bool
		err := orders.Update(ctx, r.e.store, func(tx *sql.Tx) error {
			now := r.e.clock.Now()
			o, ok, err := orders.Create(ctx, tx, orders.New{
				Strategy: name, Symbol: sym, Date: r.session, Kind: signals.KindStopSell,
				Quantity: held, Price: &target, StrategyHash: r.e.strategy.Hash,
				Reason: fmt.Sprintf("Resting stop for all %s shares held, at %s.", held, target),
			}, orders.Execution, now)
			if err != nil || !ok {
				made, created = o, ok
				return err
			}
			made, err = orders.Move(ctx, tx, o.ID, orders.Change{To: orders.Approved, Actor: orders.Execution,
				Reason: "A protective stop goes without approval.", At: now})
			created = true
			return err
		})
		if err = r.e.sw.OrderError(ctx, err); err != nil {
			return nil, err
		}
		if created {
			return &made, nil
		}
	}
	r.e.log.Warn("no stop order could be made", "symbol", sym, "reason",
		"every strategy's stop order for this stock and session is already taken")
	return nil, nil
}

// call runs one broker call under the time limit.
func call[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(cctx)
}

// move makes one change in its own transaction, halting on an illegal one.
func (e *Executor) move(ctx context.Context, o orders.Order, c orders.Change) (orders.Order, error) {
	c.At = e.clock.Now()
	var out orders.Order
	err := orders.Update(ctx, e.store, func(tx *sql.Tx) error {
		var err error
		out, err = orders.Move(ctx, tx, o.ID, c)
		return err
	})
	if err = e.sw.OrderError(ctx, err); err != nil {
		return orders.Order{}, err
	}
	e.log.Info("order moved", "order_key", out.Key, "symbol", out.Symbol, "from_state", string(o.State),
		"state", string(out.State), "reason", c.Reason)
	return out, nil
}

// halt throws a halt and returns an error wrapping ErrHalted and cause.
func (e *Executor) halt(ctx context.Context, c halt.Cause, evidence string, cause error) error {
	_, _, err := e.sw.Throw(ctx, c, evidence)
	return errors.Join(fmt.Errorf("%w: %s", ErrHalted, evidence), cause, err)
}

// unknown moves a sent order whose outcome is not known to unknown, in its
// own transaction, and then halts trading.
func (e *Executor) unknown(ctx context.Context, o orders.Order, evidence string, cause error) error {
	ctx = context.WithoutCancel(ctx) // a cancelled run still records what happened
	if _, err := e.move(ctx, o, orders.Change{To: orders.Unknown, Actor: orders.Execution,
		Reason: "The broker's answer is not known; reconciliation looks it up. " + evidence}); err != nil {
		cause = errors.Join(cause, err)
	}
	return e.halt(ctx, halt.UnknownOrder, evidence, cause)
}

// describe names an order for evidence.
func describe(o orders.Order) string {
	return fmt.Sprintf("Order %d (%s %s %s shares of %s for %s)", o.ID, o.Strategy, o.Kind, o.Quantity, o.Symbol,
		clock.DateOf(o.Date).Format(clock.DateLayout))
}

// send moves an approved order to submitted, calls the broker exactly once,
// and records the answer.
func (e *Executor) send(ctx context.Context, o orders.Order, v risk.Verdict) (orders.Order, error) {
	o, err := e.move(ctx, o, orders.Change{To: orders.Submitted, Actor: orders.Execution,
		Reason: "Passed the risk check and sent to the broker.", Verdict: v.String()})
	if err != nil {
		return orders.Order{}, err
	}
	bo := broker.Order{Ref: o.Key, Symbol: o.Symbol, Side: broker.Side(o.Side), Type: broker.Type(o.Type),
		Quantity: o.Quantity, Price: o.Price, Session: o.Date}
	rep, err := call(ctx, e.timeout, func(ctx context.Context) (broker.Report, error) { return e.adapter.Place(ctx, bo) })
	switch {
	case err != nil:
		return orders.Order{}, e.unknown(ctx, o, describe(o)+" was sent, but the broker call failed: "+err.Error(), err)
	case rep.Ref != o.Key:
		return orders.Order{}, e.unknown(ctx, o, describe(o)+" was sent, but the broker answered about another order.", nil)
	}
	switch rep.Status {
	case broker.StatusAccepted:
		return e.move(ctx, o, orders.Change{To: orders.Accepted, Actor: orders.Broker, Reason: "The broker accepted it."})
	case broker.StatusRejected:
		why := "The broker refused it: " + orDefault(rep.Detail, "no reason given") + "."
		if _, err := e.move(ctx, o, orders.Change{To: orders.Rejected, Actor: orders.Broker, Reason: why}); err != nil {
			return orders.Order{}, err
		}
		return orders.Order{}, e.halt(ctx, halt.BrokerError, describe(o)+" was refused by the broker: "+
			orDefault(rep.Detail, "no reason given")+".", nil)
	default:
		return orders.Order{}, e.unknown(ctx, o, fmt.Sprintf("%s was sent, but the broker answered %q.",
			describe(o), rep.Status), nil)
	}
}

// cancel calls off one resting order at the broker, exactly once, and records
// the answer, with actor as the actor of the cancellation.
func (e *Executor) cancel(ctx context.Context, o orders.Order, actor orders.Actor, why string) (orders.Order, error) {
	rep, err := call(ctx, e.timeout, func(ctx context.Context) (broker.Report, error) { return e.adapter.Cancel(ctx, o.Key) })
	switch {
	case err != nil:
		return orders.Order{}, e.unknown(ctx, o, describe(o)+" was being called off, but the broker call failed: "+err.Error(), err)
	case rep.Ref != o.Key || rep.Status != broker.StatusCancelled:
		return orders.Order{}, e.unknown(ctx, o, fmt.Sprintf("%s was being called off, but the broker answered %q.",
			describe(o), rep.Status), nil)
	}
	return e.move(ctx, o, orders.Change{To: orders.Cancelled, Actor: actor, Reason: why})
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
