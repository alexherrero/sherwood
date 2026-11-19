package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price and amount below is made up.

func TestTheSendJobSkipsAPassedSessionAndRunsToday(t *testing.T) {
	ctx := context.Background()
	now := at(t, "2026-11-24", 9, 0)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	clk := clock.NewFake(now)
	sw, err := halt.New(halt.Config{Store: s, Clock: clk, Notifier: &notifytest.Fake{}, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	sched, err := scheduler.New(scheduler.Config{Store: s, Clock: clk, Calendar: clock.NYSE(), Logger: log, CatchUpDays: 2})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := registerTrading(env{clock: clk}, sched, config.ModePaper, acStrategy(t), s, sw, log)
	if err != nil || pb == nil {
		t.Fatalf("registerTrading = %v, %v", pb, err)
	}
	clk.Set(at(t, "2026-11-24", 9, 35))
	if err := sched.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	h := &acHarness{store: s}
	got := h.strings(t, `SELECT trading_date || ' ' || outcome || ' ' || detail FROM job_runs WHERE job_name = ? ORDER BY id`, sendJob)
	want := []string{
		"2026-11-23 skipped this session has passed, so nothing was sent",
		"2026-11-24 succeeded ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("send runs:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(out.String(), "0 sent, 0 blocked by the risk check") {
		t.Errorf("no summary logged:\n%s", out.String())
	}
}

func TestLiveModeRegistersNoSendJob(t *testing.T) {
	ctx := context.Background()
	now := at(t, "2026-11-24", 9, 0)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	clk := clock.NewFake(now)
	sched, err := scheduler.New(scheduler.Config{Store: s, Clock: clk, Calendar: clock.NYSE(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := registerTrading(env{clock: clk}, sched, config.ModeLive, acStrategy(t), s, nil, log)
	if err != nil || pb != nil {
		t.Fatalf("registerTrading = %v, %v, want nothing in live mode", pb, err)
	}
	if !strings.Contains(out.String(), "orders are not sent") {
		t.Errorf("live mode did not say orders are not sent:\n%s", out.String())
	}
}

func TestTheAfterCloseSettlesPaperFillsBeforeTheRules(t *testing.T) {
	ctx := context.Background()
	h := newAC(t, at(t, "2026-11-23", 9, 0), 1)
	if _, _, err := holdings.Record(ctx, h.store, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: at(t, "2026-11-20", 16, 0), Cash: money.MustParseMoney("1000.00")}); err != nil {
		t.Fatal(err)
	}
	// A buy of BBB sent at 09:35, resting until the close; BBB opens at
	// 10.00, below its limit.
	limit := money.MustParsePrice("10.50")
	var buy orders.Order
	err := orders.Update(ctx, h.store, func(tx *sql.Tx) error {
		o, _, err := orders.Create(ctx, tx, orders.New{Strategy: "test-breakout", Symbol: "BBB",
			Date: day(t, "2026-11-23"), Kind: signals.KindBuy, Quantity: money.MustParseQuantity("3"), Price: &limit,
			Reason: "breakout: made up", StrategyHash: acStrategy(t).Hash}, orders.Proposer, at(t, "2026-11-23", 9, 0))
		for _, to := range []orders.State{orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted} {
			if err != nil {
				return err
			}
			o, err = orders.Move(ctx, tx, o.ID, orders.Change{To: to, Actor: orders.Execution, Reason: "test",
				At: at(t, "2026-11-23", 9, 35)})
		}
		buy = o
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	h.runAt(t, at(t, "2026-11-23", 16, 30))
	got, err := orders.Get(ctx, h.store.Reader(), buy.ID)
	if err != nil || got.State != orders.Filled {
		t.Fatalf("buy is %s, %v, want filled", got.State, err)
	}
	set, err := holdings.Current(ctx, h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	bbb, ok := set.Find("BBB")
	if !ok || bbb.Quantity.String() != "3" || set.Source != holdings.SourceFills {
		t.Fatalf("holdings %+v, want 3 BBB from fills", set)
	}
	// The rules saw the new holding: it has a suggestion, with the fixed
	// stop 10% under its cost of 10.00.
	stops := h.strings(t, `SELECT symbol || ' ' || stop FROM holding_suggestions WHERE trading_date = '2026-11-23'`)
	if strings.Join(stops, ",") != "BBB 9.00" {
		t.Errorf("suggestions %q, want BBB with a stop of 9.00", stops)
	}
	for _, line := range []string{`"msg":"paper order filled"`, `"amount":"1000.00"`} {
		if !strings.Contains(h.logs.String(), line) {
			t.Errorf("log lacks %s:\n%s", line, h.logs.String())
		}
	}
}
