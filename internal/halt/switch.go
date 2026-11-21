package halt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/store"
)

// Errors from Throw and Reset.
var (
	// ErrInvalid is a cause, evidence or device that cannot be recorded.
	ErrInvalid = errors.New("halt: invalid")
	// ErrNoReason is a reset without a reason, or with one longer than
	// MaxReason.
	ErrNoReason = errors.New("halt: a reset needs a reason")
	// ErrNotHalted is a reset while the switch is off.
	ErrNotHalted = errors.New("halt: the stop switch is not on")
	// ErrStale is a reset that does not name the newest halt: one was thrown
	// after the page you reset from was loaded, so you have not seen it.
	ErrStale = errors.New("halt: a newer halt has not been seen yet")
)

// Config is what a Switch needs. Every field is required.
type Config struct {
	Store *store.Store
	Clock clock.Clock
	// Notifier receives notify.TradingHalted on every new halt. Pass
	// notify.Nowhere when notifications are not set up, never nil.
	Notifier notify.Notifier
	Logger   *slog.Logger
}

// Switch throws and resets the stop switch. It is safe for concurrent use:
// every write goes through the store's one writer.
type Switch struct {
	store  *store.Store
	clock  clock.Clock
	notify notify.Notifier
	log    *slog.Logger
}

// New builds a Switch.
func New(cfg Config) (*Switch, error) {
	if cfg.Store == nil || cfg.Clock == nil || cfg.Notifier == nil || cfg.Logger == nil {
		return nil, errors.New("halt: the switch needs a store, a clock, a notifier and a logger")
	}
	return &Switch{store: cfg.Store, clock: cfg.Clock, notify: cfg.Notifier, log: cfg.Logger}, nil
}

// Throw halts trading for cause, recording the evidence, and notifies your
// phone. thrown is false when an active halt already has the same cause and
// evidence: then nothing is recorded or sent, and that halt is returned.
//
// Throw writes on the store's writer, so never call it while holding a
// transaction there (orders.Update included): that deadlocks. Roll back or
// commit first.
//
// A failed notification does not fail the throw: the halt is recorded and
// the failure logged. An error means the halt was not recorded; the caller
// must treat that as at least as serious as the halt itself.
func (s *Switch) Throw(ctx context.Context, cause Cause, evidence string) (h Halt, thrown bool, err error) {
	return s.throw(ctx, cause, evidence, false)
}

func (s *Switch) throw(ctx context.Context, cause Cause, evidence string, anyEvidence bool) (h Halt, thrown bool, err error) {
	if !cause.Valid() {
		return Halt{}, false, fmt.Errorf("%w: cause %q is not a short snake_case word", ErrInvalid, cause)
	}
	evidence = clip(strings.TrimSpace(evidence), MaxEvidence)
	if evidence == "" {
		return Halt{}, false, fmt.Errorf("%w: a halt needs evidence", ErrInvalid)
	}
	h, thrown, err = s.record(ctx, cause, evidence, anyEvidence)
	if err != nil {
		s.log.Error("trading must halt, but the halt could not be recorded",
			"halt_cause", string(cause), "error", err)
		return Halt{}, false, err
	}
	if !thrown {
		s.log.Info("already halted for this", "halt_cause", string(cause))
		return h, false, nil
	}
	s.log.Error("trading halted", "halt_cause", string(cause))
	if err := s.notify.Notify(ctx, notify.TradingHalted()); err != nil {
		s.log.Warn("the halt notification was not sent", "halt_cause", string(cause), "reason", err.Error())
	}
	return h, true, nil
}

// ThrowOnce is Throw for a cause that keeps recurring while one problem
// lasts, such as a broker helper that crashes and is restarted again and
// again, or a broker session that has expired: while any halt with the same
// cause is active, whatever its evidence, nothing is recorded or sent, and
// that halt is returned with thrown false. So one problem gives one halt and
// one push until you reset the switch; after a reset, the next one halts
// again.
func (s *Switch) ThrowOnce(ctx context.Context, cause Cause, evidence string) (h Halt, thrown bool, err error) {
	return s.throw(ctx, cause, evidence, true)
}

// record writes the halt unless an active one matches it: exactly, or by
// cause alone when anyEvidence is set.
func (s *Switch) record(ctx context.Context, cause Cause, evidence string, anyEvidence bool) (h Halt, thrown bool, err error) {
	err = s.write(ctx, func(tx *sql.Tx) error {
		var at string
		err := tx.QueryRowContext(ctx,
			`SELECT id, evidence, at FROM halts
			 WHERE cause = ? AND (? OR evidence = ?)
			   AND id > (SELECT coalesce(max(through_halt_id), 0) FROM halt_resets)
			 ORDER BY id DESC LIMIT 1`, string(cause), anyEvidence, evidence).Scan(&h.ID, &h.Evidence, &at)
		switch {
		case err == nil:
			if h.At, err = store.ParseTime(at); err != nil {
				return err
			}
			h.Cause = cause
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		now := s.clock.Now()
		res, err := tx.ExecContext(ctx, `INSERT INTO halts (cause, evidence, at) VALUES (?, ?, ?)`,
			string(cause), evidence, store.FormatTime(now))
		if err != nil {
			return err
		}
		if h.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		h.Cause, h.Evidence, h.At, thrown = cause, evidence, now.UTC(), true
		return nil
	})
	if err != nil {
		return Halt{}, false, fmt.Errorf("halt: recording the halt: %w", err)
	}
	return h, thrown, nil
}

// ResetRequest is a reset from the phone app.
type ResetRequest struct {
	// Through is the newest halt the page showed (State.Newest). The reset
	// is refused with ErrStale if a newer one has been thrown since.
	Through int64
	// Reason is why you are resetting, in your words. Required.
	Reason string
	// Device is the approval device's tailnet address.
	Device string
}

// Reset turns the switch off, clearing every active halt, and records why
// and from which device. It is refused with ErrNoReason without a reason,
// ErrNotHalted when the switch is off, and ErrStale when a halt newer than
// req.Through has been thrown, so the switch stays on until you have seen
// every halt.
func (s *Switch) Reset(ctx context.Context, req ResetRequest) (Reset, error) {
	reason := strings.TrimSpace(req.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > MaxReason {
		return Reset{}, ErrNoReason
	}
	device := strings.TrimSpace(req.Device)
	if device == "" || utf8.RuneCountInString(device) > MaxDevice {
		return Reset{}, fmt.Errorf("%w: a reset needs the device it came from", ErrInvalid)
	}
	r := Reset{Through: req.Through, Reason: reason, Device: device}
	err := s.write(ctx, func(tx *sql.Tx) error {
		var cleared, newest int64
		if err := tx.QueryRowContext(ctx,
			`SELECT (SELECT coalesce(max(through_halt_id), 0) FROM halt_resets),
			        (SELECT coalesce(max(id), 0) FROM halts)`).Scan(&cleared, &newest); err != nil {
			return err
		}
		if newest <= cleared {
			return ErrNotHalted
		}
		if req.Through != newest {
			return ErrStale
		}
		r.At = s.clock.Now().UTC()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO halt_resets (through_halt_id, reason, device, at) VALUES (?, ?, ?, ?)`,
			r.Through, r.Reason, r.Device, store.FormatTime(r.At))
		if err != nil {
			return err
		}
		r.ID, err = res.LastInsertId()
		return err
	})
	switch {
	case errors.Is(err, ErrNotHalted), errors.Is(err, ErrStale):
		return Reset{}, err
	case err != nil:
		return Reset{}, fmt.Errorf("halt: recording the reset: %w", err)
	}
	s.log.Warn("stop switch reset: trading may resume")
	return r, nil
}

// OrderError throws the switch when err is an illegal order state change
// (orders.ErrIllegalTransition), and returns err either way. Put the result
// of every orders.Update that moves an order through it, after the
// transaction has ended:
//
//	err := orders.Update(ctx, st, func(tx *sql.Tx) error { ... orders.Move ... })
//	if err = sw.OrderError(ctx, err); err != nil { ... }
//
// A refused move writes nothing, so rolling back loses nothing. If the halt
// cannot be recorded, that error is joined to err.
func (s *Switch) OrderError(ctx context.Context, err error) error {
	if !errors.Is(err, orders.ErrIllegalTransition) {
		return err
	}
	evidence := err.Error()
	var te *orders.TransitionError
	if errors.As(err, &te) {
		evidence = fmt.Sprintf("Order %d (%s) was asked to move from %s to %s, which the order state machine does not allow.",
			te.OrderID, te.Kind, te.From, te.To)
	}
	if _, _, terr := s.Throw(ctx, IllegalTransition, evidence); terr != nil {
		return errors.Join(err, terr)
	}
	return err
}

// write runs fn in one transaction on the store's writer.
func (s *Switch) write(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.store.Writer().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// clip cuts s to at most n characters, ending with an ellipsis when cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
