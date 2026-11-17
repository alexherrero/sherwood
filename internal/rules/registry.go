package rules

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/alexherrero/sherwood/internal/config"
)

// Definition describes one rule type: its name, its kind, what it does and
// the settings it takes.
type Definition struct {
	Type     string
	Kind     Kind
	Summary  string
	Settings []Setting
	// build turns parsed settings into the rule, adding any problem that
	// involves more than one setting to v.
	build func(v *values) Rule
}

// definitions is the registry, in the same order as config.RuleTypes.
var definitions = []Definition{
	{
		Type:     config.RuleMovingAverageCross,
		Kind:     KindSignal,
		Summary:  "Buys when the fast average of closes crosses above the slow one, and sells a holding when it crosses below.",
		Settings: movingAverageCrossSettings,
		build:    newMovingAverageCross,
	},
	{
		Type:     config.RuleBreakout,
		Kind:     KindSignal,
		Summary:  "Buys when a close clears the highest high of the previous days, and sells a holding when a close falls through the lowest low of the previous exit days.",
		Settings: breakoutSettings,
		build:    newBreakout,
	},
	{
		Type:     config.RuleMomentumRanking,
		Kind:     KindRanking,
		Summary:  "Ranks the strategy's stocks by their change in close; buys those that move into the top ranks, trims holdings that fall out of them and sells holdings that fall below the exit rank.",
		Settings: momentumRankingSettings,
		build:    newMomentumRanking,
	},
	{
		Type:     config.RulePullback,
		Kind:     KindSignal,
		Summary:  "Buys a close some way below its recent high close while it is still above its trend average, and sells a holding when a close falls below that average.",
		Settings: pullbackSettings,
		build:    newPullback,
	},
	{
		Type:     config.RuleFixedStop,
		Kind:     KindStop,
		Summary:  "Sets a holding's stop a fixed share below its average cost, and sells it when it closes at or below the stop.",
		Settings: fixedStopSettings,
		build:    newFixedStop,
	},
	{
		Type:     config.RuleTrailingStop,
		Kind:     KindStop,
		Summary:  "Sets a holding's stop a fixed share below its highest close since it was bought, never lowering it, and sells it when it closes at or below the stop.",
		Settings: trailingStopSettings,
		build:    newTrailingStop,
	},
	{
		Type:     config.RuleSizeFixedAmount,
		Kind:     KindSizing,
		Summary:  "Sizes each buy to spend at most a fixed dollar amount, rounded down to the share policy.",
		Settings: sizeFixedAmountSettings,
		build:    newSizeFixedAmount,
	},
	{
		Type:     config.RuleSizeByRisk,
		Kind:     KindSizing,
		Summary:  "Sizes each buy so that a fall from its price to its stop loses at most a share of the strategy's capital, rounded down to the share policy.",
		Settings: sizeByRiskSettings,
		build:    newSizeByRisk,
	},
}

// Definitions returns every rule type, in the same order as
// config.RuleTypes.
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	for i, d := range definitions {
		d.Settings = slices.Clone(d.Settings)
		out[i] = d
	}
	return out
}

// Types returns the rule type names this package implements, in order.
func Types() []string {
	out := make([]string, len(definitions))
	for i, d := range definitions {
		out[i] = d.Type
	}
	return out
}

// Lookup returns the definition of the named rule type.
func Lookup(name string) (Definition, bool) {
	for _, d := range definitions {
		if d.Type == name {
			d.Settings = slices.Clone(d.Settings)
			return d, true
		}
	}
	return Definition{}, false
}

// Parse reads one rule entry from the strategy file: it looks up the rule
// type, parses and checks every setting, and returns the rule. A bad setting
// gives a *SettingsError listing every problem, each naming its setting.
func Parse(r config.Rule) (Rule, error) {
	def, ok := Lookup(r.Type)
	if !ok {
		return nil, fmt.Errorf("rules: unknown rule type %q (known types: %s)", r.Type, strings.Join(Types(), ", "))
	}
	v := parseSettings(def.Settings, r.Settings)
	rule := def.build(v)
	if err := v.err(def.Type); err != nil {
		return nil, err
	}
	return rule, nil
}

// Validate parses every rule of every strategy in s and checks the rules of
// each strategy fit together: at most one sizing rule, a stop rule beside
// size_by_risk, and a momentum_ranking top below the number of stocks. It
// returns a *config.ValidationError listing every problem, so errors.Is(err,
// config.ErrInvalid) is true and sherwood refuses to start on it.
func Validate(s *config.Strategy) error {
	var problems []string
	for _, def := range s.Strategies {
		var sizing []string
		hasStop, needsStop := false, ""
		for i, entry := range def.Rules {
			where := fmt.Sprintf("strategy %q: rules[%d] (%s)", def.Name, i, entry.Type)
			rule, err := Parse(entry)
			if err != nil {
				var se *SettingsError
				if errors.As(err, &se) {
					for _, p := range se.Problems {
						problems = append(problems, where+": "+p)
					}
				} else {
					problems = append(problems, where+": "+err.Error())
				}
				continue
			}
			switch rule.Kind() {
			case KindSizing:
				sizing = append(sizing, fmt.Sprintf("rules[%d]", i))
				if rule.Type() == config.RuleSizeByRisk {
					needsStop = where
				}
			case KindStop:
				hasStop = true
			case KindRanking:
				if m, ok := rule.(MomentumRanking); ok && m.Top >= len(def.Stocks) {
					problems = append(problems, fmt.Sprintf("%s: top is %d, but the strategy has %d stocks; it must be fewer",
						where, m.Top, len(def.Stocks)))
				}
			}
		}
		if len(sizing) > 1 {
			problems = append(problems, fmt.Sprintf("strategy %q has %d sizing rules (%s); it may have at most one",
				def.Name, len(sizing), strings.Join(sizing, ", ")))
		}
		if needsStop != "" && !hasStop {
			problems = append(problems, needsStop+": needs a fixed_stop or trailing_stop rule in the same strategy")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return &config.ValidationError{File: "strategy file", Problems: problems}
}
