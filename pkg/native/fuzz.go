// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// FuzzResult is a genome's final fuzz run against the kernel's oracle
// (PROJECT.md §4.4): the kernel's own fuzz target, with the genome in place
// of the reference, for Duration.
type FuzzResult struct {
	Passed   bool    `json:"passed"`
	Duration string  `json:"duration"`
	Execs    int64   `json:"execs"`   // from the fuzzer's last progress line; 0 if none
	Output   string  `json:"output"`  // go test's output on failure, else its last lines
	Seconds  float64 `json:"seconds"` // wall time of the run
}

var fuzzExecs = regexp.MustCompile(`execs: (\d+)`)

// Fuzz runs the kernel's fuzz target for d against f printed as the
// reference function, in a copy of the kernel, under cfg's memory limit.
// The subprocess timeout is d plus cfg.Timeout.
func Fuzz(k Kernel, cfg Config, f *tir.Func, work string, d time.Duration) (res *FuzzResult, err error) {
	src := tir.PrintFile(k.Package, []tir.Named{{Name: k.Func, F: f}})
	dir, err := os.MkdirTemp(work, "fuzz-"+k.Name+"-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr := os.RemoveAll(dir); err == nil {
			err = rerr
		}
	}()
	if err := copyKernel(k, dir, src); err != nil {
		return nil, err
	}
	c := cfg
	c.Timeout = d + cfg.Timeout
	start := time.Now()
	out, runErr := run(c, dir, "go", "test", "-vet=off", "-run", "^$", "-fuzz", "^Fuzz", "-fuzztime", d.String(), ".")
	res = &FuzzResult{Passed: runErr == nil, Duration: d.String(), Seconds: time.Since(start).Seconds()}
	if m := fuzzExecs.FindAllSubmatch(out, -1); len(m) > 0 {
		res.Execs, _ = strconv.ParseInt(string(m[len(m)-1][1]), 10, 64)
	}
	text := string(out)
	if res.Passed && len(text) > 2000 {
		text = text[len(text)-2000:]
	}
	res.Output = text
	if !res.Passed && res.Execs == 0 && !fuzzFailure.MatchString(text) {
		return res, fmt.Errorf("native: fuzz run did not start: %v\n%s", runErr, text)
	}
	return res, nil
}

// fuzzFailure matches go test's report of a failing fuzz input.
var fuzzFailure = regexp.MustCompile(`(?m)^\s*--- FAIL: Fuzz|Failing input written to`)
