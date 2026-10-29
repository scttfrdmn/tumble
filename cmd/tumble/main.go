// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Command tumble runs, reports on, and inspects strict-GA runs over Go kernels.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: tumble <command> [flags]

commands:
  run       run a kernel under a config and write a run record
  report    summarise a run record
  inspect   print a run record's archive and best genome
  version   print the tumble version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "run", "report", "inspect":
		fmt.Fprintf(os.Stderr, "tumble %s: not implemented yet\n", os.Args[1])
		os.Exit(1)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}
