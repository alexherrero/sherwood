package limits

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every value here is made up. The example strategy file asks for 4 weeks of
// paper trading.

// startPaper records the paper record's first reading at at.
func startPaper(t *testing.T, st *store.Store, at time.Time) {
	t.Helper()
	if _, _, err := holdings.Record(context.Background(), st, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: at, Cash: money.MustParseMoney("1000.00")}); err != nil {
		t.Fatal(err)
	}
}

func recordMode(t *testing.T, st *store.Store, m config.Mode, at time.Time) {
	t.Helper()
	if _, err := RecordMode(context.Background(), st, m, at); err != nil {
		t.Fatal(err)
	}
}

func gate(t *testing.T, st *store.Store, s *config.Strategy, m config.Mode, now time.Time) Gate {
	t.Helper()
	g, err := ReadGate(context.Background(), st.Reader(), s, m, now)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// goLive is a store with a 5-week paper record, the file recorded and live
// mode recorded at the end of it, so only the confirmation is missing.
func goLive(t *testing.T) (*store.Store, *config.Strategy, time.Time) {
	t.Helper()
	st, s := open(t), example(t, hashA)
	startPaper(t, st, t0)
	recordMode(t, st, config.ModePaper, t0)
	if _, err := Record(context.Background(), st, s, t0); err != nil {
		t.Fatal(err)
	}
	live := t0.Add(5 * week)
	recordMode(t, st, config.ModeLive, live)
	return st, s, live.Add(time.Hour)
}

func TestTheGateOpensOnlyWithAllThree(t *testing.T) {
	ctx := context.Background()
	st, s, now := goLive(t)
	g := gate(t, st, s, config.ModeLive, now)
	if !g.Live || g.Confirmed || !g.PaperMet || g.PaperWeeks != 5 || g.Open() {
		t.Fatalf("before the confirmation: %+v", g)
	}
	if r := g.Reasons(); len(r) != 1 || !strings.Contains(r[0], "not been confirmed") {
		t.Errorf("reasons = %q", r)
	}
	c, err := ConfirmLive(ctx, st, s, config.ModeLive, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Setting != LiveSetting || c.New != hashA || c.Source != Phone {
		t.Errorf("confirmation = %+v", c)
	}
	g = gate(t, st, s, config.ModeLive, now)
	if !g.Open() || !g.ConfirmedAt.Equal(now) || len(g.Reasons()) != 0 {
		t.Fatalf("after the confirmation: %+v %q", g, g.Reasons())
	}
	if _, err := ConfirmLive(ctx, st, s, config.ModeLive, now); !errors.Is(err, ErrConfirmed) {
		t.Errorf("a second confirmation: %v, want ErrConfirmed", err)
	}
	// The settings file naming paper mode shuts the gate, whatever is
	// recorded.
	if g := gate(t, st, s, config.ModePaper, now); g.Live || g.Confirmed || g.Open() {
		t.Errorf("in paper mode: %+v", g)
	}
}

func TestTheConfirmationIsRefusedOutsideLiveModeAndBeforeThePaperPeriod(t *testing.T) {
	ctx := context.Background()
	st, s := open(t), example(t, hashA)
	startPaper(t, st, t0)
	recordMode(t, st, config.ModePaper, t0)
	if _, err := ConfirmLive(ctx, st, s, config.ModePaper, t0.Add(10*week)); !errors.Is(err, ErrNotLive) {
		t.Errorf("in paper mode: %v, want ErrNotLive", err)
	}
	// Live mode named by the settings file but not yet recorded at start-up.
	if _, err := ConfirmLive(ctx, st, s, config.ModeLive, t0.Add(10*week)); !errors.Is(err, ErrNotLive) {
		t.Errorf("with no live mode recorded: %v, want ErrNotLive", err)
	}
	// Three weeks of paper trading, then live mode: one week short.
	recordMode(t, st, config.ModeLive, t0.Add(3*week))
	_, err := ConfirmLive(ctx, st, s, config.ModeLive, t0.Add(10*week))
	if !errors.Is(err, ErrPaperShort) || !strings.Contains(err.Error(), "3 weeks long") {
		t.Errorf("a short paper record: %v, want ErrPaperShort", err)
	}
	if n := rows(t, st, Phone); n != 0 {
		t.Errorf("%d phone rows recorded, want none", n)
	}
}

func TestWeeksInLiveModeDoNotCountAsPaperTrading(t *testing.T) {
	st, s := open(t), example(t, hashA)
	startPaper(t, st, t0)
	recordMode(t, st, config.ModeLive, t0.Add(2*week))
	g := gate(t, st, s, config.ModeLive, t0.Add(30*week))
	if g.PaperWeeks != 2 || g.PaperMet {
		t.Errorf("live since week 2, read at week 30: %+v", g)
	}
	if r := g.Reasons(); len(r) != 2 || !strings.Contains(r[1], "2 weeks long") || !strings.Contains(r[1], "4 weeks") {
		t.Errorf("reasons = %q", r)
	}
	// Back in paper mode, the weeks count to now.
	if g := gate(t, st, s, config.ModePaper, t0.Add(30*week)); g.PaperWeeks != 30 || !g.PaperMet {
		t.Errorf("in paper mode: %+v", g)
	}
}

func TestWithoutAPaperRecordTheGateStaysShut(t *testing.T) {
	st, s := open(t), example(t, hashA)
	recordMode(t, st, config.ModeLive, t0)
	g := gate(t, st, s, config.ModeLive, t0.Add(10*week))
	if g.PaperStarted || g.PaperMet || g.Open() {
		t.Errorf("no record: %+v", g)
	}
	if _, err := ConfirmLive(context.Background(), st, s, config.ModeLive, t0.Add(10*week)); !errors.Is(err, ErrPaperShort) ||
		!strings.Contains(err.Error(), "no paper record") {
		t.Errorf("confirmation without a record: %v", err)
	}
}

func TestANewStrategyFileLapsesTheConfirmation(t *testing.T) {
	ctx := context.Background()
	st, s, now := goLive(t)
	if _, err := ConfirmLive(ctx, st, s, config.ModeLive, now); err != nil {
		t.Fatal(err)
	}
	b := example(t, hashB)
	if _, err := Record(ctx, st, b, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if g := gate(t, st, b, config.ModeLive, now.Add(time.Hour)); g.Confirmed || g.Open() {
		t.Errorf("after the file changed: %+v", g)
	}
	// Going back to the confirmed file does not bring the confirmation back:
	// its hash row is newer than the confirmation.
	if _, err := Record(ctx, st, s, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if g := gate(t, st, s, config.ModeLive, now.Add(2*time.Hour)); g.Confirmed {
		t.Errorf("after the file changed back: %+v", g)
	}
	if _, err := ConfirmLive(ctx, st, s, config.ModeLive, now.Add(3*time.Hour)); err != nil {
		t.Errorf("confirming again: %v", err)
	}
}

func TestAChangeOfModeLapsesTheConfirmation(t *testing.T) {
	ctx := context.Background()
	st, s, now := goLive(t)
	if _, err := ConfirmLive(ctx, st, s, config.ModeLive, now); err != nil {
		t.Fatal(err)
	}
	// A restart in live mode keeps it: the mode is unchanged, so nothing
	// is recorded.
	if wrote, err := RecordMode(ctx, st, config.ModeLive, now.Add(time.Hour)); err != nil || wrote {
		t.Fatalf("RecordMode(live) again = %v, %v", wrote, err)
	}
	if g := gate(t, st, s, config.ModeLive, now.Add(time.Hour)); !g.Open() {
		t.Fatalf("after a restart in live mode: %+v", g)
	}
	recordMode(t, st, config.ModePaper, now.Add(2*time.Hour))
	recordMode(t, st, config.ModeLive, now.Add(3*time.Hour))
	if g := gate(t, st, s, config.ModeLive, now.Add(3*time.Hour)); g.Confirmed || g.Open() {
		t.Errorf("after paper and back to live: %+v", g)
	}
}

func TestTheModeAndConfirmationAreInTheHistory(t *testing.T) {
	ctx := context.Background()
	st, s, now := goLive(t)
	if _, err := ConfirmLive(ctx, st, s, config.ModeLive, now); err != nil {
		t.Fatal(err)
	}
	h, err := History(ctx, st.Reader(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 3 || h[0].Setting != LiveSetting || h[1].Setting != ModeSetting || h[1].Old != "paper" ||
		h[1].New != "live" || h[1].Source != NAS {
		t.Errorf("history = %+v", h)
	}
}
