// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

// Command tumble runs, reports on, and inspects strict-GA runs over Go kernels.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/scttfrdmn/tumble/kernels"
	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/native"
	"github.com/scttfrdmn/tumble/pkg/pop"
	"github.com/scttfrdmn/tumble/pkg/report"
	"github.com/scttfrdmn/tumble/pkg/tir"
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
	var err error
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "run":
		err = runCmd(os.Args[2:], os.Stdout)
	case "report":
		err = reportCmd(os.Args[2:], os.Stdout)
	case "inspect":
		err = inspectCmd(os.Args[2:], os.Stdout)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tumble %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

// errWriter keeps the first write error, so output code can print freely and
// check once.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	e.err = err
	return n, err
}

// Config is a run config file. Fields left out keep their defaults.
type Config struct {
	Arm       string        `json:"arm"`
	Evaluator eval.Config   `json:"evaluator"`
	Params    pop.Params    `json:"params"`
	Native    NativeConfig  `json:"native"`
	WallClock time.Duration `json:"-"`
	Wall      string        `json:"wall_clock"` // a time.Duration, e.g. "4h"; "" for none
}

// NativeConfig switches the native stage and configures it.
type NativeConfig struct {
	Enabled bool `json:"enabled"`
	native.Config
}

func defaultConfig() Config {
	return Config{
		Arm:       "A",
		Evaluator: eval.Config{Name: "stub"},
		Params:    pop.Defaults(),
		Native:    NativeConfig{Enabled: true, Config: native.Defaults()},
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, err
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", path, err)
		}
	}
	if cfg.Wall != "" {
		d, err := time.ParseDuration(cfg.Wall)
		if err != nil {
			return cfg, fmt.Errorf("wall_clock: %w", err)
		}
		cfg.WallClock = d
	}
	return cfg, nil
}

// underExperiments reports whether dir is inside an experiments/ directory.
func underExperiments(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	return slices.Contains(strings.Split(filepath.ToSlash(abs), "/"), "experiments")
}

func runCmd(args []string, out io.Writer) error {
	stdout := &errWriter{w: out}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	kernel := fs.String("kernel", "popcount", "kernel to run")
	cfgPath := fs.String("config", "", "run config (JSON); defaults if empty")
	outDir := fs.String("out", "", "run record directory (required)")
	work := fs.String("work", "", "scratch directory (default: a new temporary directory, removed after)")
	root := fs.String("root", ".", "the tumble module root, where kernels/ and the benchstat tool are")
	gens := fs.Int("generations", 0, "override params.generations")
	seed := fs.Uint64("seed", 0, "override params.seed")
	noNative := fs.Bool("no-native", false, "skip the native stage")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outDir == "" {
		return errors.New("--out is required")
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *gens > 0 {
		cfg.Params.Generations = *gens
	}
	if *seed > 0 {
		cfg.Params.Seed = *seed
	}
	if *noNative {
		cfg.Native.Enabled = false
	}
	k, err := kernels.Get(*kernel)
	if err != nil {
		return err
	}
	prob, err := k.Problem()
	if err != nil {
		return err
	}
	_, hold, err := k.Vectors()
	if err != nil {
		return err
	}

	scratch := *work
	if scratch == "" {
		if scratch, err = os.MkdirTemp("", "tumble-run-"); err != nil {
			return err
		}
		defer func() {
			if rerr := os.RemoveAll(scratch); rerr != nil {
				fmt.Fprintf(os.Stderr, "tumble run: removing scratch: %v\n", rerr)
			}
		}()
	}
	ev, err := eval.New(cfg.Evaluator, filepath.Join(scratch, "eval"))
	if err != nil {
		return err
	}
	// Requirement 5: a grammar may admit only calls the evaluator models
	// concretely; an opaque call would poison the fingerprint index.
	if c, ok := ev.(interface{ IsConcrete(pkg, name string) bool }); ok {
		for _, call := range prob.Grammar.Calls {
			if call.Pkg != "" && !c.IsConcrete(call.Pkg, call.Name) {
				return fmt.Errorf("grammar admits %s, which %s does not model concretely", call, ev.Name())
			}
		}
	}
	if underExperiments(*outDir) {
		if err := eval.CheckExperiment(ev); err != nil {
			return fmt.Errorf("%s is under experiments/: %w", *outDir, err)
		}
	}

	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	nk := k.Native
	nk.Dir = filepath.Join(rootAbs, nk.Dir)
	cfg.Native.BenchstatDir = rootAbs

	giriPath := ""
	if s, ok := ev.(*eval.Stub); ok {
		giriPath = s.Giri
	}
	rec := &report.Record{
		Kernel: k.Name, Arm: cfg.Arm, Evaluator: ev.Name(), Instrument: ev.IsInstrument(),
		Seed: cfg.Params.Seed, WallClock: cfg.Wall,
		Hardware: report.DetectHardware(), Versions: report.DetectVersions(giriPath),
		Params: cfg.Params, Grammar: prob.Grammar,
		Native:  report.NativeSettings{Enabled: cfg.Native.Enabled, Config: cfg.Native.Config},
		Vectors: report.VectorCounts{Selection: len(prob.Selection), Holdout: len(hold)},
	}
	if !ev.IsInstrument() {
		rec.Notes = append(rec.Notes,
			fmt.Sprintf("evaluator %q is not an instrument: per-generation readings are a plumbing check, not measurements", ev.Name()),
			"vc_hit_rate is 0 and vc_measured is false: the stub has no value cache")
	}
	w, err := report.Create(*outDir, rec)
	if err != nil {
		return err
	}

	start := time.Now()
	obs := func(rd *pop.Readings, a *pop.Archive) error {
		nativeStart := time.Now()
		if cfg.Native.Enabled {
			// pop never offers a cost twice, so no genome repeats.
			for _, el := range rd.Elites {
				id := el.F.Identity()
				res, err := native.Run(nk, cfg.Native.Config, el.F, scratch)
				if err != nil {
					return err
				}
				if err := w.Native(report.NativeEntry{Gen: rd.Gen, ID: el.ID, Identity: fmt.Sprintf("%x", id), Cost: el.Cost, Result: res}); err != nil {
					return err
				}
			}
		}
		if err := w.Generation(rd, a, time.Since(nativeStart).Seconds()); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "gen %4d  evals %5d  fully-correct %4d  archive %4d  discards %v\n",
			rd.Gen, rd.Evaluations, rd.FullyCorrect, rd.ArchiveSize, rd.Discards)
		if cfg.WallClock > 0 && time.Since(start) >= cfg.WallClock {
			return pop.ErrStop
		}
		return stdout.err
	}
	res, err := pop.Run(ev, prob, cfg.Params, filepath.Join(scratch, "pop"), obs)
	if err != nil {
		return err
	}
	return w.Close(res.Archive.Best())
}

func reportCmd(args []string, out io.Writer) error {
	stdout := &errWriter{w: out}
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: tumble report <record dir>")
	}
	l, err := report.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	r := l.Record
	_, _ = fmt.Fprintf(stdout, "kernel %s  arm %s  evaluator %s (instrument: %v)  seed %d  generations %d\n",
		r.Kernel, r.Arm, r.Evaluator, r.Instrument, r.Seed, r.Gens)
	for _, n := range r.Notes {
		_, _ = fmt.Fprintf(stdout, "note: %s\n", n)
	}
	tw := tabwriter.NewWriter(stdout, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "gen\tevals\tfully-correct\tarchive\tindex\tvc\tyield x/m/s\tdiscards")
	for _, g := range l.Generations {
		var rd struct {
			Gen          int                       `json:"gen"`
			Evaluations  int                       `json:"evaluations"`
			FullyCorrect int                       `json:"fully_correct"`
			ArchiveSize  int                       `json:"archive_size"`
			IndexSize    int                       `json:"index_size"`
			VCHitRate    float64                   `json:"vc_hit_rate"`
			VCMeasured   bool                      `json:"vc_measured"`
			Yield        map[string]map[string]any `json:"yield"`
			Discards     map[string]int            `json:"discards"`
		}
		if err := json.Unmarshal(g, &rd); err != nil {
			return err
		}
		vc := "n/m"
		if rd.VCMeasured {
			vc = fmt.Sprintf("%.3f", rd.VCHitRate)
		}
		// A rate over zero attempts is not a reading: the last generation
		// breeds nothing, and substitution may never apply.
		y := func(k string) string {
			if n, _ := rd.Yield[k]["attempted"].(float64); n == 0 {
				return "n/m"
			}
			return fmt.Sprintf("%.2f", rd.Yield[k]["rate"])
		}
		_, _ = fmt.Fprintf(tw, "%d\t%d\t%d\t%d\t%d\t%s\t%s/%s/%s\t%v\n", rd.Gen, rd.Evaluations, rd.FullyCorrect,
			rd.ArchiveSize, rd.IndexSize, vc, y("crossover"), y("mutation"), y("substitution"), rd.Discards)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	sum, err := report.Summarize(fs.Arg(0), l)
	if err != nil {
		return err
	}
	rateStr := func(r report.Rate) string {
		if !r.Measured {
			return fmt.Sprintf("n/m (%d/%d)", r.Num, r.Den)
		}
		return fmt.Sprintf("%.4g (%d/%d)", r.Value, r.Num, r.Den)
	}
	_, _ = fmt.Fprintf(stdout, "\nover the run:\n")
	for _, k := range []string{"crossover", "mutation", "substitution"} {
		_, _ = fmt.Fprintf(stdout, "  yield %-12s %s\n", k, rateStr(sum.Yield[k]))
	}
	_, _ = fmt.Fprintf(stdout, "  fp collisions      %s\n", rateStr(sum.Collisions))
	for _, f := range sum.CostFit {
		_, _ = fmt.Fprintf(stdout, "  cost fit %-20s spearman %.3f over %d genomes\n", f.Benchmark, f.Spearman, f.N)
	}
	if len(sum.CostFit) == 0 {
		_, _ = fmt.Fprintf(stdout, "  cost fit           n/m (no verified native results)\n")
	}
	_, _ = fmt.Fprintf(stdout, "  seconds/generation %.2f (evaluate %.2f, index %.2f, breed %.2f, native %.2f)\n",
		sum.MeanSeconds, sum.MeanPhases["evaluate"], sum.MeanPhases["index"], sum.MeanPhases["breed"], sum.MeanPhases["native"])
	_, _ = fmt.Fprintf(stdout, "  interpret ns/instr %.1f (island wall time over interpreted instructions)\n", sum.NsPerInstr)
	if sum.MaxLoad >= 0 {
		_, _ = fmt.Fprintf(stdout, "  max load average   %.1f on %d CPUs\n", sum.MaxLoad, l.Record.Hardware.NumCPU)
	} else {
		_, _ = fmt.Fprintf(stdout, "  max load average   not recorded\n")
	}
	return stdout.err
}

func inspectCmd(args []string, out io.Writer) error {
	stdout := &errWriter{w: out}
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: tumble inspect <record dir>")
	}
	l, err := report.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "archive after generation %d: %d cells\n", l.ArchiveGen, len(l.Archive))
	tw := tabwriter.NewWriter(stdout, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "cost-bucket\tlen-bucket\tdepth\tid\tisland\tgen\torigin\tcorrect\tcost\tlen")
	for _, e := range l.Archive {
		_, _ = fmt.Fprintf(tw, "%d\t%d\t%d\t%d\t%d\t%d\t%s\t%d/%d\t%d\t%d\n", e.Cell.CostBucket, e.Cell.LenBucket, e.Cell.Depth,
			e.ID, e.Island, e.Gen, e.Origin, e.Correct, e.Vectors, e.Cost, e.Len)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(l.Best) == 0 {
		return errors.New("record has no best genome")
	}
	_, _ = fmt.Fprintf(stdout, "\nbest genome:\n\n%s\n", l.Best)
	if err := tir.CheckSource(l.Best); err != nil {
		return fmt.Errorf("best genome does not type-check: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "type-check: ok")
	return stdout.err
}
