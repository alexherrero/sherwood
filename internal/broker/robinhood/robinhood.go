// Package robinhood is the live broker.Adapter: it passes each call to the
// broker helper over its socket, with typed arguments and strictly checked
// answers. It never talks to the broker itself, keeps no state about orders
// beyond what it is asked, and never retries a call. Only live mode builds it,
// and only once the setting, the in-app confirmation and the minimum paper
// period all allow it (live-trading task 4).
//
// Every error it returns means the outcome is unknown: the call failed, the
// helper answered with an error code, or the answer was not a valid one. The
// caller halts trading and never retries. A refusal by the broker is a Report
// with broker.StatusRejected.
package robinhood

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/money"
)

// The helper operations the adapter calls. Ping is the supervisor's.
const (
	opPlace      = "place"
	opCancel     = "cancel"
	opLookup     = "lookup"
	opOpenOrders = "open_orders"
	opHoldings   = "holdings"
	opCash       = "cash"
	opAccount    = "account"
)

// Each call's time limit. The order calls page through the account's whole
// order history (place makes one read and one send), so they get far longer
// than the client's usual ten seconds; holdings reads every page of positions
// and may read each stock's record, as the holdings import does. A caller's
// earlier deadline still wins.
const (
	OrderCallTimeout    = 60 * time.Second
	HoldingsCallTimeout = 120 * time.Second
	AccountCallTimeout  = 30 * time.Second
)

// ForeignPrefix starts the Ref of an open order that no key sherwood holds
// gives: one placed in the broker's own app, or by anything else. It is
// followed by the broker's own order id, so the order can be found, and it
// can never equal one of sherwood's keys.
const ForeignPrefix = "foreign-order:"

// ErrBadReply means the helper's answer did not fit the adapter's contract:
// a report about another order, an unknown status, an amount that is not a
// decimal. The outcome is unknown, as for any broker error.
var ErrBadReply = errors.New("robinhood: the helper's answer was not valid")

// Caller is the helper client the adapter calls through: *broker.Client.
type Caller interface {
	CallFor(ctx context.Context, op string, timeout time.Duration, args, result any) error
}

// Keys lists the idempotency keys of every order sherwood may have at the
// broker. The helper reports an open order only by its one-way client
// reference, so the adapter computes ClientRef for each key to put the key
// back. OrderKeys reads them from the orders table.
type Keys func(ctx context.Context) ([]string, error)

// Config is what the adapter needs. Every field is required.
type Config struct {
	// Client calls the helper; the supervisor's client.
	Client Caller
	// Helper is the helper's health: the supervisor.
	Helper broker.Health
	// Login hears every call's outcome, so a login_needed streak sends one
	// alert.
	Login *broker.LoginAlert
	// Keys resolves open orders' client references to keys.
	Keys Keys
	// Logger records an alert that could not be sent.
	Logger *slog.Logger
}

// Adapter is the live broker adapter.
type Adapter struct {
	client Caller
	helper broker.Health
	login  *broker.LoginAlert
	keys   Keys
	log    *slog.Logger
	// lapsed is set while the helper answers login_needed, until a call
	// works again.
	lapsed atomic.Bool
}

var (
	_ broker.Adapter    = (*Adapter)(nil)
	_ broker.Health     = (*Adapter)(nil)
	_ broker.CashReader = (*Adapter)(nil)
)

// New builds the adapter, refusing a missing part.
func New(cfg Config) (*Adapter, error) {
	switch {
	case cfg.Client == nil:
		return nil, errors.New("robinhood: no helper client")
	case cfg.Helper == nil:
		return nil, errors.New("robinhood: no helper health")
	case cfg.Login == nil:
		return nil, errors.New("robinhood: no login alert")
	case cfg.Keys == nil:
		return nil, errors.New("robinhood: no order keys")
	case cfg.Logger == nil:
		return nil, errors.New("robinhood: no logger")
	}
	return &Adapter{client: cfg.Client, helper: cfg.Helper, login: cfg.Login, keys: cfg.Keys, log: cfg.Logger}, nil
}

// Healthy reports whether the helper is running and ready, and the broker
// session has not lapsed: a login_needed answer makes the adapter unhealthy
// until a call works again.
func (a *Adapter) Healthy() bool { return a.helper.Healthy() && !a.lapsed.Load() }

// call makes one helper call and passes its outcome to the login alert. It
// never retries.
func (a *Adapter) call(ctx context.Context, op string, timeout time.Duration, args any) (json.RawMessage, error) {
	var raw json.RawMessage
	err := a.client.CallFor(ctx, op, timeout, args, &raw)
	switch {
	case err == nil:
		a.lapsed.Store(false)
	case broker.LoginNeeded(err):
		a.lapsed.Store(true)
	}
	if _, aerr := a.login.Seen(ctx, err); aerr != nil {
		a.log.Error("the broker login alert could not be sent", "op", op, "error", aerr.Error())
	}
	if err != nil {
		return nil, fmt.Errorf("robinhood: %s: %w", op, err)
	}
	return raw, nil
}

// decode reads a helper result strictly: every field known, nothing after it.
func decode(op string, raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrBadReply, op, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: %s: more than one value", ErrBadReply, op)
	}
	return nil
}

func badReply(op, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrBadReply, op, fmt.Sprintf(format, args...))
}

// placeArgs is the helper's place request. Every amount is a decimal string.
type placeArgs struct {
	Ref      string  `json:"ref"`
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"`
	Type     string  `json:"type"`
	Quantity string  `json:"quantity"`
	Price    *string `json:"price,omitempty"`
}

type refArgs struct {
	Ref string `json:"ref"`
}

// wireReport is the helper's order report.
type wireReport struct {
	Ref       *string `json:"ref"`
	ClientRef *string `json:"client_ref"`
	BrokerID  *string `json:"broker_id"`
	Status    *string `json:"status"`
	Filled    *string `json:"filled"`
	FillPrice *string `json:"fill_price"`
	Detail    *string `json:"detail"`
}

// Place sends the order once. A malformed order is refused here, as the
// broker would refuse it, without calling the helper.
func (a *Adapter) Place(ctx context.Context, o broker.Order) (broker.Report, error) {
	if err := o.Check(); err != nil {
		// A refusal is the broker's answer, not a failed call.
		return broker.Report{Ref: o.Ref, Status: broker.StatusRejected, Detail: err.Error()}, nil //nolint:nilerr // see above
	}
	args := placeArgs{Ref: o.Ref, Symbol: o.Symbol, Side: string(o.Side), Type: string(o.Type), Quantity: o.Quantity.String()}
	if o.Price != nil {
		p := o.Price.String()
		args.Price = &p
	}
	raw, err := a.call(ctx, opPlace, OrderCallTimeout, args)
	if err != nil {
		return broker.Report{}, err
	}
	return orderReport(opPlace, raw, o.Ref)
}

// Cancel asks the helper to call the order off; the helper sends nothing for
// an order that has already finished and answers its status.
func (a *Adapter) Cancel(ctx context.Context, ref string) (broker.Report, error) {
	raw, err := a.call(ctx, opCancel, OrderCallTimeout, refArgs{Ref: ref})
	if err != nil {
		return broker.Report{}, err
	}
	return orderReport(opCancel, raw, ref)
}

// Lookup reads one order by its key. An order the broker does not list is an
// error (the helper's order_not_found).
func (a *Adapter) Lookup(ctx context.Context, ref string) (broker.Report, error) {
	raw, err := a.call(ctx, opLookup, OrderCallTimeout, refArgs{Ref: ref})
	if err != nil {
		return broker.Report{}, err
	}
	return orderReport(opLookup, raw, ref)
}

// orderReport reads the report about the order with key ref, checking it is
// about that order.
func orderReport(op string, raw json.RawMessage, ref string) (broker.Report, error) {
	var w wireReport
	if err := decode(op, raw, &w); err != nil {
		return broker.Report{}, err
	}
	switch {
	case w.Ref == nil || *w.Ref != ref:
		return broker.Report{}, badReply(op, "the report is about another order")
	case w.ClientRef == nil || *w.ClientRef != ClientRef(ref):
		return broker.Report{}, badReply(op, "the report carries another client reference")
	}
	r, err := report(op, w)
	if err != nil {
		return broker.Report{}, err
	}
	r.Ref = ref
	return r, nil
}

// report reads the fields every order report shares.
func report(op string, w wireReport) (broker.Report, error) {
	var r broker.Report
	if w.Status == nil || w.Filled == nil || w.Detail == nil {
		return r, badReply(op, "the report lacks its status, filled shares or detail")
	}
	r.Status = broker.Status(*w.Status)
	if !r.Status.Valid() {
		return r, badReply(op, "unknown status %q", *w.Status)
	}
	// Only an order refused before it was sent has no broker id.
	if w.BrokerID == nil && r.Status != broker.StatusRejected {
		return r, badReply(op, "a %s order has no broker id", r.Status)
	}
	filled, err := money.ParseQuantity(*w.Filled)
	if err != nil {
		return r, badReply(op, "filled: %v", err)
	}
	r.Filled = filled
	if w.FillPrice != nil {
		if filled.IsZero() {
			return r, badReply(op, "a fill price with no shares filled")
		}
		p, err := fillPrice(*w.FillPrice)
		if err != nil {
			return r, badReply(op, "fill_price: %v", err)
		}
		r.FillPrice = &p
	}
	r.Detail = *w.Detail
	return r, nil
}

// fillPrice reads the broker's average fill price. An average over several
// fills can run finer than a cent; it is rounded to the cent, half away from
// zero, because sherwood keeps every price in whole cents.
func fillPrice(s string) (money.Price, error) {
	p, err := money.ParsePrice(s)
	if err == nil || !errors.Is(err, money.ErrPrecision) {
		if err == nil && p.IsZero() {
			return p, errors.New("a fill price of zero")
		}
		return p, err
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return money.Price{}, err
	}
	p, err = money.RoundPrice(d)
	if err == nil && p.IsZero() {
		return p, errors.New("a fill price that rounds to zero")
	}
	return p, err
}

type openOrdersReply struct {
	AsOf   string       `json:"as_of"`
	Orders []wireReport `json:"orders"`
}

// OpenOrders lists the orders still open at the broker. The helper reports
// each by its client reference only; the adapter puts back the key whose
// ClientRef it is, from Keys. An order no key gives keeps a Ref of
// ForeignPrefix and the broker's order id, so reconciliation names it as an
// order sherwood does not have. Two open orders with one key's reference
// would be a double send, and are an error.
func (a *Adapter) OpenOrders(ctx context.Context) ([]broker.Report, error) {
	raw, err := a.call(ctx, opOpenOrders, OrderCallTimeout, nil)
	if err != nil {
		return nil, err
	}
	var w openOrdersReply
	if err := decode(opOpenOrders, raw, &w); err != nil {
		return nil, err
	}
	if w.Orders == nil {
		return nil, badReply(opOpenOrders, "no order list")
	}
	keys, err := a.keys(ctx)
	if err != nil {
		return nil, fmt.Errorf("robinhood: open_orders: reading sherwood's order keys: %w", err)
	}
	byRef := make(map[string]string, len(keys))
	for _, k := range keys {
		byRef[ClientRef(k)] = k
	}
	out := make([]broker.Report, 0, len(w.Orders))
	seen := map[string]bool{}
	for _, o := range w.Orders {
		if o.Ref != nil {
			return nil, badReply(opOpenOrders, "an open order came with a key")
		}
		r, err := report(opOpenOrders, o)
		if err != nil {
			return nil, err
		}
		if r.Status != broker.StatusAccepted && r.Status != broker.StatusPartiallyFilled {
			return nil, badReply(opOpenOrders, "an open order is %s", r.Status)
		}
		if o.BrokerID == nil {
			return nil, badReply(opOpenOrders, "an open order has no broker id")
		}
		key, ours := "", false
		if o.ClientRef != nil {
			key, ours = byRef[*o.ClientRef]
		}
		if !ours {
			r.Ref = ForeignPrefix + *o.BrokerID
			out = append(out, r)
			continue
		}
		if seen[key] {
			return nil, badReply(opOpenOrders, "two open orders carry the reference of order %s: it may have been sent twice", key)
		}
		seen[key] = true
		r.Ref = key
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

type holdingsReply struct {
	AsOf     string `json:"as_of"`
	Holdings []struct {
		Symbol      string `json:"symbol"`
		Quantity    string `json:"quantity"`
		AverageCost string `json:"average_cost"`
		OpenedOn    string `json:"opened_on"`
	} `json:"holdings"`
}

// Holdings lists the shares held, in the helper's order (by symbol).
func (a *Adapter) Holdings(ctx context.Context) ([]broker.Position, error) {
	raw, err := a.call(ctx, opHoldings, HoldingsCallTimeout, nil)
	if err != nil {
		return nil, err
	}
	var w holdingsReply
	if err := decode(opHoldings, raw, &w); err != nil {
		return nil, err
	}
	if w.Holdings == nil {
		return nil, badReply(opHoldings, "no holdings list")
	}
	out := make([]broker.Position, 0, len(w.Holdings))
	seen := map[string]bool{}
	for _, h := range w.Holdings {
		if h.Symbol == "" || strings.TrimSpace(h.Symbol) != h.Symbol || seen[h.Symbol] {
			return nil, badReply(opHoldings, "a holding has no symbol, or shares one")
		}
		seen[h.Symbol] = true
		q, err := money.ParseQuantity(h.Quantity)
		if err != nil || q.IsZero() {
			return nil, badReply(opHoldings, "%s: the share count is not a positive decimal", h.Symbol)
		}
		c, err := money.ParseMoney(h.AverageCost)
		if err != nil || c.IsNegative() {
			return nil, badReply(opHoldings, "%s: the average cost is not a decimal of zero or more", h.Symbol)
		}
		out = append(out, broker.Position{Symbol: h.Symbol, Quantity: q, AverageCost: c})
	}
	return out, nil
}

type cashReply struct {
	AsOf        string `json:"as_of"`
	Cash        string `json:"cash"`
	BuyingPower string `json:"buying_power"`
}

// Cash reads the broker's cash figure, which may be negative.
func (a *Adapter) Cash(ctx context.Context) (money.Money, error) {
	raw, err := a.call(ctx, opCash, AccountCallTimeout, nil)
	if err != nil {
		return money.Money{}, err
	}
	var w cashReply
	if err := decode(opCash, raw, &w); err != nil {
		return money.Money{}, err
	}
	c, err := money.ParseMoney(w.Cash)
	if err != nil {
		return money.Money{}, badReply(opCash, "cash: %v", err)
	}
	return c, nil
}

type accountReply struct {
	AsOf        string `json:"as_of"`
	Cash        string `json:"cash"`
	BuyingPower string `json:"buying_power"`
	Value       string `json:"value"`
}

// Account reads the cash and the account's value (the portfolio's equity).
func (a *Adapter) Account(ctx context.Context) (broker.Account, error) {
	raw, err := a.call(ctx, opAccount, AccountCallTimeout, nil)
	if err != nil {
		return broker.Account{}, err
	}
	var w accountReply
	if err := decode(opAccount, raw, &w); err != nil {
		return broker.Account{}, err
	}
	c, err := money.ParseMoney(w.Cash)
	if err != nil {
		return broker.Account{}, badReply(opAccount, "cash: %v", err)
	}
	v, err := money.ParseMoney(w.Value)
	if err != nil {
		return broker.Account{}, badReply(opAccount, "value: %v", err)
	}
	return broker.Account{Cash: c, Value: v}, nil
}
