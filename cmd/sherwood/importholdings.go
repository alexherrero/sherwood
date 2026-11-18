package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

// importCommand is the subcommand that reads the holdings and cash from the
// broker once, read-only, and records them.
const importCommand = "import-holdings"

// The only two helper operations the import uses. Both are reads; the import
// has no path to any order operation.
const (
	opHoldings = "holdings"
	opCash     = "cash"
)

// The import's call timeouts. The holdings call makes one broker request per
// page of positions plus one per position without a symbol, so it gets far
// longer than the client's usual 10s. The cash call is a single request.
const (
	importHoldingsTimeout = 120 * time.Second
	importCashTimeout     = 30 * time.Second
)

// errImport wraps every reason the import stopped. Whatever the reason, it
// wrote nothing.
var errImport = errors.New("import-holdings: nothing was written")

// holdingsReply and cashReply are the helper's answers, in its wire shapes.
// Every decimal travels as a string.
type holdingsReply struct {
	AsOf     string `json:"as_of"`
	Holdings []struct {
		Symbol      string `json:"symbol"`
		Quantity    string `json:"quantity"`
		AverageCost string `json:"average_cost"`
		OpenedOn    string `json:"opened_on"`
	} `json:"holdings"`
}

type cashReply struct {
	AsOf        string `json:"as_of"`
	Cash        string `json:"cash"`
	BuyingPower string `json:"buying_power"`
}

// runImportHoldings starts the broker helper just for this run, reads the
// holdings and the cash once, stops the helper, and records both as one
// import snapshot. Any helper error, bad reply or refused holding stops it
// with nothing written. It never retries: a broker error is a reason to stop
// and look, not to ask again.
func runImportHoldings(ctx context.Context, e env, args []string) error {
	fs := flag.NewFlagSet("sherwood "+importCommand, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	settingsPath := fs.String("settings", orDefault(e.getenv(envSettings), defaultSettings),
		"path to the app settings file (env "+envSettings+")")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: sherwood "+importCommand+" [-settings FILE]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q", importCommand, fs.Arg(0))
	}

	settings, err := config.LoadSettings(*settingsPath)
	if err != nil {
		return fmt.Errorf("%w: %w", errImport, err)
	}
	db, err := store.Open(ctx, settings.DatabasePath, e.clock.Now())
	if err != nil {
		return fmt.Errorf("%w: opening the database: %w", errImport, err)
	}
	defer func() { _ = db.Close() }()

	snap, err := readFromBroker(ctx, e, settings.DataDir)
	if err != nil {
		return fmt.Errorf("%w: %w", errImport, err)
	}
	set, cash, err := holdings.Record(ctx, db, snap)
	if err != nil {
		return fmt.Errorf("%w: %w", errImport, err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("import-holdings: closing the database: %w", err)
	}
	return writeImportReport(e.stdout, set, cash)
}

// readFromBroker starts a helper on a private socket, makes the two read
// calls and stops the helper again. The helper is supervised only for this
// call: its first failure ends the run, so it is never restarted and no call
// is ever repeated.
func readFromBroker(ctx context.Context, e env, dataDir string) (holdings.Snapshot, error) {
	sockDir, err := os.MkdirTemp("", "swi") // short: Unix socket paths are limited
	if err != nil {
		return holdings.Snapshot{}, fmt.Errorf("making the broker socket folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(sockDir) }()

	cmd := helperCommand
	if e.helperCommand != nil {
		cmd = e.helperCommand
	}
	cmd = append(append([]string(nil), cmd...), "--data-dir", dataDir)

	helperCtx, stopHelper := context.WithCancelCause(ctx)
	defer stopHelper(nil)
	ready := make(chan struct{})
	var once sync.Once
	sup, err := broker.NewSupervisor(broker.SupervisorConfig{
		Command:     cmd,
		SocketPath:  filepath.Join(sockDir, "h.sock"),
		CallTimeout: importHoldingsTimeout,
		Clock:       e.clock,
		Stderr:      e.stderr,
		// The first start failure or exit ends the run, before the
		// supervisor's restart delay.
		OnError: func(err error) { stopHelper(fmt.Errorf("the broker helper stopped: %w", err)) },
		OnReady: func() { once.Do(func() { close(ready) }) },
	})
	if err != nil {
		return holdings.Snapshot{}, err
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = sup.Run(helperCtx)
	}()
	// The helper is stopped, and has exited, before anything is written.
	defer func() {
		stopHelper(nil)
		<-stopped
	}()

	select {
	case <-ready:
	case <-helperCtx.Done():
		return holdings.Snapshot{}, context.Cause(helperCtx)
	}

	client := sup.Client()
	var h holdingsReply
	if err := callStrict(helperCtx, client, opHoldings, importHoldingsTimeout, &h); err != nil {
		return holdings.Snapshot{}, err
	}
	var c cashReply
	if err := callStrict(helperCtx, client, opCash, importCashTimeout, &c); err != nil {
		return holdings.Snapshot{}, err
	}
	return toSnapshot(h, c)
}

// callStrict makes one call and decodes its result, refusing fields the
// import does not know: a changed reply shape stops the import rather than
// being half read.
func callStrict(ctx context.Context, client *broker.Client, op string, timeout time.Duration, result any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw json.RawMessage
	if err := client.Call(ctx, op, nil, &raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(result); err != nil {
		return fmt.Errorf("%w: %s: %w", broker.ErrBadReply, op, err)
	}
	return nil
}

// toSnapshot turns the helper's replies into an import snapshot. Symbols are
// upper-cased; anything that still fails the holdings rules, and any decimal
// or date that does not parse, stops the import.
func toSnapshot(h holdingsReply, c cashReply) (holdings.Snapshot, error) {
	holdingsAt, err := parseAsOf(opHoldings, h.AsOf)
	if err != nil {
		return holdings.Snapshot{}, err
	}
	cashAt, err := parseAsOf(opCash, c.AsOf)
	if err != nil {
		return holdings.Snapshot{}, err
	}
	// The snapshot is as of the later read, by which time both were taken.
	asOf := holdingsAt
	if cashAt.After(asOf) {
		asOf = cashAt
	}

	hs := make([]holdings.Holding, 0, len(h.Holdings))
	for i, row := range h.Holdings {
		symbol := strings.ToUpper(strings.TrimSpace(row.Symbol))
		if symbol == "" {
			return holdings.Snapshot{}, fmt.Errorf("%w: holdings: row %d has no symbol", broker.ErrBadReply, i+1)
		}
		qty, err := money.ParseQuantity(row.Quantity)
		if err != nil {
			return holdings.Snapshot{}, fmt.Errorf("%w: holdings: %s quantity: %w", broker.ErrBadReply, symbol, err)
		}
		cost, err := money.ParseMoney(row.AverageCost)
		if err != nil {
			return holdings.Snapshot{}, fmt.Errorf("%w: holdings: %s average cost: %w", broker.ErrBadReply, symbol, err)
		}
		opened, err := time.ParseInLocation(clock.DateLayout, row.OpenedOn, clock.Eastern)
		if err != nil {
			return holdings.Snapshot{}, fmt.Errorf("%w: holdings: %s opened date %q is not YYYY-MM-DD", broker.ErrBadReply, symbol, row.OpenedOn)
		}
		hs = append(hs, holdings.Holding{Symbol: symbol, Quantity: qty, AverageCost: cost, Opened: opened})
	}
	cash, err := money.ParseMoney(c.Cash)
	if err != nil {
		return holdings.Snapshot{}, fmt.Errorf("%w: cash: %w", broker.ErrBadReply, err)
	}
	return holdings.Snapshot{Source: holdings.SourceImport, AsOf: asOf, Holdings: hs, Cash: cash}, nil
}

func parseAsOf(op, s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s: as_of %q is not an RFC 3339 time", broker.ErrBadReply, op, s)
	}
	return t.UTC(), nil
}

// writeImportReport prints what was recorded, for the operator to compare
// with the broker's own app.
func writeImportReport(w io.Writer, set holdings.Set, cash holdings.Cash) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Imported %d holdings and the cash balance from the broker, as of %s.\n",
		len(set.Holdings), set.AsOf.Format(time.RFC3339))
	if set.OverLimit {
		fmt.Fprintf(&b, "Warning: that is more than the limit of %d holdings. All %d are recorded, "+
			"flagged as over the limit.\n",
			holdings.Limit, len(set.Holdings))
	}
	if len(set.Holdings) > 0 {
		b.WriteString("\n")
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "Symbol\tShares\tAverage cost\tOpened")
		for _, h := range set.Holdings {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", h.Symbol, h.Quantity, h.AverageCost, h.Opened.Format(clock.DateLayout))
		}
		_ = tw.Flush()
	}
	fmt.Fprintf(&b, "\nCash: %s\n", cash.Amount)
	b.WriteString("Nothing was ordered. Compare these figures with the broker's app before any strategy runs.\n")
	_, err := io.WriteString(w, b.String())
	return err
}
