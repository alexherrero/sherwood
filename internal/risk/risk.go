package risk

import (
	"fmt"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// Kind is what an order does. The zero value is deliberately invalid.
type Kind int

// The three kinds of order the risk check knows.
const (
	// Buy is a day limit buy, of a new stock or more of one already held.
	Buy Kind = iota + 1
	// Sell is an ordinary sell sent at the open: a trim or a rule's exit. It
	// waits for approval like a buy.
	Sell
	// ProtectiveSell is a stop-loss or trailing-stop sell, resting at the
	// broker as a standing stop order. It skips approval and passes the
	// shorter list.
	ProtectiveSell
)

// String names the kind in plain words.
func (k Kind) String() string {
	switch k {
	case Buy:
		return "buy"
	case Sell:
		return "sell"
	case ProtectiveSell:
		return "protective sell"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// IsSell reports whether k sells shares.
func (k Kind) IsSell() bool { return k == Sell || k == ProtectiveSell }

// Order is the order being checked.
type Order struct {
	// Strategy names the strategy that made the order. The check does not
	// use it; it is here so a verdict can be traced.
	Strategy string
	Symbol   string
	Kind     Kind
	Quantity money.Quantity
	// Price is a buy's limit price or a protective sell's stop price. An
	// ordinary sell goes at the open and is valued at the stock's last
	// close, so it leaves Price zero.
	Price money.Price
	// Session is the trading date the order is for: any time on that
	// Eastern date.
	Session time.Time
}

// DayOrder is one of the session's other orders that count against today's
// limits.
//
// Pass every order for the session that has traded or may still trade:
// approved, submitted, accepted, partly or wholly filled, or unknown. Leave
// out declined, blocked and rejected orders, orders called off or expired that
// filled nothing, and the order being checked itself. A resting protective
// stop counts only once it has filled, wholly or partly, in the session.
type DayOrder struct {
	Symbol string
	Kind   Kind
	// Value is the order's value: shares times the limit or stop price, or
	// times the last close for an ordinary sell.
	Value money.Money
	// Open is true for a buy whose cash is promised but not yet out of
	// Snapshot.Cash: approved or sent, and not yet settled.
	Open bool
}

// Snapshot is everything the check reads besides the order.
type Snapshot struct {
	// Now is the time of the check, from the injected clock.
	Now time.Time
	// Calendar is the exchange calendar.
	Calendar *clock.Calendar
	// Preview is true when the check runs at proposal or on the approval
	// card rather than just before sending. A previewed order passes the
	// market check if its session has not closed yet; at send the market
	// must be open. The zero value is the stricter send check.
	Preview bool

	// Limits are the effective limits: the strategy file's, tightened by
	// any tightening recorded from the app.
	Limits config.Limits
	// Sectors gives each stock's sector, from the strategy file. It is read
	// only when Limits.MaxSectorShare is set.
	Sectors map[string]string
	// Watchlist is the watchlist in force: the strategy file's, plus the
	// stocks added from the phone app (limits.Watchlist).
	Watchlist []string

	// Held is the shares held of each stock. A stock with zero shares, or
	// none listed, is not held.
	Held map[string]money.Quantity
	// Cash is the cash on hand, before the session's open buys settle.
	Cash money.Money
	// Value is the portfolio's value now: cash plus holdings.
	Value money.Money
	// PreviousCloseValue is the portfolio's value at the previous close.
	PreviousCloseValue money.Money
	// LastCloses is each stock's last checked close.
	LastCloses map[string]money.Price
	// DayOrders are the session's other orders; see DayOrder.
	DayOrders []DayOrder

	// SwitchOff is true when the stop switch is off: no halt is active.
	SwitchOff bool
	// BrokerHealthy is true when the broker adapter is answering.
	BrokerHealthy bool
}

// Checker is the risk check as the execution layer holds it.
type Checker interface {
	Check(Order, Snapshot) Verdict
}

// Standard is the real risk check, Check.
type Standard struct{}

// Check runs the real risk check.
func (Standard) Check(o Order, s Snapshot) Verdict { return Check(o, s) }

// The checks, by the names a verdict shows.
const (
	CheckDetails     = "Order details"
	CheckStockShare  = "One stock's share of the portfolio"
	CheckOrderValue  = "Value of one order"
	CheckOrderCount  = "Orders in one day"
	CheckDailyLoss   = "Daily loss limit"
	CheckHoldings    = "Number of holdings"
	CheckSectorShare = "One sector's share of the portfolio"
	CheckCash        = "Cash only"
	CheckWatchlist   = "On the watchlist or held"
	CheckLimitPrice  = "Limit price near the last close"
	CheckSharesHeld  = "Shares held"
	CheckSameDay     = "No buy and sell of one stock in one day"
	CheckSwitch      = "Stop switch off and broker healthy"
	CheckMarketOpen  = "Market open"
)

// Result is one check's outcome.
type Result struct {
	// Check is the check's name, one of the Check constants.
	Check string
	// Passed reports whether the order passed this check.
	Passed bool
	// Detail says in plain words why it passed or was refused.
	Detail string
}

// Verdict is the risk check's answer: every check that applies to the order,
// in a fixed order.
type Verdict struct {
	Results []Result
}

// Passed reports whether the order may go: at least one check ran and every
// check passed.
func (v Verdict) Passed() bool {
	if len(v.Results) == 0 {
		return false
	}
	for _, r := range v.Results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// Refusals returns the checks that refused the order.
func (v Verdict) Refusals() []Result {
	var out []Result
	for _, r := range v.Results {
		if !r.Passed {
			out = append(out, r)
		}
	}
	return out
}

// Reason gives the refusal reasons in one line, for a blocked order's stored
// reason. It is empty when the order passed.
func (v Verdict) Reason() string {
	if len(v.Results) == 0 {
		return "The risk check ran no checks."
	}
	var parts []string
	for _, r := range v.Refusals() {
		parts = append(parts, r.Check+": "+r.Detail)
	}
	return strings.Join(parts, " ")
}

// String lists every check on its own line, passed or refused, for the
// approval card and the order's stored verdict.
func (v Verdict) String() string {
	if len(v.Results) == 0 {
		return "Refused: the risk check ran no checks."
	}
	var b strings.Builder
	for i, r := range v.Results {
		if i > 0 {
			b.WriteByte('\n')
		}
		if r.Passed {
			b.WriteString("Passed: ")
		} else {
			b.WriteString("Refused: ")
		}
		b.WriteString(r.Check)
		b.WriteString(". ")
		b.WriteString(r.Detail)
	}
	return b.String()
}
