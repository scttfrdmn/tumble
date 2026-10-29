// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	for _, p := range []string{"testdata/smoke.json", "../../experiments/e0/config.json"} {
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
