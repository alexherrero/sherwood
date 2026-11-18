// Package holdings records and reads the holdings and cash balance that
// migration 0003 keeps.
//
// Both are append-only snapshots. Writing holdings always records the whole
// set as it stood at one moment, so an import from the broker, and later a
// change derived from fills or a reviewed reconciliation, replaces the
// current set in one transaction and leaves every earlier set in place for
// reconciliation to compare against. The newest snapshot is the current one.
//
// The design limits sherwood to 20 holdings, but a broker account may hold
// more. A set of more than 20 is still recorded, with its OverLimit flag set,
// so the views can show it and later parts can refuse to trade on it.
//
// Every amount is a money type stored as text; nothing here uses a float.
// Writers take a transaction on the store's single writer connection, and
// readers take either the reader pool or that same transaction.
package holdings
