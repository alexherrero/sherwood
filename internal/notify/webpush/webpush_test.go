package webpush_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/webpush"
	"github.com/alexherrero/sherwood/internal/notify/webpush/webpushtest"
	"github.com/alexherrero/sherwood/internal/secret"
	"github.com/alexherrero/sherwood/internal/store"
)

const (
	contact = "mailto:you@example.com"
	phone   = "https://web.push.apple.com/QGRldmljZS1vbmU"
	android = "https://fcm.googleapis.com/fcm/send/device-two"
)

type fixture struct {
	t       *testing.T
	sender  *webpush.Sender
	service *webpushtest.Service
	store   *store.Store
	clock   *clock.Fake
	log     *loggingtest.Output
	public  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	start := time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), start)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logger, out := loggingtest.New(t)
	pub, priv := webpushtest.VAPIDKeys(t)
	f := &fixture{t: t, service: &webpushtest.Service{}, store: st, clock: clock.NewFake(start), log: out, public: pub}
	f.sender, err = webpush.New(webpush.Config{
		PublicKey: pub, PrivateKey: secret.New(priv), Contact: contact,
		Client: f.service, Store: st, Clock: f.clock, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) subscribe(d *webpushtest.Device) {
	f.t.Helper()
	err := f.sender.Subscribe(context.Background(), webpush.Subscription{Endpoint: d.Endpoint, P256dh: d.P256dh(), Auth: d.Auth()})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) count() int {
	f.t.Helper()
	n, err := f.sender.Count(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestThePhoneReceivesOnlyTheShortAlert(t *testing.T) {
	f := newFixture(t)
	d := webpushtest.NewDevice(t, phone)
	f.subscribe(d)

	alerts := []notify.Alert{notify.Test(), notify.BuysWaiting(2), notify.TradingHalted()}
	for _, a := range alerts {
		if err := f.sender.Notify(context.Background(), a); err != nil {
			t.Fatalf("%s: %v", a.Name(), err)
		}
	}
	reqs := f.service.Requests()
	if len(reqs) != len(alerts) {
		t.Fatalf("the push service got %d requests, want %d", len(reqs), len(alerts))
	}
	for i, r := range reqs {
		a := alerts[i]
		if r.Endpoint != phone {
			t.Errorf("sent to the wrong endpoint")
		}
		plain, err := d.Decrypt(r.Body)
		if err != nil {
			t.Fatalf("%s: the phone could not decrypt the message: %v", a.Name(), err)
		}
		want, _ := a.Payload()
		if !bytes.Equal(plain, want) {
			t.Errorf("%s: the phone read %s, want exactly %s", a.Name(), plain, want)
		}
		var p notify.Payload
		dec := json.NewDecoder(bytes.NewReader(plain))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			t.Errorf("%s: payload has more than text and path: %v", a.Name(), err)
		}
		if bytes.Contains(r.Body, []byte(a.Text())) {
			t.Errorf("%s: the alert's words travel unencrypted", a.Name())
		}
		h := r.Header
		if h.Get("Content-Encoding") != "aes128gcm" || h.Get("Urgency") != "high" || h.Get("TTL") != "43200" ||
			h.Get("Topic") != a.Name() {
			t.Errorf("%s: headers = %v", a.Name(), h)
		}
		if auth := h.Get("Authorization"); !strings.HasPrefix(auth, "vapid t=") || !strings.HasSuffix(auth, ", k="+f.public) {
			t.Errorf("%s: no VAPID authorization with the configured public key", a.Name())
		}
	}
	if !strings.Contains(f.log.String(), `"push_alert":"trading_halted","push_delivered":1`) {
		t.Errorf("no push line in the log:\n%s", f.log)
	}
}

func TestNotifyGoesToEveryDeviceAndDropsEndedOnes(t *testing.T) {
	f := newFixture(t)
	a, b := webpushtest.NewDevice(t, phone), webpushtest.NewDevice(t, android)
	f.subscribe(a)
	f.subscribe(b)
	f.service.Answer(android, http.StatusGone)

	if err := f.sender.Notify(context.Background(), notify.TradingHalted()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.service.Requests()); n != 2 {
		t.Fatalf("sent %d, want 2", n)
	}
	if n := f.count(); n != 1 {
		t.Errorf("%d subscriptions left, want 1 after a 410", n)
	}

	// With the only one left also gone, nothing reaches anyone.
	f.service.Answer(phone, http.StatusNotFound)
	err := f.sender.Notify(context.Background(), notify.TradingHalted())
	if !errors.Is(err, notify.ErrNoRecipients) {
		t.Errorf("err = %v, want ErrNoRecipients", err)
	}
	if n := f.count(); n != 0 {
		t.Errorf("%d subscriptions left, want 0", n)
	}
	if err := f.sender.Notify(context.Background(), notify.TradingHalted()); !errors.Is(err, notify.ErrNoRecipients) {
		t.Errorf("err = %v with no subscriptions, want ErrNoRecipients", err)
	}
}

func TestAFailedSendIsReportedWithoutTheEndpointAndNotRetried(t *testing.T) {
	f := newFixture(t)
	f.subscribe(webpushtest.NewDevice(t, phone))
	for _, c := range []struct {
		status int
		err    error
	}{
		{status: http.StatusTooManyRequests},
		{status: http.StatusInternalServerError},
		{err: errors.New(`Post "` + phone + `": dial tcp: lookup failed`)},
	} {
		before := len(f.service.Requests())
		f.service.Status, f.service.Err = c.status, c.err
		err := f.sender.Notify(context.Background(), notify.BuysWaiting(3))
		if err == nil {
			t.Fatalf("status %d / %v: no error", c.status, c.err)
		}
		if strings.Contains(err.Error(), "push.apple.com") {
			t.Errorf("the error names the endpoint: %v", err)
		}
		if n := len(f.service.Requests()) - before; n != 1 {
			t.Errorf("sent %d times, want exactly 1 (no retry)", n)
		}
		if f.count() != 1 {
			t.Error("a failed send removed the subscription")
		}
	}
	if strings.Contains(f.log.String(), "push.apple.com") || strings.Contains(f.log.String(), "QGRldmljZS1vbmU") {
		t.Errorf("the log holds the endpoint:\n%s", f.log)
	}
}

func TestSubscribeKeepsOnlyKnownPushServicesAndGoodKeys(t *testing.T) {
	f := newFixture(t)
	d := webpushtest.NewDevice(t, phone)
	bad := map[string]webpush.Subscription{
		"plain http":        {Endpoint: "http://web.push.apple.com/x", P256dh: d.P256dh(), Auth: d.Auth()},
		"unknown host":      {Endpoint: "https://push.example.com/x", P256dh: d.P256dh(), Auth: d.Auth()},
		"lookalike host":    {Endpoint: "https://evilpush.apple.com.example.com/x", P256dh: d.P256dh(), Auth: d.Auth()},
		"bare suffix":       {Endpoint: "https://push.apple.com/x", P256dh: d.P256dh(), Auth: d.Auth()},
		"the NAS":           {Endpoint: "https://192.168.1.10/x", P256dh: d.P256dh(), Auth: d.Auth()},
		"another port":      {Endpoint: "https://web.push.apple.com:8443/x", P256dh: d.P256dh(), Auth: d.Auth()},
		"user part":         {Endpoint: "https://me@web.push.apple.com/x", P256dh: d.P256dh(), Auth: d.Auth()},
		"too long":          {Endpoint: phone + strings.Repeat("a", webpush.MaxEndpointLen), P256dh: d.P256dh(), Auth: d.Auth()},
		"no keys":           {Endpoint: phone},
		"short auth":        {Endpoint: phone, P256dh: d.P256dh(), Auth: "AAAA"},
		"not a curve point": {Endpoint: phone, P256dh: strings.Repeat("A", 87), Auth: d.Auth()},
	}
	for name, sub := range bad {
		if err := f.sender.Subscribe(context.Background(), sub); !errors.Is(err, webpush.ErrBadSubscription) {
			t.Errorf("%s: err = %v, want ErrBadSubscription", name, err)
		}
	}
	if n := f.count(); n != 0 {
		t.Fatalf("%d bad subscriptions were saved", n)
	}

	// Subscribing again on the same phone replaces its keys.
	f.subscribe(d)
	again := webpushtest.NewDevice(t, phone)
	f.subscribe(again)
	if n := f.count(); n != 1 {
		t.Errorf("%d subscriptions after re-subscribing, want 1", n)
	}
	if err := f.sender.Notify(context.Background(), notify.Test()); err != nil {
		t.Fatal(err)
	}
	body := f.service.Requests()[0].Body
	if _, err := again.Decrypt(body); err != nil {
		t.Errorf("the alert was not encrypted with the newer keys: %v", err)
	}
	if _, err := d.Decrypt(body); err == nil {
		t.Error("the replaced keys can still read the alert")
	}
}

func TestSubscriptionsAreCapped(t *testing.T) {
	f := newFixture(t)
	for i := range webpush.MaxSubscriptions {
		f.subscribe(webpushtest.NewDevice(t, phone+string(rune('a'+i))))
	}
	extra := webpushtest.NewDevice(t, android)
	err := f.sender.Subscribe(context.Background(), webpush.Subscription{Endpoint: extra.Endpoint, P256dh: extra.P256dh(), Auth: extra.Auth()})
	if !errors.Is(err, webpush.ErrTooManySubscriptions) {
		t.Errorf("err = %v, want ErrTooManySubscriptions", err)
	}
	// An existing device can still renew its keys.
	f.subscribe(webpushtest.NewDevice(t, phone+"a"))
	if n := f.count(); n != webpush.MaxSubscriptions {
		t.Errorf("count = %d", n)
	}
}

func TestNewRefusesBadKeysWithoutEchoingThem(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logger, _ := loggingtest.New(t)
	pub, priv := webpushtest.VAPIDKeys(t)
	otherPub, _ := webpushtest.VAPIDKeys(t)
	base := webpush.Config{Contact: contact, Client: &webpushtest.Service{}, Store: st, Clock: clock.NewFake(time.Now()), Logger: logger}
	cases := map[string]struct{ pub, priv string }{
		"placeholder private": {pub, "replace-me-with-the-vapid-private-key"},
		"placeholder public":  {"replace-me-with-the-vapid-public-key", priv},
		"mismatched pair":     {otherPub, priv},
		"public as private":   {pub, pub},
	}
	for name, c := range cases {
		cfg := base
		cfg.PublicKey, cfg.PrivateKey = c.pub, secret.New(c.priv)
		_, err := webpush.New(cfg)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), c.priv) || strings.Contains(err.Error(), priv) {
			t.Errorf("%s: the error holds the private key: %v", name, err)
		}
	}
	cfg := base
	cfg.PublicKey, cfg.PrivateKey = pub, secret.New(priv)
	cfg.Client = nil
	if _, err := webpush.New(cfg); err == nil {
		t.Error("New accepted a missing client; a test could then reach a real push service")
	}
}

// TestNoTestCanReachAPushService checks that the only real client is built
// by the running app: no test file in the module calls NewHTTPClient, and
// New refuses to run without a client (above), so every test sends to a
// fake.
func TestNoTestCanReachAPushService(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "v1" || d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // walking this repository's own files
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewHTTPClient" {
				t.Errorf("%s calls NewHTTPClient, which reaches real push services", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
