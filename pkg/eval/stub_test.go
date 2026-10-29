// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"os/exec"
	"path/filepath"
	"testing"
)

const genSrc = `package g

import "math/bits"

func g_0001(xs []uint64) int {
	v0 := 0
	for i0 := 0; i0 < len(xs); i0++ {
		v1 := xs[i0]
		v2 := bits.OnesCount64(v1)
		v0 = v0 + v2
	}
	return v0
}

func g_0002(xs []uint64) int {
	v0 := xs[3]
	v1 := int(v0)
	return v1
}

func g_0003(xs []uint64) int {
	v0 := 0
	for i0 := 0; i0 < 1000000000; i0++ {
		v0 = v0 + i0
	}
	return v0
}

func g_0004(xs []uint64) int {
	v0 := len(xs)
	return v0
}

func g_0005(xs []uint64) int {
	for i0 := 0; i0 < len(xs); i0++ {
		xs[i0] = 0
	}
	v0 := len(xs)
	return v0
}
`

func newTestStub(t *testing.T) *Stub {
	t.Helper()
	if _, err := exec.LookPath("giri"); err != nil {
		t.Skip("giri not on PATH")
	}
	s, err := NewStub("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var testInputs = []Input{
	{Args: []any{[]uint64{}}},
	{Args: []any{[]uint64{1, 3, 7}}},
	{Args: []any{[]uint64{^uint64(0), 0, 0, 5}}},
}

func runAll(t *testing.T, s *Stub) map[string]Result {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "gen")
	if err := WriteGeneration(dir, []byte(genSrc)); err != nil {
		t.Fatal(err)
	}
	p, err := s.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Release(p); err != nil {
			t.Error(err)
		}
	}()
	out := map[string]Result{}
	for name, fn := range p.Funcs() {
		r, err := s.Run(fn, testInputs, RunOptions{MaxSteps: 100000})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = r
	}
	return out
}

func TestStub(t *testing.T) {
	s := newTestStub(t)
	rs := runAll(t, s)

	ref := rs["g_0001"]
	want := []int{0, 6, 66}
	for i, w := range want {
		if len(ref.Outputs[i]) != 1 || ref.Outputs[i][0] != w {
			t.Errorf("reference genome on vector %d = %v, want %d", i, ref.Outputs[i], w)
		}
	}
	if ref.Steps == 0 || ref.Exhausted || len(ref.Violations) != 0 || len(ref.Traces) != 0 {
		t.Errorf("reference genome: %+v", ref)
	}

	oob := rs["g_0002"]
	if len(oob.Violations) == 0 {
		t.Errorf("out-of-bounds genome has no violations: %+v", oob)
	}
	native, giri := false, false
	for _, v := range oob.Violations {
		native = native || v.Category == "native-panic"
		giri = giri || v.Category == "out-of-bounds"
		if v.Input != 0 && v.Input != 1 {
			t.Errorf("violation attributed to vector %d: %+v", v.Input, v)
		}
	}
	if !native || !giri {
		t.Errorf("want both a native panic and a giri finding: %+v", oob.Violations)
	}
	if oob.Outputs[2][0] != 5 {
		t.Errorf("xs[3] of vector 2 = %v natively, want 5", oob.Outputs[2])
	}

	if loop := rs["g_0003"]; !loop.Exhausted {
		t.Errorf("billion-iteration loop not exhausted: %+v", loop)
	}

	// len costs more than returning a constant, and the store loop more
	// than len alone; the baseline is subtracted, so steps are small.
	if l, st := rs["g_0004"].Steps, rs["g_0005"].Steps; l == 0 || st <= l || l > 50 {
		t.Errorf("steps: len %d, store loop %d", l, st)
	}
	// The store genome mutates its input; the harness copies inputs, so
	// later genomes are unaffected (g_0001 ran on the originals).
	if rs["g_0005"].Outputs[1][0] != 3 {
		t.Errorf("store genome output %v", rs["g_0005"].Outputs[1])
	}
}

func TestStubDeterministic(t *testing.T) {
	s := newTestStub(t)
	a, b := runAll(t, s), runAll(t, s)
	for name := range a {
		if a[name].Steps != b[name].Steps || a[name].Exhausted != b[name].Exhausted ||
			len(a[name].Violations) != len(b[name].Violations) {
			t.Errorf("%s differs between runs: %+v vs %+v", name, a[name], b[name])
		}
	}
}

func TestCheckExperimentRefusesStub(t *testing.T) {
	s := &Stub{}
	if CheckExperiment(s) == nil {
		t.Fatal("experiments accepted the stub")
	}
	if _, err := New(Config{Name: "giri"}, t.TempDir()); err == nil {
		t.Error("giri evaluator available without -tags giriapi")
	}
}
