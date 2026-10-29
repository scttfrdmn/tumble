// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/ssa"
)

// DefaultMaxSteps is the step budget when RunOptions.MaxSteps is 0.
const DefaultMaxSteps = 1 << 20

// Stub is the scaffolding evaluator (SETUP.md, "Evaluator interface"). It is
// not an instrument and no experiment runs against it.
//
// giri's CLI exposes violations and step-limit exhaustion, but no step count
// and no program output, and today it does not carry element values through
// memory (scttfrdmn/giri#248). So the stub takes from giri what giri can
// give and gets outputs elsewhere:
//
//   - Violations and Exhausted come from giri, run once per function under a
//     driver main that calls it on each input vector.
//   - Steps comes from giri by searching -max-steps for the smallest budget
//     the driver runs within, less the same search on a driver calling a
//     zero-returning function of the same signature. StepsBy is nil.
//   - Outputs come from a native harness: the generation compiled once with
//     fuel-limited loops, every function run on fresh copies of the vectors.
//     A native panic is a violation; running out of fuel is exhaustion.
//
// Traces are always empty.
type Stub struct {
	Giri    string        // the giri binary
	Work    string        // scratch directory for harnesses and drivers
	Timeout time.Duration // per subprocess

	mu    sync.Mutex
	progs map[*ssa.Package]*stubProgram
	seq   int
}

// NewStub returns a stub using the giri binary at giri ("" looks it up on
// PATH) and scratch space under work.
func NewStub(giri, work string) (*Stub, error) {
	if giri == "" {
		p, err := exec.LookPath("giri")
		if err != nil {
			return nil, fmt.Errorf("eval: giri not found on PATH (GOEXPERIMENT=arenas go install ./cmd/giri in a giri checkout): %w", err)
		}
		giri = p
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	return &Stub{Giri: giri, Work: work, Timeout: 2 * time.Minute, progs: map[*ssa.Package]*stubProgram{}}, nil
}

// IsInstrument reports false: the stub's readings are not measurements.
func (*Stub) IsInstrument() bool { return false }

// Name is the evaluator's name in run records.
func (*Stub) Name() string { return "stub" }

type stubProgram struct {
	*ssaProgram
	dir string // this program's scratch directory
	src []byte // the generation source

	mu     sync.Mutex
	inputs map[[32]byte]*inputState
}

// inputState is everything computed once per (program, input set).
type inputState struct {
	once     sync.Once
	err      error
	native   map[string][]nativeOut
	baseline uint64
	shape    sigShape
}

type nativeOut struct {
	Out   []string `json:"out"`
	Fault string   `json:"fault,omitempty"`
}

// Build loads the generation package in pkgDir.
func (s *Stub) Build(pkgDir string) (Program, error) {
	p, err := loadSSA(pkgDir)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(filepath.Join(pkgDir, GenFile))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.seq++
	dir := filepath.Join(s.Work, fmt.Sprintf("p%04d", s.seq))
	sp := &stubProgram{ssaProgram: p, dir: dir, src: src, inputs: map[[32]byte]*inputState{}}
	s.progs[p.pkg] = sp
	s.mu.Unlock()
	return sp, nil
}

// Release drops a program's state and scratch files.
func (s *Stub) Release(p Program) error {
	sp, ok := p.(*stubProgram)
	if !ok {
		return nil
	}
	s.mu.Lock()
	delete(s.progs, sp.pkg)
	s.mu.Unlock()
	return os.RemoveAll(sp.dir)
}

// Run evaluates fn, a function of a program from Build, on in.
func (s *Stub) Run(fn *ssa.Function, in []Input, opt RunOptions) (Result, error) {
	s.mu.Lock()
	sp, ok := s.progs[fn.Pkg]
	s.mu.Unlock()
	if !ok {
		return Result{}, fmt.Errorf("eval: %s is not from a program this stub built", fn.Name())
	}
	budget := opt.MaxSteps
	if budget == 0 {
		budget = DefaultMaxSteps
	}
	st, key, err := sp.prepare(s, in, budget)
	if err != nil {
		return Result{}, err
	}

	res := Result{Outputs: make([][]any, len(in))}
	outs, ok := st.native[fn.Name()]
	if !ok {
		return Result{}, fmt.Errorf("eval: %s has no harness entry (signature differs from the generation's)", fn.Name())
	}
	for i, o := range outs {
		switch {
		case o.Fault == "fuel":
			res.Exhausted, res.Partial = true, true
		case o.Fault != "":
			res.Partial = true
			res.Violations = append(res.Violations, Violation{Category: "native-panic", Message: o.Fault, Input: i})
		default:
			vals, err := parseOutputs(o.Out, fn.Signature.Results())
			if err != nil {
				return Result{}, fmt.Errorf("eval: %s: %w", fn.Name(), err)
			}
			res.Outputs[i] = vals
		}
	}

	dir := filepath.Join(sp.dir, fmt.Sprintf("drv-%x", key[:6]), fn.Name())
	drv := driverMain(fn.Name(), in)
	gen, err := rewriteGen(sp.src, false)
	if err != nil {
		return Result{}, err
	}
	if err := writeModule(dir, "tumble.gen/drv", map[string][]byte{"main.go": drv, GenFile: gen}); err != nil {
		return Result{}, err
	}
	steps, exhausted, findings, err := s.steps(dir, budget+st.baseline)
	if err != nil {
		return Result{}, fmt.Errorf("eval: %s: %w", fn.Name(), err)
	}
	res.Steps = max(steps, st.baseline) - st.baseline
	res.Exhausted = res.Exhausted || exhausted
	for _, f := range findings {
		res.Violations = append(res.Violations, Violation{Category: f.Category, Message: f.Message, Input: vectorOf(f.StackTrace)})
	}
	return res, nil
}

// prepare builds and runs the native harness and measures the driver
// baseline, once per input set.
func (sp *stubProgram) prepare(s *Stub, in []Input, budget uint64) (*inputState, [32]byte, error) {
	enc, err := json.Marshal(struct {
		In     []Input
		Budget uint64
	}{in, budget})
	if err != nil {
		return nil, [32]byte{}, err
	}
	key := sha256.Sum256(enc)
	sp.mu.Lock()
	st, ok := sp.inputs[key]
	if !ok {
		st = &inputState{}
		sp.inputs[key] = st
	}
	sp.mu.Unlock()
	st.once.Do(func() { st.err = st.init(s, sp, in, budget, key) })
	return st, key, st.err
}

func (st *inputState) init(s *Stub, sp *stubProgram, in []Input, budget uint64, key [32]byte) error {
	// One signature per generation: every function sharing the first's
	// shape (in name order) goes in the harness.
	var names []string
	for name := range sp.funcs {
		names = append(names, name)
	}
	slices.Sort(names)
	var shaped []string
	for _, name := range names {
		sh, err := shapeOf(sp.funcs[name])
		if err != nil {
			continue
		}
		if len(shaped) == 0 {
			st.shape = sh
		}
		if sh.equal(st.shape) {
			shaped = append(shaped, name)
		}
	}
	if len(shaped) == 0 {
		return errors.New("eval: no function in the generation has a supported signature")
	}

	// Native harness.
	hdir := filepath.Join(sp.dir, fmt.Sprintf("native-%x", key[:6]))
	gen, err := rewriteGen(sp.src, true)
	if err != nil {
		return err
	}
	if err := writeModule(hdir, "tumble.gen/native", map[string][]byte{
		"main.go": harnessMain(st.shape, shaped), GenFile: gen,
	}); err != nil {
		return err
	}
	bin := filepath.Join(hdir, "harness")
	if _, errb, err := s.run(hdir, nil, "go", "build", "-o", bin, "."); err != nil {
		return fmt.Errorf("eval: building native harness: %v\n%s", err, errb)
	}
	var vin struct {
		Fuel    int64            `json:"fuel"`
		Vectors []map[string]any `json:"vectors"`
	}
	vin.Fuel = int64(budget)
	for _, v := range in {
		m := map[string]any{}
		for i, a := range v.Args {
			m[fmt.Sprintf("a%d", i)] = a
		}
		vin.Vectors = append(vin.Vectors, m)
	}
	stdin, _ := json.Marshal(vin)
	stdout, errb, err := s.run(hdir, stdin, bin)
	if err != nil {
		return fmt.Errorf("eval: native harness: %v\n%s", err, errb)
	}
	if err := json.Unmarshal(stdout, &st.native); err != nil {
		return fmt.Errorf("eval: native harness output: %w", err)
	}

	// Baseline: the driver's own steps, calling a zero-returning function.
	bdir := filepath.Join(sp.dir, fmt.Sprintf("base-%x", key[:6]))
	drv := driverMain("tumbleBase", in)
	if err := writeModule(bdir, "tumble.gen/base", map[string][]byte{
		"main.go": drv, "base.go": baselineFunc("tumbleBase", st.shape),
	}); err != nil {
		return err
	}
	steps, exhausted, _, err := s.steps(bdir, budget)
	if err != nil {
		return fmt.Errorf("eval: baseline: %w", err)
	}
	if exhausted {
		return errors.New("eval: the driver alone exhausts the step budget")
	}
	st.baseline = steps
	return nil
}

// run runs a subprocess in dir under the stub's timeout, with stdin, and
// returns its stdout and stderr.
func (s *Stub) run(dir string, stdin []byte, name string, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "CGO_ENABLED=0", "GOMEMLIMIT=1GiB")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// finding is one entry of giri's JSON report.
type finding struct {
	Category   string `json:"category"`
	Message    string `json:"message"`
	StackTrace string `json:"stack_trace"`
}

// probe runs giri on the driver in dir with a step budget and reports
// whether the budget was exceeded and the other findings.
func (s *Stub) probe(dir string, maxSteps uint64) (exhausted bool, findings []finding, err error) {
	out, stderr, err := s.run(dir, nil, s.Giri, "-no-cache", "-max-goroutines", "1",
		"-max-steps", strconv.FormatUint(maxSteps, 10), "-format", "json", ".")
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) {
		return false, nil, fmt.Errorf("giri: %v\n%s", err, stderr)
	}
	var rep struct {
		Findings []finding `json:"findings"`
	}
	if err := json.Unmarshal(out, &rep); err != nil {
		return false, nil, fmt.Errorf("giri output: %v\n%s%s", err, out, stderr)
	}
	for _, f := range rep.Findings {
		if strings.HasPrefix(f.Message, "execution limit of ") {
			exhausted = true
			continue
		}
		findings = append(findings, f)
	}
	return exhausted, findings, nil
}

// steps finds the driver's step count: the smallest budget it runs within,
// by doubling from 1024 and then bisecting. If it exceeds limit, it reports
// exhaustion with steps = limit. Findings are from the run at the found
// budget (or at limit).
func (s *Stub) steps(dir string, limit uint64) (steps uint64, exhausted bool, findings []finding, err error) {
	lo, hi := uint64(0), min(uint64(1024), limit) // lo exhausts, hi is the probe
	for {
		ex, fs, err := s.probe(dir, hi)
		if err != nil {
			return 0, false, nil, err
		}
		if !ex {
			findings = fs
			break
		}
		if hi == limit {
			return limit, true, fs, nil
		}
		lo, hi = hi, min(2*hi, limit)
	}
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		ex, fs, err := s.probe(dir, mid)
		if err != nil {
			return 0, false, nil, err
		}
		if ex {
			lo = mid
		} else {
			hi, findings = mid, fs
		}
	}
	return hi, false, findings, nil
}

var driverFrame = regexp.MustCompile(`\.tumbleV(\d+)\b`)

// vectorOf attributes a finding to the input vector whose driver function
// appears in its stack trace, or -1.
func vectorOf(stack string) int {
	if m := driverFrame.FindStringSubmatch(stack); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return -1
}

// parseOutputs converts the harness's printed results back to Go values.
func parseOutputs(out []string, results *types.Tuple) ([]any, error) {
	if len(out) != results.Len() {
		return nil, fmt.Errorf("%d outputs for %d results", len(out), results.Len())
	}
	vals := make([]any, len(out))
	for i, s := range out {
		v, err := parseValue(s, results.At(i).Type())
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	return vals, nil
}

func parseValue(s string, t types.Type) (any, error) {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return nil, fmt.Errorf("result type %s", t)
	}
	switch b.Kind() {
	case types.Bool:
		return strconv.ParseBool(s)
	case types.Int:
		v, err := strconv.ParseInt(s, 10, 64)
		return int(v), err
	case types.Int8:
		v, err := strconv.ParseInt(s, 10, 8)
		return int8(v), err
	case types.Int16:
		v, err := strconv.ParseInt(s, 10, 16)
		return int16(v), err
	case types.Int32:
		v, err := strconv.ParseInt(s, 10, 32)
		return int32(v), err
	case types.Int64:
		return strconv.ParseInt(s, 10, 64)
	case types.Uint:
		v, err := strconv.ParseUint(s, 10, 64)
		return uint(v), err
	case types.Uint8:
		v, err := strconv.ParseUint(s, 10, 8)
		return uint8(v), err
	case types.Uint16:
		v, err := strconv.ParseUint(s, 10, 16)
		return uint16(v), err
	case types.Uint32:
		v, err := strconv.ParseUint(s, 10, 32)
		return uint32(v), err
	case types.Uint64:
		return strconv.ParseUint(s, 10, 64)
	}
	return nil, fmt.Errorf("result type %s", t)
}
