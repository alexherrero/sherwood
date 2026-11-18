package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/notify"
)

// Every address and reason here is a placeholder.

// postSwitch sends a form with the token and the given fields.
func (f *fixture) postSwitch(path, token string, c *http.Cookie, headers map[string][]string, fields ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	form := url.Values{TokenField: {token}}
	for i := 0; i+1 < len(fields); i += 2 {
		form.Set(fields[i], fields[i+1])
	}
	return f.do(req{method: http.MethodPost, path: path, form: form, cookie: c, headers: headers})
}

// wantToday checks for a 303 back to Today with the given result.
func wantToday(t *testing.T, w *httptest.ResponseRecorder, result string) {
	t.Helper()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?switch="+result {
		t.Fatalf("status %d, Location %q; want 303 to /?switch=%s\n%s", w.Code, w.Header().Get("Location"), result, w.Body)
	}
}

func (f *fixture) switchState() halt.State {
	f.t.Helper()
	st, err := halt.Active(context.Background(), f.store.Reader())
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func (f *fixture) rows(table string) int {
	f.t.Helper()
	var n int
	if err := f.store.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

var throughOnPage = regexp.MustCompile(`name="through" value="(\d+)"`)

// through reads the reset form's newest-halt id from Today.
func (f *fixture) through() string {
	f.t.Helper()
	m := throughOnPage.FindStringSubmatch(f.page("/"))
	if m == nil {
		f.t.Fatal("Today has no reset form")
	}
	return m[1]
}

func TestStopAllTradingThrowsTheSwitch(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	wantToday(t, f.postSwitch(switchOnPath, token, c, fromAddr(phoneAddr)), "on")

	st := f.switchState()
	if !st.On() || len(st.Active) != 1 || st.Active[0].Cause != halt.Operator {
		t.Fatalf("state = %+v", st)
	}
	if !strings.Contains(st.Active[0].Evidence, "on the approval device "+phoneAddr+".") {
		t.Errorf("evidence %q does not name the device", st.Active[0].Evidence)
	}
	if sent := f.alerts.Sent(); len(sent) != 1 || sent[0] != notify.TradingHalted() {
		t.Errorf("alerts = %v, want one TradingHalted", sent)
	}
	body := f.page("/?switch=on")
	wantIn(t, body,
		`<section class="notice notice--warning" role="status" aria-label="Stop switch result">`,
		"The stop switch is on.",
		"You pressed Stop all trading",
		"/stop-switch/reset")

	// Pressing it again from the same device adds nothing.
	wantToday(t, f.postSwitch(switchOnPath, token, c, fromAddr(phoneAddr)), "on")
	if f.rows("halts") != 1 || len(f.alerts.Sent()) != 1 {
		t.Errorf("a second press recorded or sent again")
	}
}

func TestTheSwitchActionsNeedAnApprovalDeviceAndAToken(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	for _, path := range []string{switchOnPath, switchResetPath} {
		for name, w := range map[string]*httptest.ResponseRecorder{
			"another device": f.postSwitch(path, token, c, fromAddr(laptopAddr), reasonField, "x", throughField, "1"),
			"no address":     f.postSwitch(path, token, c, nil, reasonField, "x", throughField, "1"),
			"no token":       f.postSwitch(path, "", c, fromAddr(phoneAddr), reasonField, "x", throughField, "1"),
		} {
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s: status %d, want 403", path, name, w.Code)
			}
		}
	}
	if f.rows("halts") != 0 {
		t.Fatal("a refused request threw the switch")
	}

	// Once on, a reset from another device is refused and changes nothing.
	wantToday(t, f.postSwitch(switchOnPath, token, c, fromAddr(phoneIPv6)), "on")
	w := f.postSwitch(switchResetPath, token, c, fromAddr(laptopAddr), reasonField, "looked", throughField, f.through())
	if w.Code != http.StatusForbidden || f.rows("halt_resets") != 0 || !f.switchState().On() {
		t.Errorf("reset from another device: status %d, %d resets", w.Code, f.rows("halt_resets"))
	}
	if !strings.Contains(f.log.String(), `"route":"POST /stop-switch/reset","status":403,"duration"`) {
		t.Errorf("the refusal was not logged:\n%s", f.log)
	}
}

func TestResetNeedsAReasonAndIsRecordedWithTheDevice(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	wantToday(t, f.postSwitch(switchOnPath, token, c, fromAddr(phoneAddr)), "on")
	through := f.through()

	for _, reason := range []string{"", "   ", strings.Repeat("x", halt.MaxReason+1)} {
		wantToday(t, f.postSwitch(switchResetPath, token, c, fromAddr(phoneAddr), throughField, through, reasonField, reason), "reason")
	}
	if f.rows("halt_resets") != 0 || !f.switchState().On() {
		t.Fatal("a reset without a reason changed the switch")
	}
	wantIn(t, f.page("/?switch=reason"), "The stop switch was not reset.", "Write why you are resetting it")

	f.clock.Advance(time.Hour)
	wantToday(t, f.postSwitch(switchResetPath, token, c, fromAddr("::ffff:"+phoneAddr), throughField, through,
		reasonField, "Pressed it by mistake."), "reset")
	st := f.switchState()
	if st.On() || st.LastReset == nil {
		t.Fatalf("after reset: %+v", st)
	}
	if r := st.LastReset; r.Reason != "Pressed it by mistake." || r.Device != phoneAddr || r.Through != 1 {
		t.Errorf("reset = %+v", r)
	}
	body := f.page("/?switch=reset")
	wantIn(t, body,
		"The stop switch is reset.",
		"Nothing is halted.",
		"Last reset Mon 16 Nov 2026, 11:00 am ET from "+phoneAddr+": Pressed it by mistake.",
		"Stop all trading</button>")

	// A reset while the switch is off changes nothing.
	wantToday(t, f.postSwitch(switchResetPath, token, c, fromAddr(phoneAddr), throughField, through, reasonField, "again"), "off")
	if f.rows("halt_resets") != 1 {
		t.Error("a reset while off was recorded")
	}
}

func TestAResetFromAPageThatMissedANewerHaltIsRefused(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	wantToday(t, f.postSwitch(switchOnPath, token, c, fromAddr(phoneAddr)), "on")
	seen := f.through()

	// While the page is open, the broker fails.
	if _, _, err := f.stop.Throw(context.Background(), halt.BrokerError, "broker helper exited"); err != nil {
		t.Fatal(err)
	}
	for _, through := range []string{seen, "", "abc", "99"} {
		wantToday(t, f.postSwitch(switchResetPath, token, c, fromAddr(phoneAddr), throughField, through, reasonField, "looked"), "stale")
	}
	st := f.switchState()
	if !st.On() || len(st.Active) != 2 || st.Active[0].Cause != halt.Operator || st.Active[1].Cause != halt.BrokerError {
		t.Fatalf("a second throw lost the first, or a stale reset cleared them: %+v", st)
	}
	body := f.page("/?switch=stale")
	wantIn(t, body, "Trading halted again after this page was loaded.", "You pressed Stop all trading", "The broker failed")

	wantToday(t, f.postSwitch(switchResetPath, token, c, fromAddr(phoneAddr), throughField, f.through(),
		reasonField, "Saw both."), "reset")
	if f.switchState().On() {
		t.Error("a reset that saw every halt left the switch on")
	}
}

func TestTodayIgnoresAnUnknownResult(t *testing.T) {
	body := newFixture(t).page("/?switch=%3Cscript%3E")
	wantNotIn(t, body, `role="status" aria-label="Stop switch result"`, "<script>")
}
