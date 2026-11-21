// Package feed is the research feed: the research export the Mac's nightly
// job reads, the feed file it writes back, the check every feed file must
// pass, and the tables the accepted items are kept in.
//
// # The one rule
//
// The AI never creates, approves or sends an order. This package is where
// the AI's words enter sherwood, so it must have no import path, direct or
// through another package, to the execution layer: internal/execution,
// internal/broker, internal/orders, internal/proposal, internal/halt or
// internal/risk. boundary_test.go walks the package's imports and fails if
// one appears.
//
// # The export
//
// Export is what GET /research/export serves: the symbols held and watched,
// their recent checked closes, and the latest trading date's signals with
// their rules' reasons. It has no balances, share counts, cash, order details
// or account details, and its types have no field that could carry one.
//
// # The feed file
//
// File is what the research job writes and POST /feed/upload takes. Check
// reads one:
//
//   - A file that is off the schema is refused outright: not JSON, an
//     unknown field, a wrong type, a version other than Version, trailing
//     data, more than MaxItems items or more than MaxFileBytes bytes.
//   - Within a good file, an item that breaks a rule is dropped, with a
//     reason, and the rest are kept: an unknown kind, no source, a link that
//     is not http or https, text over MaxText characters, and the others
//     Check names.
//
// The Mac's upload script and the NAS run the same Check, so a file the Mac
// accepts is one the NAS accepts.
//
// # Storage
//
// Save stores one checked feed as an upload and its items, in one
// transaction. Both tables (migration 0008) are append-only. The readers give
// the latest upload, its candidates, the latest note on each held stock, and
// the latest explanation of a signal.
package feed
