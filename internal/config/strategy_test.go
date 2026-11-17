package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/money"
)

// validStrategy is a complete, valid strategy file with placeholder values.
// Each limit sits on its own line so a test can remove one.
const validStrategy = `version: 1
limits:
  max_stock_share: 0.10
  max_order_value: "100.00"
  max_orders_per_day: 3
  daily_loss_limit: "50.00"
  max_holdings: 20
  max_sector_share: 0.30
  max_limit_distance: 0.05
  buy_limit_margin: 0.01
  fractional_shares: false
  price_cross_check_tolerance: 0.02
  min_paper_weeks: 4
watchlist: [AAPL, MSFT, BRK.B]
strategies:
  - name: alpha
    portfolio_share: 0.60
    stocks: [AAPL]
    rules:
      - type: moving_average_cross
        settings: {fast_days: 20, slow_days: 50}
      - type: fixed_stop
        settings: {distance: 0.08}
  - name: beta
    portfolio_share: 0.40
    stocks: [MSFT, BRK.B]
    rules:
      - type: breakout
`

// requiredLimits is every limit the strategy file must set.
var requiredLimits = []string{
	"max_stock_share",
	"max_order_value",
	"max_orders_per_day",
	"daily_loss_limit",
	"max_holdings",
	"max_limit_distance",
	"buy_limit_margin",
	"fractional_shares",
	"price_cross_check_tolerance",
	"min_paper_weeks",
}

// withoutLine drops the line starting with prefix, failing if there is none.
func withoutLine(t *testing.T, doc, prefix string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	out := lines[:0]
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			found = true
			continue
		}
		out = append(out, l)
	}
	if !found {
		t.Fatalf("no line starts with %q", prefix)
	}
	return strings.Join(out, "\n")
}

// replaced replaces old with new exactly once, failing if old is absent.
func replaced(t *testing.T, doc, old, new string) string {
	t.Helper()
	if strings.Count(doc, old) != 1 {
		t.Fatalf("want exactly one %q in the document", old)
	}
	return strings.Replace(doc, old, new, 1)
}

// refuse parses doc and requires a ValidationError naming every want. It
// returns the reported problems.
func refuse(t *testing.T, doc string, want ...string) []string {
	t.Helper()
	s, err := ParseStrategy([]byte(doc))
	if err == nil {
		t.Fatalf("ParseStrategy accepted the file: %+v", s)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("error does not wrap ErrInvalid: %v", err)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error is %T, want *ValidationError: %v", err, err)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error does not mention %q:\n%v", w, err)
		}
	}
	return ve.Problems
}

func TestValidStrategyParses(t *testing.T) {
	s, err := ParseStrategy([]byte(validStrategy))
	if err != nil {
		t.Fatal(err)
	}
	l := s.Limits
	checks := []struct{ name, got, want string }{
		{"max_stock_share", l.MaxStockShare.String(), "0.10"},
		{"max_order_value", l.MaxOrderValue.String(), "100.00"},
		{"daily_loss_limit", l.DailyLossLimit.String(), "50.00"},
		{"max_limit_distance", l.MaxLimitDistance.String(), "0.05"},
		{"buy_limit_margin", l.BuyLimitMargin.String(), "0.01"},
		{"price_cross_check_tolerance", l.PriceCrossCheckTolerance.String(), "0.02"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q (decimals must keep their written text)", c.name, c.got, c.want)
		}
	}
	if !l.MaxOrderValue.Equal(money.Cents(10000)) || !l.DailyLossLimit.Equal(money.Cents(5000)) {
		t.Errorf("amounts = %s and %s, want 100.00 and 50.00", l.MaxOrderValue, l.DailyLossLimit)
	}
	if l.MaxSectorShare == nil || l.MaxSectorShare.String() != "0.30" {
		t.Errorf("max_sector_share = %v, want 0.30", l.MaxSectorShare)
	}
	if l.MaxOrdersPerDay != 3 || l.MaxHoldings != 20 || l.MinPaperWeeks != 4 || l.FractionalShares {
		t.Errorf("integer and boolean limits wrong: %+v", l)
	}
	if got := strings.Join(s.Watchlist, ","); got != "AAPL,MSFT,BRK.B" {
		t.Errorf("watchlist = %s", got)
	}
	if len(s.Strategies) != 2 || s.Strategies[0].Name != "alpha" || s.Strategies[1].Name != "beta" {
		t.Fatalf("strategies = %+v", s.Strategies)
	}
	r := s.Strategies[0].Rules[0]
	if r.Type != RuleMovingAverageCross || r.Settings["fast_days"].String() != "20" {
		t.Errorf("first rule = %+v", r)
	}
	if got := s.Strategies[0].Rules[1].Settings["distance"].String(); got != "0.08" {
		t.Errorf("fixed_stop distance = %q, want the text 0.08", got)
	}
	if s.Strategies[1].Rules[0].Settings != nil {
		t.Errorf("a rule with no settings should have nil settings")
	}
}

func TestEachMissingLimitIsRefusedByName(t *testing.T) {
	for _, name := range requiredLimits {
		t.Run(name, func(t *testing.T) {
			doc := withoutLine(t, validStrategy, "  "+name+":")
			got := refuse(t, doc, "limits."+name+" is missing")
			if len(got) != 1 {
				t.Errorf("want only the missing limit reported, got %q", got)
			}
		})
	}
}

func TestEveryMissingLimitIsReportedAtOnce(t *testing.T) {
	doc := validStrategy
	for _, name := range requiredLimits {
		doc = withoutLine(t, doc, "  "+name+":")
	}
	got := refuse(t, doc)
	if len(got) != len(requiredLimits) {
		t.Errorf("got %d problems, want %d: %q", len(got), len(requiredLimits), got)
	}
	for _, name := range requiredLimits {
		if !slices.Contains(got, "limits."+name+" is missing") {
			t.Errorf("%s not reported", name)
		}
	}
}

func TestEmptyLimitValueCountsAsMissing(t *testing.T) {
	doc := replaced(t, validStrategy, "max_order_value: \"100.00\"", "max_order_value:")
	refuse(t, doc, "limits.max_order_value is missing")
}

func TestMissingLimitsBlockIsRefused(t *testing.T) {
	doc := validStrategy
	doc = withoutLine(t, doc, "limits:")
	for _, name := range append(slices.Clone(requiredLimits), "max_sector_share") {
		doc = withoutLine(t, doc, "  "+name+":")
	}
	refuse(t, doc, "limits is missing", "limits.max_stock_share is missing", "limits.min_paper_weeks is missing")
}

func TestSectorShareIsOptional(t *testing.T) {
	doc := withoutLine(t, validStrategy, "  max_sector_share:")
	s, err := ParseStrategy([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if s.Limits.MaxSectorShare != nil {
		t.Errorf("max_sector_share = %v, want nil when left out", s.Limits.MaxSectorShare)
	}
}

func TestUnknownRuleTypeIsRefused(t *testing.T) {
	doc := replaced(t, validStrategy, "type: breakout", "type: moon_shot")
	refuse(t, doc, `strategy "beta"`, `unknown rule type "moon_shot"`)
}

func TestEveryRegisteredRuleTypeIsAccepted(t *testing.T) {
	want := []string{
		"moving_average_cross", "breakout", "momentum_ranking", "pullback",
		"fixed_stop", "trailing_stop", "size_fixed_amount", "size_by_risk",
	}
	if got := RuleTypes(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("RuleTypes() = %v, want %v", got, want)
	}
	for _, rt := range want {
		doc := replaced(t, validStrategy, "type: breakout", "type: "+rt)
		if _, err := ParseStrategy([]byte(doc)); err != nil {
			t.Errorf("rule type %s refused: %v", rt, err)
		}
	}
}

func TestOverAllocatedSharesAreRefused(t *testing.T) {
	tests := []struct {
		name, alpha, beta, wantSum string
	}{
		{"just over", "0.60", "0.41", "1.01"},
		{"tiny excess", "0.6000", "0.4001", "1.0001"},
		{"both whole", "1", "1", "2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := replaced(t, validStrategy, "portfolio_share: 0.60", "portfolio_share: "+tc.alpha)
			doc = replaced(t, doc, "portfolio_share: 0.40", "portfolio_share: "+tc.beta)
			refuse(t, doc, "portfolio_share values add up to "+tc.wantSum, "more than the whole portfolio")
		})
	}
}

func TestSharesAddingToExactlyOneAreAccepted(t *testing.T) {
	// 0.1 + 0.2 is not 0.3 in floating point; the exact sum must be.
	doc := replaced(t, validStrategy, "portfolio_share: 0.60", "portfolio_share: 0.7")
	doc = replaced(t, doc, "portfolio_share: 0.40", "portfolio_share: 0.3")
	if _, err := ParseStrategy([]byte(doc)); err != nil {
		t.Fatalf("shares adding to exactly 1 refused: %v", err)
	}
	three := validStrategy + `  - name: gamma
    portfolio_share: 0.1
    stocks: [KO]
    rules:
      - type: pullback
`
	three = replaced(t, three, "portfolio_share: 0.60", "portfolio_share: 0.2")
	three = replaced(t, three, "portfolio_share: 0.40", "portfolio_share: 0.7")
	if _, err := ParseStrategy([]byte(three)); err != nil {
		t.Fatalf("0.2 + 0.7 + 0.1 refused: %v", err)
	}
}

func TestBadValuesAreRefusedByName(t *testing.T) {
	tests := []struct {
		name, old, new, want string
	}{
		{"stock share zero", "max_stock_share: 0.10", "max_stock_share: 0", "limits.max_stock_share is 0, but it must be greater than 0"},
		{"stock share as percent", "max_stock_share: 0.10", "max_stock_share: 10", "limits.max_stock_share is 10, but it must be at most 1"},
		{"order value negative", `max_order_value: "100.00"`, `max_order_value: "-1"`, "limits.max_order_value is -1, but it must be greater than 0"},
		{"order value sub-cent", `max_order_value: "100.00"`, `max_order_value: "100.001"`, "limits.max_order_value is 100.001, but an amount has at most 2 decimal places"},
		{"loss limit zero", `daily_loss_limit: "50.00"`, `daily_loss_limit: "0.00"`, "limits.daily_loss_limit is 0.00"},
		{"loss limit negative zero", `daily_loss_limit: "50.00"`, `daily_loss_limit: -0`, "limits.daily_loss_limit is -0, but it must be greater than 0"},
		{"loss limit sub-cent unquoted", `daily_loss_limit: "50.00"`, `daily_loss_limit: 0.005`, "limits.daily_loss_limit is 0.005, but an amount has at most 2 decimal places"},
		{"amount exponent", `max_order_value: "100.00"`, `max_order_value: 1e2`, "expected a plain decimal number"},
		{"amount word", `max_order_value: "100.00"`, `max_order_value: "a hundred"`, "expected a plain decimal number"},
		{"amount with dollar sign", `daily_loss_limit: "50.00"`, `daily_loss_limit: "$50"`, "expected a plain decimal number"},
		{"amount as list", `max_order_value: "100.00"`, `max_order_value: [100]`, "expected a single value"},
		{"orders per day zero", "max_orders_per_day: 3", "max_orders_per_day: 0", "limits.max_orders_per_day is 0"},
		{"holdings over 20", "max_holdings: 20", "max_holdings: 21", "limits.max_holdings is 21, but it must be from 1 to 20"},
		{"holdings zero", "max_holdings: 20", "max_holdings: 0", "limits.max_holdings is 0"},
		{"sector share over 1", "max_sector_share: 0.30", "max_sector_share: 1.5", "limits.max_sector_share is 1.5"},
		{"limit distance 1", "max_limit_distance: 0.05", "max_limit_distance: 1", "limits.max_limit_distance is 1, but it must be less than 1"},
		{"margin negative", "buy_limit_margin: 0.01", "buy_limit_margin: -0.01", "limits.buy_limit_margin is -0.01, but it must not be negative"},
		{"margin beyond distance", "buy_limit_margin: 0.01", "buy_limit_margin: 0.06", "limits.buy_limit_margin (0.06) is larger than limits.max_limit_distance (0.05)"},
		{"tolerance zero", "price_cross_check_tolerance: 0.02", "price_cross_check_tolerance: 0", "limits.price_cross_check_tolerance is 0"},
		{"paper weeks zero", "min_paper_weeks: 4", "min_paper_weeks: 0", "limits.min_paper_weeks is 0, but it must be at least 1"},
		{"strategy share zero", "portfolio_share: 0.40", "portfolio_share: 0", `strategy "beta": portfolio_share is 0`},
		{"exponent", "max_stock_share: 0.10", "max_stock_share: 1e-1", "expected a plain decimal number"},
		{"infinity", "max_stock_share: 0.10", "max_stock_share: .inf", "expected a plain decimal number"},
		{"word", "max_stock_share: 0.10", "max_stock_share: ten", "expected a plain decimal number"},
		{"list as decimal", "max_stock_share: 0.10", "max_stock_share: [0.1]", "expected a single value"},
		{"fractional not bool", "fractional_shares: false", "fractional_shares: maybe", "cannot unmarshal"},
		{"orders per day not int", "max_orders_per_day: 3", "max_orders_per_day: 3.5", "expected a whole number"},
		{"bad watchlist symbol", "watchlist: [AAPL, MSFT, BRK.B]", "watchlist: [AAPL, msft, BRK.B]", `watchlist[1] is "msft"`},
		{"long symbol", "watchlist: [AAPL, MSFT, BRK.B]", "watchlist: [AAPL, ABCDEFGHIJK, BRK.B]", `watchlist[1] is "ABCDEFGHIJK"`},
		{"repeated watchlist symbol", "watchlist: [AAPL, MSFT, BRK.B]", "watchlist: [AAPL, MSFT, AAPL]", "watchlist lists AAPL more than once"},
		{"bad strategy stock", "stocks: [AAPL]", "stocks: [$AAPL]", `strategy "alpha": stocks[0] is "$AAPL"`},
		{"empty stocks", "stocks: [AAPL]", "stocks: []", `strategy "alpha": stocks is missing or empty`},
		{"duplicate name", "name: beta", "name: alpha", `strategy name "alpha" is used more than once`},
		{"missing name", "  - name: beta\n    portfolio_share: 0.40", "  - portfolio_share: 0.40", "strategies[1].name is missing"},
		{"version wrong", "version: 1", "version: 2", "version is 2, but this build reads version 1"},
		{"unknown limit", "  min_paper_weeks: 4", "  min_paper_weeks: 4\n  max_leverage: 2", "field max_leverage not found"},
		{"unknown top-level field", "version: 1", "version: 1\naccount: x", "field account not found"},
		{"rule setting is a list", "settings: {distance: 0.08}", "settings: {distance: [1, 2]}", "expected a single value"},
		{"rule type missing", "      - type: breakout", "      - settings: {x: 1}", `strategy "beta": rules[0].type is missing`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refuse(t, replaced(t, validStrategy, tc.old, tc.new), tc.want)
		})
	}
}

func TestAmountsAreReadExactlyAsMoney(t *testing.T) {
	// Unquoted, these would be floats to the YAML library; 0.29 and 100.10
	// have no exact float form. They must arrive as exact money.
	doc := replaced(t, validStrategy, `max_order_value: "100.00"`, "max_order_value: 100.10")
	doc = replaced(t, doc, `daily_loss_limit: "50.00"`, "daily_loss_limit: 0.29")
	s, err := ParseStrategy([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Limits.MaxOrderValue; !got.Equal(money.Cents(10010)) || got.String() != "100.10" {
		t.Errorf("max_order_value = %s, want exactly 100.10", got)
	}
	if got := s.Limits.DailyLossLimit; !got.Equal(money.Cents(29)) || got.String() != "0.29" {
		t.Errorf("daily_loss_limit = %s, want exactly 0.29", got)
	}

	doc = replaced(t, validStrategy, `max_order_value: "100.00"`, "max_order_value: 250")
	s, err = ParseStrategy([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Limits.MaxOrderValue; !got.Equal(money.Cents(25000)) {
		t.Errorf("max_order_value = %s, want 250", got)
	}
}

func TestStructuralProblemsAreRefused(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"empty file", "", "the file is empty"},
		{"comment only", "# nothing here\n", "the file is empty"},
		{"two documents", validStrategy + "---\nversion: 1\n", "more than one YAML document"},
		{"repeated key", replaced(t, validStrategy, "  max_holdings: 20\n", "  max_holdings: 20\n  max_holdings: 5\n"), "already defined"},
		{"not yaml", "limits: [\n", "yaml:"},
		{"no version", withoutLine(t, validStrategy, "version:"), "version is missing"},
		{"no strategies", validStrategy[:strings.Index(validStrategy, "strategies:")], "strategies is missing or empty"},
		{"no rules", replaced(t, validStrategy, "    rules:\n      - type: breakout\n", ""), `strategy "beta": rules is missing or empty`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refuse(t, tc.doc, tc.want)
		})
	}
}

func TestHashIsSHA256OfTheFileBytes(t *testing.T) {
	data := []byte(validStrategy)
	s, err := ParseStrategy(data)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if want := hex.EncodeToString(sum[:]); s.Hash != want {
		t.Errorf("Hash = %s, want %s", s.Hash, want)
	}
	if len(s.Hash) != 64 {
		t.Errorf("Hash has %d characters, want 64", len(s.Hash))
	}

	// Any change to the bytes, even a comment, changes the hash.
	s2, err := ParseStrategy(append([]byte("# edited\n"), data...))
	if err != nil {
		t.Fatal(err)
	}
	if s2.Hash == s.Hash {
		t.Error("a comment changed the file but not its hash")
	}
	// The same bytes always give the same hash.
	s3, err := ParseStrategy(data)
	if err != nil {
		t.Fatal(err)
	}
	if s3.Hash != s.Hash {
		t.Error("the same bytes gave two hashes")
	}
}

func TestLoadStrategyReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategy.yaml")
	if err := os.WriteFile(path, []byte(validStrategy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadStrategy(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Hash != HashBytes([]byte(validStrategy)) {
		t.Error("LoadStrategy hash differs from the file's bytes")
	}
	if _, err := LoadStrategy(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v, want one wrapping os.ErrNotExist", err)
	}
}

func TestStrategyExampleValidates(t *testing.T) {
	s, err := LoadStrategy(filepath.Join("..", "..", "strategy.example.yaml"))
	if err != nil {
		t.Fatalf("strategy.example.yaml does not validate: %v", err)
	}
	if len(s.Strategies) == 0 || len(s.Watchlist) == 0 {
		t.Error("the example should show at least one strategy and a watchlist")
	}
	if s.Limits.MaxSectorShare == nil {
		t.Error("the example should show the optional sector limit")
	}
}

func TestDecimal(t *testing.T) {
	for _, ok := range []string{"0", "0.10", "250", "-1.5", "007"} {
		d, err := ParseDecimal(ok)
		if err != nil || d.String() != ok {
			t.Errorf("ParseDecimal(%q) = %v, %v", ok, d, err)
		}
	}
	for _, bad := range []string{"", ".5", "5.", "1e3", "NaN", "+1", "0x10", "1,000", " 1"} {
		if _, err := ParseDecimal(bad); err == nil {
			t.Errorf("ParseDecimal(%q) accepted", bad)
		}
	}
	if got := MustDecimal("0.125").Places(); got != 3 {
		t.Errorf("Places = %d, want 3", got)
	}
	if got := MustDecimal("0.10").Rat().RatString(); got != "1/10" {
		t.Errorf("Rat = %s, want 1/10", got)
	}
	if !(Decimal{}).IsZero() || MustDecimal("0").IsZero() {
		t.Error("IsZero should mean unset, not zero-valued")
	}
}
