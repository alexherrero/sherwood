package orders

import (
	"errors"
	"fmt"

	"github.com/alexherrero/sherwood/internal/signals"
)

// State is where an order stands. The states are the design's.
type State string

// The states.
const (
	// Proposed: a rule made a signal and the order record exists.
	Proposed State = "proposed"
	// AwaitingApproval: a buy, or a sell that is not protective, is waiting
	// for your tap.
	AwaitingApproval State = "awaiting_approval"
	// Approved: you approved it (or it is a protective sell that passed the
	// risk check); it waits for the next send window.
	Approved State = "approved"
	// Declined: you declined it, the stop switch declined it, or it expired
	// unapproved at the end of its trading day.
	Declined State = "declined"
	// Blocked: the risk check refused it; the reason is stored.
	Blocked State = "blocked"
	// Submitted: the execution layer sent it to the broker.
	Submitted State = "submitted"
	// Accepted: the broker acknowledged it.
	Accepted State = "accepted"
	// PartiallyFilled: some shares have filled.
	PartiallyFilled State = "partially_filled"
	// Filled: all shares have filled.
	Filled State = "filled"
	// Cancelled: cancelled by you, the stop switch or the broker.
	Cancelled State = "cancelled"
	// Rejected: the broker refused it.
	Rejected State = "rejected"
	// Expired: a day order ended unfilled, or an approved order's day ended
	// before it was sent.
	Expired State = "expired"
	// Unknown: a send or a status check failed and the true state is
	// unclear. Reconciliation settles it by looking it up.
	Unknown State = "unknown"
)

// States lists every state, in the design's order.
var States = []State{
	Proposed, AwaitingApproval, Approved, Declined, Blocked, Submitted,
	Accepted, PartiallyFilled, Filled, Cancelled, Rejected, Expired, Unknown,
}

// Valid reports whether s is one of the states.
func (s State) Valid() bool {
	for _, x := range States {
		if s == x {
			return true
		}
	}
	return false
}

// Terminal reports whether no move leaves s.
func (s State) Terminal() bool {
	return s.Valid() && len(transitions[s]) == 0
}

// Only limits a transition to some kinds of order.
type Only int

// The limits.
const (
	// AnyKind: the move is allowed for every kind.
	AnyKind Only = iota
	// ProtectiveOnly: only a protective sell (signals.KindStopSell) may make
	// the move. Protective sells skip approval.
	ProtectiveOnly
	// NeedsApproval: every kind except a protective sell. Buys and ordinary
	// sells must wait for your tap.
	NeedsApproval
)

func (o Only) allows(k signals.Kind) bool {
	switch o {
	case ProtectiveOnly:
		return k == signals.KindStopSell
	case NeedsApproval:
		return k != signals.KindStopSell
	default:
		return true
	}
}

// transitions is the state machine: for each state, the states an order may
// move to next and which kinds may make that move. It is the only copy of
// the table; everything that moves an order goes through Allowed. A state
// with no entry is terminal.
//
// Two moves are worth naming:
//
//   - A send goes approved → submitted before the broker is called, then to
//     accepted, rejected or unknown on the answer, so a crash mid-call leaves
//     submitted, which reconciliation looks up rather than resends.
//   - Filled and partially_filled are reached only by RecordFill, which writes
//     the fill in the same transaction.
var transitions = map[State]map[State]Only{
	Proposed: {
		AwaitingApproval: NeedsApproval,
		Approved:         ProtectiveOnly,
		Blocked:          AnyKind,
		Declined:         AnyKind,
	},
	AwaitingApproval: {
		Approved: AnyKind,
		Declined: AnyKind,
		Blocked:  AnyKind,
	},
	Approved: {
		Submitted: AnyKind,
		Blocked:   AnyKind,
		Cancelled: AnyKind,
		Expired:   AnyKind,
	},
	Submitted: {
		Accepted:        AnyKind,
		Rejected:        AnyKind,
		PartiallyFilled: AnyKind,
		Filled:          AnyKind,
		Cancelled:       AnyKind,
		Expired:         AnyKind,
		Unknown:         AnyKind,
	},
	Accepted: {
		PartiallyFilled: AnyKind,
		Filled:          AnyKind,
		Cancelled:       AnyKind,
		Rejected:        AnyKind,
		Expired:         AnyKind,
		Unknown:         AnyKind,
	},
	PartiallyFilled: {
		Filled:    AnyKind,
		Cancelled: AnyKind,
		Expired:   AnyKind,
		Unknown:   AnyKind,
	},
	Unknown: {
		Accepted:        AnyKind,
		PartiallyFilled: AnyKind,
		Filled:          AnyKind,
		Cancelled:       AnyKind,
		Rejected:        AnyKind,
		Expired:         AnyKind,
	},
}

// Allowed reports whether an order of kind k may move from one state to
// another. A move to the same state is never allowed.
func Allowed(from, to State, k signals.Kind) bool {
	only, ok := transitions[from][to]
	return ok && only.allows(k)
}

// Next lists the states an order of kind k may move to from s, in the
// design's order.
func Next(s State, k signals.Kind) []State {
	var out []State
	for _, to := range States {
		if Allowed(s, to, k) {
			out = append(out, to)
		}
	}
	return out
}

// ErrIllegalTransition is wrapped by every refused move. The design treats an
// illegal move as a fault that halts trading: callers test for it with
// errors.Is and throw the halt (paper-trading task 3). Use errors.As with
// *TransitionError for the details.
var ErrIllegalTransition = errors.New("orders: illegal state transition")

// TransitionError is a refused move: the order, its kind and the two states.
type TransitionError struct {
	OrderID  int64
	Kind     signals.Kind
	From, To State
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("orders: illegal state transition for order %d (%s): %s → %s", e.OrderID, e.Kind, e.From, e.To)
}

// Unwrap makes errors.Is(err, ErrIllegalTransition) true.
func (e *TransitionError) Unwrap() error { return ErrIllegalTransition }

// check returns a *TransitionError unless the move is allowed.
func check(id int64, k signals.Kind, from, to State) error {
	if !Allowed(from, to, k) {
		return &TransitionError{OrderID: id, Kind: k, From: from, To: to}
	}
	return nil
}
