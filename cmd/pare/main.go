// Command pare trims command output to a byte budget while keeping the head,
// the tail, and error lines with surrounding context — the middle a blind
// `| tail` throws away. It is a composable pipe filter for AI coding agents.
//
// All logic lives in internal/{budget,cli,version}; main only maps the CLI's
// resolved exit code to the process. See the README for the exit-code contract.
package main

import (
	"os"

	"github.com/akira-toriyama/pare/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
