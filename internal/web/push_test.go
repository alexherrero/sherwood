package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/webpush"
	"github.com/alexherrero/sherwood/internal/notify/webpush/webpushtest"
	"github.com/alexherrero/sherwood/internal/secret"
)

const pushEndpoint = "https://web.push.apple.com/QGRldmljZS1vbmU"

type pushFixture struct {
	*fixture
	sender  *webpush.Sender
	service *webpushtest.Service
	public  string
}

// newPushFixture builds a server whose push channel is the real web push
// sender over a fake push service, so nothing reaches the network.
func newPushFixture(t *testing.T) *pushFixture {
	t.Helper()
	pf := &pushFixture{service: &webpushtest.Service{}}
	pf.fixture = newFixtureWith(t, config.ModePaper, func(f *fixture, cfg *Config) {
		pub, priv := webpushtest.VAPIDKeys(t)
		sender, err := webpush.New(webpush.Config{
			PublicKey: pub, PrivateKey: secret.New(priv), Contact: "mailto:you@example.com",
			Client: pf.service, Store: f.store, Clock: f.clock, Logger: cfg.Logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		pf.sender, pf.public = sender, pub
		cfg.Push = sender
	})
	return pf
}

func (f *fixture) postForm(path string, c *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(req{method: http.MethodPost, path: path, form: form, cookie: c})
}

func wantRedirect(t *testing.T, w *httptest.ResponseRecorder, to string) {
	t.Helper()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != to {
		t.Errorf("answer = %d to %q, want 303 to %q", w.Code, w.Header().Get("Location"), to)
	}
}

func subscribeForm(token string, d *webpushtest.Device) url.Values {
	return url.Values{TokenField: {token}, "endpoint": {d.Endpoint}, "p256dh": {d.P256dh()}, "auth": {d.Auth()}}
}

func TestSettingsOffersNotificationsWithTheVAPIDKeyAndToken(t *testing.T) {
	f := newPushFixture(t)
	c, _ := f.session()
	w := f.do(req{method: http.MethodGet, path: "/settings", cookie: c})
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("GET /settings = %d", w.Code)
	}
	for _, want := range []string{
		`<form method="post" action="/notifications/subscribe" id="push-subscribe" data-key="` + f.public + `">`,
		`<form method="post" action="/notifications/test">`,
		"No device has notifications on yet.",
		"add sherwood to your home screen first",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Settings lacks %q", want)
		}
	}
	// Both notification forms, and the tighten form, carry the token for
	// this session.
	tokens := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(tokens) != 3 {
		t.Fatalf("found %d token fields, want 3", len(tokens))
	}
	resp := f.postForm(testPushPath, c, url.Values{TokenField: {tokens[1][1]}})
	wantRedirect(t, resp, "/settings?push=none")
}

func TestSettingsSaysWhenNotificationsAreNotSetUp(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	body := f.do(req{method: http.MethodGet, path: "/settings", cookie: c}).Body.String()
	if !strings.Contains(body, "Notifications are not set up.") || strings.Contains(body, "push-subscribe") {
		t.Errorf("Settings without a push channel:\n%s", body)
	}
	d := webpushtest.NewDevice(t, pushEndpoint)
	wantRedirect(t, f.postForm(subscribePath, c, subscribeForm(token, d)), "/settings?push=off")
	wantRedirect(t, f.postForm(testPushPath, c, url.Values{TokenField: {token}}), "/settings?push=off")
}

func TestTurningOnNotificationsThenATestPushReachesThePhone(t *testing.T) {
	f := newPushFixture(t)
	c, token := f.session()
	d := webpushtest.NewDevice(t, pushEndpoint)

	wantRedirect(t, f.postForm(subscribePath, c, subscribeForm(token, d)), "/settings?push=on")
	body := f.do(req{method: http.MethodGet, path: "/settings?push=on", cookie: c}).Body.String()
	for _, want := range []string{"Notifications are on for this device.", "1 device has notifications on."} {
		if !strings.Contains(body, want) {
			t.Errorf("Settings lacks %q", want)
		}
	}

	wantRedirect(t, f.postForm(testPushPath, c, url.Values{TokenField: {token}}), "/settings?push=sent")
	reqs := f.service.Requests()
	if len(reqs) != 1 {
		t.Fatalf("the push service got %d requests, want 1", len(reqs))
	}
	plain, err := d.Decrypt(reqs[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := notify.Test().Payload()
	if !bytes.Equal(plain, want) {
		t.Errorf("the phone read %s, want %s", plain, want)
	}

	// A push service failure is reported on Settings, not retried.
	f.service.Status = http.StatusInternalServerError
	wantRedirect(t, f.postForm(testPushPath, c, url.Values{TokenField: {token}}), "/settings?push=failed")
	if n := len(f.service.Requests()); n != 2 {
		t.Errorf("the push service got %d requests, want 2", n)
	}

	if log := f.log.String(); strings.Contains(log, "QGRldmljZS1vbmU") || strings.Contains(log, d.P256dh()) ||
		strings.Contains(log, d.Auth()) {
		t.Errorf("the log holds the subscription:\n%s", log)
	}
}

func TestTheNotificationActionsNeedTheTokenAndAPost(t *testing.T) {
	f := newPushFixture(t)
	c, token := f.session()
	d := webpushtest.NewDevice(t, pushEndpoint)

	noToken := subscribeForm("", d)
	if resp := f.postForm(subscribePath, c, noToken); resp.Code != http.StatusForbidden {
		t.Errorf("subscribe without a token = %d, want 403", resp.Code)
	}
	if resp := f.postForm(testPushPath, c, url.Values{}); resp.Code != http.StatusForbidden {
		t.Errorf("test push without a token = %d, want 403", resp.Code)
	}
	for _, p := range []string{subscribePath, testPushPath} {
		if w := f.do(req{method: http.MethodGet, path: p + "?" + subscribeForm(token, d).Encode(), cookie: c}); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", p, w.Code)
		}
	}
	if n, _ := f.sender.Count(context.Background()); n != 0 {
		t.Errorf("%d subscriptions saved without a valid POST", n)
	}
	if n := len(f.service.Requests()); n != 0 {
		t.Errorf("%d pushes sent without a valid POST", n)
	}
}

func TestABadSubscriptionIsRefusedAndNotSaved(t *testing.T) {
	f := newPushFixture(t)
	c, token := f.session()
	d := webpushtest.NewDevice(t, "https://192.168.1.10/push")
	wantRedirect(t, f.postForm(subscribePath, c, subscribeForm(token, d)), "/settings?push=bad")
	// The form posted without JavaScript has empty fields.
	wantRedirect(t, f.postForm(subscribePath, c, url.Values{TokenField: {token}}), "/settings?push=bad")
	if n, _ := f.sender.Count(context.Background()); n != 0 {
		t.Errorf("%d bad subscriptions saved", n)
	}
	body := f.do(req{method: http.MethodGet, path: "/settings?push=bad", cookie: c}).Body.String()
	if !strings.Contains(body, "Notifications could not be turned on.") {
		t.Error("Settings does not say the subscription was refused")
	}
	// An unknown result shows nothing.
	body = f.do(req{method: http.MethodGet, path: "/settings?push=%3Cscript%3E", cookie: c}).Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, `role="status" aria-label="Notifications"`) {
		t.Error("an unknown result was shown")
	}
}

func TestTheWorkerShowsPushesAndOpensOnlyTheAppsOwnViews(t *testing.T) {
	sw := string(shell.worker.body)
	for _, want := range []string{
		"addEventListener('push'",
		"addEventListener('notificationclick'",
		"showNotification('Sherwood'",
		"openWindow(target)",
		`/^\/[a-z]{0,20}$/`,
	} {
		if !strings.Contains(sw, want) {
			t.Errorf("sw.js lacks %q", want)
		}
	}
	// The push handler must not cache anything either.
	if strings.Contains(sw, ".put(") || strings.Count(sw, "caches.open(") != 2 {
		t.Error("sw.js caches something other than the shell")
	}
	// Every alert opens a view the app serves.
	reads := map[string]bool{}
	for _, v := range views {
		reads[v.path] = true
	}
	for _, a := range []notify.Alert{notify.Test(), notify.BuysWaiting(1), notify.TradingHalted()} {
		if !reads[a.Path()] {
			t.Errorf("alert %s opens %s, which is not a view", a.Name(), a.Path())
		}
	}
}
