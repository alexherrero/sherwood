package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/store"
)

// settleCommand is the subcommand that records, by hand, a fill
// reconciliation can't record: most often a partial fill that grew, which the
// broker reports with one average price. You read the new shares' price in
// the broker's app first. It calls no broker and resets no halt.
const settleCommand = "settle-order"

const settleUsage = "Usage: sherwood " + settleCommand + " ORDER -filled N -price P [-settings FILE]\n\n" +
	"ORDER is the order's number, as the halt names it (\"Order 12 (...)\"), or its key.\n" +
	"N is the order's total filled shares as the broker's app shows them, not the new shares alone.\n" +
	"P is the price the new shares filled at, in dollars and cents.\n" +
	"If the new shares filled at more than one price, run it once per price, each time with the running total.\n"

// runSettleOrder records one fill of one order by hand; see settleCommand.
func runSettleOrder(ctx context.Context, e env, args []string) error {
	fs := flag.NewFlagSet("sherwood "+settleCommand, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	settingsPath := fs.String("settings", orDefault(e.getenv(envSettings), defaultSettings),
		"path to the app settings file (env "+envSettings+")")
	filledText := fs.String("filled", "", "the order's total filled shares, from the broker's app")
	priceText := fs.String("price", "", "the new shares' fill price, from the broker's app")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), settleUsage)
		fs.PrintDefaults()
	}
	// The order may come before the flags or after all of them.
	var ref string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		ref, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if ref == "" && len(rest) > 0 {
		ref, rest = rest[0], rest[1:]
	}
	switch {
	case len(rest) > 0:
		return fmt.Errorf("%w: unexpected argument %q", execution.ErrHandSettle, rest[0])
	case ref == "":
		return fmt.Errorf("%w: name the order, by its number or key", execution.ErrHandSettle)
	case *filledText == "":
		return fmt.Errorf("%w: -filled is required", execution.ErrHandSettle)
	case *priceText == "":
		return fmt.Errorf("%w: -price is required", execution.ErrHandSettle)
	}
	filled, err := money.ParseQuantity(*filledText)
	if err != nil {
		return fmt.Errorf("%w: -filled %q: %w", execution.ErrHandSettle, *filledText, err)
	}
	price, err := money.ParsePrice(strings.TrimPrefix(*priceText, "$"))
	if err != nil {
		return fmt.Errorf("%w: -price %q must be dollars and whole cents: %w", execution.ErrHandSettle, *priceText, err)
	}

	settings, err := config.LoadSettings(*settingsPath)
	if err != nil {
		return fmt.Errorf("%w: %w", execution.ErrHandSettle, err)
	}
	db, err := store.Open(ctx, settings.DatabasePath, e.clock.Now())
	if err != nil {
		return fmt.Errorf("%w: opening the database: %w", execution.ErrHandSettle, err)
	}
	defer func() { _ = db.Close() }()

	got, err := execution.SettleByHand(ctx, db, clock.NYSE(), e.clock.Now(), ref,
		execution.HandFill{Filled: filled, Price: price})
	if err != nil {
		if !errors.Is(err, execution.ErrHandSettle) {
			err = fmt.Errorf("%w: %w", execution.ErrHandSettle, err)
		}
		return err
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("settle-order: closing the database: %w", err)
	}
	return writeSettleReport(e.stdout, got, filled)
}

// writeSettleReport prints what was recorded, for you to compare with the
// broker's app.
func writeSettleReport(w io.Writer, got execution.HandSettled, filled money.Quantity) error {
	o := got.Order
	var b strings.Builder
	fmt.Fprintf(&b, "Recorded %s more shares of order %d (%s %s %s for %s) at $%s, by the operator, on %s.\n",
		got.Fill.Quantity, o.ID, o.Strategy, o.Kind, o.Symbol, clock.DateOf(o.Date).Format(clock.DateLayout),
		got.Fill.Price, got.Fill.Date.Format(clock.DateLayout))
	fmt.Fprintf(&b, "It now has %s of %s shares filled (%s before) and is %s.\n", filled, o.Quantity, got.Before, o.State)
	held := "none"
	for _, h := range got.Holdings.Holdings {
		if h.Symbol == o.Symbol {
			held = fmt.Sprintf("%s shares at an average cost of %s", h.Quantity, h.AverageCost)
		}
	}
	fmt.Fprintf(&b, "Holdings and cash moved with it, as of %s: %s %s; cash %s.\n",
		got.Cash.AsOf.Format(time.RFC3339), o.Symbol, held, got.Cash.Amount)
	if o.State == orders.PartiallyFilled {
		b.WriteString("The order is still open. If the broker's app shows it ended, the next check looks it up and records that.\n")
	}
	b.WriteString("Nothing was sent to the broker and no halt was reset. Compare with the broker's app, then reset the switch on Today.\n")
	_, err := io.WriteString(w, b.String())
	return err
}
