package rules

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
)

// registryDiff lists every name on one side only.
func registryDiff(library, cfg []string) []string {
	var out []string
	for _, n := range library {
		if !slices.Contains(cfg, n) {
			out = append(out, fmt.Sprintf("%q is in the rule library but not in config.RuleTypes", n))
		}
	}
	for _, n := range cfg {
		if !slices.Contains(library, n) {
			out = append(out, fmt.Sprintf("%q is in config.RuleTypes but has no rule in the library", n))
		}
	}
	return out
}

// TestRegistryMatchesConfig fails if the rule library and config's known rule
// types disagree in either direction, or if either repeats a name.
func TestRegistryMatchesConfig(t *testing.T) {
	library, cfg := Types(), config.RuleTypes()
	for _, d := range registryDiff(library, cfg) {
		t.Error(d)
	}
	for _, list := range [][]string{library, cfg} {
		seen := map[string]bool{}
		for _, n := range list {
			if seen[n] {
				t.Errorf("%q is listed twice", n)
			}
			seen[n] = true
		}
	}
	if !slices.Equal(library, cfg) {
		t.Errorf("order differs: library %v, config %v", library, cfg)
	}
}

// TestRegistryDiffCatchesOneSidedNames proves the registry test fails when a
// name is added to one side only.
func TestRegistryDiffCatchesOneSidedNames(t *testing.T) {
	cfg := config.RuleTypes()
	if d := registryDiff(append(Types(), "moon_shot"), cfg); len(d) != 1 || !strings.Contains(d[0], "not in config.RuleTypes") {
		t.Errorf("library-only name: %v", d)
	}
	if d := registryDiff(Types(), append(cfg, "moon_shot")); len(d) != 1 || !strings.Contains(d[0], "has no rule in the library") {
		t.Errorf("config-only name: %v", d)
	}
	if d := registryDiff(Types(), cfg); len(d) != 0 {
		t.Errorf("same lists reported: %v", d)
	}
}

// TestEveryRuleParsesToItsKind checks each definition's rule has the type and
// kind the registry says, and implements the matching interface.
func TestEveryRuleParsesToItsKind(t *testing.T) {
	valid := map[string][]string{
		config.RuleMovingAverageCross: {"fast_days", "20", "slow_days", "50"},
		config.RuleBreakout:           {"days", "50", "exit_days", "20"},
		config.RuleMomentumRanking:    {"days", "60", "top", "3", "exit_rank", "5"},
		config.RulePullback:           {"trend_days", "100", "high_days", "20", "pullback", "0.05"},
		config.RuleFixedStop:          {"distance", "0.08"},
		config.RuleTrailingStop:       {"distance", "0.10"},
		config.RuleSizeFixedAmount:    {"amount", "100.00"},
		config.RuleSizeByRisk:         {"risk_per_trade", "0.01"},
	}
	for _, def := range Definitions() {
		kv, ok := valid[def.Type]
		if !ok {
			t.Errorf("%s: no valid settings in this test", def.Type)
			continue
		}
		r := mustParse(t, def.Type, kv...)
		if r.Type() != def.Type || r.Kind() != def.Kind {
			t.Errorf("%s: parsed to %s / %s, registry says %s", def.Type, r.Type(), r.Kind(), def.Kind)
		}
		var fits bool
		switch def.Kind {
		case KindSignal:
			_, fits = r.(SignalRule)
		case KindRanking:
			_, fits = r.(RankingRule)
		case KindStop:
			_, fits = r.(StopRule)
		case KindSizing:
			_, fits = r.(SizingRule)
		}
		if !fits {
			t.Errorf("%s: does not implement the %s interface", def.Type, def.Kind)
		}
		if def.Summary == "" || len(def.Settings) == 0 {
			t.Errorf("%s: missing summary or settings", def.Type)
		}
		for _, s := range def.Settings {
			if s.Doc == "" || s.Range() == "" {
				t.Errorf("%s.%s: missing doc or range", def.Type, s.Name)
			}
		}
	}
}

func TestParseUnknownType(t *testing.T) {
	_, err := Parse(config.Rule{Type: "moon_shot"})
	if err == nil || !strings.Contains(err.Error(), `unknown rule type "moon_shot"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestBadSettings(t *testing.T) {
	cases := []struct {
		ruleType string
		settings []string
		problems []string
	}{
		{config.RuleMovingAverageCross, nil, []string{
			"fast_days is missing; it must be a whole number from 2 to 299",
			"slow_days is missing; it must be a whole number from 3 to 300",
		}},
		{config.RuleMovingAverageCross, []string{"fast_days", "50", "slow_days", "20"}, []string{
			"slow_days is 20, but it must be more than fast_days (50)",
		}},
		{config.RuleMovingAverageCross, []string{"fast_days", "20", "slow_days", "20"}, []string{
			"slow_days is 20, but it must be more than fast_days (20)",
		}},
		{config.RuleMovingAverageCross, []string{"fast_days", "1", "slow_days", "301"}, []string{
			`fast_days is "1", but it must be a whole number from 2 to 299`,
			`slow_days is "301", but it must be a whole number from 3 to 300`,
		}},
		{config.RuleMovingAverageCross, []string{"fast_days", "20.5", "slow_days", "abc"}, []string{
			`fast_days is "20.5", but it must be a whole number from 2 to 299`,
			`slow_days is "abc", but it must be a whole number from 3 to 300`,
		}},
		{config.RuleMovingAverageCross, []string{"fast_days", "2e1", "slow_days", "-50"}, []string{
			`fast_days is "2e1", but it must be a whole number from 2 to 299`,
			`slow_days is "-50", but it must be a whole number from 3 to 300`,
		}},
		{config.RuleMovingAverageCross, []string{"fast_days", "20", "slow_days", "50", "zeta", "1", "fats_days", "2"}, []string{
			`unknown setting "fats_days" (known settings: fast_days, slow_days)`,
			`unknown setting "zeta" (known settings: fast_days, slow_days)`,
		}},
		{config.RuleBreakout, []string{"days", "0", "exit_days", "+5"}, []string{
			`days is "0", but it must be a whole number from 2 to 300`,
			`exit_days is "+5", but it must be a whole number from 2 to 300`,
		}},
		{config.RuleMomentumRanking, []string{"days", "60", "top", "5", "exit_rank", "3"}, []string{
			"exit_rank is 3, but it must be at least top (5)",
		}},
		{config.RuleMomentumRanking, []string{"days", "60", "top", "21", "exit_rank", "101"}, []string{
			`top is "21", but it must be a whole number from 1 to 20`,
			`exit_rank is "101", but it must be a whole number from 1 to 100`,
		}},
		{config.RulePullback, []string{"trend_days", "100", "high_days", "20", "pullback", "0"}, []string{
			`pullback is "0", but it must be a fraction above 0 and below 1, such as 0.05 for 5%`,
		}},
		{config.RulePullback, []string{"trend_days", "100", "high_days", "20", "pullback", "1"}, []string{
			`pullback is "1", but it must be a fraction above 0 and below 1, such as 0.05 for 5%`,
		}},
		{config.RuleFixedStop, []string{"distance", ".05"}, []string{
			`distance is ".05", but it must be a fraction above 0 and below 1, such as 0.05 for 5%`,
		}},
		{config.RuleFixedStop, []string{"distance", "-0.05"}, []string{
			`distance is "-0.05", but it must be a fraction above 0 and below 1, such as 0.05 for 5%`,
		}},
		{config.RuleTrailingStop, []string{"distance", "5e-2"}, []string{
			`distance is "5e-2", but it must be a fraction above 0 and below 1, such as 0.05 for 5%`,
		}},
		{config.RuleTrailingStop, []string{"distance", "10%"}, []string{
			`distance is "10%", but it must be a fraction above 0 and below 1, such as 0.05 for 5%`,
		}},
		{config.RuleSizeFixedAmount, []string{"amount", "0"}, []string{
			`amount is "0", but it must be a dollar amount above 0 with at most two decimal places`,
		}},
		{config.RuleSizeFixedAmount, []string{"amount", "100.001"}, []string{
			`amount is "100.001", but it must be a dollar amount above 0 with at most two decimal places`,
		}},
		{config.RuleSizeFixedAmount, []string{"amount", "$100"}, []string{
			`amount is "$100", but it must be a dollar amount above 0 with at most two decimal places`,
		}},
		{config.RuleSizeByRisk, []string{"risk_per_trade", "NaN"}, []string{
			`risk_per_trade is "NaN", but it must be a fraction above 0 and below 1, such as 0.05 for 5%`,
		}},
		{config.RuleSizeByRisk, []string{"risk", "50.00"}, []string{
			`unknown setting "risk" (known settings: risk_per_trade)`,
			"risk_per_trade is missing; it must be a fraction above 0 and below 1, such as 0.05 for 5%",
		}},
	}
	for _, c := range cases {
		t.Run(c.ruleType+" "+strings.Join(c.settings, " "), func(t *testing.T) {
			_, err := Parse(config.Rule{Type: c.ruleType, Settings: settings(c.settings...)})
			if !errors.Is(err, ErrSettings) {
				t.Fatalf("err = %v, want ErrSettings", err)
			}
			var se *SettingsError
			if !errors.As(err, &se) || se.Type != c.ruleType {
				t.Fatalf("err = %#v", err)
			}
			if !slices.Equal(se.Problems, c.problems) {
				t.Errorf("problems:\n got %q\nwant %q", se.Problems, c.problems)
			}
			if !strings.HasPrefix(err.Error(), c.ruleType+" settings are invalid: ") {
				t.Errorf("message = %q", err.Error())
			}
		})
	}
}

// strategyWith builds an in-memory strategy file with one strategy.
func strategyWith(stocks []string, rules ...config.Rule) *config.Strategy {
	return &config.Strategy{Strategies: []config.StrategyDef{{
		Name: "example", PortfolioShare: config.MustDecimal("0.5"), Stocks: stocks, Rules: rules,
	}}}
}

func rule(ruleType string, kv ...string) config.Rule {
	return config.Rule{Type: ruleType, Settings: settings(kv...)}
}

func TestValidate(t *testing.T) {
	ma := rule(config.RuleMovingAverageCross, "fast_days", "20", "slow_days", "50")
	stop := rule(config.RuleTrailingStop, "distance", "0.10")
	fixed := rule(config.RuleSizeFixedAmount, "amount", "100.00")
	risk := rule(config.RuleSizeByRisk, "risk_per_trade", "0.01")
	momentum := rule(config.RuleMomentumRanking, "days", "60", "top", "2", "exit_rank", "3")
	two := []string{"AAA", "BBB"}
	cases := []struct {
		name     string
		s        *config.Strategy
		problems []string
	}{
		{"valid", strategyWith(two, ma, stop, risk), nil},
		{"bad settings name the strategy and the rule", strategyWith(two,
			ma, rule(config.RuleBreakout, "days", "50")), []string{
			`strategy "example": rules[1] (breakout): exit_days is missing; it must be a whole number from 2 to 300`,
		}},
		{"unknown type", strategyWith(two, rule("moon_shot")), []string{
			`strategy "example": rules[0] (moon_shot): rules: unknown rule type "moon_shot" (known types: ` +
				strings.Join(config.RuleTypes(), ", ") + ")",
		}},
		{"two sizing rules", strategyWith(two, ma, stop, fixed, risk), []string{
			`strategy "example" has 2 sizing rules (rules[2], rules[3]); it may have at most one`,
		}},
		{"risk sizing without a stop", strategyWith(two, ma, risk), []string{
			`strategy "example": rules[1] (size_by_risk): needs a fixed_stop or trailing_stop rule in the same strategy`,
		}},
		{"momentum top not below the stocks", strategyWith(two, momentum), []string{
			`strategy "example": rules[0] (momentum_ranking): top is 2, but the strategy has 2 stocks; it must be fewer`,
		}},
		{"momentum top below the stocks", strategyWith([]string{"AAA", "BBB", "CCC"}, momentum), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.s)
			if c.problems == nil {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, config.ErrInvalid) {
				t.Fatalf("err = %v, want config.ErrInvalid", err)
			}
			var ve *config.ValidationError
			if !errors.As(err, &ve) || !slices.Equal(ve.Problems, c.problems) {
				t.Errorf("problems:\n got %q\nwant %q", ve.Problems, c.problems)
			}
		})
	}
}

// TestStrategyExampleValidates keeps the repo's example strategy file in step
// with the rule library's settings.
func TestStrategyExampleValidates(t *testing.T) {
	s, err := config.LoadStrategy("../../strategy.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(s); err != nil {
		t.Fatal(err)
	}
}
