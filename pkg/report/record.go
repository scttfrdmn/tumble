// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/native"
	"github.com/scttfrdmn/tumble/pkg/pop"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// The files of a run record directory.
const (
	RecordFile      = "record.json"       // Record
	GenerationsFile = "generations.jsonl" // one pop.Readings per line
	TimingFile      = "timing.jsonl"      // wall time per generation, kept apart so the rest is reproducible
	NativeFile      = "native.jsonl"      // one NativeEntry per line
	ArchiveDir      = "archive"           // genNNNN.json: the archive after each generation
	BestFile        = "best.go"           // the final best genome as Go source
)

// Record is a run's description (SETUP.md, "Measurement discipline").
type Record struct {
	Kernel     string `json:"kernel"`
	Arm        string `json:"arm"`
	Evaluator  string `json:"evaluator"`
	Instrument bool   `json:"instrument"`
	// Notes say what the readings are not: under a non-instrument
	// evaluator, everything here is a plumbing check.
	Notes []string `json:"notes,omitempty"`

	Seed      uint64 `json:"seed"`
	WallClock string `json:"wall_clock_budget"` // "" for none
	Started   string `json:"started"`
	Finished  string `json:"finished,omitempty"`
	Gens      int    `json:"generations_completed"`

	Hardware Hardware `json:"hardware"`
	Versions Versions `json:"versions"`

	Params  pop.Params       `json:"params"`
	Grammar *grammar.Grammar `json:"grammar"`
	Native  NativeSettings   `json:"native"`
	Vectors VectorCounts     `json:"vectors"`
}

// NativeSettings record whether and how the native stage ran.
type NativeSettings struct {
	Enabled bool          `json:"enabled"`
	Config  native.Config `json:"config"`
}

// VectorCounts are the selection (N) and holdout (M) vector counts.
type VectorCounts struct {
	Selection int `json:"selection"`
	Holdout   int `json:"holdout"`
}

// ArchiveEntry is one archive cell in a snapshot.
type ArchiveEntry struct {
	Cell     pop.Cell   `json:"cell"`
	ID       uint64     `json:"id"`
	Island   int        `json:"island"`
	Gen      int        `json:"gen"`
	Origin   pop.Origin `json:"origin"`
	Correct  int        `json:"correct"`
	Vectors  int        `json:"vectors"`
	Cost     uint64     `json:"cost"`
	Novelty  int        `json:"novelty"`
	Len      int        `json:"len"`
	Identity string     `json:"identity"`
	Source   string     `json:"source"`
}

// NativeEntry is one genome's native-stage result.
type NativeEntry struct {
	Gen      int            `json:"gen"`
	ID       uint64         `json:"id"`
	Identity string         `json:"identity"`
	Cost     uint64         `json:"cost"`
	Result   *native.Result `json:"result"`
}

// Entries snapshots an archive in descriptor order.
func Entries(a *pop.Archive) []ArchiveEntry {
	var out []ArchiveEntry
	for _, e := range a.Elites() {
		out = append(out, entry(e.Cell, e.Ind))
	}
	return out
}

func entry(c pop.Cell, ind *pop.Individual) ArchiveEntry {
	id := ind.F.Identity()
	return ArchiveEntry{
		Cell: c, ID: ind.ID, Island: ind.Island, Gen: ind.Gen, Origin: ind.Origin,
		Correct: ind.Correct, Vectors: len(ind.C), Cost: ind.Cost, Novelty: ind.Novelty,
		Len: ind.F.Len(), Identity: hex.EncodeToString(id[:]), Source: string(ind.F.Print("f")),
	}
}

// Writer writes a run record directory as the run proceeds.
type Writer struct {
	dir    string
	rec    *Record
	gens   *os.File
	timing *os.File
	native *os.File
	last   time.Time
}

// Create starts a record in dir, which must not already hold one.
func Create(dir string, rec *Record) (*Writer, error) {
	if _, err := os.Stat(filepath.Join(dir, RecordFile)); err == nil {
		return nil, fmt.Errorf("report: %s already holds a run record", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, ArchiveDir), 0o755); err != nil {
		return nil, err
	}
	w := &Writer{dir: dir, rec: rec, last: time.Now()}
	rec.Started = w.last.UTC().Format(time.RFC3339)
	var err error
	for _, f := range []struct {
		name string
		p    **os.File
	}{{GenerationsFile, &w.gens}, {TimingFile, &w.timing}, {NativeFile, &w.native}} {
		if *f.p, err = os.Create(filepath.Join(dir, f.name)); err != nil {
			return nil, err
		}
	}
	return w, w.writeRecord()
}

func (w *Writer) writeRecord() error {
	b, err := json.MarshalIndent(w.rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, RecordFile), append(b, '\n'), 0o644)
}

func writeLine(f *os.File, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// TimingEntry is one generation's line in TimingFile: wall seconds since
// the previous generation's line, the search's phases, and the native stage.
type TimingEntry struct {
	Gen     int        `json:"gen"`
	Seconds float64    `json:"seconds"`
	Phases  pop.Timing `json:"phases"`
	Native  float64    `json:"native"`
}

// Generation records one generation's readings, the archive after it, and
// its timing (nativeSeconds: the native stage's wall time for it).
func (w *Writer) Generation(rd *pop.Readings, a *pop.Archive, nativeSeconds float64) error {
	if err := writeLine(w.gens, rd); err != nil {
		return err
	}
	now := time.Now()
	if err := writeLine(w.timing, TimingEntry{Gen: rd.Gen, Seconds: now.Sub(w.last).Seconds(), Phases: rd.Timing, Native: nativeSeconds}); err != nil {
		return err
	}
	w.last = now
	b, err := json.MarshalIndent(Entries(a), "", "  ")
	if err != nil {
		return err
	}
	w.rec.Gens = rd.Gen + 1
	return os.WriteFile(filepath.Join(w.dir, ArchiveDir, fmt.Sprintf("gen%04d.json", rd.Gen)), append(b, '\n'), 0o644)
}

// Native records a native-stage result.
func (w *Writer) Native(e NativeEntry) error { return writeLine(w.native, e) }

// Close writes the best genome and the finished record.
func (w *Writer) Close(best *pop.Individual) error {
	if best != nil {
		src := tir.PrintFile("best", []tir.Named{{Name: "Best", F: best.F}})
		if err := os.WriteFile(filepath.Join(w.dir, BestFile), src, 0o644); err != nil {
			return err
		}
	}
	w.rec.Finished = time.Now().UTC().Format(time.RFC3339)
	return errors.Join(w.writeRecord(), w.gens.Close(), w.timing.Close(), w.native.Close())
}

// Loaded is a run record read back.
type Loaded struct {
	Record      Record
	Generations []json.RawMessage
	Archive     []ArchiveEntry // the last generation's
	ArchiveGen  int
	Native      []NativeEntry
	Best        []byte
}

// Load reads the record in dir.
func Load(dir string) (*Loaded, error) {
	l := &Loaded{}
	b, err := os.ReadFile(filepath.Join(dir, RecordFile))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &l.Record); err != nil {
		return nil, err
	}
	if l.Generations, err = readLines(filepath.Join(dir, GenerationsFile)); err != nil {
		return nil, err
	}
	natives, err := readLines(filepath.Join(dir, NativeFile))
	if err != nil {
		return nil, err
	}
	for _, n := range natives {
		var e NativeEntry
		if err := json.Unmarshal(n, &e); err != nil {
			return nil, err
		}
		l.Native = append(l.Native, e)
	}
	snaps, _ := filepath.Glob(filepath.Join(dir, ArchiveDir, "gen*.json"))
	slices.Sort(snaps)
	if len(snaps) > 0 {
		last := snaps[len(snaps)-1]
		if _, err := fmt.Sscanf(filepath.Base(last), "gen%04d.json", &l.ArchiveGen); err != nil {
			return nil, fmt.Errorf("report: archive snapshot %s: %w", last, err)
		}
		b, err := os.ReadFile(last)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &l.Archive); err != nil {
			return nil, err
		}
	}
	l.Best, _ = os.ReadFile(filepath.Join(dir, BestFile))
	return l, nil
}

func readLines(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	var out []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		out = append(out, json.RawMessage(append([]byte(nil), sc.Bytes()...)))
	}
	return out, sc.Err()
}
