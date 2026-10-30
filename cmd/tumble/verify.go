// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package main

import (
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
	Genome      string              `json:"genome"` // the genome's identity
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: tumble verify [--fuzztime 60s] <record dir>")
	}
	dir := fs.Arg(0)
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
	b := Baseline{Kernel: k.Name, Started: time.Now().UTC().Format(time.RFC3339), Hardware: report.DetectHardware(),
		Versions: report.DetectVersions(""), Config: cfg, Load: report.LoadAverage()}
	scratch, err := os.MkdirTemp("", "tumble-baseline-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	if b.Result, err = native.RunSource(nk, cfg, k.Source, scratch); err != nil {
		return err
	}
	if !b.Result.Verified {
		return fmt.Errorf("the reference failed its own tests:\n%s", b.Result.TestOutput)
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
