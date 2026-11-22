package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/limits"
)

// Every value here is made up. The example strategy file asks for 4 weeks of
// paper trading.

// newLiveFixture is a live-mode fixture with the example strategy file, live
// mode recorded as at start-up, and a paper record that began paperWeeks
// before live mode was.
func newLiveFixture(t *testing.T, mode config.Mode, paperWeeks int, edit func(*Config)) *fixture {
	t.Helper()
	f := newFixtureWith(t, mode, func(_ *fixture, cfg *Config) {
		cfg.Strategy = exampleStrategy(t)
		if edit != nil {
			edit(cfg)
		}
	})
	now := f.clock.Now()
	f.recordHoldings(now.Add(-time.Duration(paperWeeks)*7*24*time.Hour).Format(time.RFC3339), "1000.00")
	if _, err := limits.RecordMode(context.Background(), f.store, mode, now); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) confirm(token string, from string) *httptest.ResponseRecorder {
	f.t.Helper()
	c, tok := f.session()
	if token == "session" {
		token = tok
	}
	return f.postSwitch(confirmLivePath, token, c, fromAddr(from))
}

func wantLive(t *testing.T, w *httptest.ResponseRecorder, result string) {
	t.Helper()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings?live="+result+"#live" {
		t.Fatalf("answer = %d to %q, want 303 to the %s result\n%s", w.Code, w.Header().Get("Location"), result, w.Body)
	}
}

func (f *fixture) gate() limits.Gate {
	f.t.Helper()
	g, err := limits.ReadGate(context.Background(), f.store.Reader(), f.srv.strategy, f.srv.mode, f.clock.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	return g
}

func TestTheConfirmationNeedsTheTokenAndAnApprovalDevice(t *testing.T) {
	f := newLiveFixture(t, config.ModeLive, 5, nil)
	if w := f.confirm("", phoneAddr); w.Code != http.StatusForbidden {
		t.Errorf("without the anti-forgery token = %d, want 403", w.Code)
	}
	if w := f.confirm("not-the-token", phoneAddr); w.Code != http.StatusForbidden {
		t.Errorf("with a wrong token = %d, want 403", w.Code)
	}
	if w := f.confirm("session", laptopAddr); w.Code != http.StatusForbidden {
		t.Errorf("from a device that is not an approval device = %d, want 403", w.Code)
	}
	if n := f.phoneChanges(); n != 0 || f.gate().Confirmed {
		t.Fatalf("%d changes recorded, want none", n)
	}

	wantLive(t, f.confirm("session", phoneAddr), "confirmed")
	var setting, value, source string
	if err := f.store.Reader().QueryRow(`SELECT setting, new_value, source FROM settings_changes
		WHERE source = 'phone'`).Scan(&setting, &value, &source); err != nil {
		t.Fatal(err)
	}
	if setting != limits.LiveSetting || value != f.srv.strategy.Hash || source != "phone" {
		t.Errorf("recorded %s = %s from %s", setting, value, source)
	}
	if g := f.gate(); !g.Open() {
		t.Errorf("gate after the confirmation = %+v", g)
	}
	// The running sherwood built no adapter, so a restart is needed.
	wantIn(t, f.page("/settings?live=confirmed"), "Live mode is confirmed.", "Restart sherwood on the NAS to start live trading",
		"All three hold.", "Live mode confirmed", "The mode recorded as live")
	wantIn(t, f.page("/"), "Live mode is confirmed, but live trading is not on yet")
	wantNotIn(t, f.page("/settings"), `action="/settings/live"`)

	wantLive(t, f.confirm("session", phoneAddr), "already")
	if n := f.phoneChanges(); n != 1 {
		t.Errorf("%d confirmations recorded, want 1", n)
	}
}

func TestTheConfirmationIsRefusedInPaperModeOrBeforeThePaperPeriodEnds(t *testing.T) {
	paper := newLiveFixture(t, config.ModePaper, 5, nil)
	wantNotIn(t, paper.page("/settings"), `action="/settings/live"`)
	wantLive(t, paper.confirm("session", phoneAddr), "paper")

	short := newLiveFixture(t, config.ModeLive, 3, nil)
	body := short.page("/settings")
	wantIn(t, body, "Live mode, but live trading is off", "The paper record is 3 weeks long, and the strategy file asks for at least 4 weeks.",
		"3 of at least 4 weeks")
	wantNotIn(t, body, `action="/settings/live"`)
	wantIn(t, short.page("/"), "Live trading is off:", "The paper record is 3 weeks long")
	wantLive(t, short.confirm("session", phoneAddr), "short")
	if paper.phoneChanges()+short.phoneChanges() != 0 {
		t.Error("a refused confirmation was recorded")
	}
}

func TestSettingsShowsTheGateBesideThePaperRecord(t *testing.T) {
	f := newLiveFixture(t, config.ModeLive, 5, nil)
	body := f.page("/settings")
	wantIn(t, body, "Paper record", "Weeks so far", "Live mode needs at least 4. Weeks in live mode do not count.",
		`id="live"`, "Confirmed in the app", "5 of at least 4 weeks", `action="/settings/live"`, "Confirm live mode",
		"Live mode has not been confirmed in the app for this strategy file.")

	on := newLiveFixture(t, config.ModeLive, 5, func(cfg *Config) { cfg.LiveTrading = true })
	wantIn(t, on.page("/settings"), "On: real orders", "Live trading is on: orders you approve are placed with the broker")
}
