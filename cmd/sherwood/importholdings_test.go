package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/store"
)

// fakeHelperEnv, when set, makes the test binary act as the broker helper
// instead of running tests. Its value is the path of a fakeScenario file.
const fakeHelperEnv = "SHERWOOD_TEST_FAKE_HELPER"

func TestMain(m *testing.M) {
	if path := os.Getenv(fakeHelperEnv); path != "" {
		os.Exit(fakeHelperMain(path, os.Args[1:]))
	}
	// The research job's dry run runs the test binary as sherwood itself.
	if os.Getenv(asSherwoodEnv) != "" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeScenario tells the fake helper how to behave. It talks to no broker:
// every reply is canned, and every value in these tests is made up.
type fakeScenario struct {
	// Log is a file the fake appends one line to when it starts and one per
	// call it receives, so a test can see every start and every call.
	Log string `json:"log"`
	// ExitBeforeReady makes the fake exit without ever being ready.
	ExitBeforeReady bool `json:"exit_before_ready"`
	// CrashOn makes the fake exit, without replying, when it receives this op.
	CrashOn string `json:"crash_on"`
	// Replies maps an op to its reply, without the id. An op with no entry
	// gets unknown_op.
	Replies map[string]json.RawMessage `json:"replies"`
}

// fakeHelperMain is the fake helper process. It takes the real helper's
// --socket and --data-dir arguments and speaks its one-line JSON protocol.
func fakeHelperMain(scenarioPath string, args []string) int {
	raw, err := os.ReadFile(scenarioPath) //nolint:gosec // a file the test wrote
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake helper:", err)
		return 2
	}
	var sc fakeScenario
	if err := json.Unmarshal(raw, &sc); err != nil {
		fmt.Fprintln(os.Stderr, "fake helper:", err)
		return 2
	}
	var socket, dataDir string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--socket":
			socket = args[i+1]
		case "--data-dir":
			dataDir = args[i+1]
		}
	}
	logLine := func(s string) {
		f, err := os.OpenFile(sc.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // a file the test named
		if err == nil {
			_, _ = fmt.Fprintln(f, s)
			_ = f.Close()
		}
	}
	logLine("start data-dir=" + dataDir)
	if sc.ExitBeforeReady {
		return 3
	}

	ln, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake helper:", err)
		return 2
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-sigs
		_ = ln.Close()
		_ = os.Remove(socket) //nolint:gosec // the socket path the test passed in
		os.Exit(0)
	}()
	fmt.Println("ready")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return 0
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			_ = conn.Close()
			continue
		}
		var req struct {
			ID string `json:"id"`
			Op string `json:"op"`
		}
		_ = json.Unmarshal(line, &req)
		logLine("op " + req.Op)
		if req.Op == sc.CrashOn {
			os.Exit(4)
		}
		reply := map[string]any{"ok": false, "error": map[string]string{"code": "unknown_op", "message": "no such op"}}
		if r, ok := sc.Replies[req.Op]; ok {
			reply = map[string]any{}
			_ = json.Unmarshal(r, &reply)
		}
		reply["id"] = req.ID
		out, _ := json.Marshal(reply)
		_, _ = conn.Write(append(out, '\n'))
		_ = conn.Close()
	}
}

// Canned replies in the helper's documented shapes, with made-up values.
const (
	holdingsAsOf = "2026-11-17T21:05:00Z"
	cashAsOf     = "2026-11-17T21:05:02Z"
)

func okReply(result any) json.RawMessage {
	b, err := json.Marshal(map[string]any{"ok": true, "result": result})
	if err != nil {
		panic(err)
	}
	return b
}

func errReply(code, message string) json.RawMessage {
	b, err := json.Marshal(map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
	if err != nil {
		panic(err)
	}
	return b
}

type fakeRow = map[string]string

func holdingsResult(rows ...fakeRow) map[string]any {
	if rows == nil {
		rows = []fakeRow{}
	}
	return map[string]any{"as_of": holdingsAsOf, "holdings": rows}
}

func cashResult(amount string) map[string]any {
	return map[string]any{"as_of": cashAsOf, "cash": amount, "buying_power": "50.00"}
}

func row(symbol, qty, cost, opened string) fakeRow {
	return fakeRow{"symbol": symbol, "quantity": qty, "average_cost": cost, "opened_on": opened}
}

// goodReplies is a successful read of two made-up holdings and some cash.
func goodReplies() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		opHoldings: okReply(holdingsResult(
			row("ZZZT", "10", "12.3456", "2025-03-04"),
			row("aaax", "0.5", "100", "2026-01-02"),
		)),
		opCash: okReply(cashResult("1234.56")),
	}
}

// importRun is one run of the subcommand against the fake helper.
type importRun struct {
	f      files
	err    error
	stdout string
	log    []string // the fake helper's starts and calls, in order
}

func runImport(t *testing.T, sc fakeScenario) importRun {
	t.Helper()
	f := writeExamples(t, nil, nil)
	return runImportOn(t, f, sc)
}

func runImportOn(t *testing.T, f files, sc fakeScenario) importRun {
	t.Helper()
	dir := t.TempDir()
	sc.Log = filepath.Join(dir, "helper.log")
	scPath := filepath.Join(dir, "scenario.json")
	raw, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeHelperEnv, scPath)

	var stdout, stderr bytes.Buffer
	e := env{
		args:          []string{importCommand, "-settings", f.settings},
		getenv:        func(string) string { return "" },
		stdout:        &stdout,
		stderr:        &stderr,
		clock:         clock.NewFake(covered),
		helperCommand: []string{os.Args[0]},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := importRun{f: f, err: run(ctx, e), stdout: stdout.String()}

	logText, err := os.ReadFile(sc.Log) //nolint:gosec // a file the test named
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	r.log = strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(logText)), " ", "_"))
	return r
}

// openStore opens the run's database for checking.
func openStore(t *testing.T, f files) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), f.database, covered)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rowCounts returns the number of rows in every table of the database.
func rowCounts(t *testing.T, db *store.Store) map[string]int {
	t.Helper()
	rows, err := db.Reader().QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	counts := map[string]int{}
	for _, n := range names {
		var c int
		// n comes from sqlite_master, not from input.
		if err := db.Reader().QueryRowContext(context.Background(), `SELECT count(*) FROM "`+n+`"`).Scan(&c); err != nil {
			t.Fatal(err)
		}
		counts[n] = c
	}
	return counts
}

// importTables are the only tables an import may add rows to.
var importTables = []string{"holding_snapshots", "snapshot_holdings", "cash_snapshots"}

// checkOnlyImportTablesWritten fails if any table other than the import's
// own (and the migration record) has rows: no order, signal or anything else.
func checkOnlyImportTablesWritten(t *testing.T, counts map[string]int) {
	t.Helper()
	for name, c := range counts {
		if name == "schema_migrations" || slices.Contains(importTables, name) {
			continue
		}
		if c != 0 {
			t.Errorf("table %s has %d rows after the import, want 0", name, c)
		}
	}
}

func TestImportHoldingsRecordsHoldingsAndCash(t *testing.T) {
	r := runImport(t, fakeScenario{Replies: goodReplies()})
	if r.err != nil {
		t.Fatalf("import: %v", r.err)
	}
	if want := []string{"start_data-dir=" + r.f.dir, "op_holdings", "op_cash"}; !slices.Equal(r.log, want) {
		t.Errorf("helper saw %q, want %q", r.log, want)
	}

	db := openStore(t, r.f)
	ctx := context.Background()
	set, err := holdings.Current(ctx, db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	wantAsOf, _ := time.Parse(time.RFC3339, cashAsOf) // the later of the two reads
	if set.Source != holdings.SourceImport || !set.AsOf.Equal(wantAsOf) || set.OverLimit {
		t.Errorf("set = source %s, as of %s, over limit %v; want import, %s, false", set.Source, set.AsOf, set.OverLimit, wantAsOf)
	}
	type got struct{ symbol, qty, cost, opened string }
	var gotRows []got
	for _, h := range set.Holdings {
		if h.Opened.Location().String() != clock.Eastern.String() {
			t.Errorf("%s opened in %s, want Eastern", h.Symbol, h.Opened.Location())
		}
		gotRows = append(gotRows, got{h.Symbol, h.Quantity.String(), h.AverageCost.String(), h.Opened.Format(clock.DateLayout)})
	}
	wantRows := []got{
		{"AAAX", "0.5", "100.00", "2026-01-02"}, // upper-cased
		{"ZZZT", "10", "12.3456", "2025-03-04"}, // average cost kept exactly
	}
	if !slices.Equal(gotRows, wantRows) {
		t.Errorf("holdings = %v, want %v", gotRows, wantRows)
	}
	cash, err := holdings.LatestCash(ctx, db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if cash.Source != holdings.SourceImport || cash.Amount.String() != "1234.56" || !cash.AsOf.Equal(wantAsOf) {
		t.Errorf("cash = %+v", cash)
	}

	counts := rowCounts(t, db)
	checkOnlyImportTablesWritten(t, counts)
	if counts["holding_snapshots"] != 1 || counts["snapshot_holdings"] != 2 || counts["cash_snapshots"] != 1 {
		t.Errorf("import table rows = %v", counts)
	}

	for _, want := range []string{
		"Imported 2 holdings and the cash balance from the broker, as of 2026-11-17T21:05:02Z.",
		"AAAX", "ZZZT", "12.3456", "2025-03-04",
		"Cash: 1234.56",
		"Nothing was ordered.",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output is missing %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "Warning") {
		t.Errorf("two holdings were flagged:\n%s", r.stdout)
	}
}

func TestImportHoldingsRecordsAnEmptyAccount(t *testing.T) {
	r := runImport(t, fakeScenario{Replies: map[string]json.RawMessage{
		opHoldings: okReply(holdingsResult()),
		opCash:     okReply(cashResult("-3.21")), // a negative balance is recorded as it is
	}})
	if r.err != nil {
		t.Fatalf("import: %v", r.err)
	}
	db := openStore(t, r.f)
	set, err := holdings.Current(context.Background(), db.Reader())
	if err != nil || len(set.Holdings) != 0 {
		t.Fatalf("Current = %+v, %v; want an empty recorded set", set, err)
	}
	cash, err := holdings.LatestCash(context.Background(), db.Reader())
	if err != nil || cash.Amount.String() != "-3.21" {
		t.Fatalf("LatestCash = %+v, %v", cash, err)
	}
	if !strings.Contains(r.stdout, "Imported 0 holdings") {
		t.Errorf("output:\n%s", r.stdout)
	}
}

func TestImportHoldingsFlagsMoreThanTwentyHoldings(t *testing.T) {
	var rows []fakeRow
	for i := range holdings.Limit + 1 {
		rows = append(rows, row(fmt.Sprintf("T%02d", i), "1", "10.00", "2026-01-02"))
	}
	r := runImport(t, fakeScenario{Replies: map[string]json.RawMessage{
		opHoldings: okReply(holdingsResult(rows...)),
		opCash:     okReply(cashResult("0.00")),
	}})
	if r.err != nil {
		t.Fatalf("import: %v", r.err)
	}
	db := openStore(t, r.f)
	set, err := holdings.Current(context.Background(), db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if !set.OverLimit || len(set.Holdings) != holdings.Limit+1 {
		t.Errorf("set has %d holdings, over limit %v; want %d, true", len(set.Holdings), set.OverLimit, holdings.Limit+1)
	}
	if !strings.Contains(r.stdout, "Warning: that is more than the limit of 20 holdings. All 21 are recorded") {
		t.Errorf("output does not flag 21 holdings:\n%s", r.stdout)
	}
	checkOnlyImportTablesWritten(t, rowCounts(t, db))
}

// TestImportHoldingsHaltsOnAnyHelperError checks that every kind of failure
// stops the import with nothing written, that the helper is started once and
// never restarted, and that no call is repeated.
func TestImportHoldingsHaltsOnAnyHelperError(t *testing.T) {
	good := goodReplies()
	with := func(op string, reply json.RawMessage) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		for k, v := range good {
			m[k] = v
		}
		m[op] = reply
		return m
	}
	badHoldings := func(rows ...fakeRow) map[string]json.RawMessage {
		return with(opHoldings, okReply(holdingsResult(rows...)))
	}
	tests := []struct {
		name    string
		sc      fakeScenario
		wantLog []string
		check   func(error) bool
	}{
		{
			name:    "login needed on holdings",
			sc:      fakeScenario{Replies: with(opHoldings, errReply("login_needed", "log in to the broker again"))},
			wantLog: []string{"op_holdings"},
			check:   isHelperError("login_needed"),
		},
		{
			name:    "broker error on cash",
			sc:      fakeScenario{Replies: with(opCash, errReply("broker_error", "the broker call failed"))},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   isHelperError("broker_error"),
		},
		{
			name:    "helper exits before it is ready",
			sc:      fakeScenario{ExitBeforeReady: true, Replies: good},
			wantLog: nil,
			check:   func(err error) bool { return errors.Is(err, broker.ErrStart) },
		},
		{
			name:    "helper dies during the holdings call",
			sc:      fakeScenario{CrashOn: opHoldings, Replies: good},
			wantLog: []string{"op_holdings"},
			check:   func(err error) bool { return errors.Is(err, broker.ErrNoReply) },
		},
		{
			name:    "helper dies during the cash call",
			sc:      fakeScenario{CrashOn: opCash, Replies: good},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   func(err error) bool { return errors.Is(err, broker.ErrNoReply) },
		},
		{
			name:    "too many decimal places in a quantity",
			sc:      fakeScenario{Replies: badHoldings(row("ZZZT", "1.1234567", "10", "2026-01-02"))},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   func(err error) bool { return errors.Is(err, broker.ErrBadReply) },
		},
		{
			name:    "a symbol that is not a stock symbol",
			sc:      fakeScenario{Replies: badHoldings(row("ZZ ZT", "1", "10", "2026-01-02"))},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   func(err error) bool { return errors.Is(err, holdings.ErrInvalid) },
		},
		{
			name:    "the same symbol twice",
			sc:      fakeScenario{Replies: badHoldings(row("ZZZT", "1", "10", "2026-01-02"), row("zzzt", "2", "10", "2026-01-02"))},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   func(err error) bool { return errors.Is(err, holdings.ErrInvalid) },
		},
		{
			name:    "a bad opened date",
			sc:      fakeScenario{Replies: badHoldings(row("ZZZT", "1", "10", "2026-13-02"))},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   func(err error) bool { return errors.Is(err, broker.ErrBadReply) },
		},
		{
			name: "a field the import does not know",
			sc: fakeScenario{Replies: badHoldings(fakeRow{
				"symbol": "ZZZT", "quantity": "1", "average_cost": "10", "opened_on": "2026-01-02", "side": "buy",
			})},
			wantLog: []string{"op_holdings"},
			check:   func(err error) bool { return errors.Is(err, broker.ErrBadReply) },
		},
		{
			name:    "a bad cash amount",
			sc:      fakeScenario{Replies: with(opCash, okReply(cashResult("12,00")))},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   func(err error) bool { return errors.Is(err, broker.ErrBadReply) },
		},
		{
			name: "a bad as-of time",
			sc: fakeScenario{Replies: with(opCash, okReply(map[string]any{
				"as_of": "yesterday", "cash": "1.00", "buying_power": "1.00",
			}))},
			wantLog: []string{"op_holdings", "op_cash"},
			check:   func(err error) bool { return errors.Is(err, broker.ErrBadReply) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runImport(t, tt.sc)
			if r.err == nil {
				t.Fatalf("import succeeded, want a halt\n%s", r.stdout)
			}
			if !errors.Is(r.err, errImport) || !strings.Contains(r.err.Error(), "nothing was written") {
				t.Errorf("err = %v, want it to say nothing was written", r.err)
			}
			if !tt.check(r.err) {
				t.Errorf("err = %v, not the expected kind", r.err)
			}
			// One start, never a restart, and no call repeated.
			want := append([]string{"start_data-dir=" + r.f.dir}, tt.wantLog...)
			if !slices.Equal(r.log, want) {
				t.Errorf("helper saw %q, want %q", r.log, want)
			}
			if r.stdout != "" {
				t.Errorf("a halted import printed:\n%s", r.stdout)
			}
			db := openStore(t, r.f)
			for name, c := range rowCounts(t, db) {
				if name != "schema_migrations" && c != 0 {
					t.Errorf("table %s has %d rows after a halted import", name, c)
				}
			}
		})
	}
}

func isHelperError(code string) func(error) bool {
	return func(err error) bool {
		var he *broker.HelperError
		return errors.As(err, &he) && he.Code == code
	}
}

// TestImportHoldingsKeepsTheEarlierSnapshotOnAFailure checks that a failed
// import leaves the earlier import current.
func TestImportHoldingsKeepsTheEarlierSnapshotOnAFailure(t *testing.T) {
	first := runImport(t, fakeScenario{Replies: goodReplies()})
	if first.err != nil {
		t.Fatalf("first import: %v", first.err)
	}
	second := runImportOn(t, first.f, fakeScenario{Replies: map[string]json.RawMessage{
		opHoldings: okReply(holdingsResult(row("NEWX", "1", "1", "2026-11-17"))),
		opCash:     errReply("login_needed", "log in to the broker again"),
	}})
	if second.err == nil {
		t.Fatal("second import succeeded, want a halt")
	}
	db := openStore(t, first.f)
	set, err := holdings.Current(context.Background(), db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set.Find("NEWX"); ok || len(set.Holdings) != 2 {
		t.Errorf("current set after a failed import = %+v, want the first import's", set.Holdings)
	}
	if c := rowCounts(t, db); c["holding_snapshots"] != 1 || c["cash_snapshots"] != 1 {
		t.Errorf("rows = %v, want one snapshot of each", c)
	}
}

func TestImportHoldingsRefusesAStrayArgument(t *testing.T) {
	f := writeExamples(t, nil, nil)
	e := env{
		args:   []string{importCommand, "-settings", f.settings, "now"},
		getenv: func(string) string { return "" },
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		clock:  clock.NewFake(covered),
		// A helper that would fail the test if it were ever started.
		helperCommand: []string{"/nonexistent/helper"},
	}
	err := run(context.Background(), e)
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "now"`) {
		t.Fatalf("err = %v, want the stray argument refused", err)
	}
	if _, err := os.Stat(f.database); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused import created the database: %v", err)
	}
}

// TestImportHoldingsCallsOnlyReadOps checks the import's source: every
// helper call it makes is one of the two reads, and nothing in it names an
// order. The fake helper's call log checks the same thing at run time.
func TestImportHoldingsCallsOnlyReadOps(t *testing.T) {
	src, err := os.ReadFile("importholdings.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, line := range strings.Split(string(src), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "//") || strings.HasPrefix(code, "func ") {
			continue
		}
		if strings.Contains(code, "callStrict(") || strings.Contains(code, ".Call(") {
			calls++
			if !strings.Contains(code, "opHoldings") && !strings.Contains(code, "opCash") && !strings.Contains(code, "client.Call(ctx, op,") {
				t.Errorf("a helper call that is not one of the two reads: %s", code)
			}
		}
		if strings.Contains(strings.ToLower(code), "order") && !strings.Contains(code, `"Nothing was ordered.`) {
			t.Errorf("importholdings.go names an order outside a comment: %s", code)
		}
	}
	if calls != 3 { // the two reads, and callStrict's own client.Call
		t.Errorf("found %d helper calls, want 3", calls)
	}
}

// TestImportHoldingsWithTheRealHelperHaltsWhenNotLoggedIn runs the import
// against the real Python helper with no broker.yaml in the data folder. The
// helper answers login_needed without reaching any broker, and the import
// halts with nothing written.
func TestImportHoldingsWithTheRealHelperHaltsWhenNotLoggedIn(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	src, err := filepath.Abs(filepath.Join("..", "..", "brokerhelper", "src"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", src)
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	f := writeExamples(t, nil, nil)
	var stdout bytes.Buffer
	e := env{
		args:          []string{importCommand, "-settings", f.settings},
		getenv:        func(string) string { return "" },
		stdout:        &stdout,
		stderr:        &bytes.Buffer{},
		clock:         clock.NewFake(covered),
		helperCommand: []string{python, "-B", "-m", "brokerhelper"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err = run(ctx, e)
	if !isHelperError("login_needed")(err) || !errors.Is(err, errImport) {
		t.Fatalf("err = %v, want login_needed and nothing written", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("a halted import printed:\n%s", stdout.String())
	}
	for name, c := range rowCounts(t, openStore(t, f)) {
		if name != "schema_migrations" && c != 0 {
			t.Errorf("table %s has %d rows after a halted import", name, c)
		}
	}
}
