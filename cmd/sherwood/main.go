// Command sherwood is the sherwood application: it serves the phone app, runs
// the scheduled jobs and supervises the broker helper. For now it only starts
// and exits; later tasks wire in the settings, store and scheduler.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sherwood:", err)
		os.Exit(1)
	}
}

func run() error {
	return nil
}
