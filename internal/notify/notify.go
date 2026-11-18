// Package notify sends short alerts to your phone.
//
// An alert travels through the phone maker's push service, so it carries
// only a few fixed words, such as "2 buys waiting" or "Trading halted", and
// never an amount, a stock, a holding or a reason. Tapping it opens the app
// over the tailnet, where the details are. The words come only from the
// constructors in this package: an Alert has no field a caller can fill with
// free text, so no caller can put anything else in one.
//
// A channel is anything with a Notify method. Web push (package
// notify/webpush) is the first; notifytest.Fake stands in for it in tests.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
)

// Notifier sends an alert to every device that turned notifications on.
type Notifier interface {
	// Notify sends a to every subscribed device. It returns ErrNoRecipients
	// when none is subscribed, and an error naming how many sends failed
	// when any did. Errors never hold a device's address or keys.
	Notify(ctx context.Context, a Alert) error
}

// ErrNoRecipients means no device has turned notifications on, so the alert
// went nowhere.
var ErrNoRecipients = errors.New("notify: no device has turned notifications on")

// kind is what an alert is about. Each kind has fixed words and opens a
// fixed view when tapped.
type kind string

const (
	kindTest          kind = "test"
	kindBuysWaiting   kind = "buys_waiting"
	kindTradingHalted kind = "trading_halted"
)

// Alert is one notification. Make one with Test, BuysWaiting or
// TradingHalted; the zero Alert is not valid and is never sent.
type Alert struct {
	kind  kind
	count int
}

// Test is the alert the Settings view sends to check that notifications
// reach the phone.
func Test() Alert { return Alert{kind: kindTest} }

// BuysWaiting says that n buys are waiting for your approval. It carries the
// count only, never which stocks or how much. A count above 99 shows as "99+".
func BuysWaiting(n int) Alert {
	if n > maxCount {
		n = maxCount
	}
	return Alert{kind: kindBuysWaiting, count: n}
}

// maxCount is where BuysWaiting stops counting; anything above 99 shows as
// "99+".
const maxCount = 100

// TradingHalted says that trading halted. It never says why: the cause is on
// the Today view.
func TradingHalted() Alert { return Alert{kind: kindTradingHalted} }

// Valid reports whether a was made by one of the constructors with sensible
// values.
func (a Alert) Valid() bool {
	switch a.kind {
	case kindTest, kindTradingHalted:
		return a.count == 0
	case kindBuysWaiting:
		return a.count >= 1 && a.count <= maxCount
	}
	return false
}

// Name is the alert's kind as a short snake_case word, for the log.
func (a Alert) Name() string { return string(a.kind) }

// Text is the alert's words, exactly as the phone shows them.
func (a Alert) Text() string {
	switch a.kind {
	case kindTest:
		return "Notifications are working"
	case kindBuysWaiting:
		switch {
		case a.count == 1:
			return "1 buy waiting"
		case a.count > 99:
			return "99+ buys waiting"
		default:
			return strconv.Itoa(a.count) + " buys waiting"
		}
	case kindTradingHalted:
		return "Trading halted"
	}
	return ""
}

// Path is the view tapping the alert opens.
func (a Alert) Path() string {
	switch a.kind {
	case kindBuysWaiting:
		return "/approvals"
	case kindTest:
		return "/settings"
	}
	return "/"
}

// Payload is what a push message carries, before the channel encrypts it:
// the words and the view to open, and nothing else.
type Payload struct {
	Text string `json:"text"`
	Path string `json:"path"`
}

// Payload returns the alert's message body as JSON. It fails for an alert
// that is not Valid.
func (a Alert) Payload() ([]byte, error) {
	if !a.Valid() {
		return nil, errors.New("notify: the alert was not made by a constructor in package notify")
	}
	return json.Marshal(Payload{Text: a.Text(), Path: a.Path()})
}

// Nowhere is a Notifier for when no channel is set up: it sends nothing and
// returns ErrNoRecipients. Hand it on instead of a nil Notifier.
var Nowhere Notifier = nowhere{}

type nowhere struct{}

func (nowhere) Notify(_ context.Context, a Alert) error {
	if !a.Valid() {
		return errors.New("notify: the alert was not made by a constructor in package notify")
	}
	return ErrNoRecipients
}
