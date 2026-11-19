package halt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/alexherrero/sherwood/internal/store"
)

// Cause is what threw the switch, as a short snake_case word.
type Cause string

// The causes. The design's other causes (an order whose state is unknown, a
// reconciliation difference, the daily loss limit) are named here so every
// task throws them with the same word.
const (
	// Operator is you, pressing Stop all trading on the phone app.
	Operator Cause = "operator"
	// BrokerError is a failed broker call, or the broker helper failing to
	// start or exiting.
	BrokerError Cause = "broker_error"
	// PriceDisagreement is the two price sources disagreeing on a held
	// stock.
	PriceDisagreement Cause = "price_disagreement"
	// IllegalTransition is an order asked to make a state change the state
	// machine does not allow.
	IllegalTransition Cause = "illegal_transition"
	// UnknownOrder is an order whose state at the broker is unknown.
	UnknownOrder Cause = "unknown_order"
	// Reconciliation is a difference between sherwood's records and the
	// broker's.
	Reconciliation Cause = "reconciliation"
	// DailyLoss is the daily loss limit being reached.
	DailyLoss Cause = "daily_loss"
	// StopsCancelled is you confirming, a second time, that the resting
	// protective stops are cancelled too. While a halt with this cause is
	// active, no protective stop is placed or raised.
	StopsCancelled Cause = "stops_cancelled"
)

// causeWords are the words Today shows for each cause.
var causeWords = map[Cause]string{
	Operator:          "You pressed Stop all trading",
	BrokerError:       "The broker failed",
	PriceDisagreement: "The price sources disagree on a stock you hold",
	IllegalTransition: "An order was asked to make a change that is not allowed",
	UnknownOrder:      "An order's state at the broker is unknown",
	Reconciliation:    "Sherwood's records and the broker's differ",
	DailyLoss:         "The daily loss limit was reached",
	StopsCancelled:    "You cancelled the protective stops",
}

// Words is the cause in a few plain words, for the phone app. A cause this
// build does not know shows as itself.
func (c Cause) Words() string {
	if w, ok := causeWords[c]; ok {
		return w
	}
	return string(c)
}

// causePattern is the schema's rule for a cause: lowercase letters and
// underscores, starting with a letter, at most 32 characters.
var causePattern = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// Valid reports whether c fits the schema's rule for a cause.
func (c Cause) Valid() bool { return causePattern.MatchString(string(c)) }

// Limits on what is stored.
const (
	// MaxEvidence is the most evidence kept, in characters; longer evidence
	// is cut short with an ellipsis.
	MaxEvidence = 2000
	// MaxReason is the longest reset reason accepted, in characters.
	MaxReason = 500
	// MaxDevice is the longest device address accepted, in characters.
	MaxDevice = 64
)

// Halt is one throw of the switch.
type Halt struct {
	ID       int64
	Cause    Cause
	Evidence string
	At       time.Time
}

// Reset is one reset of the switch. It cleared every halt up to and
// including Through.
type Reset struct {
	ID      int64
	Through int64
	Reason  string
	Device  string
	At      time.Time
}

// State is the switch as it stands.
type State struct {
	// Active lists the halts not yet reset, oldest first. The switch is on
	// exactly when it is not empty.
	Active []Halt
	// LastReset is the newest reset, or nil if the switch was never reset.
	LastReset *Reset
}

// On reports whether the switch is on: trading is halted.
func (s State) On() bool { return len(s.Active) > 0 }

// Has reports whether an active halt has cause c.
func (s State) Has(c Cause) bool {
	for _, h := range s.Active {
		if h.Cause == c {
			return true
		}
	}
	return false
}

// Newest is the id of the newest active halt, which a reset must name, or 0
// when the switch is off.
func (s State) Newest() int64 {
	if len(s.Active) == 0 {
		return 0
	}
	return s.Active[len(s.Active)-1].ID
}

// Querier is what Active reads through: a *sql.DB (such as store.Reader())
// or a *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Active reads the switch's state. Read it inside the same transaction as
// anything else a decision depends on, so both come from one moment.
func Active(ctx context.Context, q Querier) (State, error) {
	var st State
	r, err := lastReset(ctx, q)
	if err != nil {
		return State{}, err
	}
	var cleared int64
	if r != nil {
		st.LastReset, cleared = r, r.Through
	}
	rows, err := q.QueryContext(ctx,
		`SELECT id, cause, evidence, at FROM halts WHERE id > ? ORDER BY id`, cleared)
	if err != nil {
		return State{}, fmt.Errorf("halt: reading halts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h Halt
		var cause, at string
		if err := rows.Scan(&h.ID, &cause, &h.Evidence, &at); err != nil {
			return State{}, fmt.Errorf("halt: reading halts: %w", err)
		}
		h.Cause = Cause(cause)
		if h.At, err = store.ParseTime(at); err != nil {
			return State{}, fmt.Errorf("halt: reading halt %d: %w", h.ID, err)
		}
		st.Active = append(st.Active, h)
	}
	if err := rows.Err(); err != nil {
		return State{}, fmt.Errorf("halt: reading halts: %w", err)
	}
	return st, nil
}

// lastReset reads the newest reset, or nil if there is none.
func lastReset(ctx context.Context, q Querier) (*Reset, error) {
	var r Reset
	var at string
	err := q.QueryRowContext(ctx,
		`SELECT id, through_halt_id, reason, device, at FROM halt_resets ORDER BY id DESC LIMIT 1`).
		Scan(&r.ID, &r.Through, &r.Reason, &r.Device, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("halt: reading the last reset: %w", err)
	}
	if r.At, err = store.ParseTime(at); err != nil {
		return nil, fmt.Errorf("halt: reading reset %d: %w", r.ID, err)
	}
	return &r, nil
}
