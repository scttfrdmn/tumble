// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"fmt"
	"go/token"
)

// Signature is the run's fixed kernel signature. Parameter names are printed
// as given; locals are named canonically by the printer.
type Signature struct {
	Params []Param `json:"params"`
	Result Type    `json:"result"`
}

// Param is one named, typed parameter.
type Param struct {
	Name string `json:"name"`
	T    Type   `json:"type"`
}

// Func is a genome: one function body over the run's signature. Parameters
// are the Vars with IDs 0..len(Sig.Params)-1; locals have higher IDs. IDs are
// unique within a Func and carry no meaning beyond identity.
type Func struct {
	Sig    *Signature
	Body   []Stmt
	NextID int // the next unused Var ID
}

// NewFunc returns an empty Func over sig.
func NewFunc(sig *Signature) *Func {
	return &Func{Sig: sig, NextID: len(sig.Params)}
}

// Param is the Var for parameter i.
func (f *Func) Param(i int) Var { return Var{ID: i, T: f.Sig.Params[i].T} }

// Params are the parameter Vars in order.
func (f *Func) Params() []Var {
	vs := make([]Var, len(f.Sig.Params))
	for i := range vs {
		vs[i] = f.Param(i)
	}
	return vs
}

// IsParam reports whether v is one of f's parameters.
func (f *Func) IsParam(v Var) bool { return v.ID < len(f.Sig.Params) }

// NewVar allocates a fresh local of type t.
func (f *Func) NewVar(t Type) Var {
	v := Var{ID: f.NextID, T: t}
	f.NextID++
	return v
}

// Clone returns a deep copy of f sharing only the Signature.
func (f *Func) Clone() *Func {
	return &Func{Sig: f.Sig, Body: CloneStmts(f.Body), NextID: f.NextID}
}

// Len is the number of statements in f, counting nested ones and not
// counting sinks, which are repair artifacts rather than genetic material.
func (f *Func) Len() int {
	n := 0
	Walk(f.Body, func(s Stmt, _ int) bool {
		if _, ok := s.(*Sink); !ok {
			n++
		}
		return true
	})
	return n
}

// Depth is the maximum loop nesting depth in f.
func (f *Func) Depth() int {
	var depth func([]Stmt) int
	depth = func(ss []Stmt) int {
		d := 0
		for _, s := range ss {
			switch s := s.(type) {
			case *Loop:
				d = max(d, 1+depth(s.Body))
			case *If:
				d = max(d, depth(s.Then), depth(s.Else))
			}
		}
		return d
	}
	return depth(f.Body)
}

// Operand is an atom: a Var or a Const.
type Operand interface {
	Type() Type
	operand()
}

// Var is a variable: a parameter or a local.
type Var struct {
	ID int
	T  Type
}

// Const is a typed constant. Bits holds the value's two's-complement bits;
// for bool, 0 is false and 1 is true.
type Const struct {
	T    Type
	Bits uint64
}

func (v Var) Type() Type   { return v.T }
func (c Const) Type() Type { return c.T }
func (Var) operand()       {}
func (Const) operand()     {}

// Int64 is the constant's value as a signed integer.
func (c Const) Int64() int64 { return int64(c.Bits) }

// IntConst returns a constant of integer type t with value v.
func IntConst(t Type, v int64) Const { return Const{T: t, Bits: uint64(v)} }

// BoolConst returns the bool constant b.
func BoolConst(b bool) Const {
	if b {
		return Const{T: Bool, Bits: 1}
	}
	return Const{T: Bool}
}

// Fits reports whether the constant's value is representable in its type.
func (c Const) Fits() bool {
	switch {
	case c.T.IsBool():
		return c.Bits <= 1
	case !c.T.IsInteger():
		return false
	case c.T.IsUnsigned():
		w := c.T.Width()
		return w == 64 || c.Bits < 1<<w
	default:
		w := c.T.Width()
		v := c.Int64()
		return w == 64 || (v >= -(1<<(w-1)) && v < 1<<(w-1))
	}
}

// Expr is the right-hand side of an Assign.
type Expr interface{ expr() }

// Atom is a copy of an operand: v := x, or v := 3.
type Atom struct{ X Operand }

// Binop is x op y. Op is one of the arithmetic, bitwise, shift, comparison,
// or logical tokens.
type Binop struct {
	Op   token.Token
	X, Y Operand
}

// Unop is op x, for op in -, ^, !.
type Unop struct {
	Op token.Token
	X  Operand
}

// Index is x[i] for a slice x.
type Index struct {
	X Var
	I Operand
}

// Len is len(x) for a slice x.
type Len struct{ X Var }

// Call is a call to an admitted function: a builtin when Pkg is empty
// (min, max), otherwise Pkg.Name (math/bits.OnesCount64). The result type is
// the type of the Assign's destination.
type Call struct {
	Pkg, Name string
	Args      []Operand
}

// Convert is T(x), where T is the type of the Assign's destination.
type Convert struct{ X Operand }

func (*Atom) expr()    {}
func (*Binop) expr()   {}
func (*Unop) expr()    {}
func (*Index) expr()   {}
func (*Len) expr()     {}
func (*Call) expr()    {}
func (*Convert) expr() {}

// Stmt is a statement.
type Stmt interface{ stmt() }

// Assign is dst := x when Define is set, else dst = x. Reassignment is only
// for loop-carried accumulators: dst is defined outside a loop that encloses
// the Assign.
type Assign struct {
	Dst    Var
	Define bool
	X      Expr
}

// Store is x[i] = val.
type Store struct {
	X   Var
	I   Operand
	Val Operand
}

// Loop is for ind := lo; ind < hi; ind += step { body }. Hi is an *Atom or a
// *Len; Step is positive.
type Loop struct {
	Ind  Var
	Lo   Operand
	Hi   Expr
	Step int64
	Body []Stmt
}

// If is if cond { then } else { else }; Else may be empty.
type If struct {
	Cond       Var
	Then, Else []Stmt
}

// Return is return x.
type Return struct{ X Operand }

// Sink is _ = x: the repair pass's way of keeping an otherwise unused
// variable legal Go.
type Sink struct{ X Var }

func (*Assign) stmt() {}
func (*Store) stmt()  {}
func (*Loop) stmt()   {}
func (*If) stmt()     {}
func (*Return) stmt() {}
func (*Sink) stmt()   {}

// Walk calls fn for each statement in pre-order with its block depth (0 for
// top level). If fn returns false, the statement's children are skipped.
func Walk(ss []Stmt, fn func(s Stmt, depth int) bool) {
	walk(ss, 0, fn)
}

func walk(ss []Stmt, depth int, fn func(Stmt, int) bool) {
	for _, s := range ss {
		if !fn(s, depth) {
			continue
		}
		switch s := s.(type) {
		case *Loop:
			walk(s.Body, depth+1, fn)
		case *If:
			walk(s.Then, depth+1, fn)
			walk(s.Else, depth+1, fn)
		}
	}
}

// Defs are the variables a statement defines itself (not in nested blocks):
// an Assign with Define, or a Loop's induction variable.
func Defs(s Stmt) []Var {
	switch s := s.(type) {
	case *Assign:
		if s.Define {
			return []Var{s.Dst}
		}
	case *Loop:
		return []Var{s.Ind}
	}
	return nil
}

// Uses are the variables a statement reads itself, not counting nested
// blocks. A reassigned accumulator is not a use; a Loop's induction variable
// is not a use of the Loop.
func Uses(s Stmt) []Var {
	var vs []Var
	add := func(ops ...Operand) {
		for _, o := range ops {
			if v, ok := o.(Var); ok {
				vs = append(vs, v)
			}
		}
	}
	switch s := s.(type) {
	case *Assign:
		add(ExprOperands(s.X)...)
	case *Store:
		add(s.X, s.I, s.Val)
	case *Loop:
		add(s.Lo)
		add(ExprOperands(s.Hi)...)
	case *If:
		add(s.Cond)
	case *Return:
		add(s.X)
	case *Sink:
		add(s.X)
	}
	return vs
}

// ExprOperands are the operands an expression reads, in order.
func ExprOperands(e Expr) []Operand {
	switch e := e.(type) {
	case *Atom:
		return []Operand{e.X}
	case *Binop:
		return []Operand{e.X, e.Y}
	case *Unop:
		return []Operand{e.X}
	case *Index:
		return []Operand{e.X, e.I}
	case *Len:
		return []Operand{e.X}
	case *Call:
		return append([]Operand(nil), e.Args...)
	case *Convert:
		return []Operand{e.X}
	}
	panic(fmt.Sprintf("tir: unknown expr %T", e))
}

// MapOperands returns a copy of s (not its nested blocks) with every read
// operand replaced by fn(operand). Definitions are unchanged.
func MapOperands(s Stmt, fn func(Operand) Operand) Stmt {
	switch s := s.(type) {
	case *Assign:
		c := *s
		c.X = mapExpr(s.X, fn)
		return &c
	case *Store:
		c := *s
		c.X = mapVar(s.X, fn)
		c.I = fn(s.I)
		c.Val = fn(s.Val)
		return &c
	case *Loop:
		c := *s
		c.Lo = fn(s.Lo)
		c.Hi = mapExpr(s.Hi, fn)
		return &c
	case *If:
		c := *s
		c.Cond = mapVar(s.Cond, fn)
		return &c
	case *Return:
		return &Return{X: fn(s.X)}
	case *Sink:
		return &Sink{X: mapVar(s.X, fn)}
	}
	panic(fmt.Sprintf("tir: unknown stmt %T", s))
}

// mapVar applies fn to a Var-only position; a replacement that is not a Var
// keeps the original.
func mapVar(v Var, fn func(Operand) Operand) Var {
	if r, ok := fn(v).(Var); ok {
		return r
	}
	return v
}

func mapExpr(e Expr, fn func(Operand) Operand) Expr {
	switch e := e.(type) {
	case *Atom:
		return &Atom{X: fn(e.X)}
	case *Binop:
		return &Binop{Op: e.Op, X: fn(e.X), Y: fn(e.Y)}
	case *Unop:
		return &Unop{Op: e.Op, X: fn(e.X)}
	case *Index:
		return &Index{X: mapVar(e.X, fn), I: fn(e.I)}
	case *Len:
		return &Len{X: mapVar(e.X, fn)}
	case *Call:
		args := make([]Operand, len(e.Args))
		for i, a := range e.Args {
			args[i] = fn(a)
		}
		return &Call{Pkg: e.Pkg, Name: e.Name, Args: args}
	case *Convert:
		return &Convert{X: fn(e.X)}
	}
	panic(fmt.Sprintf("tir: unknown expr %T", e))
}

// CloneStmts deep-copies a statement list.
func CloneStmts(ss []Stmt) []Stmt {
	if ss == nil {
		return nil
	}
	out := make([]Stmt, len(ss))
	for i, s := range ss {
		out[i] = CloneStmt(s)
	}
	return out
}

// CloneStmt deep-copies a statement.
func CloneStmt(s Stmt) Stmt {
	id := func(o Operand) Operand { return o }
	c := MapOperands(s, id)
	switch c := c.(type) {
	case *Loop:
		c.Body = CloneStmts(c.Body)
	case *If:
		c.Then = CloneStmts(c.Then)
		c.Else = CloneStmts(c.Else)
	}
	return c
}
