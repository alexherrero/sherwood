package broker

import (
	"context"
	"errors"
	"sync"

	"github.com/alexherrero/sherwood/internal/notify"
)

// LoginAlert tells you, once, that the broker needs a new login. Pass every
// broker call's outcome to Seen: the first login-needed error sends
// notify.BrokerLoginNeeded, later ones stay quiet, and the next call that
// succeeds re-arms it, so a session that lapses again is reported again. A
// restart re-arms it too. It is safe for concurrent use.
//
// Nothing calls the broker on a schedule yet. The live adapter (the
// live-trading part) passes its calls through Seen.
type LoginAlert struct {
	Notifier notify.Notifier

	mu   sync.Mutex
	sent bool
}

// Seen records one broker call's outcome and reports whether it sent the
// alert. A failed send is returned with the outcome, and the alert is tried
// again on the next login-needed error.
func (l *LoginAlert) Seen(ctx context.Context, callErr error) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case callErr == nil:
		l.sent = false
		return false, nil
	case !LoginNeeded(callErr) || l.sent:
		return false, nil
	}
	err := l.Notifier.Notify(context.WithoutCancel(ctx), notify.BrokerLoginNeeded())
	if err != nil && !errors.Is(err, notify.ErrNoRecipients) {
		return false, err
	}
	l.sent = true
	return true, nil
}
