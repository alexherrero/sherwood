package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/webpush"
)

// PushChannel is the web push channel as the app uses it: the Settings view
// shows how many devices have notifications on and lets you turn them on and
// send a test. *webpush.Sender implements it.
type PushChannel interface {
	notify.Notifier
	// PublicKey is the VAPID public key the browser subscribes with.
	PublicKey() string
	// Subscribe records a browser's subscription.
	Subscribe(ctx context.Context, sub webpush.Subscription) error
	// Count reports how many devices have notifications on.
	Count(ctx context.Context) (int, error)
}

// The notification actions. Each answers with a redirect back to Settings,
// whose ?push= value picks one of pushResults to show. The value only picks
// fixed words; it never changes anything.
const (
	subscribePath = "/notifications/subscribe"
	testPushPath  = "/notifications/test"
	settingsPath  = "/settings"
)

// pushResults are the words Settings shows after a notification action.
var pushResults = map[string]pushResult{
	"on":     {Class: "notice--info", Title: "Notifications are on for this device.", Body: "Send a test notification to check that one arrives."},
	"bad":    {Class: "notice--warning", Title: "Notifications could not be turned on.", Body: "This browser's push service is not one sherwood sends to. Use Safari on iPhone or Chrome on Android, from the app on your home screen."},
	"full":   {Class: "notice--warning", Title: "Notifications could not be turned on.", Body: "Too many devices already have notifications on."},
	"sent":   {Class: "notice--info", Title: "A test notification was sent.", Body: "It should arrive within a few seconds. If it does not, check that notifications for sherwood are allowed in the phone's settings."},
	"none":   {Class: "notice--warning", Title: "Nothing was sent.", Body: "No device has notifications on. Turn them on first."},
	"failed": {Class: "notice--warning", Title: "The test notification did not reach every device.", Body: "Sherwood's log has a line for each send that failed."},
	"off":    {Class: "notice--warning", Title: "Notifications are not set up.", Body: "Sherwood could not read the VAPID keys in its settings file."},
}

type pushResult struct {
	Class string
	Title string
	Body  string
}

// settingsData is what the Settings view renders.
type settingsData struct {
	// Push is nil when notifications are not set up.
	Push *pushView
	// Result is the outcome of the last notification or limit action, if
	// any.
	Result *pushResult
	// Live is true in live mode.
	Live bool
	// Limits, Other, Changes and Paper are described in settings.go.
	Limits  []limitView
	Other   []otherView
	Changes []changeView
	Paper   paperRecord
}

type pushView struct {
	PublicKey string
	Devices   int
}

func loadSettings(s *Server, r *http.Request) (any, error) {
	d := settingsData{}
	if res, ok := pushResults[r.URL.Query().Get("push")]; ok {
		d.Result = &res
	}
	if res, ok := limitResults[r.URL.Query().Get("limit")]; ok {
		d.Result = &res
	}
	if err := s.loadLimits(r.Context(), &d); err != nil {
		return nil, err
	}
	if s.push == nil {
		return d, nil
	}
	n, err := s.push.Count(r.Context())
	if err != nil {
		return nil, err
	}
	d.Push = &pushView{PublicKey: s.push.PublicKey(), Devices: n}
	return d, nil
}

// mountPush registers the notification actions.
func (s *Server) mountPush() {
	s.HandleChange(subscribePath, http.HandlerFunc(s.subscribe))
	s.HandleChange(testPushPath, http.HandlerFunc(s.testPush))
}

func backToSettings(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, settingsPath+"?push="+result, http.StatusSeeOther)
}

// subscribe records the browser's push subscription, which app.js puts in
// the form's endpoint, p256dh and auth fields.
func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		backToSettings(w, r, "off")
		return
	}
	err := s.push.Subscribe(r.Context(), webpush.Subscription{
		Endpoint: r.PostForm.Get("endpoint"),
		P256dh:   r.PostForm.Get("p256dh"),
		Auth:     r.PostForm.Get("auth"),
	})
	switch {
	case err == nil:
		backToSettings(w, r, "on")
	case errors.Is(err, webpush.ErrBadSubscription):
		backToSettings(w, r, "bad")
	case errors.Is(err, webpush.ErrTooManySubscriptions):
		backToSettings(w, r, "full")
	default:
		refuse(w, r, "the handler failed", http.StatusInternalServerError, "The subscription could not be saved. Try again.")
	}
}

// testPush sends the test alert to every subscribed device.
func (s *Server) testPush(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		backToSettings(w, r, "off")
		return
	}
	err := s.push.Notify(r.Context(), notify.Test())
	switch {
	case err == nil:
		backToSettings(w, r, "sent")
	case errors.Is(err, notify.ErrNoRecipients):
		backToSettings(w, r, "none")
	default:
		backToSettings(w, r, "failed")
	}
}
