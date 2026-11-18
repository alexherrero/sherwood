package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
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
	// Suggestion and Note are filled by later parts: the rule runner's
	// suggestion and the research feed's latest AI note.
	Suggestion string
	Note       string
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
	var invested, value money.Money
	for _, h := range set.Holdings {
		v := holdingView{
			Symbol:      h.Symbol,
			Quantity:    h.Quantity.String(),
			AverageCost: dollars(h.AverageCost),
			Invested:    cents(h.Cost()),
			Opened:      day(h.Opened),
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
