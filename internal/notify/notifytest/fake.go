// Package notifytest holds a fake notifier for tests. It records the alerts
// it is asked to send and reaches no push service.
package notifytest

import (
	"context"
	"errors"
	"sync"

	"github.com/alexherrero/sherwood/internal/notify"
)

// Fake records every alert sent through it. Set Err to make each Notify fail
// with that error. It is safe for concurrent use.
type Fake struct {
	mu   sync.Mutex
	sent []notify.Alert
	// Err, when set, is what Notify returns after recording the alert.
	Err error
}

var _ notify.Notifier = (*Fake)(nil)

// Notify records a. An alert not made by a package notify constructor is
// refused, as the real channel refuses it.
func (f *Fake) Notify(_ context.Context, a notify.Alert) error {
	if !a.Valid() {
		return errors.New("notifytest: the alert is not valid")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, a)
	return f.Err
}

// Sent returns the alerts sent so far, oldest first.
func (f *Fake) Sent() []notify.Alert {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notify.Alert(nil), f.sent...)
}
