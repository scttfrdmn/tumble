// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package grammar

import (
	"go/token"
	"math/rand/v2"

	"github.com/scttfrdmn/tumble/pkg/tir"
)

// Scope is what is visible at a point in a genome: the variables that may be
// read, and which of them may be reassigned there.
type Scope struct {
	vars   []scoped
	depth  int          // loop depth at this point
	bounds map[int]bool // Hi variables of the enclosing loops
}

type scoped struct {
	v     tir.Var
	depth int // loop depth of the definition
	param bool
	ind   bool // a loop induction variable
}

// NewScope is the scope at the top of f's body: the parameters.
func NewScope(f *tir.Func) *Scope {
	s := &Scope{bounds: map[int]bool{}}
	for _, p := range f.Params() {
		s.vars = append(s.vars, scoped{v: p, param: true})
	}
	return s
}

// Child is a nested block's scope: everything visible here, plus whatever
// the block defines, which does not leak back.
func (s *Scope) Child() *Scope {
	c := &Scope{vars: append([]scoped(nil), s.vars...), depth: s.depth, bounds: s.bounds}
	return c
}

// Enter is the scope of l's body.
func (s *Scope) Enter(l *tir.Loop) *Scope {
	c := s.Child()
	c.depth++
	c.bounds = map[int]bool{}
	for id := range s.bounds {
		c.bounds[id] = true
	}
	if a, ok := l.Hi.(*tir.Atom); ok {
		if v, ok := a.X.(tir.Var); ok {
			c.bounds[v.ID] = true
		}
	}
	c.vars = append(c.vars, scoped{v: l.Ind, depth: c.depth, ind: true})
	return c
}

// Define makes v visible from here on.
func (s *Scope) Define(v tir.Var) {
	s.vars = append(s.vars, scoped{v: v, depth: s.depth})
}

// Step advances the scope past statement st in the current block.
func (s *Scope) Step(st tir.Stmt) {
	if a, ok := st.(*tir.Assign); ok && a.Define {
		s.Define(a.Dst)
	}
}

// Depth is the loop depth at this point.
func (s *Scope) Depth() int { return s.depth }

// Vars are the visible variables of type t.
func (s *Scope) Vars(t tir.Type) []tir.Var {
	var out []tir.Var
	for _, sc := range s.vars {
		if sc.v.T == t {
			out = append(out, sc.v)
		}
	}
	return out
}

// All are the visible variables, parameters first, then in definition order.
func (s *Scope) All() []tir.Var {
	out := make([]tir.Var, len(s.vars))
	for i, sc := range s.vars {
		out[i] = sc.v
	}
	return out
}

// Has reports whether v is visible.
func (s *Scope) Has(v tir.Var) bool {
	for _, sc := range s.vars {
		if sc.v == v {
			return true
		}
	}
	return false
}

// Accumulators are the variables that may be reassigned here: locals defined
// outside an enclosing loop, other than loop variables and enclosing loop
// bounds.
func (s *Scope) Accumulators() []tir.Var {
	var out []tir.Var
	for _, sc := range s.vars {
		if !sc.param && !sc.ind && sc.depth < s.depth && !s.bounds[sc.v.ID] {
			out = append(out, sc.v)
		}
	}
	return out
}

// Walk calls fn at every insertion point of f: each block and index i in it
// (0..len(block)), with the scope visible just before (*block)[i]. Blocks are
// visited in pre-order; fn may not modify the blocks.
func Walk(f *tir.Func, fn func(block []tir.Stmt, i int, sc *Scope)) {
	walkBlock(f.Body, NewScope(f), fn)
}

func walkBlock(ss []tir.Stmt, sc *Scope, fn func([]tir.Stmt, int, *Scope)) {
	sc = sc.Child()
	for i, s := range ss {
		fn(ss, i, sc)
		switch s := s.(type) {
		case *tir.Loop:
			walkBlock(s.Body, sc.Enter(s), fn)
		case *tir.If:
			walkBlock(s.Then, sc, fn)
			walkBlock(s.Else, sc, fn)
		}
		sc.Step(s)
	}
	fn(ss, len(ss), sc)
}

// Gen draws random program material from the grammar. It is the random
// generator for initialization and the source of new material for mutation.
type Gen struct {
	G *Grammar
	R *rand.Rand
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

// Const draws a constant of type t.
func (g Gen) Const(t tir.Type) tir.Const {
	if t.IsBool() {
		return tir.BoolConst(g.R.IntN(2) == 1)
	}
	lo, hi := g.G.ConstMin, g.G.ConstMax
	if t.IsUnsigned() {
		lo = max(lo, 0)
	}
	if w := t.Width(); w < 64 {
		if t.IsUnsigned() {
			hi = min(hi, 1<<w-1)
		} else {
			lo, hi = max(lo, -(1<<(w-1))), min(hi, 1<<(w-1)-1)
		}
	}
	if hi < lo {
		hi = lo
	}
	return tir.IntConst(t, lo+g.R.Int64N(hi-lo+1))
}

// Index draws a non-negative int constant for an index position.
func (g Gen) Index() tir.Const {
	return tir.IntConst(tir.Int, g.R.Int64N(g.G.IndexMax+1))
}

// ShiftCount draws a constant shift count for a value of type t.
func (g Gen) ShiftCount(t tir.Type) tir.Const {
	return tir.IntConst(tir.Int, g.R.Int64N(int64(t.Width())))
}

// constProb is the chance an operand slot that may hold a constant does.
const constProb = 0.25

// Operand draws an operand of type t: a visible variable, or a constant with
// probability constProb (always, if no variable of t is visible). It fails
// only if constants are excluded and no variable of t is visible.
func (g Gen) Operand(sc *Scope, t tir.Type, allowConst bool) (tir.Operand, bool) {
	vs := sc.Vars(t)
	if len(vs) > 0 && (!allowConst || g.R.Float64() >= constProb) {
		return pick(g.R, vs), true
	}
	if !allowConst {
		return nil, false
	}
	return g.Const(t), true
}

// pair draws two operands of type t, at least one a variable.
func (g Gen) pair(sc *Scope, t tir.Type) (x, y tir.Operand, ok bool) {
	v, ok := g.Operand(sc, t, false)
	if !ok {
		return nil, nil, false
	}
	o, _ := g.Operand(sc, t, true)
	if g.R.IntN(2) == 0 {
		return v, o, true
	}
	return o, v, true
}

// isArith reports whether op takes and yields one integer type.
func isArith(op token.Token) bool {
	switch op {
	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
		token.AND, token.OR, token.XOR, token.AND_NOT:
		return true
	}
	return false
}

func isShift(op token.Token) bool { return op == token.SHL || op == token.SHR }

func isCompare(op token.Token) bool {
	switch op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		return true
	}
	return false
}

// Binop draws a binary expression yielding t with operator op, if the scope
// can supply its operands.
func (g Gen) Binop(sc *Scope, op token.Token, t tir.Type) (tir.Expr, bool) {
	switch {
	case isArith(op) && t.IsInteger():
		x, y, ok := g.pair(sc, t)
		if !ok {
			return nil, false
		}
		if c, isC := y.(tir.Const); isC && c.Bits == 0 && (op == token.QUO || op == token.REM) {
			y = tir.IntConst(t, 1)
		}
		return &tir.Binop{Op: op, X: x, Y: y}, true
	case isShift(op) && t.IsInteger():
		x, ok := g.Operand(sc, t, false)
		if !ok {
			return nil, false
		}
		var y tir.Operand = g.ShiftCount(t)
		if vs := sc.Vars(tir.Int); len(vs) > 0 && g.R.IntN(2) == 0 {
			y = pick(g.R, vs)
		}
		return &tir.Binop{Op: op, X: x, Y: y}, true
	case isCompare(op) && t.IsBool():
		var us []tir.Type
		for _, u := range g.G.Types {
			if len(sc.Vars(u)) > 0 && (u.IsInteger() || op == token.EQL || op == token.NEQ) {
				us = append(us, u)
			}
		}
		if len(us) == 0 {
			return nil, false
		}
		x, y, _ := g.pair(sc, pick(g.R, us))
		return &tir.Binop{Op: op, X: x, Y: y}, true
	case (op == token.LAND || op == token.LOR) && t.IsBool():
		x, y, ok := g.pair(sc, tir.Bool)
		if !ok {
			return nil, false
		}
		return &tir.Binop{Op: op, X: x, Y: y}, true
	}
	return nil, false
}

// Unop draws a unary expression yielding t with operator op.
func (g Gen) Unop(sc *Scope, op token.Token, t tir.Type) (tir.Expr, bool) {
	if (op == token.NOT) != t.IsBool() {
		return nil, false
	}
	x, ok := g.Operand(sc, t, false)
	if !ok {
		return nil, false
	}
	return &tir.Unop{Op: op, X: x}, true
}

// Call draws a call to c yielding t.
func (g Gen) Call(sc *Scope, c Callee, t tir.Type) (tir.Expr, bool) {
	call := &tir.Call{Pkg: c.Pkg, Name: c.Name}
	if c.Poly {
		if !t.IsInteger() {
			return nil, false
		}
		v, ok := g.Operand(sc, t, false)
		if !ok {
			return nil, false
		}
		call.Args = append(call.Args, v)
		for range c.Arity - 1 {
			o, _ := g.Operand(sc, t, true)
			call.Args = append(call.Args, o)
		}
		g.R.Shuffle(len(call.Args), func(i, j int) { call.Args[i], call.Args[j] = call.Args[j], call.Args[i] })
		return call, true
	}
	if c.Result != t {
		return nil, false
	}
	for _, p := range c.Params {
		o, _ := g.Operand(sc, p, true)
		call.Args = append(call.Args, o)
	}
	return call, true
}

// Expr draws an expression yielding t: one of the feasible forms, uniformly,
// with copies and bare constants at a quarter weight.
func (g Gen) Expr(sc *Scope, t tir.Type) tir.Expr {
	type cand struct {
		w    float64
		draw func() (tir.Expr, bool)
	}
	var cands []cand
	add := func(w float64, fn func() (tir.Expr, bool)) { cands = append(cands, cand{w, fn}) }

	add(0.25, func() (tir.Expr, bool) { return &tir.Atom{X: g.Const(t)}, true })
	if vs := sc.Vars(t); len(vs) > 0 {
		add(0.25, func() (tir.Expr, bool) { return &tir.Atom{X: pick(g.R, vs)}, true })
	}
	for _, op := range g.G.binOps() {
		add(1, func() (tir.Expr, bool) { return g.Binop(sc, op, t) })
	}
	for _, op := range g.G.unOps() {
		add(1, func() (tir.Expr, bool) { return g.Unop(sc, op, t) })
	}
	for _, p := range sc.vars {
		if !p.v.T.Slice {
			continue
		}
		x := p.v
		if x.T.Elem() == t {
			add(1, func() (tir.Expr, bool) {
				var i tir.Operand = g.Index()
				if vs := sc.Vars(tir.Int); len(vs) > 0 && g.R.Float64() >= constProb {
					i = pick(g.R, vs)
				}
				return &tir.Index{X: x, I: i}, true
			})
		}
		if t == tir.Int {
			add(1, func() (tir.Expr, bool) { return &tir.Len{X: x}, true })
		}
	}
	for _, c := range g.G.Calls {
		add(1, func() (tir.Expr, bool) { return g.Call(sc, c, t) })
	}
	if g.G.Convert && t.IsInteger() {
		var from []tir.Var
		for _, u := range g.G.Types {
			if u != t && u.IsInteger() {
				from = append(from, sc.Vars(u)...)
			}
		}
		if len(from) > 0 {
			add(1, func() (tir.Expr, bool) { return &tir.Convert{X: pick(g.R, from)}, true })
		}
	}

	// Draw by weight; a draw whose operands the scope cannot supply is
	// removed and the draw repeated. The constant always succeeds.
	for {
		total := 0.0
		for _, c := range cands {
			total += c.w
		}
		x := g.R.Float64() * total
		for i, c := range cands {
			if x -= c.w; x < 0 || i == len(cands)-1 {
				if e, ok := c.draw(); ok {
					return e
				}
				cands = append(cands[:i], cands[i+1:]...)
				break
			}
		}
	}
}

// Type draws a local type.
func (g Gen) Type() tir.Type { return pick(g.R, g.G.Types) }

// Block draws up to budget statements into a block with scope sc, which it
// advances. It returns the statements and how many it used.
func (g Gen) Block(f *tir.Func, sc *Scope, budget int) ([]tir.Stmt, int) {
	var out []tir.Stmt
	used := 0
	for used < budget {
		ss, n := g.Stmt(f, sc, budget-used)
		if n == 0 {
			break
		}
		for _, s := range ss {
			sc.Step(s)
		}
		out = append(out, ss...)
		used += n
	}
	return out, used
}

// Stmt draws one statement (with any definition it needs first, such as an
// if's condition) using at most budget statements, and returns it with the
// count used. Statement kinds are weighted: define 4, reassign 2, store,
// loop, if 1, inner return 1/4, among those the scope and budget allow.
func (g Gen) Stmt(f *tir.Func, sc *Scope, budget int) ([]tir.Stmt, int) {
	if budget <= 0 {
		return nil, 0
	}
	type cand struct {
		w    float64
		draw func() ([]tir.Stmt, int)
	}
	var cands []cand
	add := func(w float64, fn func() ([]tir.Stmt, int)) { cands = append(cands, cand{w, fn}) }

	add(4, func() ([]tir.Stmt, int) {
		t := g.Type()
		x := g.Expr(sc, t)
		return []tir.Stmt{&tir.Assign{Dst: f.NewVar(t), Define: true, X: x}}, 1
	})
	if accs := sc.Accumulators(); len(accs) > 0 {
		add(2, func() ([]tir.Stmt, int) {
			a := pick(g.R, accs)
			return []tir.Stmt{&tir.Assign{Dst: a, X: g.Expr(sc, a.T)}}, 1
		})
	}
	var slices []tir.Var
	for _, v := range sc.All() {
		if v.T.Slice {
			slices = append(slices, v)
		}
	}
	if g.G.Forms.Store && len(slices) > 0 {
		add(1, func() ([]tir.Stmt, int) {
			x := pick(g.R, slices)
			var i tir.Operand = g.Index()
			if vs := sc.Vars(tir.Int); len(vs) > 0 && g.R.Float64() >= constProb {
				i = pick(g.R, vs)
			}
			val, _ := g.Operand(sc, x.T.Elem(), true)
			return []tir.Stmt{&tir.Store{X: x, I: i, Val: val}}, 1
		})
	}
	if g.G.Forms.Loop && sc.depth < g.G.MaxDepth && budget >= 2 {
		add(1, func() ([]tir.Stmt, int) {
			l := g.LoopHead(f, sc)
			body, n := g.Block(f, sc.Enter(l), 1+g.R.IntN(budget-1))
			l.Body = body
			return []tir.Stmt{l}, 1 + n
		})
	}
	if g.G.Forms.If && budget >= 2 {
		add(1, func() ([]tir.Stmt, int) {
			var pre []tir.Stmt
			conds := sc.Vars(tir.Bool)
			used := 1
			if len(conds) == 0 || g.R.IntN(2) == 0 {
				if budget < 3 {
					if len(conds) == 0 {
						return nil, 0
					}
				} else {
					c := f.NewVar(tir.Bool)
					pre = append(pre, &tir.Assign{Dst: c, Define: true, X: g.Expr(sc, tir.Bool)})
					conds = []tir.Var{c}
					used++
				}
			}
			is := &tir.If{Cond: pick(g.R, conds)}
			inner := sc.Child()
			for _, s := range pre {
				inner.Step(s)
			}
			n := 1 + g.R.IntN(budget-used)
			then, tn := g.Block(f, inner.Child(), n)
			is.Then = then
			used += tn
			if rest := budget - used; rest > 0 && g.R.IntN(2) == 0 {
				els, en := g.Block(f, inner.Child(), 1+g.R.IntN(rest))
				is.Else = els
				used += en
			}
			return append(pre, is), used
		})
	}
	if g.G.Forms.Return && sc.depth > 0 {
		add(0.25, func() ([]tir.Stmt, int) { return []tir.Stmt{g.Return(sc)}, 1 })
	}

	for len(cands) > 0 {
		total := 0.0
		for _, c := range cands {
			total += c.w
		}
		x := g.R.Float64() * total
		for i, c := range cands {
			if x -= c.w; x < 0 || i == len(cands)-1 {
				if ss, n := c.draw(); n > 0 {
					return ss, n
				}
				cands = append(cands[:i], cands[i+1:]...)
				break
			}
		}
	}
	return nil, 0
}

// LoopHead draws a loop with an empty body: start 0 or an int operand, bound
// len(slice) or an int operand, step 1 or (one time in four) up to MaxStep.
func (g Gen) LoopHead(f *tir.Func, sc *Scope) *tir.Loop {
	var lo tir.Operand = tir.IntConst(tir.Int, 0)
	if g.R.IntN(4) == 0 {
		lo, _ = g.Operand(sc, tir.Int, true)
	}
	var hi tir.Expr
	var slices []tir.Var
	for _, v := range sc.All() {
		if v.T.Slice {
			slices = append(slices, v)
		}
	}
	if len(slices) > 0 && g.R.IntN(4) != 0 {
		hi = &tir.Len{X: pick(g.R, slices)}
	} else {
		o, _ := g.Operand(sc, tir.Int, true)
		hi = &tir.Atom{X: o}
	}
	step := int64(1)
	if g.G.MaxStep > 1 && g.R.IntN(4) == 0 {
		step = 1 + g.R.Int64N(g.G.MaxStep)
	}
	return &tir.Loop{Ind: f.NewVar(tir.Int), Lo: lo, Hi: hi, Step: step}
}

// Return draws a return of a visible variable of the result type, or a
// constant if there is none.
func (g Gen) Return(sc *Scope) *tir.Return {
	t := g.G.Sig.Result
	if vs := sc.Vars(t); len(vs) > 0 {
		return &tir.Return{X: pick(g.R, vs)}
	}
	return &tir.Return{X: g.Const(t)}
}

// Random draws a genome of about size statements (at least the final return).
func (g Gen) Random(size int) *tir.Func {
	f := tir.NewFunc(g.G.Sig)
	sc := NewScope(f)
	body, _ := g.Block(f, sc, max(size-1, 0))
	f.Body = append(body, g.Return(sc))
	f.Sink()
	return f
}

// Ramped draws n genomes with lengths ramped evenly from InitMinLen to
// InitMaxLen.
func (g Gen) Ramped(n int) []*tir.Func {
	out := make([]*tir.Func, n)
	for i := range out {
		size := g.G.InitMinLen
		if n > 1 {
			size += i * (g.G.InitMaxLen - g.G.InitMinLen) / (n - 1)
		}
		out[i] = g.Random(size)
	}
	return out
}
