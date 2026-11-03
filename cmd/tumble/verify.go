// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/scttfrdmn/tumble/kernels"
	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/native"
	"github.com/scttfrdmn/tumble/pkg/pop"
	"github.com/scttfrdmn/tumble/pkg/report"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// VerifyFile is the verification a run record's best genome gets
// (PROJECT.md §4.4): a kernel is reported only if its genome passes the
// selection vectors, the holdout vectors, and a final fuzz run against the
// reference's oracle.
const VerifyFile = "verify.json"

// Verification is what VerifyFile holds.
type Verification struct {
	Genome      string              `json:"genome,omitempty"`  // the genome's identity
	Program     string              `json:"program,omitempty"` // or, with --program, the source's SHA-256
	Selection   report.VectorCounts `json:"-"`
	SelCorrect  int                 `json:"selection_correct"`
	SelTotal    int                 `json:"selection_total"`
	HoldCorrect int                 `json:"holdout_correct"`
	HoldTotal   int                 `json:"holdout_total"`
	Fuzz        *native.FuzzResult  `json:"fuzz"`
	Verified    bool                `json:"verified"` // all three pass
	Versions    report.Versions     `json:"versions"`
	Hardware    report.Hardware     `json:"hardware"`
}

func verifyCmd(args []string, out io.Writer) error {
	stdout := &errWriter{w: out}
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	root := fs.String("root", ".", "the tumble module root, where kernels/ is")
	fuzzFor := fs.Duration("fuzztime", 60*time.Second, "final fuzz run length")
	program := fs.String("program", "", "verify this Go source file (an arm C program) instead of a record's best genome; the directory argument is then where verify.json goes")
	kernel := fs.String("kernel", "popcount", "with --program, the kernel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: tumble verify [--fuzztime 60s] [--program file.go --kernel k] <dir>")
	}
	dir := fs.Arg(0)
	if *program != "" {
		return verifyProgram(*program, *kernel, *root, dir, *fuzzFor, stdout)
	}
	l, err := report.Load(dir)
	if err != nil {
		return err
	}
	if len(l.Best) == 0 {
		return errors.New("record has no best genome")
	}
	if _, err := os.Stat(filepath.Join(dir, VerifyFile)); err == nil {
		return fmt.Errorf("%s already holds %s", dir, VerifyFile)
	}
	k, err := kernels.Get(l.Record.Kernel)
	if err != nil {
		return err
	}
	f, err := tir.Parse(l.Best, "Best")
	if err != nil {
		return err
	}
	sel, hold, err := k.Vectors()
	if err != nil {
		return err
	}
	ev := eval.NewGiri(eval.Config{NoCache: true})
	count := func(in []eval.Input) (int, error) {
		want, wantAfter := k.Reference(in)
		gdir, err := os.MkdirTemp("", "tumble-verify-")
		if err != nil {
			return 0, err
		}
		defer func() { _ = os.RemoveAll(gdir) }()
		if err := eval.WriteGeneration(filepath.Join(gdir, "g"), tir.PrintFile("g", []tir.Named{{Name: "Best", F: f}})); err != nil {
			return 0, err
		}
		p, err := ev.Build(filepath.Join(gdir, "g"))
		if err != nil {
			return 0, err
		}
		res, err := ev.Run(p.Funcs()["Best"], in, eval.RunOptions{MaxSteps: l.Record.Params.MaxSteps})
		if err != nil {
			return 0, err
		}
		n := 0
		for i := range in {
			if i < len(res.Outputs) && reflect.DeepEqual(res.Outputs[i], want[i]) && reflect.DeepEqual(res.ArgsAfter[i], wantAfter[i]) {
				n++
			}
		}
		return n, nil
	}
	v := Verification{SelTotal: len(sel), HoldTotal: len(hold), Hardware: report.DetectHardware(), Versions: report.DetectVersions("")}
	id := f.Identity()
	v.Genome = fmt.Sprintf("%x", id)
	if v.SelCorrect, err = count(sel); err != nil {
		return err
	}
	if v.HoldCorrect, err = count(hold); err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	nk := k.Native
	nk.Dir = filepath.Join(rootAbs, nk.Dir)
	scratch, err := os.MkdirTemp("", "tumble-verify-fuzz-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	if v.Fuzz, err = native.Fuzz(nk, native.Defaults(), f, scratch, *fuzzFor); err != nil {
		return err
	}
	return writeVerification(v, dir, stdout)
}

// verifyProgram verifies a source file as written, not a genome parsed from
// it: its selection and holdout vectors through giri, as tumble score runs
// it, and the fuzz run with it in place of the reference.
func verifyProgram(program, kernel, root, dir string, fuzzFor time.Duration, stdout *errWriter) error {
	if _, err := os.Stat(filepath.Join(dir, VerifyFile)); err == nil {
		return fmt.Errorf("%s already holds %s", dir, VerifyFile)
	}
	k, err := kernels.Get(kernel)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(program)
	if err != nil {
		return err
	}
	sel, hold, err := k.Vectors()
	if err != nil {
		return err
	}
	count := func(in []eval.Input) (int, error) {
		want, wantAfter := k.Reference(in)
		res, err := evalSource(k, src, in, pop.Defaults().MaxSteps)
		if err != nil {
			return 0, err
		}
		n := 0
		for i := range in {
			if i < len(res.Outputs) && reflect.DeepEqual(res.Outputs[i], want[i]) && reflect.DeepEqual(res.ArgsAfter[i], wantAfter[i]) {
				n++
			}
		}
		return n, nil
	}
	v := Verification{Program: fmt.Sprintf("%x", sha256.Sum256(src)), SelTotal: len(sel), HoldTotal: len(hold), Hardware: report.DetectHardware(), Versions: report.DetectVersions("")}
	if v.SelCorrect, err = count(sel); err != nil {
		return err
	}
	if v.HoldCorrect, err = count(hold); err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	nk := k.Native
	nk.Dir = filepath.Join(rootAbs, nk.Dir)
	nsrc, err := withPackage(src, nk.Package)
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "tumble-verify-fuzz-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	if v.Fuzz, err = native.FuzzSource(nk, native.Defaults(), nsrc, scratch, fuzzFor); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeVerification(v, dir, stdout)
}

func writeVerification(v Verification, dir string, stdout *errWriter) error {
	v.Verified = v.SelCorrect == v.SelTotal && v.HoldCorrect == v.HoldTotal && v.Fuzz.Passed
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, VerifyFile), append(b, '\n'), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "selection %d/%d  holdout %d/%d  fuzz %s for %s (%d execs)  verified %v\n",
		v.SelCorrect, v.SelTotal, v.HoldCorrect, v.HoldTotal, map[bool]string{true: "passed", false: "FAILED"}[v.Fuzz.Passed], v.Fuzz.Duration, v.Fuzz.Execs, v.Verified)
	return stdout.err
}

// Baseline is a gc-compiled reference benchmark record (PROJECT.md §6.7).
type Baseline struct {
	Kernel   string          `json:"kernel"`
	Program  string          `json:"program,omitempty"` // with --program, the source's SHA-256; else the reference
	Started  string          `json:"started"`
	Hardware report.Hardware `json:"hardware"`
	Versions report.Versions `json:"versions"`
	Config   native.Config   `json:"config"`
	Load     float64         `json:"load1_before"` // -1 if unreadable
	Result   *native.Result  `json:"result"`
}

func baselineCmd(args []string, out io.Writer) error {
	stdout := &errWriter{w: out}
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	kernel := fs.String("kernel", "popcount", "kernel")
	outDir := fs.String("out", "", "record directory (required)")
	root := fs.String("root", ".", "the tumble module root")
	cpus := fs.String("cpus", "", "pin the benchmark to these CPUs with taskset, as a run's native.cpus (Linux)")
	program := fs.String("program", "", "benchmark this Go source file (an arm C program) in place of the reference")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outDir == "" {
		return errors.New("--out is required")
	}
	if _, err := os.Stat(filepath.Join(*outDir, "baseline.json")); err == nil {
		return fmt.Errorf("%s already holds a baseline", *outDir)
	}
	k, err := kernels.Get(*kernel)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	nk := k.Native
	nk.Dir = filepath.Join(rootAbs, nk.Dir)
	cfg := native.Defaults()
	cfg.BenchstatDir = rootAbs
	cfg.CPUs = *cpus
	b := Baseline{Kernel: k.Name, Started: time.Now().UTC().Format(time.RFC3339), Hardware: report.DetectHardware(),
		Versions: report.DetectVersions(""), Config: cfg, Load: report.LoadAverage()}
	scratch, err := os.MkdirTemp("", "tumble-baseline-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	src := k.Source
	if *program != "" {
		raw, err := os.ReadFile(*program)
		if err != nil {
			return err
		}
		if src, err = withPackage(raw, nk.Package); err != nil {
			return err
		}
		b.Program = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	if b.Result, err = native.RunSource(nk, cfg, src, scratch); err != nil {
		return err
	}
	if !b.Result.Verified {
		return fmt.Errorf("the program failed the kernel's tests:\n%s", b.Result.TestOutput)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	j, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*outDir, "baseline.json"), append(j, '\n'), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, b.Result.Table)
	return stdout.err
}
