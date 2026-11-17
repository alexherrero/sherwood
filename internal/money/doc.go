// Package money holds the exact decimal types for money, prices and share
// counts. They are stored in SQLite as text and travel in JSON as strings, so a
// value is never a floating-point number anywhere in sherwood.
//
// The types are Money (an exact dollar amount, any sign), Price (a per-share
// price, never negative, always whole cents) and Quantity (a share count,
// never negative). They sit on github.com/shopspring/decimal, whose values are
// immutable and whose addition, subtraction and multiplication are exact.
//
// The rounding policy is the one written in the design:
//
//   - Prices are rounded to the cent, halves away from zero (RoundPrice,
//     Money.RoundCents). Parsing a price never rounds: a sub-cent price is an
//     error.
//   - Share counts are whole shares unless fractional shares are turned on in
//     the strategy file, in which case they go to FractionalPlaces places. The
//     caller passes that choice in as a QuantityPolicy; there is no global
//     setting. A computed share count is always rounded down, so sizing never
//     buys more than it worked out.
//
// Every value is read from text in one plain form, such as "12.50" or "-3":
// no exponent, plus sign, spaces, NaN or Infinity. A SQLite REAL, a JSON
// number and JSON null are refused when decoding. No float type appears in
// this package, and a test fails if one does.
package money
