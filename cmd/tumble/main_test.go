// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scttfrdmn/tumble/pkg/report"
)

func TestRunReportInspect(t *testing.T) {
	if _, err := exec.LookPath("giri"); err != nil {
		t.Skip("giri not on PATH")
	}
	cfg := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(cfg, []byte(`{"params": {"generations": 2, "islands": 1, "population": 4, "migrants": 0, "workers": 4}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "rec")
	var buf bytes.Buffer
	if err := runCmd([]string{"--config", cfg, "--out", out, "--root", "../..", "--no-native", "--work", t.TempDir()}, &buf); err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	for _, f := range []string{"record.json", "generations.jsonl", "archive/gen0000.json", "archive/gen0001.json", "best.go"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("record is missing %s", f)
		}
	}
	buf.Reset()
	if err := reportCmd([]string{out}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not an instrument") {
		t.Errorf("report does not flag the stub:\n%s", buf.String())
	}
	// The last generation breeds nothing: its yields are not measured.
	table, _, _ := strings.Cut(buf.String(), "\nover the run:")
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "n/m/n/m/n/m") {
		t.Errorf("last generation's yields shown as rates: %q", last)
	}
	if !strings.Contains(buf.String(), "fp collisions") {
		t.Errorf("report has no run summary:\n%s", buf.String())
	}
	buf.Reset()
	if err := inspectCmd([]string{out}, &buf); err != nil {
		t.Fatalf("inspect: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "type-check: ok") || !strings.Contains(buf.String(), "func Best(xs []uint64) int") {
		t.Errorf("inspect output:\n%s", buf.String())
	}
}

func TestRunRefusesStubUnderExperiments(t *testing.T) {
	if _, err := exec.LookPath("giri"); err != nil {
		t.Skip("giri not on PATH")
	}
	out := filepath.Join(t.TempDir(), "experiments", "e0", "x")
	err := runCmd([]string{"--out", out, "--root", "../..", "--work", t.TempDir()}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not an instrument") {
		t.Fatalf("run under experiments/ with the stub: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a record was started under experiments/")
	}
}

func TestConfigsParse(t *testing.T) {
	cfgs, _ := filepath.Glob("../../experiments/*/*.json")
	for _, p := range append([]string{"testdata/smoke.json", "testdata/determinism-giri.json"}, cfgs...) {
		cfg, err := loadConfig(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if err := cfg.Params.Check(); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestRunGiriUnderExperiments(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(cfg, []byte(`{"evaluator": {"name": "giri"}, "params": {"generations": 2, "islands": 2, "population": 8, "migrate_every": 1, "migrants": 1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "experiments", "e0", "x")
	var buf bytes.Buffer
	if err := runCmd([]string{"--config", cfg, "--out", out, "--root", "../..", "--no-native", "--work", t.TempDir()}, &buf); err != nil {
		t.Fatalf("giri run under experiments/: %v\n%s", err, buf.String())
	}
	l, err := report.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Record.Instrument || l.Record.Evaluator != "giri" || len(l.Record.Notes) != 0 {
		t.Errorf("record: evaluator %s instrument %v notes %v", l.Record.Evaluator, l.Record.Instrument, l.Record.Notes)
	}
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var want string
	for _, line := range strings.Split(string(mod), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "github.com/scttfrdmn/giri" {
			want = f[1]
		}
	}
	if want == "" || l.Record.Versions.GiriModule != want {
		t.Errorf("giri module version %q recorded, go.mod requires %q", l.Record.Versions.GiriModule, want)
	}
	var rd struct {
		VCMeasured bool `json:"vc_measured"`
		IndexSize  int  `json:"index_size"`
	}
	if err := json.Unmarshal(l.Generations[len(l.Generations)-1], &rd); err != nil {
		t.Fatal(err)
	}
	if !rd.VCMeasured || rd.IndexSize == 0 {
		t.Errorf("last generation: vc_measured %v, index_size %d", rd.VCMeasured, rd.IndexSize)
	}
}

func TestVerifyAndBaseline(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(cfg, []byte(`{"evaluator": {"name": "giri"}, "params": {"generations": 2, "islands": 1, "population": 8, "migrants": 0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(t.TempDir(), "rec")
	if err := runCmd([]string{"--config", cfg, "--out", rec, "--root", "../..", "--no-native", "--work", t.TempDir()}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := verifyCmd([]string{"--root", "../..", "--fuzztime", "2s", rec}, &buf); err != nil {
		t.Fatalf("verify: %v\n%s", err, buf.String())
	}
	var v Verification
	b, err := os.ReadFile(filepath.Join(rec, VerifyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	// The best genome of a reference-seeded run is fully correct.
	if !v.Verified || v.SelCorrect != v.SelTotal || v.HoldCorrect != v.HoldTotal || v.HoldTotal == 0 || !v.Fuzz.Passed {
		t.Errorf("verification: %+v", v)
	}
	if err := verifyCmd([]string{"--root", "../..", rec}, &buf); err == nil {
		t.Error("verify overwrote an existing verification")
	}

	out := filepath.Join(t.TempDir(), "base")
	if err := baselineCmd([]string{"--kernel", "argmax", "--root", "../..", "--out", out}, &buf); err != nil {
		t.Fatal(err)
	}
	var bl Baseline
	b, _ = os.ReadFile(filepath.Join(out, "baseline.json"))
	if err := json.Unmarshal(b, &bl); err != nil {
		t.Fatal(err)
	}
	if bl.Kernel != "argmax" || bl.Result == nil || len(bl.Result.Rows) != 3 || bl.Result.Table == "" {
		t.Fatalf("baseline: %+v", bl)
	}
	for _, r := range bl.Result.Rows {
		if r.NoInterval || r.N != 10 {
			t.Errorf("baseline row without an interval: %+v", r)
		}
	}
}
