package robinhood

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
)

// The contract test runs the adapter against the real Python helper, started
// by the real supervisor, with only the helper's broker library faked
// (brokerhelper/tests/fakes.py). It covers all eight helper operations end
// to end: the Go side's arguments are what the helper accepts, the helper's
// answers are what the adapter reads, and the client reference the Go side
// computes is the one the helper sends. Every record is made up.

// The fake library's keys and records (fakes.py).
var (
	key1 = strings.Repeat("1", 64) // an open limit buy
	key2 = strings.Repeat("2", 64) // a resting stop, partly filled
	key3 = strings.Repeat("3", 64) // a filled buy
)

// within bounds how long the test waits for the helper. It is a safety net
// against hangs, not a sleep.
const within = 30 * time.Second

type liveHelper struct {
	sup    *broker.Supervisor
	a      *Adapter
	alerts *notifytest.Fake
	errs   chan error
}

func startRealHelper(t *testing.T, loggedOut bool) *liveHelper {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH, so the broker helper contract test cannot run; install Python 3 to run it")
	}
	// The helper needs Python 3.12 or later (brokerhelper/pyproject.toml);
	// an older python3 first on PATH cannot run it.
	check := exec.Command(python, "-c", "import sys; sys.exit(sys.version_info < (3, 12))") //nolint:gosec // G204: python3 from PATH, a fixed script
	if err := check.Run(); err != nil {
		t.Skipf("%s is older than Python 3.12, which the broker helper needs, so the contract test cannot run", python)
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "rhc") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	cmd := []string{python, "-B", filepath.Join(repo, "internal", "broker", "robinhood", "testdata", "fake_helper.py"),
		"--repo", repo}
	if loggedOut {
		cmd = append(cmd, "--logged-out")
	}
	h := &liveHelper{alerts: &notifytest.Fake{}, errs: make(chan error, 16)}
	ready := make(chan struct{}, 16)
	h.sup, err = broker.NewSupervisor(broker.SupervisorConfig{
		Command:    cmd,
		Env:        append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1"),
		SocketPath: filepath.Join(dir, "h.sock"),
		OnError:    func(err error) { h.errs <- err },
		OnReady:    func() { ready <- struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = h.sup.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-ready:
	case err := <-h.errs:
		t.Fatalf("the helper did not start: %v", err)
	case <-time.After(within):
		t.Fatal("timed out waiting for the helper to be ready")
	}

	log, _ := loggingtest.New(t)
	h.a, err = New(Config{
		Client: h.sup.Client(),
		Helper: h.sup,
		Login:  &broker.LoginAlert{Notifier: h.alerts},
		Keys: func(context.Context) ([]string, error) {
			return []string{key1, key2, key3, strings.Repeat("a", 64)}, nil
		},
		Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// noFailures checks the helper never failed while the test ran.
func (h *liveHelper) noFailures(t *testing.T) {
	t.Helper()
	select {
	case err := <-h.errs:
		t.Errorf("the helper failed: %v", err)
	default:
	}
}

func TestContractEveryOpAgainstTheRealHelper(t *testing.T) {
	h := startRealHelper(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	a := h.a

	// ping
	if res, err := h.sup.Client().Ping(ctx); err != nil || res.PID <= 0 {
		t.Fatalf("ping = %+v, %v", res, err)
	}
	if !a.Healthy() {
		t.Error("the adapter over a ready helper is not healthy")
	}

	// holdings: the fake's two positions, by symbol; a zero position is left out.
	held, err := a.Holdings(ctx)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if len(held) != 2 ||
		held[0].Symbol != "ZZA" || held[0].Quantity.String() != "2.5" || held[0].AverageCost.String() != "100.00" ||
		held[1].Symbol != "ZZB" || held[1].Quantity.String() != "10" || held[1].AverageCost.String() != "12.34" {
		t.Errorf("holdings = %+v", held)
	}

	// cash and account
	if c, err := a.Cash(ctx); err != nil || c.String() != "1234.56" {
		t.Errorf("cash = %s, %v", c, err)
	}
	if acct, err := a.Account(ctx); err != nil || acct.Cash.String() != "1234.56" || acct.Value.String() != "5678.90" {
		t.Errorf("account = %+v, %v", acct, err)
	}

	// open_orders: sherwood's two open orders come back under their keys,
	// the order placed in the broker's own app as a stranger.
	open, err := a.OpenOrders(ctx)
	if err != nil {
		t.Fatalf("open_orders: %v", err)
	}
	if len(open) != 3 {
		t.Fatalf("open_orders = %+v, want three", open)
	}
	if o := open[0]; o.Ref != key1 || o.Status != broker.StatusAccepted || !o.Filled.IsZero() || o.FillPrice != nil {
		t.Errorf("open order 1 = %+v", o)
	}
	if o := open[1]; o.Ref != key2 || o.Status != broker.StatusPartiallyFilled || o.Filled.String() != "4" ||
		o.FillPrice == nil || o.FillPrice.String() != "9.48" {
		t.Errorf("open order 2 = %+v", o)
	}
	if o := open[2]; o.Ref != ForeignPrefix+"00000000-0000-0000-0000-000000000004" || o.Status != broker.StatusAccepted {
		t.Errorf("the broker app's order = %+v", o)
	}

	// lookup
	if r, err := a.Lookup(ctx, key3); err != nil || r.Ref != key3 || r.Status != broker.StatusFilled ||
		r.Filled.String() != "10" || r.FillPrice == nil || r.FillPrice.String() != "12.30" {
		t.Errorf("lookup = %+v, %v", r, err)
	}
	var he *broker.HelperError
	if _, err := a.Lookup(ctx, strings.Repeat("f", 64)); !errors.As(err, &he) || he.Code != "order_not_found" {
		t.Errorf("lookup of an unknown order: err = %v, want order_not_found", err)
	}

	// place, one of each type: the helper's client reference for each key
	// is the one the Go side computes, or the adapter refuses the answer.
	price := money.MustParsePrice("12.34")
	for i, o := range []broker.Order{
		{Ref: strings.Repeat("a", 64), Symbol: "ZZA", Side: broker.Buy, Type: broker.DayLimitBuy, Quantity: money.MustParseQuantity("2"), Price: &price},
		{Ref: strings.Repeat("b", 64), Symbol: "ZZB", Side: broker.Sell, Type: broker.Stop, Quantity: money.MustParseQuantity("0.5"), Price: &price},
		{Ref: strings.Repeat("c", 64), Symbol: "ZZA", Side: broker.Sell, Type: broker.SellAtOpen, Quantity: money.MustParseQuantity("1")},
	} {
		o.Session = time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
		r, err := a.Place(ctx, o)
		if err != nil || r.Ref != o.Ref || r.Status != broker.StatusAccepted || !r.Filled.IsZero() {
			t.Errorf("place %d (%s) = %+v, %v", i, o.Type, r, err)
		}
	}
	// A symbol the broker does not list is refused with nothing sent.
	r, err := a.Place(ctx, broker.Order{Ref: strings.Repeat("d", 64), Symbol: "ZZQ", Side: broker.Buy,
		Type: broker.DayLimitBuy, Quantity: money.MustParseQuantity("1"), Price: &price, Session: time.Now()})
	if err != nil || r.Status != broker.StatusRejected || r.Detail != "the broker lists no stock with this symbol" {
		t.Errorf("place of an unlisted symbol = %+v, %v", r, err)
	}

	// cancel: an open order is called off; a finished one answers its status.
	if r, err := a.Cancel(ctx, key1); err != nil || r.Ref != key1 || r.Status != broker.StatusCancelled {
		t.Errorf("cancel of an open order = %+v, %v", r, err)
	}
	if r, err := a.Cancel(ctx, key3); err != nil || r.Ref != key3 || r.Status != broker.StatusFilled {
		t.Errorf("cancel of a filled order = %+v, %v", r, err)
	}

	if n := len(h.alerts.Sent()); n != 0 {
		t.Errorf("%d alerts sent while logged in", n)
	}
	h.noFailures(t)
}

func TestContractLoginNeededFromTheRealHelperAlertsOnce(t *testing.T) {
	h := startRealHelper(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	a := h.a
	calls := map[string]func() error{
		opPlace: func() error {
			p := money.MustParsePrice("12.34")
			_, err := a.Place(ctx, broker.Order{Ref: strings.Repeat("a", 64), Symbol: "ZZA", Side: broker.Buy,
				Type: broker.DayLimitBuy, Quantity: money.MustParseQuantity("1"), Price: &p, Session: time.Now()})
			return err
		},
		opCancel:     func() error { _, err := a.Cancel(ctx, key1); return err },
		opLookup:     func() error { _, err := a.Lookup(ctx, key1); return err },
		opOpenOrders: func() error { _, err := a.OpenOrders(ctx); return err },
		opHoldings:   func() error { _, err := a.Holdings(ctx); return err },
		opCash:       func() error { _, err := a.Cash(ctx); return err },
		opAccount:    func() error { _, err := a.Account(ctx); return err },
	}
	for op, call := range calls {
		if err := call(); !broker.LoginNeeded(err) {
			t.Errorf("%s: err = %v, want login_needed", op, err)
		}
	}
	if got := h.alerts.Sent(); len(got) != 1 || got[0].Name() != "broker_login_needed" {
		t.Errorf("alerts = %v, want one broker_login_needed for the whole streak", got)
	}
	if a.Healthy() {
		t.Error("the adapter is healthy while the helper has no broker session")
	}
	if !h.sup.Healthy() {
		t.Error("the helper itself should still be running")
	}
	h.noFailures(t)
}
