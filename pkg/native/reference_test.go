// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

//go:build native

package native

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReferenceInterval runs the full native stage once on the reference
// (go test -tags native ./pkg/native) and checks that every benchmark got
// an interval: -count=10, benchstat's distribution-free 95% interval.
func TestReferenceInterval(t *testing.T) {
	k := popcountKernel(t)
	src, err := os.ReadFile(filepath.Join(k.Dir, k.RefFile))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.BenchstatDir, _ = filepath.Abs("../..")
	res, err := RunSource(k, cfg, src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified {
		t.Fatalf("reference failed its own tests:\n%s", res.TestOutput)
	}
	if len(res.Rows) != 3 || res.Table == "" {
		t.Fatalf("rows %+v, table %q", res.Rows, res.Table)
	}
	for _, r := range res.Rows {
		if r.N != cfg.Count || !(r.Lo <= r.NsOp && r.NsOp <= r.Hi) || r.Lo == r.Hi || r.Warned {
			t.Errorf("%s: no interval: %+v", r.Name, r)
		}
	}
	t.Logf("benchstat table (plumbing check, not a reported number):\n%s", res.Table)
}
