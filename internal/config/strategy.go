package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/alexherrero/sherwood/internal/money"
)

// StrategyVersion is the only strategy file format version this build reads.
const StrategyVersion = 1

// MaxHoldings is the design's hard ceiling on holdings. The strategy file's
// max_holdings may be lower, never higher.
const MaxHoldings = 20

// Rule type names: the one list the strategy file is checked against. The
// rule library in internal/rules gives each name its implementation; it reads
// config's types, so it cannot be imported here, and a test there fails if
// the two lists disagree.
const (
	RuleMovingAverageCross = "moving_average_cross"
	RuleBreakout           = "breakout"
	RuleMomentumRanking    = "momentum_ranking"
	RulePullback           = "pullback"
	RuleFixedStop          = "fixed_stop"
	RuleTrailingStop       = "trailing_stop"
	RuleSizeFixedAmount    = "size_fixed_amount"
	RuleSizeByRisk         = "size_by_risk"
)

var ruleTypes = []string{
	RuleMovingAverageCross,
	RuleBreakout,
	RuleMomentumRanking,
	RulePullback,
	RuleFixedStop,
	RuleTrailingStop,
	RuleSizeFixedAmount,
	RuleSizeByRisk,
}

// RuleTypes returns the known rule type names, in a fixed order.
func RuleTypes() []string { return slices.Clone(ruleTypes) }

// KnownRuleType reports whether name is a rule type sherwood has.
func KnownRuleType(name string) bool { return slices.Contains(ruleTypes, name) }

// symbolText is the accepted form of a stock symbol: capital letters, digits,
// and a dot or dash for share classes (for example BRK.B).
var symbolText = regexp.MustCompile(`^[A-Z][A-Z0-9]*([.-][A-Z0-9]+)?$`)

// maxSymbolLen bounds a symbol's length.
const maxSymbolLen = 10

// Strategy is a validated strategy file.
type Strategy struct {
	// Hash is the hex SHA-256 of the file's bytes. Each signal records it.
	Hash       string
	Limits     Limits
	Watchlist  []string
	Strategies []StrategyDef
	// Sectors gives each stock's sector, for the sector limit. It must cover
	// every stock on the watchlist and in a strategy when
	// Limits.MaxSectorShare is set; otherwise it may be empty. Sector names
	// are compared exactly as written.
	Sectors map[string]string
}

// Limits are the risk check's limits. Every field except MaxSectorShare is
// required in the file; the code holds no default values.
type Limits struct {
	// MaxStockShare is the largest share of the portfolio (0 to 1) one stock
	// may hold after a buy fills.
	MaxStockShare Decimal
	// MaxOrderValue is the largest value of a single order, in dollars, at
	// most two decimal places.
	MaxOrderValue money.Money
	// MaxOrdersPerDay is the most orders sent in one trading day, protective
	// sells aside.
	MaxOrdersPerDay int
	// DailyLossLimit is the loss since the previous close, in dollars, at
	// which all trading stops for the rest of the day, at most two decimal
	// places.
	DailyLossLimit money.Money
	// MaxHoldings is the most holdings after a buy fills, at most 20.
	MaxHoldings int
	// MaxSectorShare is the largest share of the portfolio (0 to 1) in one
	// sector. Nil means no sector limit.
	MaxSectorShare *Decimal
	// MaxLimitDistance is how far (0 to 1, as a share of the last close) a
	// buy's limit price may sit from the last close.
	MaxLimitDistance Decimal
	// BuyLimitMargin is the margin (0 to 1, as a share of the previous close)
	// above the previous close at which a buy's limit price is set.
	BuyLimitMargin Decimal
	// FractionalShares turns fractional share counts on.
	FractionalShares bool
	// PriceCrossCheckTolerance is how far (0 to 1) the two price sources may
	// disagree on a stock before it gets no signals that day.
	PriceCrossCheckTolerance Decimal
	// MinPaperWeeks is the fewest weeks of paper trading before live mode
	// may be turned on.
	MinPaperWeeks int
}

// StrategyDef is one named strategy.
type StrategyDef struct {
	Name string
	// PortfolioShare is the strategy's share of the portfolio, 0 to 1.
	PortfolioShare Decimal
	Stocks         []string
	// IncludeWatchlist is the file's include_watchlist: when true, the
	// strategy's rules also run over every stock on the watchlist in force,
	// the file's watchlist plus the stocks added from the phone app (see
	// WithWatchlist).
	IncludeWatchlist bool
	Rules            []Rule
}

// Rule is one rule entry: a rule type and its settings as written.
type Rule struct {
	Type     string
	Settings map[string]Scalar
}

// The file types mirror the YAML. Pointer fields tell a missing value from a
// zero one, so a missing limit is reported by name.
type strategyFile struct {
	Version    *wholeNumber      `yaml:"version"`
	Limits     *limitsFile       `yaml:"limits"`
	Watchlist  []string          `yaml:"watchlist"`
	Strategies []strategyDefFile `yaml:"strategies"`
	Sectors    map[string]string `yaml:"sectors"`
}

type limitsFile struct {
	MaxStockShare            *Decimal     `yaml:"max_stock_share"`
	MaxOrderValue            *amountText  `yaml:"max_order_value"`
	MaxOrdersPerDay          *wholeNumber `yaml:"max_orders_per_day"`
	DailyLossLimit           *amountText  `yaml:"daily_loss_limit"`
	MaxHoldings              *wholeNumber `yaml:"max_holdings"`
	MaxSectorShare           *Decimal     `yaml:"max_sector_share"`
	MaxLimitDistance         *Decimal     `yaml:"max_limit_distance"`
	BuyLimitMargin           *Decimal     `yaml:"buy_limit_margin"`
	FractionalShares         *bool        `yaml:"fractional_shares"`
	PriceCrossCheckTolerance *Decimal     `yaml:"price_cross_check_tolerance"`
	MinPaperWeeks            *wholeNumber `yaml:"min_paper_weeks"`
}

type strategyDefFile struct {
	Name             string     `yaml:"name"`
	PortfolioShare   *Decimal   `yaml:"portfolio_share"`
	Stocks           []string   `yaml:"stocks"`
	IncludeWatchlist bool       `yaml:"include_watchlist"`
	Rules            []ruleFile `yaml:"rules"`
}

type ruleFile struct {
	Type     string            `yaml:"type"`
	Settings map[string]Scalar `yaml:"settings"`
}

// LoadStrategy reads and validates the strategy file at path.
func LoadStrategy(path string) (*Strategy, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own configured file
	if err != nil {
		return nil, fmt.Errorf("reading strategy file: %w", err)
	}
	return ParseStrategy(data)
}

// ParseStrategy validates strategy file bytes and returns the strategy with
// the bytes' hash. Every problem found is reported, each naming its field.
func ParseStrategy(data []byte) (*Strategy, error) {
	var f strategyFile
	if err := decodeStrict(data, &f); err != nil {
		return nil, &ValidationError{File: "strategy file", Problems: []string{err.Error()}}
	}
	p := &problems{file: "strategy file"}
	s := &Strategy{Hash: HashBytes(data)}

	switch {
	case f.Version == nil:
		p.add("version is missing")
	case int(*f.Version) != StrategyVersion:
		p.add("version is %d, but this build reads version %d", *f.Version, StrategyVersion)
	}

	if f.Limits == nil {
		p.add("limits is missing")
		f.Limits = &limitsFile{}
	}
	s.Limits = checkLimits(p, f.Limits)
	s.Watchlist = checkSymbols(p, "watchlist", f.Watchlist)
	s.Strategies = checkStrategies(p, f.Strategies)
	s.Sectors = checkSectors(p, f.Sectors, s)

	if err := p.err(); err != nil {
		return nil, err
	}
	return s, nil
}

// HashBytes returns the hex SHA-256 of data, the form recorded on signals.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var (
	ratZero = new(big.Rat)
	ratOne  = big.NewRat(1, 1)
)

// share checks a fraction of the whole. With allowZero false it must be above
// 0; it must be at most 1, or below 1 when below is set.
func share(p *problems, field string, d Decimal, allowZero, below bool) {
	r := d.Rat()
	if allowZero && r.Cmp(ratZero) < 0 || !allowZero && r.Cmp(ratZero) <= 0 {
		if allowZero {
			p.add("%s is %s, but it must not be negative", field, d)
		} else {
			p.add("%s is %s, but it must be greater than 0", field, d)
		}
		return
	}
	if below && r.Cmp(ratOne) >= 0 {
		p.add("%s is %s, but it must be less than 1 (it is a share, so 0.05 means 5%%)", field, d)
	} else if !below && r.Cmp(ratOne) > 0 {
		p.add("%s is %s, but it must be at most 1 (it is a share, so 0.10 means 10%%)", field, d)
	}
}

// amount checks a positive dollar amount with at most two decimal places and
// returns it. Problems quote the amount as it was written in the file.
func amount(p *problems, field string, a amountText) money.Money {
	if !a.m.IsPositive() {
		p.add("%s is %s, but it must be greater than 0", field, a.text)
	} else if !a.m.IsCents() {
		p.add("%s is %s, but an amount has at most 2 decimal places", field, a.text)
	}
	return a.m
}

func checkLimits(p *problems, f *limitsFile) Limits {
	var l Limits
	missing := func(name string) { p.add("limits.%s is missing", name) }

	if f.MaxStockShare == nil {
		missing("max_stock_share")
	} else {
		l.MaxStockShare = *f.MaxStockShare
		share(p, "limits.max_stock_share", l.MaxStockShare, false, false)
	}
	if f.MaxOrderValue == nil {
		missing("max_order_value")
	} else {
		l.MaxOrderValue = amount(p, "limits.max_order_value", *f.MaxOrderValue)
	}
	if f.MaxOrdersPerDay == nil {
		missing("max_orders_per_day")
	} else {
		l.MaxOrdersPerDay = int(*f.MaxOrdersPerDay)
		if l.MaxOrdersPerDay < 1 {
			p.add("limits.max_orders_per_day is %d, but it must be at least 1", l.MaxOrdersPerDay)
		}
	}
	if f.DailyLossLimit == nil {
		missing("daily_loss_limit")
	} else {
		l.DailyLossLimit = amount(p, "limits.daily_loss_limit", *f.DailyLossLimit)
	}
	if f.MaxHoldings == nil {
		missing("max_holdings")
	} else {
		l.MaxHoldings = int(*f.MaxHoldings)
		if l.MaxHoldings < 1 || l.MaxHoldings > MaxHoldings {
			p.add("limits.max_holdings is %d, but it must be from 1 to %d", l.MaxHoldings, MaxHoldings)
		}
	}
	if f.MaxSectorShare != nil {
		d := *f.MaxSectorShare
		l.MaxSectorShare = &d
		share(p, "limits.max_sector_share", d, false, false)
	}
	if f.MaxLimitDistance == nil {
		missing("max_limit_distance")
	} else {
		l.MaxLimitDistance = *f.MaxLimitDistance
		share(p, "limits.max_limit_distance", l.MaxLimitDistance, false, true)
	}
	if f.BuyLimitMargin == nil {
		missing("buy_limit_margin")
	} else {
		l.BuyLimitMargin = *f.BuyLimitMargin
		share(p, "limits.buy_limit_margin", l.BuyLimitMargin, true, true)
	}
	if f.MaxLimitDistance != nil && f.BuyLimitMargin != nil &&
		l.BuyLimitMargin.Rat().Cmp(l.MaxLimitDistance.Rat()) > 0 {
		p.add("limits.buy_limit_margin (%s) is larger than limits.max_limit_distance (%s), so every buy would be refused",
			l.BuyLimitMargin, l.MaxLimitDistance)
	}
	if f.FractionalShares == nil {
		missing("fractional_shares")
	} else {
		l.FractionalShares = *f.FractionalShares
	}
	if f.PriceCrossCheckTolerance == nil {
		missing("price_cross_check_tolerance")
	} else {
		l.PriceCrossCheckTolerance = *f.PriceCrossCheckTolerance
		share(p, "limits.price_cross_check_tolerance", l.PriceCrossCheckTolerance, false, true)
	}
	if f.MinPaperWeeks == nil {
		missing("min_paper_weeks")
	} else {
		l.MinPaperWeeks = int(*f.MinPaperWeeks)
		if l.MinPaperWeeks < 1 {
			p.add("limits.min_paper_weeks is %d, but it must be at least 1", l.MinPaperWeeks)
		}
	}
	return l
}

// ValidSymbol reports whether sym is written as a stock symbol: capital
// letters, digits, and one dot or dash for a share class, at most 10
// characters.
func ValidSymbol(sym string) bool { return len(sym) <= maxSymbolLen && symbolText.MatchString(sym) }

// WithWatchlist returns a copy of s whose watchlist is watched, the
// watchlist in force, and in which each strategy with IncludeWatchlist runs
// its rules over its own stocks followed by every watched stock it does not
// already list. s itself is left as it is; the copy keeps s's hash, limits
// and sectors.
func (s *Strategy) WithWatchlist(watched []string) *Strategy {
	out := *s
	out.Watchlist = slices.Clone(watched)
	out.Strategies = make([]StrategyDef, len(s.Strategies))
	for i, def := range s.Strategies {
		def.Stocks = slices.Clone(def.Stocks)
		if def.IncludeWatchlist {
			for _, sym := range watched {
				if !slices.Contains(def.Stocks, sym) {
					def.Stocks = append(def.Stocks, sym)
				}
			}
		}
		out.Strategies[i] = def
	}
	return &out
}

// checkSymbols checks a list of stock symbols: well formed and not repeated.
func checkSymbols(p *problems, field string, symbols []string) []string {
	seen := make(map[string]bool, len(symbols))
	for i, sym := range symbols {
		if len(sym) > maxSymbolLen || !symbolText.MatchString(sym) {
			p.add("%s[%d] is %q, which is not a stock symbol (capital letters, digits, and one . or - for a share class)", field, i, sym)
			continue
		}
		if seen[sym] {
			p.add("%s lists %s more than once", field, sym)
		}
		seen[sym] = true
	}
	return slices.Clone(symbols)
}

func checkStrategies(p *problems, defs []strategyDefFile) []StrategyDef {
	if len(defs) == 0 {
		p.add("strategies is missing or empty")
		return nil
	}
	out := make([]StrategyDef, 0, len(defs))
	names := make(map[string]bool, len(defs))
	total := new(big.Rat)
	places := 0
	for i, f := range defs {
		where := fmt.Sprintf("strategies[%d]", i)
		if f.Name == "" {
			p.add("%s.name is missing", where)
		} else {
			where = fmt.Sprintf("strategy %q", f.Name)
			if names[f.Name] {
				p.add("strategy name %q is used more than once", f.Name)
			}
			names[f.Name] = true
		}
		def := StrategyDef{Name: f.Name, IncludeWatchlist: f.IncludeWatchlist}
		if f.PortfolioShare == nil {
			p.add("%s: portfolio_share is missing", where)
		} else {
			def.PortfolioShare = *f.PortfolioShare
			share(p, where+": portfolio_share", def.PortfolioShare, false, false)
			total.Add(total, def.PortfolioShare.Rat())
			places = max(places, def.PortfolioShare.Places())
		}
		if len(f.Stocks) == 0 {
			p.add("%s: stocks is missing or empty", where)
		}
		def.Stocks = checkSymbols(p, where+": stocks", f.Stocks)
		if len(f.Rules) == 0 {
			p.add("%s: rules is missing or empty", where)
		}
		for j, r := range f.Rules {
			switch {
			case r.Type == "":
				p.add("%s: rules[%d].type is missing", where, j)
			case !KnownRuleType(r.Type):
				p.add("%s: rules[%d] has unknown rule type %q (known types: %v)", where, j, r.Type, ruleTypes)
			}
			def.Rules = append(def.Rules, Rule(r))
		}
		out = append(out, def)
	}
	if total.Cmp(ratOne) > 0 {
		// FloatString formats the exact rational as decimal text; with the
		// shares' own number of places it is exact.
		p.add("the strategies' portfolio_share values add up to %s, which is more than the whole portfolio (1)",
			total.FloatString(places))
	}
	return out
}

// maxSectorLen bounds a sector name's length.
const maxSectorLen = 40

// checkSectors checks the sectors map: each key a stock symbol, each sector a
// short name. When the sector limit is set, every stock on the watchlist and
// in a strategy must have a sector, because the risk check could not measure
// a buy of a stock with none.
func checkSectors(p *problems, sectors map[string]string, s *Strategy) map[string]string {
	symbols := make([]string, 0, len(sectors))
	for sym := range sectors {
		symbols = append(symbols, sym)
	}
	slices.Sort(symbols)
	out := make(map[string]string, len(sectors))
	for _, sym := range symbols {
		name := sectors[sym]
		if len(sym) > maxSymbolLen || !symbolText.MatchString(sym) {
			p.add("sectors has %q, which is not a stock symbol (capital letters, digits, and one . or - for a share class)", sym)
			continue
		}
		switch {
		case strings.TrimSpace(name) == "":
			p.add("sectors.%s is empty; give the stock's sector, such as technology", sym)
		case strings.TrimSpace(name) != name:
			p.add("sectors.%s is %q, which starts or ends with a space", sym, name)
		case len(name) > maxSectorLen || strings.ContainsFunc(name, unicode.IsControl):
			p.add("sectors.%s is not a sector name (at most %d characters, on one line)", sym, maxSectorLen)
		}
		out[sym] = name
	}
	if s.Limits.MaxSectorShare == nil {
		return out
	}
	if len(sectors) == 0 {
		p.add("sectors is missing, but limits.max_sector_share is set, so every stock on the watchlist and in a strategy needs a sector")
		return out
	}
	seen := make(map[string]bool)
	need := slices.Clone(s.Watchlist)
	for _, def := range s.Strategies {
		need = append(need, def.Stocks...)
	}
	for _, sym := range need {
		if seen[sym] {
			continue
		}
		seen[sym] = true
		if _, ok := sectors[sym]; !ok {
			p.add("sectors has no sector for %s, but limits.max_sector_share is set", sym)
		}
	}
	return out
}
