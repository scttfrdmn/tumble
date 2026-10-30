// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package kernels

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// TestHeadroom measures the abstract cost, on the selection vectors, of
// hand-written popcount genomes inside the v0 grammar against the
// reference. Unrolled loops are correct and cheaper; the GA has not found
// them (#77). The costs are logged (go test -v); the test asserts only that
// they are inside the grammar, correct, and cheaper than the reference.
func TestHeadroom(t *testing.T) {
	k, _ := Get("popcount")
	prob, err := k.Problem()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("popcount/testdata/candidates/candidates.go")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"Ref", "Unroll2", "Unroll4"}
	var named []tir.Named
	for _, n := range names {
		f, err := tir.Parse(src, n)
		if err != nil {
			t.Fatal(n, err)
		}
		if err := prob.Grammar.Admits(f); err != nil {
			t.Errorf("%s is outside the grammar: %v", n, err)
		}
		named = append(named, tir.Named{Name: n, F: f})
	}
	dir := filepath.Join(t.TempDir(), "gen")
	if err := eval.WriteGeneration(dir, tir.PrintFile("g", named)); err != nil {
		t.Fatal(err)
	}
	ev := eval.NewGiri(eval.Config{NoCache: true})
	p, err := ev.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	cost := map[string]uint64{}
	for _, n := range names {
		r, err := ev.Run(p.Funcs()[n], prob.Selection, eval.RunOptions{MaxSteps: 1 << 20})
		if err != nil {
			t.Fatal(err)
		}
		for i := range prob.Want {
			if !reflect.DeepEqual(r.Outputs[i], prob.Want[i]) || !reflect.DeepEqual(r.ArgsAfter[i], prob.WantAfter[i]) {
				t.Errorf("%s wrong on selection vector %d", n, i)
			}
		}
		if len(r.Violations) > 0 {
			t.Errorf("%s: violations %v", n, r.Violations)
		}
		cost[n] = r.Steps
		t.Logf("%-8s %2d statements  abstract cost %d", n, named[len(cost)-1].F.Len(), r.Steps)
	}
	for _, n := range names[1:] {
		if cost[n] >= cost["Ref"] {
			t.Errorf("%s costs %d, not less than the reference's %d", n, cost[n], cost["Ref"])
		}
	}
}
