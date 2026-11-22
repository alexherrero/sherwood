package limits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

// Name is a limit's dotted path, as the strategy file and settings_changes
// name it.
type Name string

// The limits the phone app can tighten: the risk check's limits.
const (
	StockShare    Name = "limits.max_stock_share"
	OrderValue    Name = "limits.max_order_value"
	OrdersPerDay  Name = "limits.max_orders_per_day"
	DailyLoss     Name = "limits.daily_loss_limit"
	Holdings      Name = "limits.max_holdings"
	SectorShare   Name = "limits.max_sector_share"
	LimitDistance Name = "limits.max_limit_distance"
)

// HashSetting is the settings_changes row that records the strategy file's
// hash. Tightenings count only after the newest one, and only while it names
// the file loaded now.
const HashSetting = "strategy_file.hash"

// Source is where a change came from, as settings_changes stores it.
type Source string

// The two sources.
const (
	Phone Source = "phone"
	NAS   Source = "nas"
)

// Kind is how a limit's value is written.
type Kind int

// The kinds of value.
const (
	// Share is a share of the whole from above 0 to 1, such as 0.10 for
	// 10%.
	Share Kind = iota + 1
	// ShareBelowOne is a share above 0 and below 1.
	ShareBelowOne
	// Amount is a dollar amount above 0 with at most two decimal places.
	Amount
	// Count is a whole number of at least 1.
	Count
)

// Errors Tighten returns. Each is wrapped with a sentence for the phone app.
var (
	// ErrUnknown means the name is not a limit the app can tighten.
	ErrUnknown = errors.New("limits: not a limit the phone app can tighten")
	// ErrInvalid means the value is not a valid value for the limit.
	ErrInvalid = errors.New("limits: not a valid value")
	// ErrLooser means the value would loosen the limit in force, which only
	// an edit of the strategy file at the NAS may do.
	ErrLooser = errors.New("limits: a loosening is made at the NAS, not from the phone app")
	// ErrSame means the value is the limit already in force.
	ErrSame = errors.New("limits: that is already the limit in force")
	// ErrCorrupt means a recorded change cannot be read back.
	ErrCorrupt = errors.New("limits: a recorded limit change cannot be read")
)

// Spec describes one limit the app can tighten.
type Spec struct {
	Name Name
	// Words is the limit in a few plain words.
	Words string
	Kind  Kind
	// Optional is true for a limit the file may leave unset (no limit).
	Optional bool
	get      func(config.Limits) string
	set      func(*config.Limits, string)
}

func decimalText(d config.Decimal) string { return d.String() }

// specs are the tightenable limits, in the design's order.
var specs = []Spec{
	{Name: StockShare, Words: "Largest share of the portfolio in one stock", Kind: Share,
		get: func(l config.Limits) string { return decimalText(l.MaxStockShare) },
		set: func(l *config.Limits, v string) { l.MaxStockShare = config.MustDecimal(v) }},
	{Name: OrderValue, Words: "Largest value of one order", Kind: Amount,
		get: func(l config.Limits) string { return l.MaxOrderValue.String() },
		set: func(l *config.Limits, v string) { l.MaxOrderValue = money.MustParseMoney(v) }},
	{Name: OrdersPerDay, Words: "Most orders sent in one trading day", Kind: Count,
		get: func(l config.Limits) string { return strconv.Itoa(l.MaxOrdersPerDay) },
		set: func(l *config.Limits, v string) { l.MaxOrdersPerDay, _ = strconv.Atoi(v) }},
	{Name: DailyLoss, Words: "Daily loss limit", Kind: Amount,
		get: func(l config.Limits) string { return l.DailyLossLimit.String() },
		set: func(l *config.Limits, v string) { l.DailyLossLimit = money.MustParseMoney(v) }},
	{Name: Holdings, Words: "Most holdings", Kind: Count,
		get: func(l config.Limits) string { return strconv.Itoa(l.MaxHoldings) },
		set: func(l *config.Limits, v string) { l.MaxHoldings, _ = strconv.Atoi(v) }},
	{Name: SectorShare, Words: "Largest share of the portfolio in one sector", Kind: Share, Optional: true,
		get: func(l config.Limits) string {
			if l.MaxSectorShare == nil {
				return ""
			}
			return decimalText(*l.MaxSectorShare)
		},
		set: func(l *config.Limits, v string) {
			if v == "" {
				l.MaxSectorShare = nil
				return
			}
			d := config.MustDecimal(v)
			l.MaxSectorShare = &d
		}},
	{Name: LimitDistance, Words: "Largest distance of a buy's limit price from the last close", Kind: ShareBelowOne,
		get: func(l config.Limits) string { return decimalText(l.MaxLimitDistance) },
		set: func(l *config.Limits, v string) { l.MaxLimitDistance = config.MustDecimal(v) }},
}

// Specs returns the tightenable limits, in the design's order.
func Specs() []Spec { return append([]Spec(nil), specs...) }

// Lookup returns the spec named n.
func Lookup(n Name) (Spec, bool) {
	for _, s := range specs {
		if s.Name == n {
			return s, true
		}
	}
	return Spec{}, false
}

// Value returns the spec's value in l as text, or "" for an unset optional
// limit.
func (s Spec) Value(l config.Limits) string { return s.get(l) }

// parse checks text as a value of the spec's kind against the rest of the
// limits l, and returns it in its stored form.
func (s Spec) parse(text string, l config.Limits) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%w: write a value", ErrInvalid)
	}
	switch s.Kind {
	case Share, ShareBelowOne:
		d, err := config.ParseDecimal(text)
		if err != nil {
			return "", fmt.Errorf("%w: %s is not a plain decimal such as 0.10", ErrInvalid, text)
		}
		r := d.Rat()
		if r.Sign() <= 0 {
			return "", fmt.Errorf("%w: it must be greater than 0", ErrInvalid)
		}
		one := big.NewRat(1, 1)
		if s.Kind == ShareBelowOne && r.Cmp(one) >= 0 {
			return "", fmt.Errorf("%w: it must be less than 1 (it is a share, so 0.05 means 5%%)", ErrInvalid)
		}
		if r.Cmp(one) > 0 {
			return "", fmt.Errorf("%w: it must be at most 1 (it is a share, so 0.10 means 10%%)", ErrInvalid)
		}
		if s.Name == LimitDistance && r.Cmp(l.BuyLimitMargin.Rat()) < 0 {
			return "", fmt.Errorf("%w: it must be at least the buy limit margin, %s, or every buy would be refused; "+
				"to stop buying, use the stop switch", ErrInvalid, l.BuyLimitMargin)
		}
		return text, nil
	case Amount:
		m, err := money.ParseMoney(strings.TrimPrefix(text, "$"))
		switch {
		case err != nil:
			return "", fmt.Errorf("%w: %s is not an amount such as 500.00", ErrInvalid, text)
		case !m.IsPositive():
			return "", fmt.Errorf("%w: it must be greater than 0", ErrInvalid)
		case !m.IsCents():
			return "", fmt.Errorf("%w: an amount has at most 2 decimal places", ErrInvalid)
		}
		return m.String(), nil
	case Count:
		n, err := strconv.Atoi(text)
		switch {
		case err != nil:
			return "", fmt.Errorf("%w: %s is not a whole number", ErrInvalid, text)
		case n < 1:
			return "", fmt.Errorf("%w: it must be at least 1", ErrInvalid)
		case s.Name == Holdings && n > config.MaxHoldings:
			return "", fmt.Errorf("%w: it must be at most %d", ErrInvalid, config.MaxHoldings)
		}
		return strconv.Itoa(n), nil
	}
	return "", fmt.Errorf("%w: unknown kind", ErrInvalid)
}

// rat reads a stored value of the spec's kind as an exact number; nil means
// no limit.
func (s Spec) rat(text string) (*big.Rat, error) {
	if text == "" {
		if s.Optional {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s has no value", ErrCorrupt, s.Name)
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("%w: %s is %q", ErrCorrupt, s.Name, text)
	}
	return r, nil
}

// tighter reports whether a is strictly tighter than b. For every limit a
// lower value is tighter, and no limit is the loosest of all.
func (s Spec) tighter(a, b string) (bool, error) {
	ra, err := s.rat(a)
	if err != nil {
		return false, err
	}
	rb, err := s.rat(b)
	if err != nil {
		return false, err
	}
	switch {
	case ra == nil:
		return false, nil
	case rb == nil:
		return true, nil
	}
	return ra.Cmp(rb) < 0, nil
}

// Querier reads: a *sql.DB or a *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Change is one recorded limit change.
type Change struct {
	ID      int64
	Setting string
	// Old and New are the values before and after, "" for none.
	Old, New string
	Source   Source
	At       time.Time
}

// anchor returns the id of the newest hash row if it names hash, or 0.
func anchor(ctx context.Context, q Querier, hash string) (int64, error) {
	var id int64
	var v sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT id, new_value FROM settings_changes WHERE setting = ? ORDER BY id DESC LIMIT 1`, HashSetting).Scan(&id, &v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("limits: reading the strategy file's recorded hash: %w", err)
	case !v.Valid || v.String != hash:
		return 0, nil
	}
	return id, nil
}

// Applied is the effective limits and, for each limit tightened from the
// phone app, the tightening in force.
type Applied struct {
	Limits config.Limits
	// Tightened maps a limit to the tightening that set its value.
	Tightened map[Name]Change
}

// Effective reads the limits in force for the strategy file s: the file's
// limits, each tightened by the phone's tightenings recorded since the file's
// hash was last recorded. When the newest recorded hash is not the file's,
// the file changed after it, so no tightening applies. A recorded change that
// cannot be read is an error, never ignored.
func Effective(ctx context.Context, q Querier, s *config.Strategy) (config.Limits, error) {
	a, err := Read(ctx, q, s)
	if err != nil {
		return config.Limits{}, err
	}
	return a.Limits, nil
}

// Read is Effective with the tightenings that set each value.
func Read(ctx context.Context, q Querier, s *config.Strategy) (Applied, error) {
	l := s.Limits
	if l.MaxSectorShare != nil {
		d := *l.MaxSectorShare
		l.MaxSectorShare = &d
	}
	a := Applied{Limits: l, Tightened: map[Name]Change{}}
	from, err := anchor(ctx, q, s.Hash)
	if err != nil || from == 0 {
		return a, err
	}
	rows, err := q.QueryContext(ctx,
		`SELECT id, changed_at, setting, old_value, new_value FROM settings_changes
		 WHERE source = ? AND id > ? AND setting LIKE 'limits.%' ORDER BY id`, string(Phone), from)
	if err != nil {
		return Applied{}, fmt.Errorf("limits: reading tightenings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		c, err := scanChange(rows, Phone)
		if err != nil {
			return Applied{}, err
		}
		spec, ok := Lookup(Name(c.Setting))
		if !ok {
			return Applied{}, fmt.Errorf("%w: %s is not a limit the phone app can tighten", ErrCorrupt, c.Setting)
		}
		if _, err := spec.parse(c.New, a.Limits); err != nil {
			return Applied{}, fmt.Errorf("%w: change %d: %w", ErrCorrupt, c.ID, err)
		}
		t, err := spec.tighter(c.New, spec.get(a.Limits))
		if err != nil {
			return Applied{}, err
		}
		if t {
			spec.set(&a.Limits, c.New)
			a.Tightened[spec.Name] = c
		}
	}
	if err := rows.Err(); err != nil {
		return Applied{}, fmt.Errorf("limits: reading tightenings: %w", err)
	}
	return a, nil
}

func scanChange(rows *sql.Rows, src Source) (Change, error) {
	var c Change
	var at string
	var oldV, newV sql.NullString
	if err := rows.Scan(&c.ID, &at, &c.Setting, &oldV, &newV); err != nil {
		return Change{}, fmt.Errorf("limits: reading a change: %w", err)
	}
	t, err := store.ParseTime(at)
	if err != nil {
		return Change{}, fmt.Errorf("%w: change %d: %w", ErrCorrupt, c.ID, err)
	}
	c.At, c.Old, c.New, c.Source = t, oldV.String, newV.String, src
	return c, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func insert(ctx context.Context, tx *sql.Tx, at time.Time, setting, oldV, newV string, src Source) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO settings_changes (changed_at, setting, old_value, new_value, source) VALUES (?, ?, ?, ?, ?)`,
		store.FormatTime(at), setting, nullable(oldV), nullable(newV), string(src))
	if err != nil {
		return 0, fmt.Errorf("limits: recording %s: %w", setting, err)
	}
	return res.LastInsertId()
}

// newest returns the newest recorded value of setting, from src if src is
// not empty; ok is false when none is recorded.
func newest(ctx context.Context, q Querier, setting string, src Source) (v string, ok bool, err error) {
	query := `SELECT new_value FROM settings_changes WHERE setting = ? ORDER BY id DESC LIMIT 1`
	args := []any{setting}
	if src != "" {
		query = `SELECT new_value FROM settings_changes WHERE setting = ? AND source = ? ORDER BY id DESC LIMIT 1`
		args = append(args, string(src))
	}
	var nv sql.NullString
	err = q.QueryRowContext(ctx, query, args...).Scan(&nv)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("limits: reading %s: %w", setting, err)
	}
	return nv.String, true, nil
}

// record adds the hash row and the file's changed limits when the newest
// recorded hash is not s's. It reports whether it wrote anything.
func record(ctx context.Context, tx *sql.Tx, s *config.Strategy, at time.Time) (bool, error) {
	prev, _, err := newest(ctx, tx, HashSetting, "")
	if err != nil {
		return false, err
	}
	if prev == s.Hash {
		return false, nil
	}
	if _, err := insert(ctx, tx, at, HashSetting, prev, s.Hash, NAS); err != nil {
		return false, err
	}
	for _, spec := range specs {
		was, ok, err := newest(ctx, tx, string(spec.Name), NAS)
		if err != nil {
			return false, err
		}
		now := spec.get(s.Limits)
		if (ok && was == now) || (!ok && now == "") {
			continue
		}
		if _, err := insert(ctx, tx, at, string(spec.Name), was, now, NAS); err != nil {
			return false, err
		}
	}
	return true, nil
}

// write runs fn in one transaction on the store's writer.
func write(ctx context.Context, st *store.Store, fn func(*sql.Tx) error) (err error) {
	tx, err := st.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("limits: starting the write: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("limits: committing: %w", err)
	}
	return nil
}

// Record records the strategy file loaded at start-up: when its hash is not
// the newest recorded, it adds the hash and each risk limit whose value in
// the file changed, all with source nas. From then on the phone's earlier
// tightenings no longer apply. It reports whether it wrote anything.
func Record(ctx context.Context, st *store.Store, s *config.Strategy, at time.Time) (bool, error) {
	var wrote bool
	err := write(ctx, st, func(tx *sql.Tx) error {
		var err error
		wrote, err = record(ctx, tx, s, at)
		return err
	})
	return wrote, err
}

// Tighten records a tightening of the limit name to text, from the phone
// app, and returns it. It refuses a value that is not valid for the limit
// (ErrInvalid), one that equals the limit in force (ErrSame) and one that
// would loosen it (ErrLooser). The limit in force is read in the same
// transaction as the write, so two taps never both pass against a stale
// value.
func Tighten(ctx context.Context, st *store.Store, s *config.Strategy, at time.Time, name Name, text string) (Change, error) {
	spec, ok := Lookup(name)
	if !ok {
		return Change{}, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	var c Change
	err := write(ctx, st, func(tx *sql.Tx) error {
		if _, err := record(ctx, tx, s, at); err != nil {
			return err
		}
		eff, err := Effective(ctx, tx, s)
		if err != nil {
			return err
		}
		v, err := spec.parse(text, eff)
		if err != nil {
			return err
		}
		cur := spec.get(eff)
		t, err := spec.tighter(v, cur)
		if err != nil {
			return err
		}
		if !t {
			if same, _ := spec.tighter(cur, v); !same {
				return fmt.Errorf("%w: %s is already %s", ErrSame, spec.Words, orNone(cur))
			}
			return fmt.Errorf("%w: %s is %s now, and %s would loosen it", ErrLooser, spec.Words, orNone(cur), v)
		}
		id, err := insert(ctx, tx, at, string(spec.Name), cur, v, Phone)
		if err != nil {
			return err
		}
		c = Change{ID: id, Setting: string(spec.Name), Old: cur, New: v, Source: Phone, At: at}
		return nil
	})
	return c, err
}

func orNone(v string) string {
	if v == "" {
		return "not set"
	}
	return v
}

// History returns the newest n recorded changes to the limits, the
// watchlist, the strategy file's hash, the mode and the confirmation of live
// mode, newest first.
func History(ctx context.Context, q Querier, n int) ([]Change, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, changed_at, setting, old_value, new_value, source FROM settings_changes
		 WHERE setting IN (?, ?, ?, ?) OR setting LIKE 'limits.%' ORDER BY id DESC LIMIT ?`,
		HashSetting, WatchSetting, ModeSetting, LiveSetting, n)
	if err != nil {
		return nil, fmt.Errorf("limits: reading changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Change
	for rows.Next() {
		var c Change
		var at, src string
		var oldV, newV sql.NullString
		if err := rows.Scan(&c.ID, &at, &c.Setting, &oldV, &newV, &src); err != nil {
			return nil, fmt.Errorf("limits: reading a change: %w", err)
		}
		if c.At, err = store.ParseTime(at); err != nil {
			return nil, fmt.Errorf("%w: change %d: %w", ErrCorrupt, c.ID, err)
		}
		c.Old, c.New, c.Source = oldV.String, newV.String, Source(src)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("limits: reading changes: %w", err)
	}
	return out, nil
}
