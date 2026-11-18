package holdings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

// Limit is the design's ceiling on holdings. A snapshot with more is
// recorded with OverLimit set.
const Limit = config.MaxHoldings

// Source says where a snapshot's figures came from.
type Source string

// The sources a snapshot may have. The SQL CHECK in migration 0003 allows
// exactly these.
const (
	// SourceImport is the read-only import from the broker.
	SourceImport Source = "import"
	// SourceFills is holdings derived from sherwood's own fills.
	SourceFills Source = "fills"
	// SourceReconcile is the broker's figures taken as the record after a
	// reviewed reconciliation difference.
	SourceReconcile Source = "reconcile"
)

func (s Source) valid() bool {
	return s == SourceImport || s == SourceFills || s == SourceReconcile
}

var (
	// ErrNone means nothing has been recorded yet: no holdings snapshot or no
	// cash reading. An empty set of holdings that was recorded is not ErrNone.
	ErrNone = errors.New("holdings: nothing recorded yet")
	// ErrInvalid wraps every refusal of a malformed holding, source or time.
	ErrInvalid = errors.New("holdings: invalid")
	// ErrOutOfOrder means a snapshot is older than the newest one already
	// recorded. Snapshots must be recorded in time order.
	ErrOutOfOrder = errors.New("holdings: snapshot older than the current one")
	// ErrCorrupt means a stored snapshot does not hold what its header says.
	ErrCorrupt = errors.New("holdings: stored snapshot is inconsistent")
)

// Holding is one stock held.
type Holding struct {
	// Symbol is the stock's symbol in upper case, such as "ABC" or "XYZ.B".
	Symbol string
	// Quantity is the shares held, more than zero.
	Quantity money.Quantity
	// AverageCost is the average cost per share exactly as the source gave
	// it, which may have more than two places. Use CostPrice for rules.
	AverageCost money.Money
	// Opened is the trading date the holding was opened. Only its calendar
	// date, in its own location, is stored; pass a time in clock.Eastern.
	// Read back, it is midnight Eastern on that date.
	Opened time.Time
}

// CostPrice is the average cost rounded to the cent, halves away from zero,
// as rules.Holding.Cost wants it. It fails only for a negative cost, which
// Replace never records.
func (h Holding) CostPrice() (money.Price, error) {
	return money.RoundPrice(h.AverageCost.Decimal())
}

// Value is the holding's value at price p, exactly.
func (h Holding) Value(p money.Price) money.Money { return p.Mul(h.Quantity) }

// Cost is what the holding cost in all: quantity × average cost, exactly.
func (h Holding) Cost() money.Money {
	return money.FromDecimal(h.Quantity.Decimal().Mul(h.AverageCost.Decimal()))
}

// Set is one snapshot of the whole set of holdings.
type Set struct {
	// ID is the snapshot's row id. A higher ID is a later snapshot.
	ID int64
	// Source is where the figures came from.
	Source Source
	// AsOf is when the holdings were as recorded, in UTC.
	AsOf time.Time
	// OverLimit is true exactly when the set has more than Limit holdings.
	OverLimit bool
	// Holdings are sorted by symbol.
	Holdings []Holding
}

// Find returns the holding of symbol, if the set has one.
func (s Set) Find(symbol string) (Holding, bool) {
	i, ok := slices.BinarySearchFunc(s.Holdings, symbol, func(h Holding, sym string) int {
		return strings.Compare(h.Symbol, sym)
	})
	if !ok {
		return Holding{}, false
	}
	return s.Holdings[i], true
}

// Cash is one reading of the cash balance.
type Cash struct {
	// ID is the reading's row id. A higher ID is a later reading.
	ID int64
	// Source is where the figure came from.
	Source Source
	// AsOf is when the balance was as recorded, in UTC.
	AsOf time.Time
	// Amount is the balance exactly as read. It may be negative if the broker
	// reports it so; reconciliation treats that as a difference.
	Amount money.Money
}

// Querier is what the readers need: a *sql.DB (such as store.Reader()) or a
// *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Execer is what the writers need: a transaction on the store's writer.
type Execer interface {
	Querier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// symbolText is the accepted form of a stock symbol, the same rule as the
// prices package and the SQL CHECK.
var symbolText = regexp.MustCompile(`^[A-Z][A-Z0-9]*([.-][A-Z0-9]+)?$`)

const maxSymbolLen = 10

func checkSymbol(s string) error {
	if len(s) > maxSymbolLen || !symbolText.MatchString(s) {
		return fmt.Errorf("%w: %q is not a stock symbol", ErrInvalid, s)
	}
	return nil
}

func checkHeader(src Source, asOf time.Time) error {
	if !src.valid() {
		return fmt.Errorf("%w: unknown source %q", ErrInvalid, src)
	}
	if asOf.IsZero() {
		return fmt.Errorf("%w: the as-of time is not set", ErrInvalid)
	}
	return nil
}

// check validates hs and returns it sorted by symbol.
func check(hs []Holding) ([]Holding, error) {
	out := slices.Clone(hs)
	slices.SortFunc(out, func(a, b Holding) int { return strings.Compare(a.Symbol, b.Symbol) })
	for i, h := range out {
		if err := checkSymbol(h.Symbol); err != nil {
			return nil, err
		}
		if i > 0 && out[i-1].Symbol == h.Symbol {
			return nil, fmt.Errorf("%w: %s is listed twice", ErrInvalid, h.Symbol)
		}
		if h.Quantity.IsZero() {
			return nil, fmt.Errorf("%w: %s has no shares", ErrInvalid, h.Symbol)
		}
		if h.AverageCost.IsNegative() {
			return nil, fmt.Errorf("%w: %s has a negative average cost", ErrInvalid, h.Symbol)
		}
		if h.Opened.IsZero() {
			return nil, fmt.Errorf("%w: %s has no opened date", ErrInvalid, h.Symbol)
		}
	}
	return out, nil
}

// Replace records hs as the whole current set of holdings, as of asOf, from
// src. It runs on tx, which must be a transaction on the store's writer; the
// caller commits it. A set of more than Limit holdings is recorded with
// OverLimit set, not refused. An empty hs records that nothing is held.
//
// It refuses (wrapping ErrInvalid) a malformed holding, a repeated symbol, an
// unknown source or a zero time, and (ErrOutOfOrder) an asOf earlier than the
// current snapshot's. Nothing is written when it refuses.
func Replace(ctx context.Context, tx Execer, src Source, asOf time.Time, hs []Holding) (Set, error) {
	if err := checkHeader(src, asOf); err != nil {
		return Set{}, err
	}
	sorted, err := check(hs)
	if err != nil {
		return Set{}, err
	}
	if err := checkOrder(ctx, tx, "holding_snapshots", asOf); err != nil {
		return Set{}, err
	}

	set := Set{Source: src, AsOf: asOf.UTC(), OverLimit: len(sorted) > Limit, Holdings: sorted}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO holding_snapshots (source, as_of, holding_count, over_limit) VALUES (?, ?, ?, ?)`,
		string(src), store.FormatTime(asOf), len(sorted), boolInt(set.OverLimit))
	if err != nil {
		return Set{}, fmt.Errorf("holdings: recording the snapshot: %w", err)
	}
	if set.ID, err = res.LastInsertId(); err != nil {
		return Set{}, fmt.Errorf("holdings: recording the snapshot: %w", err)
	}
	for _, h := range sorted {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO snapshot_holdings (snapshot_id, symbol, quantity, average_cost, opened_on) VALUES (?, ?, ?, ?, ?)`,
			set.ID, h.Symbol, h.Quantity, h.AverageCost, store.FormatDate(h.Opened)); err != nil {
			return Set{}, fmt.Errorf("holdings: recording %s: %w", h.Symbol, err)
		}
	}
	// Return the holdings as they read back, so Opened is midnight Eastern
	// either way.
	for i := range set.Holdings {
		set.Holdings[i].Opened = eastern(set.Holdings[i].Opened)
	}
	return set, nil
}

// RecordCash records amount as the cash balance as of asOf, from src, on tx
// (a transaction on the store's writer; the caller commits it). It refuses an
// unknown source or a zero time (ErrInvalid) and an asOf earlier than the
// latest reading's (ErrOutOfOrder).
func RecordCash(ctx context.Context, tx Execer, src Source, asOf time.Time, amount money.Money) (Cash, error) {
	if err := checkHeader(src, asOf); err != nil {
		return Cash{}, err
	}
	if err := checkOrder(ctx, tx, "cash_snapshots", asOf); err != nil {
		return Cash{}, err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO cash_snapshots (source, as_of, amount) VALUES (?, ?, ?)`,
		string(src), store.FormatTime(asOf), amount)
	if err != nil {
		return Cash{}, fmt.Errorf("holdings: recording cash: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Cash{}, fmt.Errorf("holdings: recording cash: %w", err)
	}
	return Cash{ID: id, Source: src, AsOf: asOf.UTC(), Amount: amount}, nil
}

// Snapshot is a full reading from one source: the holdings and the cash.
type Snapshot struct {
	Source   Source
	AsOf     time.Time
	Holdings []Holding
	Cash     money.Money
}

// Record writes snap's holdings and cash together in one transaction on the
// store's writer, so they are either both current or neither is. Don't call
// it while holding a transaction on the writer: that deadlocks. Use Replace
// and RecordCash on that transaction instead.
func Record(ctx context.Context, s *store.Store, snap Snapshot) (set Set, cash Cash, err error) {
	tx, err := s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return Set{}, Cash{}, fmt.Errorf("holdings: starting the write: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if set, err = Replace(ctx, tx, snap.Source, snap.AsOf, snap.Holdings); err != nil {
		return Set{}, Cash{}, err
	}
	if cash, err = RecordCash(ctx, tx, snap.Source, snap.AsOf, snap.Cash); err != nil {
		return Set{}, Cash{}, err
	}
	if err = tx.Commit(); err != nil {
		return Set{}, Cash{}, fmt.Errorf("holdings: committing: %w", err)
	}
	return set, cash, nil
}

// table is always one of the two constant snapshot table names.
func checkOrder(ctx context.Context, q Querier, table string, asOf time.Time) error {
	var latest sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT max(as_of) FROM `+table).Scan(&latest); err != nil {
		return fmt.Errorf("holdings: reading the latest %s: %w", table, err)
	}
	if latest.Valid && store.FormatTime(asOf) < latest.String {
		return fmt.Errorf("%w: %s is before %s", ErrOutOfOrder, store.FormatTime(asOf), latest.String)
	}
	return nil
}

// Current returns the newest snapshot of holdings, or ErrNone if none has
// been recorded.
func Current(ctx context.Context, q Querier) (Set, error) {
	return readSet(ctx, q, `SELECT id, source, as_of, holding_count, over_limit FROM holding_snapshots ORDER BY id DESC LIMIT 1`)
}

// Get returns the snapshot with the given id, or ErrNone if there is none.
func Get(ctx context.Context, q Querier, id int64) (Set, error) {
	return readSet(ctx, q, `SELECT id, source, as_of, holding_count, over_limit FROM holding_snapshots WHERE id = ?`, id)
}

func readSet(ctx context.Context, q Querier, query string, args ...any) (Set, error) {
	var (
		set       Set
		src, asOf string
		count     int
		over      int
	)
	err := q.QueryRowContext(ctx, query, args...).Scan(&set.ID, &src, &asOf, &count, &over)
	if errors.Is(err, sql.ErrNoRows) {
		return Set{}, ErrNone
	}
	if err != nil {
		return Set{}, fmt.Errorf("holdings: reading the snapshot: %w", err)
	}
	set.Source = Source(src)
	set.OverLimit = over == 1
	if set.AsOf, err = store.ParseTime(asOf); err != nil {
		return Set{}, fmt.Errorf("holdings: snapshot %d: %w", set.ID, err)
	}

	rows, err := q.QueryContext(ctx,
		`SELECT symbol, quantity, average_cost, opened_on FROM snapshot_holdings WHERE snapshot_id = ? ORDER BY symbol`, set.ID)
	if err != nil {
		return Set{}, fmt.Errorf("holdings: reading snapshot %d: %w", set.ID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			h      Holding
			opened string
		)
		if err := rows.Scan(&h.Symbol, &h.Quantity, &h.AverageCost, &opened); err != nil {
			return Set{}, fmt.Errorf("holdings: reading snapshot %d: %w", set.ID, err)
		}
		if h.Opened, err = time.ParseInLocation(store.DateLayout, opened, clock.Eastern); err != nil {
			return Set{}, fmt.Errorf("holdings: snapshot %d, %s: opened date %q: %w", set.ID, h.Symbol, opened, err)
		}
		set.Holdings = append(set.Holdings, h)
	}
	if err := rows.Err(); err != nil {
		return Set{}, fmt.Errorf("holdings: reading snapshot %d: %w", set.ID, err)
	}
	if len(set.Holdings) != count || set.OverLimit != (count > Limit) {
		return Set{}, fmt.Errorf("%w: snapshot %d says %d holdings and has %d", ErrCorrupt, set.ID, count, len(set.Holdings))
	}
	return set, nil
}

// LatestCash returns the newest cash reading, or ErrNone if none has been
// recorded.
func LatestCash(ctx context.Context, q Querier) (Cash, error) {
	var (
		c         Cash
		src, asOf string
	)
	err := q.QueryRowContext(ctx,
		`SELECT id, source, as_of, amount FROM cash_snapshots ORDER BY id DESC LIMIT 1`).Scan(&c.ID, &src, &asOf, &c.Amount)
	if errors.Is(err, sql.ErrNoRows) {
		return Cash{}, ErrNone
	}
	if err != nil {
		return Cash{}, fmt.Errorf("holdings: reading cash: %w", err)
	}
	c.Source = Source(src)
	if c.AsOf, err = store.ParseTime(asOf); err != nil {
		return Cash{}, fmt.Errorf("holdings: cash reading %d: %w", c.ID, err)
	}
	return c, nil
}

// eastern is midnight Eastern on t's calendar date in t's own location,
// which is how an opened date reads back.
func eastern(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, clock.Eastern)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
