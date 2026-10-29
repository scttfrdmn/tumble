// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package grammar

import (
	"errors"
	"fmt"
	"go/token"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Grammar is the run grammar: the only place design knowledge enters the
// search (PROJECT.md §2). It is serialized into the run record.
type Grammar struct {
	Sig *tir.Signature `json:"signature"`

	// Forms are the admitted statement forms beyond assign and the final
	// return, which every genome has.
	Forms Forms `json:"forms"`

	MaxLen   int `json:"max_len"`   // statements, nested ones included, sinks not
	MaxDepth int `json:"max_depth"` // loop nesting

	// Types are the types a local may have. Slices are parameters only.
	Types []tir.Type `json:"types"`

	// BinOps and UnOps are the admitted operators, as Go spells them.
	BinOps []string `json:"binops"`
	UnOps  []string `json:"unops"`

	// Convert admits T(x) between the integer types in Types.
	Convert bool `json:"convert"`

	// Calls is the call whitelist. len is an expression form, always
	// admitted when a parameter is a slice.
	Calls []Callee `json:"calls"`

	// Constants are drawn from [ConstMin, ConstMax], clamped to the type
	// (so unsigned constants from [0, ConstMax]); shift counts from
	// [0, width-1]; loop steps from [1, MaxStep].
	ConstMin int64 `json:"const_min"`
	ConstMax int64 `json:"const_max"`
	MaxStep  int64 `json:"max_step"`

	// IndexMax bounds constant indices, drawn from [0, IndexMax].
	IndexMax int64 `json:"index_max"`

	// InitMinLen and InitMaxLen are the ramp for initial genome lengths.
	InitMinLen int `json:"init_min_len"`
	InitMaxLen int `json:"init_max_len"`
}

// Forms switches the optional statement forms.
type Forms struct {
	Store  bool `json:"store"`
	Loop   bool `json:"loop"`
	If     bool `json:"if"`
	Return bool `json:"return"` // a return inside a block
}

// Callee is a whitelisted call. Pkg is empty for a builtin. A Poly callee
// (min, max) takes Arity arguments of one integer type and returns it.
type Callee struct {
	Pkg    string     `json:"pkg,omitempty"`
	Name   string     `json:"name"`
	Params []tir.Type `json:"params,omitempty"`
	Result tir.Type   `json:"result,omitzero"`
	Poly   bool       `json:"poly,omitempty"`
	Arity  int        `json:"arity,omitempty"`
}

func (c Callee) String() string {
	if c.Pkg == "" {
		return c.Name
	}
	return c.Pkg + "." + c.Name
}

// Popcount is the v0 grammar for popcount(xs []uint64) int (PROJECT.md §4.1,
// §6.6): every statement form; int, bool and uint64 locals; the integer and
// boolean operators except / and %; conversions; and the call whitelist
// math/bits.OnesCount64, min, max.
func Popcount() *Grammar {
	return &Grammar{
		Sig: &tir.Signature{
			Params: []tir.Param{{Name: "xs", T: tir.Uint64s}},
			Result: tir.Int,
		},
		Forms:    Forms{Store: true, Loop: true, If: true, Return: true},
		MaxLen:   32,
		MaxDepth: 2,
		Types:    []tir.Type{tir.Int, tir.Bool, tir.Uint64},
		BinOps: []string{
			"+", "-", "*", "&", "|", "^", "&^", "<<", ">>",
			"==", "!=", "<", "<=", ">", ">=", "&&", "||",
		},
		UnOps:   []string{"-", "^", "!"},
		Convert: true,
		Calls: []Callee{
			{Pkg: "math/bits", Name: "OnesCount64", Params: []tir.Type{tir.Uint64}, Result: tir.Int},
			{Name: "min", Poly: true, Arity: 2},
			{Name: "max", Poly: true, Arity: 2},
		},
		ConstMin:   -4,
		ConstMax:   64,
		MaxStep:    4,
		IndexMax:   3,
		InitMinLen: 3,
		InitMaxLen: 16,
	}
}

var tokens = map[string]token.Token{
	"+": token.ADD, "-": token.SUB, "*": token.MUL, "/": token.QUO, "%": token.REM,
	"&": token.AND, "|": token.OR, "^": token.XOR, "&^": token.AND_NOT,
	"<<": token.SHL, ">>": token.SHR,
	"==": token.EQL, "!=": token.NEQ, "<": token.LSS, "<=": token.LEQ, ">": token.GTR, ">=": token.GEQ,
	"&&": token.LAND, "||": token.LOR, "!": token.NOT,
}

// Check reports whether the grammar is usable.
func (g *Grammar) Check() error {
	var errs []error
	if g.Sig == nil || !g.Sig.Result.Valid() || g.Sig.Result.Slice {
		errs = append(errs, errors.New("grammar: signature needs a non-slice result"))
	}
	if g.MaxLen < 2 || g.MaxDepth < 0 || g.InitMinLen < 1 || g.InitMaxLen < g.InitMinLen || g.InitMaxLen > g.MaxLen {
		errs = append(errs, errors.New("grammar: bad length or depth limits"))
	}
	if g.ConstMin > g.ConstMax || g.MaxStep < 1 || g.IndexMax < 0 {
		errs = append(errs, errors.New("grammar: bad constant or step range"))
	}
	hasResult := false
	for _, t := range g.Types {
		if t.Slice || !t.Valid() {
			errs = append(errs, fmt.Errorf("grammar: local type %s", t))
		}
		hasResult = hasResult || t == g.Sig.Result
	}
	if !hasResult {
		errs = append(errs, fmt.Errorf("grammar: result type %s not among local types", g.Sig.Result))
	}
	for _, op := range append(append([]string(nil), g.BinOps...), g.UnOps...) {
		if _, ok := tokens[op]; !ok {
			errs = append(errs, fmt.Errorf("grammar: unknown operator %q", op))
		}
	}
	for _, c := range g.Calls {
		if c.Poly && c.Arity < 1 {
			errs = append(errs, fmt.Errorf("grammar: %s: poly call needs an arity", c))
		}
	}
	return errors.Join(errs...)
}

// binOps and unOps resolve the operator spellings.
func (g *Grammar) binOps() []token.Token { return resolve(g.BinOps) }
func (g *Grammar) unOps() []token.Token  { return resolve(g.UnOps) }

func resolve(ss []string) []token.Token {
	out := make([]token.Token, 0, len(ss))
	for _, s := range ss {
		if t, ok := tokens[s]; ok {
			out = append(out, t)
		}
	}
	return out
}

// HasType reports whether a local may have type t.
func (g *Grammar) HasType(t tir.Type) bool {
	for _, u := range g.Types {
		if u == t {
			return true
		}
	}
	return false
}

// Admits reports whether f stays inside the grammar: its length and depth
// limits, local types, operators, and calls. It does not check validity;
// that is tir.Func.Validate.
func (g *Grammar) Admits(f *tir.Func) error {
	if f.Len() > g.MaxLen {
		return fmt.Errorf("grammar: %d statements > %d", f.Len(), g.MaxLen)
	}
	if f.Depth() > g.MaxDepth {
		return fmt.Errorf("grammar: loop depth %d > %d", f.Depth(), g.MaxDepth)
	}
	bin := map[token.Token]bool{}
	for _, t := range g.binOps() {
		bin[t] = true
	}
	un := map[token.Token]bool{}
	for _, t := range g.unOps() {
		un[t] = true
	}
	var err error
	tir.Walk(f.Body, func(s tir.Stmt, depth int) bool {
		if err != nil {
			return false
		}
		switch s := s.(type) {
		case *tir.Store:
			if !g.Forms.Store {
				err = errors.New("grammar: store not admitted")
			}
		case *tir.Loop:
			if !g.Forms.Loop {
				err = errors.New("grammar: loop not admitted")
			}
		case *tir.If:
			if !g.Forms.If {
				err = errors.New("grammar: if not admitted")
			}
		case *tir.Return:
			if depth > 0 && !g.Forms.Return {
				err = errors.New("grammar: inner return not admitted")
			}
		case *tir.Assign:
			if !g.HasType(s.Dst.T) {
				err = fmt.Errorf("grammar: local of type %s", s.Dst.T)
				return false
			}
			switch x := s.X.(type) {
			case *tir.Binop:
				if !bin[x.Op] {
					err = fmt.Errorf("grammar: operator %s", x.Op)
				}
			case *tir.Unop:
				if !un[x.Op] {
					err = fmt.Errorf("grammar: operator %s", x.Op)
				}
			case *tir.Convert:
				if !g.Convert {
					err = errors.New("grammar: conversion not admitted")
				}
			case *tir.Call:
				if _, ok := g.callee(x.Pkg, x.Name); !ok {
					err = fmt.Errorf("grammar: call %s.%s not admitted", x.Pkg, x.Name)
				}
			}
		}
		return true
	})
	return err
}

func (g *Grammar) callee(pkg, name string) (Callee, bool) {
	for _, c := range g.Calls {
		if c.Pkg == pkg && c.Name == name {
			return c, true
		}
	}
	return Callee{}, false
}
