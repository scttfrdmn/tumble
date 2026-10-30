// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"

	"github.com/scttfrdmn/tumble/kernels"
	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Score is one candidate program's score for E1's arm C (#24): the same
// giri evaluator, selection vectors, correctness rule and abstract cost as
// tumble's arms use.
type Score struct {
	// CombinedScore is what OpenEvolve maximises: the fraction of
	// selection vectors correct, in [0, 1), while any vector is wrong; and
	// 1 + reference cost / candidate cost once all are correct, so a
	// cheaper correct program scores higher than a dearer one, and any
	// correct program higher than any incorrect one.
	CombinedScore float64 `json:"combined_score"`
	Correct       int     `json:"correct"`
	Total         int     `json:"total"`
	Cost          uint64  `json:"cost"` // abstract cost (giri steps over the selection vectors)
	ReferenceCost uint64  `json:"reference_cost"`
	Violations    int     `json:"violations"`
	Exhausted     bool    `json:"exhausted"`
	Error         string  `json:"error,omitempty"` // the candidate could not be scored: score 0
}

func scoreCmd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	kernel := fs.String("kernel", "popcount", "kernel")
	program := fs.String("program", "", "Go source file defining the kernel's function (required)")
	maxSteps := fs.Uint64("max-steps", 1<<20, "step budget over all selection vectors")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *program == "" {
		return errors.New("--program is required")
	}
	k, err := kernels.Get(*kernel)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(*program)
	if err != nil {
		return err
	}
	s := score(k, src, *maxSteps)
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(b))
	return err
}

// score evaluates src's definition of the kernel function. A candidate that
// fails to parse, imports outside the grammar's call whitelist, or
// type-checks badly scores 0 with Error set.
func score(k *kernels.Kernel, src []byte, maxSteps uint64) Score {
	sel, _, err := k.Vectors()
	if err != nil {
		return Score{Error: err.Error()}
	}
	want, wantAfter := k.Reference(sel)
	s := Score{Total: len(sel)}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "candidate.go", src, parser.ImportsOnly)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	allowed := map[string]bool{}
	for _, c := range k.Grammar().Calls {
		if c.Pkg != "" {
			allowed[c.Pkg] = true
		}
	}
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if !allowed[p] {
			s.Error = fmt.Sprintf("import %q is outside the kernel's grammar (allowed: %v)", p, keys(allowed))
			return s
		}
	}
	ref, err := evalSource(k, k.Source, sel, maxSteps)
	if err != nil {
		s.Error = "reference: " + err.Error()
		return s
	}
	s.ReferenceCost = ref.Steps
	res, err := evalSource(k, src, sel, maxSteps)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	s.Cost, s.Violations, s.Exhausted = res.Steps, len(res.Violations), res.Exhausted
	for i := range sel {
		if i < len(res.Outputs) && reflect.DeepEqual(res.Outputs[i], want[i]) && reflect.DeepEqual(res.ArgsAfter[i], wantAfter[i]) {
			s.Correct++
		}
	}
	switch {
	case s.Violations > 0 || s.Exhausted || s.Correct < s.Total:
		s.CombinedScore = float64(s.Correct) / float64(s.Total) * 0.999
	default:
		s.CombinedScore = 1 + float64(s.ReferenceCost)/float64(s.Cost)
	}
	return s
}

// evalSource runs the kernel function defined in src (package name aside)
// through the giri evaluator, as a one-function generation package.
func evalSource(k *kernels.Kernel, src []byte, in []eval.Input, maxSteps uint64) (eval.Result, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "candidate.go", src, parser.PackageClauseOnly)
	if err != nil {
		return eval.Result{}, err
	}
	// The generation package is named g: replace the candidate's package
	// clause, keeping everything around it (comments above it included).
	start, end := fset.Position(f.Package).Offset, fset.Position(f.Name.End()).Offset
	gen := append(append(append([]byte(nil), src[:start]...), "package g"...), src[end:]...)
	if err := tir.CheckSource(gen); err != nil {
		return eval.Result{}, fmt.Errorf("type check: %w", err)
	}
	dir, err := os.MkdirTemp("", "tumble-score-")
	if err != nil {
		return eval.Result{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	gdir := filepath.Join(dir, "g")
	if err := eval.WriteGeneration(gdir, gen); err != nil {
		return eval.Result{}, err
	}
	ev := eval.NewGiri(eval.Config{NoCache: true})
	p, err := ev.Build(gdir)
	if err != nil {
		return eval.Result{}, err
	}
	fn, ok := p.Funcs()[k.Func]
	if !ok {
		return eval.Result{}, fmt.Errorf("no function %s", k.Func)
	}
	return ev.Run(fn, in, eval.RunOptions{MaxSteps: maxSteps})
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
