package prices_test

import (
	"context"
	"strings"
	"testing"
)

// The table constraints are the last line of defense; these show they hold
// even when Go code is bypassed.
func TestSchemaRefusesBadRows(t *testing.T) {
	h := newHarness(t)
	db := h.store.Writer()
	ctx := context.Background()
	const at = "2026-01-07T22:00:00.000000000Z"
	if _, err := db.ExecContext(ctx, `INSERT INTO price_days (trading_date, primary_source, check_source, tolerance, usable, detail, checked_at)
		VALUES ('2026-01-07', 'primary', 'cross', '0.02', 1, '', ?)`, at); err != nil {
		t.Fatal(err)
	}
	bar := `INSERT INTO price_bars (symbol, trading_date, source, open, high, low, close, volume, fetched_at) VALUES `
	refused := []struct{ name, sql string }{
		{"real price", bar + `('AAPL', '2026-01-07', 'primary', 100.5, '101.00', '99.00', '100.00', 1, '` + at + `')`},
		{"sub-cent price", bar + `('AAPL', '2026-01-07', 'primary', '100.005', '101.00', '99.00', '100.00', 1, '` + at + `')`},
		{"negative price", bar + `('AAPL', '2026-01-07', 'primary', '-1.00', '101.00', '99.00', '100.00', 1, '` + at + `')`},
		{"two points", bar + `('AAPL', '2026-01-07', 'primary', '1.0.00', '101.00', '99.00', '100.00', 1, '` + at + `')`},
		{"negative volume", bar + `('AAPL', '2026-01-07', 'primary', '100.00', '101.00', '99.00', '100.00', -1, '` + at + `')`},
		{"lower-case symbol", bar + `('aapl', '2026-01-07', 'primary', '100.00', '101.00', '99.00', '100.00', 1, '` + at + `')`},
		{"usable day with detail", `INSERT INTO price_days VALUES ('2026-01-08', 'primary', 'cross', '0.02', 1, 'no bar', '` + at + `')`},
		{"same source twice", `INSERT INTO price_days VALUES ('2026-01-08', 'primary', 'primary', '0.02', 0, 'x', '` + at + `')`},
		{"halt on a watched stock", `INSERT INTO price_checks VALUES ('2026-01-07', 'AAPL', 0, 'disagreed', 1, 'x')`},
		{"held disagreement without halt", `INSERT INTO price_checks VALUES ('2026-01-07', 'AAPL', 1, 'disagreed', 0, 'x')`},
		{"agreed with detail", `INSERT INTO price_checks VALUES ('2026-01-07', 'AAPL', 0, 'agreed', 0, 'x')`},
		{"unknown outcome", `INSERT INTO price_checks VALUES ('2026-01-07', 'AAPL', 0, 'stale', 0, 'x')`},
		{"check for an unchecked day", `INSERT INTO price_checks VALUES ('2026-01-09', 'AAPL', 0, 'agreed', 0, '')`},
	}
	for _, tt := range refused {
		if _, err := db.ExecContext(ctx, tt.sql); err == nil {
			t.Errorf("%s: accepted", tt.name)
		}
	}
	ok := bar + `('AAPL', '2026-01-07', 'primary', '100.00', '101.00', '99.00', '100.00', 1, '` + at + `')`
	if _, err := db.ExecContext(ctx, ok); err != nil {
		t.Fatalf("a good bar was refused: %v", err)
	}
	if _, err := db.ExecContext(ctx, ok); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("a second bar for the same stock, date and source: %v", err)
	}
}
