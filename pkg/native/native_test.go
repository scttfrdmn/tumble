// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
