package rules

import (
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
)

// Stub rules give fixed decisions, so these tests check the combining alone.

type stubSignal struct {
	name string
	out  Outcome
}

func (s stubSignal) Type() string { return s.name }
func (stubSignal) Kind() Kind     { return KindSignal }
func (s stubSignal) Decide(in Input) (Decision, error) {
	return Decision{Symbol: in.Symbol, Outcome: s.out, Reason: string(s.out) + " from " + s.name}, nil
}

type stubRanking struct {
	name string
	out  map[string]Outcome
	seen *[]string
}

func (s stubRanking) Type() string { return s.name }
func (stubRanking) Kind() Kind     { return KindRanking }
func (s stubRanking) Rank(in []Input) ([]Decision, error) {
	out := make([]Decision, len(in))
	for i, x := range in {
		if s.seen != nil {
			*s.seen = append(*s.seen, x.Symbol)
		}
		o, ok := s.out[x.Symbol]
		if !ok {
			o = NoChange
		}
		out[i] = Decision{Symbol: x.Symbol, Outcome: o, Reason: string(o) + " from " + s.name}
	}
	return out, nil
}

type stubStop struct {
	name string
	out  Outcome
	stop string
}

func (s stubStop) Type() string { return s.name }
func (stubStop) Kind() Kind     { return KindStop }
func (s stubStop) Stop(in Input) (Decision, error) {
	d := Decision{Symbol: in.Symbol, Outcome: s.out, Reason: string(s.out) + " from " + s.name}
	if s.stop != "" {
		d.Stop = pricePtr(s.stop)
	}
	return d, nil
}

func set(rs ...Rule) Set {
	s := Set{All: rs}
	for _, r := range rs {
		if x, ok := r.(StopRule); ok {
			s.Stops = append(s.Stops, x)
		}
	}
	return s
}

func combineOne(t *testing.T, s Set, in Input) Verdict {
	t.Helper()
	vs, err := s.Combine([]Input{in}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(vs))
	}
	return vs[0]
}

func TestCombinePriority(t *testing.T) {
	v := &Verdict{Action: actionUnset}
	v.offer(ActionNone, "z", Decision{Outcome: NoChange, Reason: "nothing"})
	v.offer(ActionEntry, "a", Decision{Outcome: Buy, Reason: "buy"})
	v.offer(ActionTrim, "b", Decision{Outcome: Trim, Reason: "trim"})
	v.offer(ActionEntry, "c", Decision{Outcome: Buy, Reason: "buy again"})
	if v.Action != ActionTrim || v.Reason != "b: trim" {
		t.Fatalf("trim did not beat a buy: %+v", v)
	}
	v.offer(ActionSell, "d", Decision{Outcome: Sell, Reason: "sell"})
	v.offer(ActionSell, "e", Decision{Outcome: Sell, Reason: "a later sell"})
	if v.Action != ActionSell || v.Reason != "d: sell" {
		t.Fatalf("the first sell did not win: %+v", v)
	}
	v.offer(ActionStopSell, "f", Decision{Outcome: Sell, Reason: "stop"})
	v.offer(ActionNone, "g", Decision{Outcome: NoChange, Reason: "nothing"})
	if v.Action != ActionStopSell || v.Reason != "f: stop" || v.Outcome != Sell {
		t.Fatalf("a stop sell did not come first: %+v", v)
	}
}

func TestCombineHeldStock(t *testing.T) {
	s := set(
		stubSignal{"sig_a", Buy},
		stubRanking{name: "rank_b", out: map[string]Outcome{"AAA": Trim}},
		stubSignal{"sig_c", Sell},
		stubStop{"stop_d", Sell, "9.00"},
		stubStop{"stop_e", NoChange, "9.50"},
	)
	v := combineOne(t, s, Input{Symbol: "AAA", Bars: closes("10.00"), Holding: held("10.00")})
	if v.Action != ActionStopSell || v.Outcome != Sell || v.Rule != "stop_d" || v.Reason != "stop_d: sell from stop_d" {
		t.Errorf("verdict = %+v, want the stop sell from stop_d", v)
	}
	if stopText(v.Stop) != "9.50" {
		t.Errorf("stop = %s, want the higher stop 9.50", stopText(v.Stop))
	}
}

func TestCombineFileOrderBreaksTies(t *testing.T) {
	// The ranking rule comes first in the file, so its buy's reason is kept
	// over the signal rule's buy.
	s := set(
		stubRanking{name: "rank_a", out: map[string]Outcome{"AAA": Buy}},
		stubSignal{"sig_b", Buy},
		stubStop{"stop_c", Sell, "9.00"},
	)
	v := combineOne(t, s, Input{Symbol: "AAA", Bars: closes("10.00")})
	if v.Action != ActionEntry || v.Outcome != Buy || v.Reason != "rank_a: buy from rank_a" {
		t.Errorf("verdict = %+v, want the ranking rule's buy", v)
	}
	if v.Stop != nil {
		t.Errorf("a stock not held got a stop: %s (stop rules run only for held stocks)", stopText(v.Stop))
	}
}

func TestCombineNoChangeKeepsTheFirstReason(t *testing.T) {
	s := set(stubStop{"stop_a", NoChange, "9.00"}, stubSignal{"sig_b", NoChange})
	v := combineOne(t, s, Input{Symbol: "AAA", Bars: closes("10.00"), Holding: held("10.00")})
	if v.Action != ActionNone || v.Outcome != NoChange || v.Reason != "stop_a: no_change from stop_a" {
		t.Errorf("verdict = %+v, want no change with stop_a's reason", v)
	}

	// Only a sizing rule: no change and no reason.
	v = combineOne(t, set(mustParse(t, config.RuleSizeFixedAmount, "amount", "100.00")),
		Input{Symbol: "AAA", Bars: closes("10.00")})
	if v.Action != ActionNone || v.Rule != "" || v.Reason != "" {
		t.Errorf("verdict = %+v, want no change and no reason", v)
	}
}

func TestCombineDecidesOnlyThePickedStocksButRanksAll(t *testing.T) {
	var seen []string
	s := set(stubRanking{name: "rank", out: map[string]Outcome{"AAA": Buy, "BBB": Buy}, seen: &seen})
	in := []Input{{Symbol: "AAA", Bars: closes("1.00")}, {Symbol: "BBB", Bars: closes("1.00")}}
	vs, err := s.Combine(in, func(i int) bool { return i == 1 })
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Symbol != "BBB" || vs[0].Action != ActionEntry {
		t.Errorf("verdicts = %+v, want one buy for BBB", vs)
	}
	if len(seen) != 2 {
		t.Errorf("the ranking rule saw %v, want both stocks", seen)
	}
}

func TestParseSetAndInitialStop(t *testing.T) {
	def := config.StrategyDef{Name: "x", Stocks: []string{"AAA"}, Rules: []config.Rule{
		{Type: config.RuleBreakout, Settings: settings("days", "2", "exit_days", "2")},
		{Type: config.RuleFixedStop, Settings: settings("distance", "0.10")},
		{Type: config.RuleTrailingStop, Settings: settings("distance", "0.05")},
		{Type: config.RuleSizeByRisk, Settings: settings("risk_per_trade", "0.01")},
	}}
	s, err := ParseSet(def)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.All) != 4 || len(s.Stops) != 2 || s.Sizing == nil || s.All[0].Type() != config.RuleBreakout {
		t.Fatalf("set = %+v", s)
	}
	// Fixed: 100 × 0.90 = 90.00; trailing: highest close 100.00 × 0.95 = 95.00.
	stop, err := s.InitialStop(Input{Symbol: "AAA", Bars: closes("90.00", "100.00")}, price("100.00"))
	if err != nil {
		t.Fatal(err)
	}
	if stopText(stop) != "95.00" {
		t.Errorf("initial stop = %s, want 95.00", stopText(stop))
	}

	def.Rules[0].Settings = settings("days", "2")
	if _, err := ParseSet(def); err == nil {
		t.Error("a rule with a missing setting parsed")
	}
}
