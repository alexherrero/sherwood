package logging

// allowedKeys is every field key the logger will write. It is the one list to
// review: a key that is not here is refused, and its value never reaches the
// log. Keys are flat snake_case; groups are refused.
//
// Never add a key whose value is a header, cookie, token, password, session,
// URL, request body or broker reply. A setting's value is safe to log only
// because settings secrets are secret.Secret values, which log as a mask.
var allowedKeys = []string{
	// Errors and reasons.
	"error",  // an error's text; errors that wrap a URL are refused
	"reason", // why something happened, in words

	// Scheduler job runs.
	"job",          // job name
	"trading_date", // exchange trading date, YYYY-MM-DD
	"input_hash",   // hash of the job's inputs
	"outcome",      // running, succeeded, failed or skipped
	"duration",     // how long a run or call took
	"attempt",      // attempt number for a retried run
	"missed_dates", // how many trading dates a catch-up covers
	"missed_runs",  // how many runs a pass found missed and ran late

	// Signals, risk decisions and orders.
	"strategy_hash", // hash of the strategy file in force
	"strategy",      // a strategy's name in the strategy file
	"rule",          // rule that produced a signal or a risk decision
	"signal",        // signal kind
	"symbol",        // stock symbol
	"side",          // buy or sell
	"quantity",      // share quantity, as a decimal string
	"price",         // price, as a decimal string
	"amount",        // money amount, as a decimal string
	"decision",      // risk decision: allowed or blocked
	"order_key",     // sherwood's own idempotency key for an order
	"state",         // order state after a change
	"from_state",    // order state before a change

	// Prices.
	"price_source", // price source name, such as tiingo or stooq

	// Halts, resets and mode.
	"mode",          // paper or live
	"halt_cause",    // why trading halted
	"change_source", // where a change came from: phone or nas

	// Settings and limit changes.
	"setting",   // setting or limit name
	"old_value", // value before a change
	"new_value", // value after a change

	// Broker helper calls (never arguments or replies).
	"op",   // helper operation name
	"code", // helper error code

	// Start-up and storage.
	"version",     // build version
	"migrations",  // number of database migrations applied
	"backup_file", // a backup copy's file name, never its folder
	"backup_kept", // how many backup copies are kept after a backup

	// Web requests (never headers, cookies, query strings or bodies).
	"method", // HTTP method
	"route",  // the matched route pattern, not the raw path
	"status", // HTTP status code
	// The address the web server listens on, such as ":8080". It is the
	// server's own address, never a caller's.
	"listen_address",

	// Notifications (counts and the alert's kind; never an endpoint or a key).
	"push_alert",         // the alert's kind, such as trading_halted
	"push_delivered",     // how many devices a push service accepted it for
	"push_failed",        // how many sends failed
	"push_removed",       // how many ended subscriptions were dropped
	"push_subscriptions", // how many devices have notifications on
}

// allowed is allowedKeys as a set.
var allowed = func() map[string]bool {
	m := make(map[string]bool, len(allowedKeys))
	for _, k := range allowedKeys {
		m[k] = true
	}
	return m
}()

// Allowed reports whether key is on the allowlist.
func Allowed(key string) bool { return allowed[key] }

// AllowedKeys returns a copy of the allowlist, in its listed order.
func AllowedKeys() []string { return append([]string(nil), allowedKeys...) }
