// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
)

// Parse reads the function named name from a Go source file and converts it
// to TIR. It accepts a wider subset than the printer emits, so that seed
// genomes can be written as plain Go:
//
//   - nested expressions are flattened into fresh definitions, in evaluation
//     order (so && and || lose short-circuiting; seeds must not rely on it);
//   - x op= y, x++ and x-- become x = x op y;
//   - for i, x := range s becomes for i := 0; i < len(s); i++ { x := s[i]; ... };
//   - var x T and var x = e become definitions;
//   - else if becomes a nested if in the else block;
//   - a loop bound or if condition that is not an atom is hoisted before it.
//
// The printed form of the result is in the printer's subset, so
// Print(Parse(Print(f))) == Print(f).
func Parse(src []byte, name string) (*Func, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "seed.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	if _, err := checkFiles(fset, []*ast.File{file}, info); err != nil {
		return nil, err
	}
	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name && fd.Recv == nil {
			decl = fd
		}
	}
	if decl == nil {
		return nil, fmt.Errorf("tir: no function %s", name)
	}
	c := &conv{fset: fset, info: info, vars: map[types.Object]Var{}}
	return c.fn(decl)
}

type conv struct {
	fset *token.FileSet
	info *types.Info
	f    *Func
	vars map[types.Object]Var
}

type parseError struct {
	pos token.Position
	msg string
}

func (e *parseError) Error() string { return fmt.Sprintf("tir: %s: %s", e.pos, e.msg) }

func (c *conv) fail(n ast.Node, format string, args ...any) {
	panic(&parseError{c.fset.Position(n.Pos()), fmt.Sprintf(format, args...)})
}

func (c *conv) fn(decl *ast.FuncDecl) (f *Func, err error) {
	defer func() {
		if r := recover(); r != nil {
			pe, ok := r.(*parseError)
			if !ok {
				panic(r)
			}
			err = pe
		}
	}()
	sig := &Signature{}
	for _, field := range decl.Type.Params.List {
		for _, id := range field.Names {
			obj := c.info.Defs[id]
			t := c.typeOf(id, obj.Type())
			sig.Params = append(sig.Params, Param{Name: id.Name, T: t})
		}
	}
	res := decl.Type.Results
	if res == nil || len(res.List) != 1 || len(res.List[0].Names) > 1 {
		c.fail(decl, "want exactly one unnamed result")
	}
	sig.Result = c.typeOf(res.List[0].Type, c.info.TypeOf(res.List[0].Type))
	c.f = NewFunc(sig)
	i := 0
	for _, field := range decl.Type.Params.List {
		for _, id := range field.Names {
			c.vars[c.info.Defs[id]] = c.f.Param(i)
			i++
		}
	}
	c.f.Body = c.block(decl.Body.List)
	return c.f, nil
}

func (c *conv) typeOf(n ast.Node, gt types.Type) Type {
	t, ok := FromGoType(gt)
	if !ok {
		c.fail(n, "type %s is not in tir", gt)
	}
	return t
}

func (c *conv) block(list []ast.Stmt) []Stmt {
	var out []Stmt
	for _, s := range list {
		c.stmt(s, &out)
	}
	return out
}

func (c *conv) define(id *ast.Ident) Var {
	obj := c.info.Defs[id]
	if obj == nil {
		c.fail(id, "%s is not a new definition", id.Name)
	}
	v := c.f.NewVar(c.typeOf(id, obj.Type()))
	c.vars[obj] = v
	return v
}

func (c *conv) lookup(id *ast.Ident) Var {
	v, ok := c.vars[c.info.Uses[id]]
	if !ok {
		c.fail(id, "%s is not a variable of this function", id.Name)
	}
	return v
}

var opAssign = map[token.Token]token.Token{
	token.ADD_ASSIGN: token.ADD, token.SUB_ASSIGN: token.SUB, token.MUL_ASSIGN: token.MUL,
	token.QUO_ASSIGN: token.QUO, token.REM_ASSIGN: token.REM, token.AND_ASSIGN: token.AND,
	token.OR_ASSIGN: token.OR, token.XOR_ASSIGN: token.XOR, token.SHL_ASSIGN: token.SHL,
	token.SHR_ASSIGN: token.SHR, token.AND_NOT_ASSIGN: token.AND_NOT,
}

func (c *conv) stmt(s ast.Stmt, out *[]Stmt) {
	switch s := s.(type) {
	case *ast.AssignStmt:
		if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			c.fail(s, "only single assignments")
		}
		c.assign(s, s.Lhs[0], s.Tok, s.Rhs[0], out)
	case *ast.IncDecStmt:
		tok := token.ADD_ASSIGN
		if s.Tok == token.DEC {
			tok = token.SUB_ASSIGN
		}
		one := &ast.BasicLit{ValuePos: s.TokPos, Kind: token.INT, Value: "1"}
		c.info.Types[one] = types.TypeAndValue{Type: c.info.TypeOf(s.X), Value: constant.MakeInt64(1)}
		c.assign(s, s.X, tok, one, out)
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			c.fail(s, "only var declarations")
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, id := range vs.Names {
				if len(vs.Values) == 0 {
					v := c.define(id)
					*out = append(*out, &Assign{Dst: v, Define: true, X: &Atom{X: Const{T: v.T}}})
				} else {
					x := c.expr(vs.Values[i], out)
					*out = append(*out, &Assign{Dst: c.define(id), Define: true, X: x})
				}
			}
		}
	case *ast.ForStmt:
		c.forStmt(s, out)
	case *ast.RangeStmt:
		c.rangeStmt(s, out)
	case *ast.IfStmt:
		c.ifStmt(s, out)
	case *ast.ReturnStmt:
		if len(s.Results) != 1 {
			c.fail(s, "return needs exactly one value")
		}
		*out = append(*out, &Return{X: c.operand(s.Results[0], out)})
	default:
		c.fail(s, "unsupported statement %T", s)
	}
}

func (c *conv) assign(n ast.Node, lhs ast.Expr, tok token.Token, rhs ast.Expr, out *[]Stmt) {
	if op, ok := opAssign[tok]; ok {
		cur := c.operand(lhs, out)
		y := c.operand(rhs, out)
		var x Expr = &Binop{Op: op, X: cur, Y: y}
		if ix, ok := lhs.(*ast.IndexExpr); ok {
			t := c.f.NewVar(cur.Type())
			*out = append(*out, &Assign{Dst: t, Define: true, X: x})
			c.store(ix, t, out)
			return
		}
		*out = append(*out, &Assign{Dst: cur.(Var), X: x})
		return
	}
	switch tok {
	case token.DEFINE:
		id, ok := lhs.(*ast.Ident)
		if !ok {
			c.fail(n, "define of non-identifier")
		}
		x := c.expr(rhs, out)
		*out = append(*out, &Assign{Dst: c.define(id), Define: true, X: x})
	case token.ASSIGN:
		switch l := lhs.(type) {
		case *ast.Ident:
			if l.Name == "_" {
				v, ok := c.operand(rhs, out).(Var)
				if !ok {
					c.fail(n, "_ = constant")
				}
				*out = append(*out, &Sink{X: v})
				return
			}
			dst := c.lookup(l)
			*out = append(*out, &Assign{Dst: dst, X: c.expr(rhs, out)})
		case *ast.IndexExpr:
			c.store(l, c.operand(rhs, out), out)
		default:
			c.fail(n, "assignment to %T", lhs)
		}
	default:
		c.fail(n, "unsupported assignment %s", tok)
	}
}

func (c *conv) store(ix *ast.IndexExpr, val Operand, out *[]Stmt) {
	x := c.sliceVar(ix.X)
	i := c.operand(ix.Index, out)
	*out = append(*out, &Store{X: x, I: i, Val: val})
}

func (c *conv) sliceVar(e ast.Expr) Var {
	id, ok := ast.Unparen(e).(*ast.Ident)
	if !ok {
		c.fail(e, "slice must be a parameter")
	}
	v := c.lookup(id)
	if !v.T.Slice {
		c.fail(e, "%s is not a slice", id.Name)
	}
	return v
}

func (c *conv) forStmt(s *ast.ForStmt, out *[]Stmt) {
	init, ok := s.Init.(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 {
		c.fail(s, "for needs init i := lo")
	}
	lo := c.operand(init.Rhs[0], out)
	cond, ok := s.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.LSS {
		c.fail(s, "for needs condition i < hi")
	}
	if cid, ok := ast.Unparen(cond.X).(*ast.Ident); !ok || c.info.Uses[cid] != c.info.Defs[init.Lhs[0].(*ast.Ident)] {
		c.fail(s, "for condition must compare the loop variable")
	}
	hi := c.bound(cond.Y, out)
	step := int64(0)
	switch p := s.Post.(type) {
	case *ast.IncDecStmt:
		if p.Tok == token.INC {
			step = 1
		}
	case *ast.AssignStmt:
		if p.Tok == token.ADD_ASSIGN {
			if tv := c.info.Types[p.Rhs[0]]; tv.Value != nil {
				step, _ = constant.Int64Val(tv.Value)
			}
		}
	}
	if step <= 0 {
		c.fail(s, "for post must be i++ or i += positive constant")
	}
	ind := c.define(init.Lhs[0].(*ast.Ident))
	*out = append(*out, &Loop{Ind: ind, Lo: lo, Hi: hi, Step: step, Body: c.block(s.Body.List)})
}

// bound converts a loop bound: len(s) stays a Len, anything else becomes an
// operand, hoisted before the loop if it is not one already.
func (c *conv) bound(e ast.Expr, out *[]Stmt) Expr {
	if call, ok := ast.Unparen(e).(*ast.CallExpr); ok {
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "len" {
			if _, ok := c.info.Uses[id].(*types.Builtin); ok {
				return &Len{X: c.sliceVar(call.Args[0])}
			}
		}
	}
	return &Atom{X: c.operand(e, out)}
}

func (c *conv) rangeStmt(s *ast.RangeStmt, out *[]Stmt) {
	if s.Tok != token.DEFINE && (s.Key != nil || s.Value != nil) {
		c.fail(s, "range must define its variables")
	}
	x := c.sliceVar(s.X)
	var ind Var
	if id, ok := s.Key.(*ast.Ident); ok && id.Name != "_" {
		ind = c.define(id)
	} else {
		ind = c.f.NewVar(Int)
	}
	var body []Stmt
	if id, ok := s.Value.(*ast.Ident); ok && id.Name != "_" {
		body = append(body, &Assign{Dst: c.define(id), Define: true, X: &Index{X: x, I: ind}})
	}
	body = append(body, c.block(s.Body.List)...)
	*out = append(*out, &Loop{Ind: ind, Lo: IntConst(Int, 0), Hi: &Len{X: x}, Step: 1, Body: body})
}

func (c *conv) ifStmt(s *ast.IfStmt, out *[]Stmt) {
	if s.Init != nil {
		c.stmt(s.Init, out)
	}
	cond, ok := c.operand(s.Cond, out).(Var)
	if !ok {
		c.fail(s.Cond, "constant if condition")
	}
	is := &If{Cond: cond, Then: c.block(s.Body.List)}
	switch e := s.Else.(type) {
	case *ast.BlockStmt:
		is.Else = c.block(e.List)
	case *ast.IfStmt:
		c.ifStmt(e, &is.Else)
	}
	*out = append(*out, is)
}

// operand converts e to an atom, flattening it into a fresh definition if it
// is not one.
func (c *conv) operand(e ast.Expr, out *[]Stmt) Operand {
	e = ast.Unparen(e)
	if k, ok := c.constant(e); ok {
		return k
	}
	if id, ok := e.(*ast.Ident); ok {
		return c.lookup(id)
	}
	x := c.expr(e, out)
	if a, ok := x.(*Atom); ok {
		return a.X
	}
	v := c.f.NewVar(c.typeOf(e, defaultType(c.info.TypeOf(e))))
	*out = append(*out, &Assign{Dst: v, Define: true, X: x})
	return v
}

// defaultType is the type an untyped value takes when it is defined: bool for
// untyped bool (comparisons inside && and ||), int otherwise.
func defaultType(gt types.Type) types.Type {
	if b, ok := gt.(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
		if b.Kind() == types.UntypedBool {
			return types.Typ[types.Bool]
		}
		return types.Typ[types.Int]
	}
	return gt
}

// constant converts a constant-valued expression. Untyped constants take the
// type go/types gave them in context, int if it left them untyped.
func (c *conv) constant(e ast.Expr) (Const, bool) {
	tv, ok := c.info.Types[e]
	if !ok || tv.Value == nil {
		return Const{}, false
	}
	t := c.typeOf(e, defaultType(tv.Type))
	switch tv.Value.Kind() {
	case constant.Bool:
		return BoolConst(constant.BoolVal(tv.Value)), true
	case constant.Int:
		if u, exact := constant.Uint64Val(tv.Value); exact {
			return Const{T: t, Bits: u}, true
		}
		i, _ := constant.Int64Val(tv.Value)
		return IntConst(t, i), true
	}
	c.fail(e, "unsupported constant %s", tv.Value)
	return Const{}, false
}

func (c *conv) expr(e ast.Expr, out *[]Stmt) Expr {
	e = ast.Unparen(e)
	if k, ok := c.constant(e); ok {
		return &Atom{X: k}
	}
	switch e := e.(type) {
	case *ast.Ident:
		return &Atom{X: c.lookup(e)}
	case *ast.BinaryExpr:
		x := c.operand(e.X, out)
		y := c.operand(e.Y, out)
		return &Binop{Op: e.Op, X: x, Y: y}
	case *ast.UnaryExpr:
		if e.Op == token.ADD {
			return &Atom{X: c.operand(e.X, out)}
		}
		return &Unop{Op: e.Op, X: c.operand(e.X, out)}
	case *ast.IndexExpr:
		x := c.sliceVar(e.X)
		return &Index{X: x, I: c.operand(e.Index, out)}
	case *ast.CallExpr:
		if c.info.Types[e.Fun].IsType() {
			if len(e.Args) != 1 {
				c.fail(e, "conversion needs one argument")
			}
			return &Convert{X: c.operand(e.Args[0], out)}
		}
		var call Call
		switch fun := ast.Unparen(e.Fun).(type) {
		case *ast.Ident:
			if _, ok := c.info.Uses[fun].(*types.Builtin); !ok {
				c.fail(e, "call of %s: only builtins and package functions", fun.Name)
			}
			if fun.Name == "len" {
				return &Len{X: c.sliceVar(e.Args[0])}
			}
			call.Name = fun.Name
		case *ast.SelectorExpr:
			obj, ok := c.info.Uses[fun.Sel].(*types.Func)
			if !ok || obj.Pkg() == nil {
				c.fail(e, "call of %s: not a package function", fun.Sel.Name)
			}
			call.Pkg, call.Name = obj.Pkg().Path(), obj.Name()
		default:
			c.fail(e, "unsupported call")
		}
		for _, a := range e.Args {
			call.Args = append(call.Args, c.operand(a, out))
		}
		return &call
	}
	c.fail(e, "unsupported expression %T", e)
	return nil
}
