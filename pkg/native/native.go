// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Kernel locates a reference kernel package: Dir holds the reference in
// RefFile (package Package, function Func) and its tests and benchmarks.
type Kernel struct {
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	Package string `json:"package"`
	Func    string `json:"func"`
	RefFile string `json:"ref_file"`
}

// Config is the native stage's configuration.
type Config struct {
	Count     int           `json:"count"`     // go test -count; at least 10 for a reported number
	Benchtime string        `json:"benchtime"` // go test -benchtime
	Timeout   time.Duration `json:"timeout"`   // per subprocess
	MemLimit  string        `json:"memlimit"`  // GOMEMLIMIT for the subprocesses
	// Benchstat runs benchstat with label=rawfile as its last argument, in
	// BenchstatDir (the tumble module, which carries it as a tool).
	Benchstat    []string `json:"benchstat"`
	BenchstatDir string   `json:"benchstat_dir"`
	Confidence   float64  `json:"confidence"`
	// CPUs, if set, pins the native stage's go test runs to these CPUs with
	// taskset -c (Linux), e.g. a host's performance cores, so benchmarks do
	// not move between core types. Empty runs unpinned.
	CPUs string `json:"cpus,omitempty"`
}

// Defaults is -count=10 (SETUP.md's minimum), -benchtime=100ms, a 5-minute
// timeout, GOMEMLIMIT=1GiB, and 95% intervals.
func Defaults() Config {
	return Config{
		Count:      10,
		Benchtime:  "100ms",
		Timeout:    5 * time.Minute,
		MemLimit:   "1GiB",
		Benchstat:  []string{"go", "tool", "benchstat"},
		Confidence: 0.95,
	}
}

// Row is one benchmark's ns/op: the median and its distribution-free
// confidence interval (benchmath.AssumeNothing, as benchstat computes it).
type Row struct {
	Name   string  `json:"name"`
	N      int     `json:"n"`
	NsOp   float64 `json:"ns_op"`
	Lo     float64 `json:"lo"`
	Hi     float64 `json:"hi"`
	Conf   float64 `json:"confidence"`
	Warned bool    `json:"warned,omitempty"` // benchmath warned (too few samples for the confidence)
	// NoInterval is set when too few samples bound the interval at the
	// confidence: benchmath's bounds are infinite, and Lo and Hi are 0. A
	// row without an interval is not a result (SETUP.md).
	NoInterval bool `json:"no_interval,omitempty"`
}

// Result is one genome through the native stage.
type Result struct {
	// Verified is true when the kernel's tests pass with the genome in
	// place of the reference; TestOutput holds their output otherwise.
	Verified   bool   `json:"verified"`
	TestOutput string `json:"test_output,omitempty"`
	Raw        string `json:"raw"`   // raw go test -bench output
	Table      string `json:"table"` // the benchstat table
	Rows       []Row  `json:"rows"`
}

// Run prints f into a copy of the kernel as the reference function, runs
// the kernel's tests on it, and benchmarks it.
func Run(k Kernel, cfg Config, f *tir.Func, work string) (*Result, error) {
	src := tir.PrintFile(k.Package, []tir.Named{{Name: k.Func, F: f}})
	return RunSource(k, cfg, src, work)
}

// RunSource is Run with the replacement for RefFile given as source; the
// reference itself is RunSource with the kernel's own file.
func RunSource(k Kernel, cfg Config, src []byte, work string) (res *Result, err error) {
	dir, err := os.MkdirTemp(work, "native-"+k.Name+"-")
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

	res = &Result{}
	// -vet=off: go test's default vet checks reject legal Go that evolution
	// produces as neutral material (x || x, x == x). Vet style is not part
	// of a genome's correctness; the kernel's tests are.
	out, err := run(cfg, dir, "go", "test", "-vet=off", "-count=1", "-run", "^Test", ".")
	if err != nil {
		res.TestOutput = string(out)
		return res, nil
	}
	res.Verified = true

	raw, err := run(cfg, dir, "go", "test", "-vet=off", "-run", "^$", "-bench", "^Benchmark",
		"-count", strconv.Itoa(cfg.Count), "-benchtime", cfg.Benchtime, ".")
	if err != nil {
		return nil, fmt.Errorf("native: benchmark: %v\n%s", err, raw)
	}
	res.Raw = string(raw)
	if res.Rows, err = summarize(raw, cfg.Confidence); err != nil {
		return nil, err
	}
	if len(cfg.Benchstat) > 0 {
		rawFile := filepath.Join(dir, "bench.txt")
		if err := os.WriteFile(rawFile, raw, 0o644); err != nil {
			return nil, err
		}
		// benchstat's label=file form names the column after the kernel
		// rather than the temporary path.
		args := append(append([]string(nil), cfg.Benchstat[1:]...), k.Name+"="+rawFile)
		table, err := run(cfg, cfg.BenchstatDir, cfg.Benchstat[0], args...)
		if err != nil {
			return nil, fmt.Errorf("native: benchstat: %v\n%s", err, table)
		}
		res.Table = string(table)
	}
	return res, nil
}

// copyKernel copies the kernel directory to dir as its own module, with
// src in place of the reference file.
func copyKernel(k Kernel, dir string, src []byte) error {
	err := filepath.WalkDir(k.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(k.Dir, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		if rel == k.RefFile || rel == "go.mod" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), b, 0o644)
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, k.RefFile), src, 0o644); err != nil {
		return err
	}
	mod := fmt.Sprintf("module tumble.native/%s\n\ngo 1.24\n", k.Name)
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644)
}

func run(cfg Config, dir, name string, args ...string) ([]byte, error) {
	if cfg.CPUs != "" && name == "go" {
		args = append([]string{"-c", cfg.CPUs, name}, args...)
		name = "taskset"
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOMEMLIMIT="+cfg.MemLimit)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %s", cfg.Timeout)
	}
	return out.Bytes(), err
}

// summarize parses go test -bench output with benchfmt and computes each
// benchmark's ns/op median and interval with benchmath.
func summarize(raw []byte, conf float64) ([]Row, error) {
	samples := map[string][]float64{}
	var order []string
	r := benchfmt.NewReader(bytes.NewReader(raw), "bench.txt")
	for r.Scan() {
		res, ok := r.Result().(*benchfmt.Result)
		if !ok {
			continue
		}
		v, ok := res.Value("sec/op")
		if !ok {
			continue
		}
		name := string(res.Name.Full())
		if _, seen := samples[name]; !seen {
			order = append(order, name)
		}
		samples[name] = append(samples[name], v*1e9)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, errors.New("native: no benchmark results")
	}
	var rows []Row
	for _, name := range order {
		s := benchmath.NewSample(samples[name], &benchmath.DefaultThresholds)
		sum := benchmath.AssumeNothing.Summary(s, conf)
		row := Row{
			Name: name, N: len(samples[name]),
			NsOp: sum.Center, Lo: sum.Lo, Hi: sum.Hi, Conf: sum.Confidence,
			Warned: len(sum.Warnings) > 0,
		}
		if math.IsInf(row.Lo, 0) || math.IsInf(row.Hi, 0) || math.IsNaN(row.Lo) || math.IsNaN(row.Hi) {
			row.Lo, row.Hi, row.NoInterval = 0, 0, true
		}
		rows = append(rows, row)
	}
	return rows, nil
}
