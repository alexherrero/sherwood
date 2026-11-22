package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/store"
)

// Settings shows the risk check's limits, the mode and the paper record, and
// lets you tighten any limit. A tightening applies at once: the proposal,
// the approval re-check and the 09:35 send all read the limits in force
// (limits.Effective) for each check. A loosening is refused; it is made by
// editing the strategy file at the NAS and restarting.
const (
	tightenPath = "/settings/limits"
	// The tighten form's fields: which limit, and its new value.
	limitField = "limit"
	valueField = "value"
	// changesShown is how many recent limit changes Settings lists.
	changesShown = 10
)

// limitResults are the words Settings shows after a tighten, picked by the
// ?limit= value. The value only picks fixed words; it never changes
// anything.
var limitResults = map[string]pushResult{
	"tightened": {Class: "notice--info", Title: "The limit is tightened.",
		Body: "It applies at once to every order checked from now on, and the change is recorded with this device's tap. It lasts until the strategy file changes."},
	"looser": {Class: "notice--warning", Title: "The limit was not changed.",
		Body: "That would loosen it. A loosening is made by editing the strategy file on the NAS and restarting, never from the app."},
	"same": {Class: "notice--info", Title: "The limit was not changed.", Body: "That is already the limit in force."},
	"invalid": {Class: "notice--warning", Title: "The limit was not changed.",
		Body: "That is not a valid value for this limit. Write a share like 0.10 for 10%, an amount like 500.00, or a whole number; the limit's hint says which. A buy's limit distance cannot go below the buy limit margin."},
	"unknown": {Class: "notice--warning", Title: "The limit was not changed.", Body: "That limit cannot be tightened from the app."},
}

// limitView is one tightenable limit.
type limitView struct {
	Name  string
	Words string
	// Hint says how to write a value.
	Hint string
	// File is the strategy file's value, and InForce the value in force;
	// each is in words, such as "10% (0.10)" or "Not set".
	File    string
	InForce string
	// Tightened says when the value in force was set from the phone app, or
	// is "" when it is the file's.
	Tightened string
	// Current is the value in force as written, for the form.
	Current string
}

// otherView is a strategy-file setting that only changes at the NAS.
type otherView struct {
	Words string
	Value string
}

// changeView is one recorded limit change.
type changeView struct {
	At      string
	Setting string
	From    string
	To      string
	Source  string
}

// paperRecord is the paper record so far.
type paperRecord struct {
	// Started is false until holdings or cash have been recorded.
	Started bool
	Since   string
	// Start is the cash and the holdings at cost when the record began;
	// Now is the cash and the holdings at their latest checked closes, or
	// "" when a holding has no close. Return compares them.
	Start  string
	Now    string
	Return delta
	// Holdings counts the holdings now.
	Holdings int
	// Orders counts the orders by what became of them.
	Filled  int
	Open    int
	Waiting int
	NoTrade int
	Fills   int
}

var hints = map[limits.Kind]string{
	limits.Share:         "A share, from above 0 to 1: 0.10 means 10%.",
	limits.ShareBelowOne: "A share, above 0 and below 1: 0.05 means 5%.",
	limits.Amount:        "A dollar amount, such as 500.00.",
	limits.Count:         "A whole number, at least 1.",
}

// limitWords writes a limit's value for the page.
func limitWords(k limits.Kind, v string) string {
	if v == "" {
		return "Not set"
	}
	switch k {
	case limits.Share, limits.ShareBelowOne:
		d, err := decimal.NewFromString(v)
		if err != nil {
			return v
		}
		return d.Mul(hundred).String() + "% (" + v + ")"
	case limits.Amount:
		m, err := money.ParseMoney(v)
		if err != nil {
			return v
		}
		return cents(m)
	}
	return v
}

var sourceWords = map[limits.Source]string{
	limits.Phone: "the phone app",
	limits.NAS:   "the strategy file on the NAS",
}

// loadLimits fills Settings' limits, recent changes and paper record, from
// one read transaction.
func (s *Server) loadLimits(ctx context.Context, d *settingsData) error {
	tx, err := s.store.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	d.Live = s.mode == config.ModeLive

	applied, err := limits.Read(ctx, tx, s.strategy)
	if err != nil {
		return err
	}
	for _, spec := range limits.Specs() {
		v := limitView{
			Name:    string(spec.Name),
			Words:   spec.Words,
			Hint:    hints[spec.Kind],
			File:    limitWords(spec.Kind, spec.Value(s.strategy.Limits)),
			InForce: limitWords(spec.Kind, spec.Value(applied.Limits)),
			Current: spec.Value(applied.Limits),
		}
		if c, ok := applied.Tightened[spec.Name]; ok {
			v.Tightened = when(c.At)
		}
		d.Limits = append(d.Limits, v)
	}
	l := s.strategy.Limits
	fractional := "Off: whole shares only"
	if l.FractionalShares {
		fractional = "On"
	}
	d.Other = []otherView{
		{"Buy limit margin above the last close", limitWords(limits.Share, l.BuyLimitMargin.String())},
		{"Fractional shares", fractional},
		{"Price sources may disagree by", limitWords(limits.Share, l.PriceCrossCheckTolerance.String())},
		{"Fewest weeks of paper trading before live mode", strconv.Itoa(l.MinPaperWeeks)},
	}

	changes, err := limits.History(ctx, tx, changesShown)
	if err != nil {
		return err
	}
	for _, c := range changes {
		cv := changeView{At: when(c.At), Setting: c.Setting, Source: sourceWords[c.Source]}
		if c.Setting == limits.HashSetting {
			cv.Setting = "The strategy file"
			cv.From, cv.To = "", "changed"
		} else if c.Setting == limits.WatchSetting {
			cv.Setting = c.New
			cv.From, cv.To = "", "added to the watchlist"
		} else if c.Setting == limits.ModeSetting {
			cv.Setting = "The mode"
			cv.From, cv.To = c.Old, c.New
			if cv.From == "" {
				cv.From, cv.To = "", "recorded as "+c.New
			}
		} else if c.Setting == limits.LiveSetting {
			cv.Setting = "Live mode"
			cv.From, cv.To = "", "confirmed"
		} else if spec, ok := limits.Lookup(limits.Name(c.Setting)); ok {
			cv.Setting = spec.Words
			cv.From, cv.To = limitWords(spec.Kind, c.Old), limitWords(spec.Kind, c.New)
		}
		d.Changes = append(d.Changes, cv)
	}

	if d.Gate, err = s.loadLive(ctx, tx); err != nil {
		return err
	}
	d.Paper, err = loadPaperRecord(ctx, tx)
	return err
}

// loadPaperRecord reads the paper record: what the record began with, what
// it is worth now and what became of its orders.
func loadPaperRecord(ctx context.Context, tx *sql.Tx) (paperRecord, error) {
	var p paperRecord
	var startCash money.Money
	var cashAt string
	err := tx.QueryRowContext(ctx, `SELECT amount, as_of FROM cash_snapshots ORDER BY id LIMIT 1`).Scan(&startCash, &cashAt)
	hasCash := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("web: reading the first cash reading: %w", err)
	}
	var first sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT min(id) FROM holding_snapshots`).Scan(&first); err != nil {
		return p, fmt.Errorf("web: reading the first holdings: %w", err)
	}
	if !hasCash && !first.Valid {
		return p, nil
	}
	p.Started = true
	start := startCash
	var began string
	if hasCash {
		began = cashAt
	}
	if first.Valid {
		set, err := holdings.Get(ctx, tx, first.Int64)
		if err != nil {
			return p, err
		}
		for _, h := range set.Holdings {
			start = start.Add(h.Cost())
		}
		if at := store.FormatTime(set.AsOf); began == "" || at < began {
			began = at
		}
	}
	if t, err := store.ParseTime(began); err == nil {
		p.Since = day(t)
	}
	p.Start = cents(start)

	var now money.Money
	priced := true
	switch c, err := holdings.LatestCash(ctx, tx); {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return p, err
	default:
		now = c.Amount
	}
	switch set, err := holdings.Current(ctx, tx); {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return p, err
	default:
		p.Holdings = len(set.Holdings)
		closes, err := latestCloses(ctx, tx)
		if err != nil {
			return p, err
		}
		for _, h := range set.Holdings {
			c, ok := closes[h.Symbol]
			if !ok {
				priced = false
				break
			}
			now = now.Add(h.Value(c.price))
		}
	}
	if priced {
		p.Now = cents(now)
		p.Return = gainOf(now, start)
	}

	rows, err := tx.QueryContext(ctx, `SELECT state, count(*) FROM orders GROUP BY state`)
	if err != nil {
		return p, fmt.Errorf("web: counting orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return p, fmt.Errorf("web: counting orders: %w", err)
		}
		switch orders.State(st) {
		case orders.Filled:
			p.Filled += n
		case orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Unknown, orders.Approved:
			p.Open += n
		case orders.Proposed, orders.AwaitingApproval:
			p.Waiting += n
		default:
			p.NoTrade += n
		}
	}
	if err := rows.Err(); err != nil {
		return p, fmt.Errorf("web: counting orders: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM fills`).Scan(&p.Fills); err != nil {
		return p, fmt.Errorf("web: counting fills: %w", err)
	}
	return p, nil
}

// mountLimits registers the tighten action. Like every other change to
// trading, it is limited to the approval devices.
func (s *Server) mountLimits() {
	s.HandleChange(tightenPath, http.HandlerFunc(s.tighten), ApprovalDevicesOnly)
}

func backToLimits(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, settingsPath+"?limit="+result+"#limits", http.StatusSeeOther)
}

// tighten records a tightening of one limit, which applies at once.
func (s *Server) tighten(w http.ResponseWriter, r *http.Request) {
	name := limits.Name(r.PostForm.Get(limitField))
	c, err := limits.Tighten(r.Context(), s.store, s.strategy, s.clock.Now(), name, r.PostForm.Get(valueField))
	switch {
	case err == nil:
		s.logger.Warn("limit tightened", "setting", c.Setting, "old_value", c.Old, "new_value", c.New,
			"change_source", string(limits.Phone))
		backToLimits(w, r, "tightened")
	case errors.Is(err, limits.ErrLooser):
		backToLimits(w, r, "looser")
	case errors.Is(err, limits.ErrSame):
		backToLimits(w, r, "same")
	case errors.Is(err, limits.ErrInvalid):
		backToLimits(w, r, "invalid")
	case errors.Is(err, limits.ErrUnknown):
		backToLimits(w, r, "unknown")
	default:
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"The limit could not be recorded, so it is unchanged. Try again.")
	}
}
