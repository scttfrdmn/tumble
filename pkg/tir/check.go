// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os/exec"
	"strings"
	"sync"
)

// Validate checks the structural rules of the IR that go/types does not
// state directly: every variable is defined once and before use in an
// enclosing scope; reassignment is only to accumulators (defined outside an
// enclosing loop); a loop's bound is not reassigned in its body; slices are
// parameters only; operand types agree with their operation; constants fit
// their type; there is no constant division by zero or negative constant
// shift; every local is read; the body ends in a Return. A Func that
// validates is expected to type-check; TypeCheck is the authority.
func (f *Func) Validate() error {
	v := validator{f: f, defined: map[int]bool{}, read: map[int]bool{}, defDepth: map[int]int{}}
	for i := range f.Sig.Params {
		v.defined[i] = true
		v.read[i] = true
	}
	v.block(f.Body, nil)
	if len(f.Body) == 0 {
		v.errf("empty body")
	} else if _, ok := f.Body[len(f.Body)-1].(*Return); !ok {
		v.errf("body does not end in a return")
	}
	for id := range v.defined {
		if !v.read[id] {
			v.errf("v%d declared and not used", id)
		}
	}
	return errors.Join(v.errs...)
}

type validator struct {
	f        *Func
	defined  map[int]bool
	read     map[int]bool
	defDepth map[int]int // loop depth at which a local was defined
	errs     []error
}

func (v *validator) errf(format string, args ...any) {
	if len(v.errs) < 20 {
		v.errs = append(v.errs, fmt.Errorf(format, args...))
	}
}

// block validates ss with scope inherited from the enclosing blocks. loops
// are the enclosing loops, innermost last.
func (v *validator) block(ss []Stmt, loops []*Loop) {
	var local []int
	defer func() {
		for _, id := range local {
			v.defined[id] = false
		}
	}()
	define := func(d Var) {
		if _, seen := v.defined[d.ID]; seen || d.ID >= v.f.NextID {
			v.errf("v%d defined twice or ID out of range", d.ID)
		}
		if d.T.Slice {
			v.errf("v%d: slice-typed local", d.ID)
		}
		v.defined[d.ID] = true
		v.defDepth[d.ID] = len(loops)
		local = append(local, d.ID)
	}
	for _, s := range ss {
		for _, u := range Uses(s) {
			if !v.defined[u.ID] {
				v.errf("v%d used before definition or out of scope", u.ID)
			}
			v.read[u.ID] = true
		}
		v.stmt(s, loops)
		switch s := s.(type) {
		case *Loop:
			define(s.Ind)
			v.read[s.Ind.ID] = true // read by the loop condition
			v.block(s.Body, append(loops, s))
		case *If:
			v.block(s.Then, loops)
			v.block(s.Else, loops)
		case *Assign:
			if s.Define {
				define(s.Dst)
			}
		}
	}
}

func (v *validator) stmt(s Stmt, loops []*Loop) {
	switch s := s.(type) {
	case *Assign:
		if !s.Define {
			switch {
			case !v.defined[s.Dst.ID]:
				v.errf("v%d reassigned before definition", s.Dst.ID)
			case v.f.IsParam(s.Dst):
				v.errf("parameter reassigned")
			case len(loops) == 0 || v.defDepth[s.Dst.ID] >= len(loops):
				v.errf("v%d reassigned outside a loop enclosing its definition", s.Dst.ID)
			}
			for _, l := range loops {
				if l.Ind == s.Dst {
					v.errf("loop variable reassigned")
				}
				if a, ok := l.Hi.(*Atom); ok && a.X == Operand(s.Dst) {
					v.errf("loop bound v%d reassigned in its loop", s.Dst.ID)
				}
			}
		}
		v.expr(s.X, s.Dst.T)
	case *Store:
		if !s.X.T.Slice {
			v.errf("store into non-slice")
		}
		v.wantType(s.I, Int, "store index")
		v.nonNegConst(s.I)
		v.wantType(s.Val, s.X.T.Elem(), "stored value")
	case *Loop:
		if s.Ind.T != Int {
			v.errf("loop variable is %s, not int", s.Ind.T)
		}
		v.wantType(s.Lo, Int, "loop start")
		switch hi := s.Hi.(type) {
		case *Atom:
			v.wantType(hi.X, Int, "loop bound")
		case *Len:
			v.expr(hi, Int)
		default:
			v.errf("loop bound is %T, not an atom or len", s.Hi)
		}
		if s.Step <= 0 {
			v.errf("loop step %d is not positive", s.Step)
		}
	case *If:
		if !s.Cond.T.IsBool() {
			v.errf("if condition is %s", s.Cond.T)
		}
	case *Return:
		v.wantType(s.X, v.f.Sig.Result, "return value")
	case *Sink:
	default:
		v.errf("unknown statement %T", s)
	}
}

func (v *validator) wantType(o Operand, t Type, what string) {
	if o.Type() != t {
		v.errf("%s is %s, want %s", what, o.Type(), t)
	}
	if c, ok := o.(Const); ok && !c.Fits() {
		v.errf("%s: constant %#x overflows %s", what, c.Bits, c.T)
	}
}

func (v *validator) nonNegConst(o Operand) {
	if c, ok := o.(Const); ok && c.Int64() < 0 {
		v.errf("negative constant index %d", c.Int64())
	}
}

func isVar(o Operand) bool { _, ok := o.(Var); return ok }

// expr validates e as the definition of a value of type t.
func (v *validator) expr(e Expr, t Type) {
	if t.Slice {
		v.errf("expression of slice type")
	}
	switch e := e.(type) {
	case *Atom:
		v.wantType(e.X, t, "copied value")
	case *Binop:
		if !isVar(e.X) && !isVar(e.Y) {
			v.errf("binop %s of two constants", e.Op)
		}
		switch e.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
			token.AND, token.OR, token.XOR, token.AND_NOT:
			if !t.IsInteger() {
				v.errf("%s on %s", e.Op, t)
			}
			v.wantType(e.X, t, e.Op.String()+" left")
			v.wantType(e.Y, t, e.Op.String()+" right")
			if c, ok := e.Y.(Const); ok && c.Bits == 0 && (e.Op == token.QUO || e.Op == token.REM) {
				v.errf("constant division by zero")
			}
		case token.SHL, token.SHR:
			if !isVar(e.X) {
				v.errf("shift of a constant")
			}
			v.wantType(e.X, t, "shifted value")
			if !e.Y.Type().IsInteger() {
				v.errf("shift count is %s", e.Y.Type())
			}
			if c, ok := e.Y.(Const); ok && (c.Int64() < 0 || !c.Fits()) {
				v.errf("bad constant shift count %d", c.Int64())
			}
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			if !t.IsBool() {
				v.errf("comparison defines %s", t)
			}
			if e.X.Type() != e.Y.Type() {
				v.errf("comparison of %s and %s", e.X.Type(), e.Y.Type())
			}
			if e.X.Type().IsBool() && e.Op != token.EQL && e.Op != token.NEQ {
				v.errf("ordered comparison of bools")
			}
			v.wantType(e.X, e.X.Type(), "compared value")
			v.wantType(e.Y, e.X.Type(), "compared value")
		case token.LAND, token.LOR:
			if !t.IsBool() {
				v.errf("%s defines %s", e.Op, t)
			}
			v.wantType(e.X, Bool, e.Op.String()+" left")
			v.wantType(e.Y, Bool, e.Op.String()+" right")
		default:
			v.errf("unknown binop %s", e.Op)
		}
	case *Unop:
		if !isVar(e.X) {
			v.errf("unop %s of a constant", e.Op)
		}
		switch e.Op {
		case token.SUB, token.XOR:
			if !t.IsInteger() {
				v.errf("%s on %s", e.Op, t)
			}
		case token.NOT:
			if !t.IsBool() {
				v.errf("! on %s", t)
			}
		default:
			v.errf("unknown unop %s", e.Op)
		}
		v.wantType(e.X, t, "unop operand")
	case *Index:
		if !e.X.T.Slice {
			v.errf("index of non-slice")
		}
		if e.X.T.Elem() != t {
			v.errf("index yields %s, want %s", e.X.T.Elem(), t)
		}
		v.wantType(e.I, Int, "index")
		v.nonNegConst(e.I)
	case *Len:
		if !e.X.T.Slice {
			v.errf("len of non-slice")
		}
		if t != Int {
			v.errf("len defines %s", t)
		}
	case *Call:
		if e.Name == "" {
			v.errf("call with no name")
		}
		for _, a := range e.Args {
			if c, ok := a.(Const); ok && !c.Fits() {
				v.errf("call argument overflows %s", c.T)
			}
		}
	case *Convert:
		if !isVar(e.X) {
			v.errf("conversion of a constant")
		}
		if !t.IsInteger() || !e.X.Type().IsInteger() {
			v.errf("conversion %s(%s)", t, e.X.Type())
		}
	default:
		v.errf("unknown expr %T", e)
	}
}

// TypeCheck prints f and checks it with go/types, as the fitness cascade's
// first stage does (PROJECT.md §4.3).
func (f *Func) TypeCheck() error {
	src := PrintFile("g", []Named{{Name: IdentityName, F: f}})
	return CheckSource(src)
}

// CheckSource parses and type-checks one Go file.
func CheckSource(src []byte) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "g.go", src, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	_, err = checkFiles(fset, []*ast.File{file}, nil)
	return err
}

// stdImporter is shared because importing math/bits from source is the
// expensive part of a check; go/types importers are not safe for concurrent
// use, hence the lock. It is created on first use.
var stdImporter struct {
	sync.Mutex
	types.Importer
}

type lockedImporter struct{}

func (lockedImporter) Import(path string) (*types.Package, error) {
	stdImporter.Lock()
	defer stdImporter.Unlock()
	if stdImporter.Importer == nil {
		// A binary built with -trimpath does not know its GOROOT, and the
		// source importer reads the standard library from build.Default;
		// ask the go command, as go/packages would.
		if build.Default.GOROOT == "" {
			if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
				build.Default.GOROOT = strings.TrimSpace(string(out))
			}
		}
		stdImporter.Importer = importer.ForCompiler(token.NewFileSet(), "source", nil)
	}
	return stdImporter.Import(path)
}

func checkFiles(fset *token.FileSet, files []*ast.File, info *types.Info) (*types.Package, error) {
	var errs []error
	conf := types.Config{
		Importer: lockedImporter{},
		Error: func(err error) {
			if len(errs) < 10 {
				errs = append(errs, err)
			}
		},
	}
	pkg, _ := conf.Check("g", fset, files, info)
	return pkg, errors.Join(errs...)
}
