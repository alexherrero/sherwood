// Package brokertest is the fake broker adapter for tests. It answers from a
// script, records every call and never touches a network, a helper or an
// account.
package brokertest

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/alexherrero/sherwood/internal/broker"
)

// Method names an adapter operation.
type Method string

// The adapter's operations.
const (
	Place      Method = "place"
	Cancel     Method = "cancel"
	Lookup     Method = "lookup"
	OpenOrders Method = "open_orders"
	Holdings   Method = "holdings"
	Account    Method = "account"
)

// Call is one call the fake received.
type Call struct {
	Method Method
	// Order is the order passed to Place.
	Order broker.Order
	// Ref is the reference passed to Cancel or Lookup, or the order's
	// reference for Place.
	Ref string
}

type answer struct {
	report broker.Report
	err    error
}

// ErrUnknownOrder is what a tracking Fake answers for a reference it was never
// sent.
var ErrUnknownOrder = errors.New("brokertest: the fake has no order with this reference")

// Fake is a scripted broker.Adapter. With nothing scripted, Place accepts a
// well-formed order and refuses a malformed one (as Order.Check finds it),
// Cancel cancels, Lookup reports the order accepted, and the listing and
// account calls return the fields below. It is safe for concurrent use.
//
// Set Track for a fake that keeps a book as a broker would: the unscripted
// answers then remember each accepted order, Cancel calls it off (and answers
// a finished order's status), Lookup and Cancel of a reference never placed
// fail with ErrUnknownOrder, and OpenOrders lists the book's open orders
// after Open.
type Fake struct {
	mu      sync.Mutex
	calls   []Call
	scripts map[Method][]answer
	errs    map[Method][]error
	unwell  bool
	book    map[string]broker.Status

	// Track turns on the book; see above. Set it before the first call.
	Track bool

	// Open, Positions and Acct are what OpenOrders, Holdings and Account
	// return when no error is scripted for them.
	Open      []broker.Report
	Positions []broker.Position
	Acct      broker.Account
}

var _ broker.Adapter = (*Fake)(nil)
var _ broker.Health = (*Fake)(nil)

// Answer scripts the next unscripted call of m (Place, Cancel or Lookup) to
// return r and err. Answers are used in the order they were scripted.
func (f *Fake) Answer(m Method, r broker.Report, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scripts == nil {
		f.scripts = map[Method][]answer{}
	}
	f.scripts[m] = append(f.scripts[m], answer{r, err})
}

// Fail scripts the next unscripted call of m to fail with err.
func (f *Fake) Fail(m Method, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[Method][]error{}
	}
	f.errs[m] = append(f.errs[m], err)
}

// SetHealthy sets what Healthy reports; a new Fake is healthy.
func (f *Fake) SetHealthy(ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unwell = !ok
}

// Healthy reports the health SetHealthy last set.
func (f *Fake) Healthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.unwell
}

// Calls returns every call so far, oldest first.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Count returns how many calls of m the fake has received.
func (f *Fake) Count(m Method) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Method == m {
			n++
		}
	}
	return n
}

// next records the call and returns its scripted answer, if any.
func (f *Fake) next(c Call) (answer, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if errs := f.errs[c.Method]; len(errs) > 0 {
		f.errs[c.Method] = errs[1:]
		return answer{err: errs[0]}, true
	}
	if as := f.scripts[c.Method]; len(as) > 0 {
		f.scripts[c.Method] = as[1:]
		return as[0], true
	}
	return answer{}, false
}

// Place records the order and answers from the script, or accepts a
// well-formed order and refuses a malformed one.
func (f *Fake) Place(_ context.Context, o broker.Order) (broker.Report, error) {
	if a, ok := f.next(Call{Method: Place, Order: o, Ref: o.Ref}); ok {
		return a.report, a.err
	}
	if err := o.Check(); err != nil {
		return broker.Report{Ref: o.Ref, Status: broker.StatusRejected, Detail: err.Error()}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Track {
		if f.book == nil {
			f.book = map[string]broker.Status{}
		}
		f.book[o.Ref] = broker.StatusAccepted
	}
	return broker.Report{Ref: o.Ref, Status: broker.StatusAccepted}, nil
}

// Cancel records the call and answers from the script, or cancels.
func (f *Fake) Cancel(_ context.Context, ref string) (broker.Report, error) {
	if a, ok := f.next(Call{Method: Cancel, Ref: ref}); ok {
		return a.report, a.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.Track {
		return broker.Report{Ref: ref, Status: broker.StatusCancelled}, nil
	}
	st, ok := f.book[ref]
	switch {
	case !ok:
		return broker.Report{}, ErrUnknownOrder
	case open(st):
		st = broker.StatusCancelled
		f.book[ref] = st
	}
	return broker.Report{Ref: ref, Status: st}, nil
}

// Lookup records the call and answers from the script, or reports the order
// accepted (a tracking fake reports its book).
func (f *Fake) Lookup(_ context.Context, ref string) (broker.Report, error) {
	if a, ok := f.next(Call{Method: Lookup, Ref: ref}); ok {
		return a.report, a.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.Track {
		return broker.Report{Ref: ref, Status: broker.StatusAccepted}, nil
	}
	st, ok := f.book[ref]
	if !ok {
		return broker.Report{}, ErrUnknownOrder
	}
	return broker.Report{Ref: ref, Status: st}, nil
}

// OpenOrders records the call and returns Open (and a tracking fake's open
// orders, by reference), or a scripted error.
func (f *Fake) OpenOrders(context.Context) ([]broker.Report, error) {
	if a, ok := f.next(Call{Method: OpenOrders}); ok && a.err != nil {
		return nil, a.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]broker.Report(nil), f.Open...)
	var refs []string
	for ref, st := range f.book {
		if open(st) {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	for _, ref := range refs {
		out = append(out, broker.Report{Ref: ref, Status: f.book[ref]})
	}
	return out, nil
}

// open reports whether an order with status st is still open at the broker.
func open(st broker.Status) bool {
	return st == broker.StatusAccepted || st == broker.StatusPartiallyFilled
}

// Holdings records the call and returns Positions, or a scripted error.
func (f *Fake) Holdings(context.Context) ([]broker.Position, error) {
	if a, ok := f.next(Call{Method: Holdings}); ok && a.err != nil {
		return nil, a.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]broker.Position(nil), f.Positions...), nil
}

// Account records the call and returns Acct, or a scripted error.
func (f *Fake) Account(context.Context) (broker.Account, error) {
	if a, ok := f.next(Call{Method: Account}); ok && a.err != nil {
		return broker.Account{}, a.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Acct, nil
}
