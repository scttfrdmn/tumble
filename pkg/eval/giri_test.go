// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func buildGen(t *testing.T, ev Evaluator) Program {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "gen")
	if err := WriteGeneration(dir, []byte(genSrc)); err != nil {
		t.Fatal(err)
	}
	p, err := ev.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func runGiri(t *testing.T, g *Giri, p Program, opt RunOptions) map[string]Result {
	t.Helper()
	out := map[string]Result{}
	for name, fn := range p.Funcs() {
		r, err := g.Run(fn, testInputs, opt)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = r
	}
	return out
}

func TestGiri(t *testing.T) {
	g := NewGiri(Config{})
	rs := runGiri(t, g, buildGen(t, g), RunOptions{MaxSteps: 100000, Trace: true})

	ref := rs["g_0001"]
	for i, w := range []int{0, 6, 66} {
		if len(ref.Outputs[i]) != 1 || ref.Outputs[i][0] != w {
			t.Errorf("reference genome on vector %d = %v, want %d", i, ref.Outputs[i], w)
		}
	}
	if ref.Steps == 0 || ref.Exhausted || ref.Partial || len(ref.Violations) != 0 {
		t.Errorf("reference genome: %+v", ref)
	}
	var sum uint64
	for _, n := range ref.StepsBy {
		sum += n
	}
	if sum != ref.Steps || ref.StepsBy["load"] == 0 {
		t.Errorf("steps by class %v do not sum to %d or count no loads", ref.StepsBy, ref.Steps)
	}

	// Traces: one sequence per vector, and the loop's per-iteration values
	// have one entry per word of the vector.
	if len(ref.Traces) == 0 {
		t.Fatal("no traces")
	}
	perWord := false
	for _, tr := range ref.Traces {
		if len(tr.Seqs) != len(testInputs) || tr.Truncated {
			t.Fatalf("trace %s: %d sequences, truncated %v", tr.Value, len(tr.Seqs), tr.Truncated)
		}
		if len(tr.Seqs[0]) == 0 && len(tr.Seqs[1]) == 3 && len(tr.Seqs[2]) == 4 {
			perWord = true
		}
	}
	if !perWord {
		t.Error("no traced value takes one value per word")
	}
	if !slices.IsSortedFunc(ref.Traces, func(a, b ValueTrace) int { return int(a.Value.Pos() - b.Value.Pos()) }) {
		t.Error("traces are not ordered by position")
	}

	// xs[3]: out of range on vectors 0 and 1, 5 on vector 2.
	oob := rs["g_0002"]
	got := map[int]bool{}
	for _, v := range oob.Violations {
		got[v.Input] = true
	}
	if !got[0] || !got[1] || got[2] || len(oob.Outputs[2]) != 1 || oob.Outputs[2][0] != 5 {
		t.Errorf("out-of-range genome: violations %+v, outputs %v", oob.Violations, oob.Outputs)
	}

	loop := rs["g_0003"]
	if !loop.Exhausted || !loop.Partial || loop.Steps != 100000 {
		t.Errorf("billion-iteration loop: exhausted %v partial %v steps %d", loop.Exhausted, loop.Partial, loop.Steps)
	}
	if st := rs["g_0005"]; st.Outputs[1][0] != 3 || !reflect.DeepEqual(testInputs[1].Args[0], []uint64{1, 3, 7}) {
		t.Errorf("store genome output %v; inputs after the run %v", st.Outputs[1], testInputs[1].Args)
	}
	if !g.IsConcrete("math/bits", "OnesCount64") || g.IsConcrete("crypto/rsa", "GenerateKey") {
		t.Error("concreteness query")
	}
}

func TestGiriValueCache(t *testing.T) {
	plain := NewGiri(Config{NoCache: true})
	cached := NewGiri(Config{})
	opt := RunOptions{MaxSteps: 100000, Trace: true}
	want := runGiri(t, plain, buildGen(t, plain), opt)
	p := buildGen(t, cached)
	first := runGiri(t, cached, p, opt)
	cached.EndGeneration()
	second := runGiri(t, cached, p, opt)

	if cached.Cache().Len() == 0 {
		t.Fatal("nothing committed to the cache")
	}
	for name, w := range want {
		for i, r := range []Result{first[name], second[name]} {
			if !reflect.DeepEqual(r.Outputs, w.Outputs) || r.Steps != w.Steps ||
				!reflect.DeepEqual(r.StepsBy, w.StepsBy) || len(r.Violations) != len(w.Violations) ||
				r.Exhausted != w.Exhausted || !reflect.DeepEqual(traceSeqs(r), traceSeqs(w)) {
				t.Errorf("%s run %d differs from the uncached run", name, i+1)
			}
		}
		if w.CacheMeasured || !second[name].CacheMeasured {
			t.Errorf("%s: CacheMeasured %v uncached, %v cached", name, w.CacheMeasured, second[name].CacheMeasured)
		}
	}
	ref1, ref2 := first["g_0001"], second["g_0001"]
	if ref2.CacheHits <= ref1.CacheHits || ref2.PureExecuted >= ref1.PureExecuted {
		t.Errorf("a committed generation did not raise hits: first %d/%d, second %d/%d",
			ref1.CacheHits, ref1.PureExecuted, ref2.CacheHits, ref2.PureExecuted)
	}
}

func traceSeqs(r Result) []string {
	var out []string
	for _, tr := range r.Traces {
		out = append(out, fmt.Sprint(tr.Value.Name(), tr.Seqs, tr.Truncated))
	}
	return out
}

// TestGiriCacheDeterministicUnderConcurrency: hits depend on the committed
// cache and the Run itself, not on which concurrent Runs stored first.
func TestGiriCacheDeterministicUnderConcurrency(t *testing.T) {
	hits := func() string {
		g := NewGiri(Config{})
		p := buildGen(t, g)
		var names []string
		for n := range p.Funcs() {
			names = append(names, n)
		}
		slices.Sort(names)
		var out string
		for gen := range 3 {
			const copies = 4
			res := make([][copies]Result, len(names))
			var wg sync.WaitGroup
			for i, n := range names {
				for c := range copies {
					wg.Add(1)
					go func() {
						defer wg.Done()
						r, err := g.Run(p.Funcs()[n], testInputs, RunOptions{MaxSteps: 100000})
						if err != nil {
							t.Error(err)
						}
						res[i][c] = r
					}()
				}
			}
			wg.Wait()
			g.EndGeneration()
			for i, n := range names {
				for c := 1; c < copies; c++ {
					if res[i][c].CacheHits != res[i][0].CacheHits {
						t.Errorf("gen %d %s: concurrent copies saw %d and %d hits", gen, n, res[i][0].CacheHits, res[i][c].CacheHits)
					}
				}
				out += fmt.Sprintf("gen %d %s %d/%d; ", gen, n, res[i][0].CacheHits, res[i][0].PureExecuted)
			}
			out += fmt.Sprintf("entries %d\n", g.Cache().Len())
		}
		return out
	}
	a, b := hits(), hits()
	if a != b {
		t.Fatalf("cache readings depend on scheduling:\n%s\nvs\n%s", a, b)
	}
}
