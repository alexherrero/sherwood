package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/feed"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/signals"
)

// portfolioData is what the Portfolio view shows. Every figure is text
// already formatted from the money types.
type portfolioData struct {
	// Imported is false until holdings have been recorded at least once.
	Imported bool
	// AsOf and Source say when the current holdings were read and where
	// they came from.
	AsOf   string
	Source string
	// Count is how many holdings the current set has; all of them are
	// listed, even past the limit.
	Count     int
	Limit     int
	OverLimit bool
	Holdings  []holdingView

	// HasCash is false until a cash reading has been recorded.
	HasCash    bool
	Cash       string
	CashAsOf   string
	CashSource string

	// Unpriced counts holdings with no checked closing price stored. While
	// any is unpriced, no total is shown, because it would be wrong.
	Unpriced int
	// Invested, Value and Gain total the priced holdings; Total adds cash.
	// Each is shown only when every holding is priced (and, for Total,
	// when cash is known).
	Invested string
	Value    string
	Gain     delta
	Total    string

	// FeedStale is true when there is no research feed, or the latest is
	// older than feed.StaleAfter; then no holding shows an AI note.
	FeedStale bool
}

// holdingView is one row of the holdings list.
type holdingView struct {
	Symbol      string
	Quantity    string
	AverageCost string
	Invested    string
	Opened      string
	// Priced is false when no checked closing price is stored for the
	// stock yet; then Close, CloseDate, Value and Gain are empty.
	Priced    bool
	Close     string
	CloseDate string
	Value     string
	Gain      delta
	// Suggestions are the rules' suggestions for the stock from its latest
	// rules run, one per strategy that holds it.
	Suggestions []suggestionView
	// Note is the research feed's latest note on the stock, or nil when
	// there is none from a fresh upload.
	Note *feedItemView
}

// suggestionView is one strategy's suggestion for a holding.
type suggestionView struct {
	Strategy string
	// Words is the suggestion: Hold, Add, Trim or Sell.
	Words string
	// Reason is the rule's reason, or "" for a hold no rule decided.
	Reason string
	// Stop is the stop the strategy's stop rules give the holding, or "".
	Stop string
	// Date is the trading day of the rules run.
	Date string
}

// suggestionWords says each suggestion as a word.
var suggestionWords = map[signals.Suggestion]string{
	signals.Hold:        "Hold",
	signals.SuggestAdd:  "Add",
	signals.SuggestTrim: "Trim",
	signals.SuggestSell: "Sell",
}

// sources says in words where a set of figures came from.
var sources = map[holdings.Source]string{
	holdings.SourceImport:    "imported from the broker",
	holdings.SourceFills:     "worked out from sherwood's own fills",
	holdings.SourceReconcile: "taken from the broker after a reconciliation",
}

// storedClose is a stock's latest checked closing price.
type storedClose struct {
	price money.Price
	date  time.Time
}

// loadPortfolio reads Portfolio's data in one read transaction, so the
// holdings, cash and closing prices all come from the same moment.
func loadPortfolio(s *Server, r *http.Request) (any, error) {
	ctx := r.Context()
	tx, err := s.store.Reader().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	d := portfolioData{Limit: holdings.Limit}
	cash, err := holdings.LatestCash(ctx, tx)
	switch {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return nil, err
	default:
		d.HasCash = true
		d.Cash = cents(cash.Amount)
		d.CashAsOf = when(cash.AsOf)
		d.CashSource = sources[cash.Source]
	}

	set, err := holdings.Current(ctx, tx)
	if errors.Is(err, holdings.ErrNone) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	d.Imported = true
	d.AsOf = when(set.AsOf)
	d.Source = sources[set.Source]
	d.Count = len(set.Holdings)
	d.OverLimit = set.OverLimit

	closes, err := latestCloses(ctx, tx)
	if err != nil {
		return nil, err
	}
	suggestions, err := latestSuggestions(ctx, tx)
	if err != nil {
		return nil, err
	}
	held := make([]string, 0, len(set.Holdings))
	for _, h := range set.Holdings {
		held = append(held, h.Symbol)
	}
	notes, err := feed.LatestNotes(ctx, tx, held)
	if err != nil {
		return nil, err
	}
	up, err := feed.LatestUpload(ctx, tx)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	d.FeedStale = up == nil || !feed.Fresh(up.At, now)
	var invested, value money.Money
	for _, h := range set.Holdings {
		v := holdingView{
			Symbol:      h.Symbol,
			Quantity:    h.Quantity.String(),
			AverageCost: dollars(h.AverageCost),
			Invested:    cents(h.Cost()),
			Opened:      day(h.Opened),
			Suggestions: suggestions[h.Symbol],
		}
		if n, ok := notes[h.Symbol]; ok {
			v.Note = freshItem(&n, now)
		}
		invested = invested.Add(h.Cost())
		if c, ok := closes[h.Symbol]; ok {
			val := h.Value(c.price)
			value = value.Add(val)
			v.Priced = true
			v.Close = dollars(c.price.Money())
			v.CloseDate = day(c.date)
			v.Value = cents(val)
			v.Gain = gainOf(val, h.Cost())
		} else {
			d.Unpriced++
		}
		d.Holdings = append(d.Holdings, v)
	}
	if d.Unpriced == 0 {
		d.Invested = cents(invested)
		d.Value = cents(value)
		d.Gain = gainOf(value, invested)
		if d.HasCash {
			d.Total = cents(value.Add(cash.Amount))
		}
	}
	return d, nil
}

// latestSuggestions reads, for each current holding, every strategy's
// suggestion from the newest rules run that made one for it, keyed by symbol.
// It reads current_holdings in the same transaction as the caller's
// holdings.Current, so both see the same set.
func latestSuggestions(ctx context.Context, tx *sql.Tx) (map[string][]suggestionView, error) {
	out := make(map[string][]suggestionView)
	rows, err := tx.QueryContext(ctx,
		`SELECT h.symbol, h.strategy, h.trading_date, h.suggestion, h.reason, h.stop FROM holding_suggestions AS h
		 WHERE h.symbol IN (SELECT symbol FROM current_holdings)
		   AND h.trading_date = (SELECT max(g.trading_date) FROM holding_suggestions AS g WHERE g.symbol = h.symbol)
		 ORDER BY h.symbol, h.strategy`)
	if err != nil {
		return nil, fmt.Errorf("web: reading suggestions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var symbol, date, suggestion string
		var v suggestionView
		var stop sql.Null[money.Price]
		if err := rows.Scan(&symbol, &v.Strategy, &date, &suggestion, &v.Reason, &stop); err != nil {
			return nil, fmt.Errorf("web: reading suggestions: %w", err)
		}
		d, err := time.ParseInLocation(clock.DateLayout, date, clock.Eastern)
		if err != nil {
			return nil, fmt.Errorf("web: reading suggestions: %w", err)
		}
		v.Date = day(d)
		v.Words = suggestion
		if w, ok := suggestionWords[signals.Suggestion(suggestion)]; ok {
			v.Words = w
		}
		if stop.Valid {
			v.Stop = dollars(stop.V.Money())
		}
		out[symbol] = append(out[symbol], v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("web: reading suggestions: %w", err)
	}
	return out, nil
}

// latestCloses reads each current holding's newest closing price that the
// rules may use: from a day whose prices were complete, from the primary
// source, and with both sources agreeing. A stock with no such price is left
// out. It reads current_holdings in the same transaction as the caller's
// holdings.Current, so both see the same set.
func latestCloses(ctx context.Context, tx *sql.Tx) (map[string]storedClose, error) {
	out := make(map[string]storedClose)
	rows, err := tx.QueryContext(ctx,
		`SELECT u.symbol, u.trading_date, u.close FROM usable_bars AS u
		 WHERE u.symbol IN (SELECT symbol FROM current_holdings)
		   AND u.trading_date = (SELECT max(v.trading_date) FROM usable_bars AS v WHERE v.symbol = u.symbol)`)
	if err != nil {
		return nil, fmt.Errorf("web: reading closing prices: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var symbol, date string
		var c storedClose
		if err := rows.Scan(&symbol, &date, &c.price); err != nil {
			return nil, fmt.Errorf("web: reading closing prices: %w", err)
		}
		if c.date, err = time.ParseInLocation(clock.DateLayout, date, clock.Eastern); err != nil {
			return nil, fmt.Errorf("web: reading closing prices: %w", err)
		}
		out[symbol] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("web: reading closing prices: %w", err)
	}
	return out, nil
}
