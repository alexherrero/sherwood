// Package scheduler runs jobs keyed by name and trading date on trading days in
// US Eastern time, records each run with a hash of its inputs, and catches up
// missed days in order after downtime.
package scheduler
