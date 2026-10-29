// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"go/token"
	"math/rand/v2"
	"slices"

	"github.com/scttfrdmn/tumble/pkg/grammar"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Point is point mutation (PROJECT.md §4.2). Each statement (sinks aside) is
// mutated with probability 1/L, L the genome's length, and at least one is.
// A mutation changes an operator, an operand, a constant, a loop bound, or a
// call; redraws the expression; or inserts a new statement before or after,
// or deletes it. Targets are mutated from last to first, so earlier
// positions stay put.
func Point(r *rand.Rand, g *grammar.Grammar, a *tir.Func) (*tir.Func, error) {
	child := a.Clone()
	n := len(positions(child))
	var targets []int
	for i := range n {
		if r.IntN(n) == 0 {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		targets = []int{r.IntN(n)}
	}
	gen := grammar.Gen{G: g, R: r}
	for _, t := range slices.Backward(targets) {
		ps := positions(child)
		if t >= len(ps) {
			continue // removed with an enclosing statement
		}
		mutateAt(gen, child, ps[t])
	}
	if err := Repair(g, child); err != nil {
		return nil, &Discarded{Op: Mutation, Reason: err}
	}
	return child, nil
}

// scopeAt is the scope just before the statement at p.
func scopeAt(f *tir.Func, p pos) *grammar.Scope {
	var out *grammar.Scope
	target := &(*p.block)[p.i]
	grammar.Walk(f, func(block []tir.Stmt, i int, sc *grammar.Scope) {
		if out == nil && i < len(block) && &block[i] == target {
			out = sc
		}
	})
	return out
}

// edit is one point mutation; it reports false if it does not apply to the
// statement, and another is drawn.
type edit func(gen grammar.Gen, f *tir.Func, p pos, sc *grammar.Scope) bool

var edits = []edit{
	editOperator, editOperand, editConstant, editLoopBound, editCall, editRedraw,
	editInsert, editDelete,
}

func mutateAt(gen grammar.Gen, f *tir.Func, p pos) {
	sc := scopeAt(f, p)
	order := gen.R.Perm(len(edits))
	for _, i := range order {
		if edits[i](gen, f, p, sc) {
			return
		}
	}
}

func opClass(op token.Token) int {
	switch op {
	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM, token.AND, token.OR, token.XOR, token.AND_NOT:
		return 0
	case token.SHL, token.SHR:
		return 1
	case token.EQL, token.NEQ:
		return 2
	case token.LSS, token.LEQ, token.GTR, token.GEQ:
		return 3
	case token.LAND, token.LOR:
		return 4
	}
	return 5
}

// editOperator swaps a binop's or unop's operator for another admitted one of
// the same class (ordered comparisons may become == or !=).
func editOperator(gen grammar.Gen, f *tir.Func, p pos, _ *grammar.Scope) bool {
	a, ok := p.stmt().(*tir.Assign)
	if !ok {
		return false
	}
	switch x := a.X.(type) {
	case *tir.Binop:
		cls := opClass(x.Op)
		var alts []token.Token
		for _, op := range gen.G.BinTokens() {
			c := opClass(op)
			if op != x.Op && (c == cls || (cls == 3 && c == 2)) {
				alts = append(alts, op)
			}
		}
		if len(alts) == 0 {
			return false
		}
		x.Op = pick(gen.R, alts)
		return true
	case *tir.Unop:
		var alts []token.Token
		for _, op := range gen.G.UnTokens() {
			if op != x.Op && (op == token.NOT) == (x.Op == token.NOT) {
				alts = append(alts, op)
			}
		}
		if len(alts) == 0 {
			return false
		}
		x.Op = pick(gen.R, alts)
		return true
	}
	return false
}

// slot is a read operand position in a statement.
type slot struct {
	get      func() tir.Operand
	set      func(tir.Operand)
	varOnly  bool
	isIndex  bool // an index position: constants come from [0, IndexMax]
	isShiftY bool // a shift count
}

// slots lists the operand positions of s itself (not nested blocks).
func slots(s tir.Stmt) []slot {
	var out []slot
	op := func(get func() tir.Operand, set func(tir.Operand)) slot { return slot{get: get, set: set} }
	switch s := s.(type) {
	case *tir.Assign:
		switch x := s.X.(type) {
		case *tir.Atom:
			out = append(out, op(func() tir.Operand { return x.X }, func(o tir.Operand) { x.X = o }))
		case *tir.Binop:
			_, xc := x.X.(tir.Const)
			_, yc := x.Y.(tir.Const)
			shift := x.Op == token.SHL || x.Op == token.SHR
			sx := op(func() tir.Operand { return x.X }, func(o tir.Operand) { x.X = o })
			sx.varOnly = yc || shift
			sy := op(func() tir.Operand { return x.Y }, func(o tir.Operand) { x.Y = o })
			sy.varOnly = xc
			sy.isShiftY = shift
			out = append(out, sx, sy)
		case *tir.Unop:
			sl := op(func() tir.Operand { return x.X }, func(o tir.Operand) { x.X = o })
			sl.varOnly = true
			out = append(out, sl)
		case *tir.Index:
			sl := op(func() tir.Operand { return x.I }, func(o tir.Operand) { x.I = o })
			sl.isIndex = true
			out = append(out, sl)
		case *tir.Call:
			nvars := 0
			for _, a := range x.Args {
				if _, ok := a.(tir.Var); ok {
					nvars++
				}
			}
			for i := range x.Args {
				sl := op(func() tir.Operand { return x.Args[i] }, func(o tir.Operand) { x.Args[i] = o })
				_, isVar := x.Args[i].(tir.Var)
				sl.varOnly = x.Pkg == "" && isVar && nvars == 1 // keep a poly call's type anchor
				out = append(out, sl)
			}
		case *tir.Convert:
			sl := op(func() tir.Operand { return x.X }, func(o tir.Operand) { x.X = o })
			sl.varOnly = true
			out = append(out, sl)
		}
	case *tir.Store:
		si := op(func() tir.Operand { return s.I }, func(o tir.Operand) { s.I = o })
		si.isIndex = true
		out = append(out, si, op(func() tir.Operand { return s.Val }, func(o tir.Operand) { s.Val = o }))
	case *tir.Loop:
		out = append(out, op(func() tir.Operand { return s.Lo }, func(o tir.Operand) { s.Lo = o }))
	case *tir.If:
		sl := op(func() tir.Operand { return s.Cond }, func(o tir.Operand) { s.Cond = o.(tir.Var) })
		sl.varOnly = true
		out = append(out, sl)
	case *tir.Return:
		out = append(out, op(func() tir.Operand { return s.X }, func(o tir.Operand) { s.X = o }))
	}
	return out
}

// editOperand replaces one operand with another visible variable of its
// type, or a constant where the position allows one.
func editOperand(gen grammar.Gen, f *tir.Func, p pos, sc *grammar.Scope) bool {
	ss := slots(p.stmt())
	if len(ss) == 0 {
		return false
	}
	sl := pick(gen.R, ss)
	cur := sl.get()
	t := cur.Type()
	var vs []tir.Var
	for _, v := range sc.Vars(t) {
		if v != cur {
			vs = append(vs, v)
		}
	}
	if !sl.varOnly && (len(vs) == 0 || gen.R.Float64() < 0.25) {
		sl.set(drawConst(gen, sl, t))
		return true
	}
	if len(vs) == 0 {
		return false
	}
	sl.set(pick(gen.R, vs))
	return true
}

func drawConst(gen grammar.Gen, sl slot, t tir.Type) tir.Const {
	switch {
	case sl.isIndex:
		return gen.Index()
	case sl.isShiftY:
		return gen.ShiftCount(tir.Uint64)
	}
	return gen.Const(t)
}

// editConstant perturbs one constant by ±1 or ±2, or redraws it, half and
// half; a perturbation that leaves the type's range is redrawn.
func editConstant(gen grammar.Gen, f *tir.Func, p pos, _ *grammar.Scope) bool {
	var cs []slot
	for _, sl := range slots(p.stmt()) {
		if _, ok := sl.get().(tir.Const); ok {
			cs = append(cs, sl)
		}
	}
	l, isLoop := p.stmt().(*tir.Loop)
	if len(cs) == 0 && !isLoop {
		return false
	}
	if isLoop && (len(cs) == 0 || gen.R.IntN(2) == 0) {
		l.Step = max(1, min(gen.G.MaxStep, l.Step+[]int64{-1, 1}[gen.R.IntN(2)]))
		return true
	}
	sl := pick(gen.R, cs)
	c := sl.get().(tir.Const)
	if c.T.IsBool() {
		c.Bits ^= 1
		sl.set(c)
		return true
	}
	if gen.R.IntN(2) == 0 {
		d := []int64{-2, -1, 1, 2}[gen.R.IntN(4)]
		n := tir.IntConst(c.T, c.Int64()+d)
		if n.Fits() && (!sl.isIndex || (n.Int64() >= 0 && n.Int64() <= gen.G.IndexMax)) &&
			(!sl.isShiftY || (n.Int64() >= 0 && n.Int64() < 64)) {
			sl.set(n)
			return true
		}
	}
	sl.set(drawConst(gen, sl, c.T))
	return true
}

// editLoopBound redraws one of a loop's start, bound, or step.
func editLoopBound(gen grammar.Gen, f *tir.Func, p pos, sc *grammar.Scope) bool {
	l, ok := p.stmt().(*tir.Loop)
	if !ok {
		return false
	}
	fresh := gen.LoopHead(f, sc)
	switch gen.R.IntN(3) {
	case 0:
		l.Lo = fresh.Lo
	case 1:
		l.Hi = fresh.Hi
	default:
		l.Step = fresh.Step
	}
	return true
}

// editCall replaces an expression with a call to an admitted callee of the
// destination type.
func editCall(gen grammar.Gen, f *tir.Func, p pos, sc *grammar.Scope) bool {
	a, ok := p.stmt().(*tir.Assign)
	if !ok || len(gen.G.Calls) == 0 {
		return false
	}
	for _, i := range gen.R.Perm(len(gen.G.Calls)) {
		if x, ok := gen.Call(sc, gen.G.Calls[i], a.Dst.T); ok {
			a.X = x
			return true
		}
	}
	return false
}

// editRedraw replaces an expression with a freshly drawn one of its type.
func editRedraw(gen grammar.Gen, f *tir.Func, p pos, sc *grammar.Scope) bool {
	a, ok := p.stmt().(*tir.Assign)
	if !ok {
		return false
	}
	a.X = gen.Expr(sc, a.Dst.T)
	return true
}

// editInsert draws a new statement (at most three, counting any nested
// block) before or after this one, within the grammar's length limit.
func editInsert(gen grammar.Gen, f *tir.Func, p pos, sc *grammar.Scope) bool {
	budget := min(3, gen.G.MaxLen-f.Len())
	if budget <= 0 {
		return false
	}
	at := p.i
	if gen.R.IntN(2) == 0 {
		// After: the scope must include this statement's definitions.
		sc = sc.Child()
		sc.Step(p.stmt())
		at++
	}
	ss, n := gen.Stmt(f, sc, budget)
	if n == 0 {
		return false
	}
	*p.block = insert(*p.block, at, ss...)
	return true
}

// editDelete removes the statement, and any block it holds.
func editDelete(gen grammar.Gen, f *tir.Func, p pos, _ *grammar.Scope) bool {
	*p.block = remove(*p.block, p.i, p.i+1)
	return true
}
