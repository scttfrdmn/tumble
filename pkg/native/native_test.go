// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

func popcountKernel(t *testing.T) Kernel {
	t.Helper()
	dir, err := filepath.Abs("../../kernels/popcount")
	if err != nil {
		t.Fatal(err)
	}
	return Kernel{Name: "popcount", Dir: dir, Package: "popcount", Func: "Popcount", RefFile: "popcount.go"}
}

func TestSummarize(t *testing.T) {
	var b strings.Builder
	b.WriteString("goos: darwin\ngoarch: arm64\npkg: x\n")
	for _, v := range []string{"5.0", "5.1", "4.9", "5.2", "5.0", "5.05", "4.95", "5.1", "5.0", "4.98"} {
		b.WriteString("BenchmarkPopcount/16-8 \t 1000000\t " + v + " ns/op\n")
	}
	rows, err := summarize([]byte(b.String()), 0.95)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].N != 10 {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if !(r.Lo < r.NsOp && r.NsOp < r.Hi) || r.NsOp < 4.9 || r.NsOp > 5.1 {
		t.Errorf("interval %v [%v, %v]", r.NsOp, r.Lo, r.Hi)
	}
}

func TestSummarizeTooFewSamples(t *testing.T) {
	raw := "BenchmarkPopcount/16-8 \t 1000000\t 5.0 ns/op\nBenchmarkPopcount/16-8 \t 1000000\t 5.1 ns/op\nBenchmarkPopcount/16-8 \t 1000000\t 4.9 ns/op\n"
	rows, err := summarize([]byte(raw), 0.95)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if !r.NoInterval || r.Lo != 0 || r.Hi != 0 {
		t.Errorf("3 samples at 95%%: %+v", r)
	}
	if _, err := json.Marshal(rows); err != nil {
		t.Errorf("row does not marshal: %v", err)
	}
}

// TestVetStyleNotACorrectnessFailure: a correct genome carrying neutral
// redundancy that go test's default vet rejects (x || x) is verified.
func TestVetStyleNotACorrectnessFailure(t *testing.T) {
	src := []byte(`package popcount

import "math/bits"

func Popcount(xs []uint64) int {
	n := 0
	for _, x := range xs {
		b := x > 3
		c := b || b
		_ = c
		n += bits.OnesCount64(x)
	}
	return n
}
`)
	cfg := Defaults()
	cfg.Count, cfg.Benchtime, cfg.Benchstat = 1, "1x", nil
	res, err := RunSource(popcountKernel(t), cfg, src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified {
		t.Errorf("correct genome with x || x not verified:\n%s", res.TestOutput)
	}
}

func TestWrongGenomeNotVerified(t *testing.T) {
	src := []byte("package popcount\n\nfunc Popcount(xs []uint64) int {\n\treturn len(xs)\n}\n")
	res, err := RunSource(popcountKernel(t), Defaults(), src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.Verified || res.Raw != "" {
		t.Errorf("a wrong genome was verified or benchmarked: %+v", res)
	}
}

func TestRunPrintsGenomeAsReference(t *testing.T) {
	k := popcountKernel(t)
	src, err := os.ReadFile(filepath.Join(k.Dir, k.RefFile))
	if err != nil {
		t.Fatal(err)
	}
	f, err := tir.Parse(src, k.Func)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.Count, cfg.Benchtime, cfg.Benchstat = 1, "1x", nil
	res, err := Run(k, cfg, f, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified || len(res.Rows) != 3 {
		t.Errorf("parsed reference through the stage: verified %v, %d rows\n%s", res.Verified, len(res.Rows), res.TestOutput)
	}
}

func TestFuzz(t *testing.T) {
	k := popcountKernel(t)
	cfg := Defaults()
	ref, err := tir.Parse(mustRead(t, filepath.Join(k.Dir, k.RefFile)), k.Func)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Fuzz(k, cfg, ref, t.TempDir(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Execs == 0 {
		t.Errorf("reference fuzz: passed %v, execs %d\n%s", res.Passed, res.Execs, res.Output)
	}
	// A genome that is right on the committed vectors' shapes but wrong in
	// general: it ignores every word past the first.
	wrong, err := tir.Parse([]byte("package popcount\n\nimport \"math/bits\"\n\nfunc Popcount(xs []uint64) int {\n\tif len(xs) == 0 {\n\t\treturn 0\n\t}\n\treturn bits.OnesCount64(xs[0])\n}\n"), "Popcount")
	if err != nil {
		t.Fatal(err)
	}
	res, err = Fuzz(k, cfg, wrong, t.TempDir(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Error("fuzzing did not catch a genome that reads only the first word")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
