package halt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price, address and reason here is made up.

var t0 = time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC)

type harness struct {
	store *store.Store
	clock *clock.Fake
	fake  *notifytest.Fake
	logs  *loggingtest.Output
	sw    *Switch
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), t0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	h := &harness{store: s, clock: clock.NewFake(t0), fake: &notifytest.Fake{}, logs: out}
	h.sw, err = New(Config{Store: s, Clock: h.clock, Notifier: h.fake, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) state(t *testing.T) State {
	t.Helper()
	st, err := Active(context.Background(), h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (h *harness) throw(t *testing.T, c Cause, evidence string) Halt {
	t.Helper()
	got, thrown, err := h.sw.Throw(context.Background(), c, evidence)
	if err != nil || !thrown {
		t.Fatalf("Throw(%s) = %v, thrown %v", c, err, thrown)
	}
	return got
}

// halted reports how many TradingHalted alerts the fake has, failing on any
// other kind.
func (h *harness) halted(t *testing.T) int {
	t.Helper()
	for _, a := range h.fake.Sent() {
		if a != notify.TradingHalted() {
			t.Fatalf("unexpected alert %s", a.Name())
		}
	}
	return len(h.fake.Sent())
}

func (h *harness) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := h.store.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNewRefusesMissingParts(t *testing.T) {
	h := newHarness(t)
	log, _ := loggingtest.New(t)
	for name, cfg := range map[string]Config{
		"store":    {Clock: h.clock, Notifier: h.fake, Logger: log},
		"clock":    {Store: h.store, Notifier: h.fake, Logger: log},
		"notifier": {Store: h.store, Clock: h.clock, Logger: log},
		"logger":   {Store: h.store, Clock: h.clock, Notifier: h.fake},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New without a %s succeeded", name)
		}
	}
}

func TestTheSwitchStartsOff(t *testing.T) {
	h := newHarness(t)
	st := h.state(t)
	if st.On() || st.Newest() != 0 || st.LastReset != nil {
		t.Errorf("fresh state = %+v", st)
	}
}

func TestEachCauseTurnsTheSwitchOnAndNotifies(t *testing.T) {
	for _, c := range []Cause{Operator, BrokerError, PriceDisagreement, IllegalTransition,
		UnknownOrder, Reconciliation, DailyLoss} {
		t.Run(string(c), func(t *testing.T) {
			h := newHarness(t)
			got := h.throw(t, c, "made-up evidence for "+string(c))
			st := h.state(t)
			if !st.On() || len(st.Active) != 1 || st.Active[0] != got {
				t.Fatalf("state = %+v, want the one halt %+v", st, got)
			}
			if got.Cause != c || got.Evidence != "made-up evidence for "+string(c) || !got.At.Equal(t0) {
				t.Errorf("halt = %+v", got)
			}
			if h.halted(t) != 1 {
				t.Errorf("sent %d alerts, want 1", h.halted(t))
			}
			if c.Words() == string(c) {
				t.Errorf("cause %s has no words for the phone", c)
			}
			if !strings.Contains(h.logs.String(), `"halt_cause":"`+string(c)+`"`) {
				t.Errorf("the throw was not logged with its cause:\n%s", h.logs)
			}
		})
	}
}

func TestASecondThrowAddsARecordAndKeepsTheFirst(t *testing.T) {
	h := newHarness(t)
	first := h.throw(t, BrokerError, "broker helper exited: exit status 1")
	h.clock.Advance(time.Minute)
	second := h.throw(t, PriceDisagreement, "Price check for 2026-11-13: AAAA differs")
	st := h.state(t)
	if len(st.Active) != 2 || st.Active[0] != first || st.Active[1] != second || st.Newest() != second.ID {
		t.Fatalf("active = %+v", st.Active)
	}
	if h.halted(t) != 2 {
		t.Errorf("sent %d alerts, want one per throw", h.halted(t))
	}
}

func TestARepeatOfAnActiveHaltAddsNothing(t *testing.T) {
	h := newHarness(t)
	first := h.throw(t, BrokerError, "broker helper exited: exit status 1")
	h.clock.Advance(time.Second)
	again, thrown, err := h.sw.Throw(context.Background(), BrokerError, "  broker helper exited: exit status 1\n")
	if err != nil || thrown || again != first {
		t.Fatalf("repeat = %+v, thrown %v, %v; want the first halt back", again, thrown, err)
	}
	if h.count(t, "halts") != 1 || h.halted(t) != 1 {
		t.Errorf("a repeat recorded or sent again: %d halts, %d alerts", h.count(t, "halts"), h.halted(t))
	}
	// The same evidence under another cause is a new halt.
	h.throw(t, UnknownOrder, "broker helper exited: exit status 1")

	// Once reset, the same failure halts again.
	if _, err := h.sw.Reset(context.Background(), ResetRequest{Through: h.state(t).Newest(),
		Reason: "restarted the helper", Device: "100.64.0.10"}); err != nil {
		t.Fatal(err)
	}
	h.throw(t, BrokerError, "broker helper exited: exit status 1")
	if h.count(t, "halts") != 3 || h.halted(t) != 3 {
		t.Errorf("%d halts, %d alerts; want 3 and 3", h.count(t, "halts"), h.halted(t))
	}
}

func TestResetNeedsAReasonAndIsRecorded(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.throw(t, Operator, "Stop all trading was pressed on the phone app.")
	h.clock.Advance(time.Minute)
	h.throw(t, BrokerError, "broker helper exited")
	newest := h.state(t).Newest()

	for _, reason := range []string{"", "   \n\t", strings.Repeat("x", MaxReason+1)} {
		if _, err := h.sw.Reset(ctx, ResetRequest{Through: newest, Reason: reason, Device: "100.64.0.10"}); !errors.Is(err, ErrNoReason) {
			t.Errorf("reason %q: %v, want ErrNoReason", reason, err)
		}
	}
	for _, device := range []string{"", strings.Repeat("1", MaxDevice+1)} {
		if _, err := h.sw.Reset(ctx, ResetRequest{Through: newest, Reason: "checked", Device: device}); !errors.Is(err, ErrInvalid) {
			t.Errorf("device %q: %v, want ErrInvalid", device, err)
		}
	}
	if h.count(t, "halt_resets") != 0 || !h.state(t).On() {
		t.Fatal("a refused reset changed the switch")
	}

	h.clock.Advance(time.Hour)
	r, err := h.sw.Reset(ctx, ResetRequest{Through: newest, Reason: "  Checked the helper; it is back.  ",
		Device: "100.64.0.10"})
	if err != nil {
		t.Fatal(err)
	}
	want := Reset{ID: r.ID, Through: newest, Reason: "Checked the helper; it is back.", Device: "100.64.0.10",
		At: t0.Add(time.Minute + time.Hour)}
	if r != want {
		t.Errorf("reset = %+v, want %+v", r, want)
	}
	st := h.state(t)
	if st.On() || st.LastReset == nil || *st.LastReset != want {
		t.Errorf("after reset: %+v", st)
	}
	// Both halts are still on record.
	if h.count(t, "halts") != 2 {
		t.Errorf("the halts were not kept")
	}
	if _, err := h.sw.Reset(ctx, ResetRequest{Through: newest, Reason: "again", Device: "100.64.0.10"}); !errors.Is(err, ErrNotHalted) {
		t.Errorf("reset while off: %v, want ErrNotHalted", err)
	}
}

func TestAResetThatHasNotSeenTheNewestHaltIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	seen := h.throw(t, Operator, "Stop all trading was pressed on the phone app.")
	h.throw(t, BrokerError, "broker helper exited")
	for _, through := range []int64{0, seen.ID, seen.ID + 2} {
		_, err := h.sw.Reset(ctx, ResetRequest{Through: through, Reason: "looked", Device: "100.64.0.10"})
		if !errors.Is(err, ErrStale) {
			t.Errorf("through %d: %v, want ErrStale", through, err)
		}
	}
	if st := h.state(t); !st.On() || len(st.Active) != 2 {
		t.Errorf("a stale reset changed the switch: %+v", st)
	}
}

func TestThrowRefusesBadInput(t *testing.T) {
	h := newHarness(t)
	for _, c := range []Cause{"", "Broker", "broker error", "1st", Cause(strings.Repeat("a", 33))} {
		if _, _, err := h.sw.Throw(context.Background(), c, "evidence"); !errors.Is(err, ErrInvalid) {
			t.Errorf("cause %q: %v, want ErrInvalid", c, err)
		}
	}
	if _, _, err := h.sw.Throw(context.Background(), BrokerError, " \n "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty evidence: %v, want ErrInvalid", err)
	}
	if h.count(t, "halts") != 0 || h.halted(t) != 0 {
		t.Error("a refused throw recorded or notified")
	}
}

func TestLongEvidenceIsCutShort(t *testing.T) {
	h := newHarness(t)
	got := h.throw(t, BrokerError, strings.Repeat("é", MaxEvidence+50))
	if n := len([]rune(got.Evidence)); n != MaxEvidence || !strings.HasSuffix(got.Evidence, "…") {
		t.Errorf("evidence is %d characters, ends %q", n, got.Evidence[len(got.Evidence)-6:])
	}
	if st := h.state(t); st.Active[0].Evidence != got.Evidence {
		t.Error("stored evidence differs from what Throw returned")
	}
}

func TestAFailedNotificationStillHalts(t *testing.T) {
	h := newHarness(t)
	h.fake.Err = notify.ErrNoRecipients
	h.throw(t, BrokerError, "broker helper exited")
	if !h.state(t).On() || h.halted(t) != 1 {
		t.Fatal("the halt was lost with its notification")
	}
	if !strings.Contains(h.logs.String(), "the halt notification was not sent") {
		t.Errorf("the failed notification was not logged:\n%s", h.logs)
	}
}

func TestNowhereSendsNothing(t *testing.T) {
	h := newHarness(t)
	log, _ := loggingtest.New(t)
	sw, err := New(Config{Store: h.store, Clock: h.clock, Notifier: notify.Nowhere, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	if _, thrown, err := sw.Throw(context.Background(), Operator, "pressed"); err != nil || !thrown {
		t.Fatalf("throw with Nowhere: %v", err)
	}
}

func TestOrderErrorThrowsOnAnIllegalMove(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	day := time.Date(2026, 11, 13, 0, 0, 0, 0, clock.Eastern)
	limit := money.MustParsePrice("10.00")
	var o orders.Order
	err := orders.Update(ctx, h.store, func(tx *sql.Tx) error {
		var err error
		o, _, err = orders.Create(ctx, tx, orders.New{Strategy: "example", Symbol: "AAAA", Date: day,
			Kind: signals.KindBuy, Quantity: money.MustParseQuantity("1"), Price: &limit,
			Reason: "breakout: made-up reason", StrategyHash: strings.Repeat("ab", 32)}, orders.Proposer, t0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A buy cannot skip approval: proposed → approved is illegal.
	err = orders.Update(ctx, h.store, func(tx *sql.Tx) error {
		_, err := orders.Move(ctx, tx, o.ID, orders.Change{To: orders.Approved, Actor: orders.Operator,
			Reason: "made-up", At: t0})
		return err
	})
	got := h.sw.OrderError(ctx, err)
	if !errors.Is(got, orders.ErrIllegalTransition) {
		t.Fatalf("OrderError returned %v, want the transition error back", got)
	}
	st := h.state(t)
	if len(st.Active) != 1 || st.Active[0].Cause != IllegalTransition {
		t.Fatalf("state = %+v", st)
	}
	want := fmt.Sprintf("Order %d (buy) was asked to move from proposed to approved", o.ID)
	if !strings.Contains(st.Active[0].Evidence, want) {
		t.Errorf("evidence %q does not say %q", st.Active[0].Evidence, want)
	}
	if h.halted(t) != 1 {
		t.Error("no notification")
	}
	// The refused move wrote nothing.
	if back, err := orders.Get(ctx, h.store.Reader(), o.ID); err != nil || back.State != orders.Proposed {
		t.Errorf("order after the refused move: %+v, %v", back, err)
	}
}

func TestOrderErrorPassesOtherErrorsThrough(t *testing.T) {
	h := newHarness(t)
	other := errors.New("made-up failure")
	if err := h.sw.OrderError(context.Background(), nil); err != nil {
		t.Errorf("nil became %v", err)
	}
	if err := h.sw.OrderError(context.Background(), other); err != other { //nolint:errorlint // must be the same value
		t.Errorf("other error became %v", err)
	}
	if err := h.sw.OrderError(context.Background(), fmt.Errorf("wrapped: %w", orders.ErrNotFound)); !errors.Is(err, orders.ErrNotFound) {
		t.Errorf("not-found became %v", err)
	}
	if h.count(t, "halts") != 0 || h.halted(t) != 0 {
		t.Error("an ordinary error halted trading")
	}
}

func TestConcurrentThrowsAreAllRecorded(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := h.sw.Throw(context.Background(), BrokerError, fmt.Sprintf("failure %d", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if st := h.state(t); len(st.Active) != 8 || h.halted(t) != 8 {
		t.Errorf("%d active, %d alerts; want 8 each", len(st.Active), h.halted(t))
	}
}

func TestTheTablesAreAppendOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first := h.throw(t, Operator, "pressed")
	if _, err := h.sw.Reset(ctx, ResetRequest{Through: first.ID, Reason: "fine", Device: "100.64.0.10"}); err != nil {
		t.Fatal(err)
	}
	w := h.store.Writer()
	for _, q := range []string{
		`UPDATE halts SET evidence = 'changed'`,
		`DELETE FROM halts`,
		`UPDATE halt_resets SET reason = 'changed'`,
		`DELETE FROM halt_resets`,
		// A reset must clear something newer than the last reset.
		fmt.Sprintf(`INSERT INTO halt_resets (through_halt_id, reason, device, at) VALUES (%d, 'again', '100.64.0.10', '%s')`,
			first.ID, store.FormatTime(t0)),
		// And name a halt that exists.
		fmt.Sprintf(`INSERT INTO halt_resets (through_halt_id, reason, device, at) VALUES (99, 'none', '100.64.0.10', '%s')`,
			store.FormatTime(t0)),
	} {
		if _, err := w.ExecContext(ctx, q); err == nil {
			t.Errorf("%s: allowed", q)
		}
	}
	// A reason of only spaces is no reason.
	second := h.throw(t, BrokerError, "broker helper exited")
	if _, err := w.ExecContext(ctx, `INSERT INTO halt_resets (through_halt_id, reason, device, at) VALUES (?, '   ', '100.64.0.10', ?)`,
		second.ID, store.FormatTime(t0)); err == nil {
		t.Error("a blank reason was allowed")
	}
	for _, q := range []string{
		`INSERT INTO halts (cause, evidence, at) VALUES ('Broker', 'x', '` + store.FormatTime(t0) + `')`,
		`INSERT INTO halts (cause, evidence, at) VALUES ('broker_error', '', '` + store.FormatTime(t0) + `')`,
		`INSERT INTO halts (cause, evidence, at) VALUES ('broker_error', 'x', '2026-11-13')`,
	} {
		if _, err := w.ExecContext(ctx, q); err == nil {
			t.Errorf("%s: allowed", q)
		}
	}
}
