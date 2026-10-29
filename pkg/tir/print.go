// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"path"
	"slices"
	"strconv"
)

// IdentityName is the function name used when printing a genome for its
// phenotype identity, so the identity does not depend on the name a
// generation gives it.
const IdentityName = "f"

// Names assigns each variable its printed name: parameters keep their
// signature names; locals are v0, v1, ... and loop induction variables i0,
// i1, ... in order of definition. The naming depends only on the program's
// structure, which is what makes the printed form canonical.
func (f *Func) Names() map[int]string {
	names := make(map[int]string, f.NextID)
	for i, p := range f.Sig.Params {
		names[i] = p.Name
	}
	nv, ni := 0, 0
	Walk(f.Body, func(s Stmt, _ int) bool {
		for _, d := range Defs(s) {
			if _, ok := s.(*Loop); ok {
				names[d.ID] = "i" + strconv.Itoa(ni)
				ni++
			} else {
				names[d.ID] = "v" + strconv.Itoa(nv)
				nv++
			}
		}
		return true
	})
	return names
}

// Imports are the package paths f's calls need, sorted.
func (f *Func) Imports() []string {
	set := map[string]bool{}
	Walk(f.Body, func(s Stmt, _ int) bool {
		if a, ok := s.(*Assign); ok {
			if c, ok := a.X.(*Call); ok && c.Pkg != "" {
				set[c.Pkg] = true
			}
		}
		return true
	})
	var out []string
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// Print returns f as gofmt-formatted Go source for a function declaration
// named name.
func (f *Func) Print(name string) []byte {
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), f.Decl(name)); err != nil {
		// Decl only builds well-formed syntax; a failure here is a tir bug.
		panic(fmt.Sprintf("tir: printing %s: %v", name, err))
	}
	return buf.Bytes()
}

// Identity is the phenotype identity: sha256 of the canonical printed form.
func (f *Func) Identity() [32]byte {
	return sha256.Sum256(f.Print(IdentityName))
}

// Named is a genome with the name it is printed under in a file.
type Named struct {
	Name string
	F    *Func
}

// PrintFile returns a gofmt-formatted Go file in package pkg holding fns, in
// order, with the imports they need.
func PrintFile(pkg string, fns []Named) []byte {
	set := map[string]bool{}
	for _, n := range fns {
		for _, p := range n.F.Imports() {
			set[p] = true
		}
	}
	var imports []string
	for p := range set {
		imports = append(imports, p)
	}
	slices.Sort(imports)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "package %s\n", pkg)
	if len(imports) > 0 {
		buf.WriteString("\nimport (\n")
		for _, p := range imports {
			fmt.Fprintf(&buf, "\t%q\n", p)
		}
		buf.WriteString(")\n")
	}
	for _, n := range fns {
		buf.WriteString("\n")
		buf.Write(n.F.Print(n.Name))
		buf.WriteString("\n")
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		panic(fmt.Sprintf("tir: formatting file: %v", err))
	}
	return out
}

// Decl builds the function declaration for f under name.
func (f *Func) Decl(name string) *ast.FuncDecl {
	p := printer{names: f.Names()}
	var params []*ast.Field
	for _, prm := range f.Sig.Params {
		params = append(params, &ast.Field{
			Names: []*ast.Ident{ast.NewIdent(prm.Name)},
			Type:  typeExpr(prm.T),
		})
	}
	return &ast.FuncDecl{
		Name: ast.NewIdent(name),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: params},
			Results: &ast.FieldList{List: []*ast.Field{{Type: typeExpr(f.Sig.Result)}}},
		},
		Body: p.block(f.Body),
	}
}

type printer struct{ names map[int]string }

func typeExpr(t Type) ast.Expr {
	if t.Slice {
		return &ast.ArrayType{Elt: ast.NewIdent(t.Elem().String())}
	}
	return ast.NewIdent(t.String())
}

func (p printer) block(ss []Stmt) *ast.BlockStmt {
	b := &ast.BlockStmt{}
	for _, s := range ss {
		b.List = append(b.List, p.stmt(s))
	}
	return b
}

func (p printer) ident(v Var) *ast.Ident {
	n, ok := p.names[v.ID]
	if !ok {
		// An undefined variable still prints, so that a broken genome can be
		// shown and type-checked; the checker reports it.
		n = "undef" + strconv.Itoa(v.ID)
	}
	return ast.NewIdent(n)
}

func (p printer) stmt(s Stmt) ast.Stmt {
	switch s := s.(type) {
	case *Assign:
		tok := token.ASSIGN
		if s.Define {
			tok = token.DEFINE
		}
		return &ast.AssignStmt{Lhs: []ast.Expr{p.ident(s.Dst)}, Tok: tok, Rhs: []ast.Expr{p.expr(s.X, s.Dst.T)}}
	case *Store:
		return &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.IndexExpr{X: p.ident(s.X), Index: p.operand(s.I)}},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{p.operand(s.Val)},
		}
	case *Loop:
		ind := p.ident(s.Ind)
		var post ast.Stmt = &ast.IncDecStmt{X: ind, Tok: token.INC}
		if s.Step != 1 {
			post = &ast.AssignStmt{Lhs: []ast.Expr{ind}, Tok: token.ADD_ASSIGN,
				Rhs: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.FormatInt(s.Step, 10)}}}
		}
		return &ast.ForStmt{
			Init: &ast.AssignStmt{Lhs: []ast.Expr{ind}, Tok: token.DEFINE, Rhs: []ast.Expr{p.operand(s.Lo)}},
			Cond: &ast.BinaryExpr{X: ind, Op: token.LSS, Y: p.expr(s.Hi, Int)},
			Post: post,
			Body: p.block(s.Body),
		}
	case *If:
		is := &ast.IfStmt{Cond: p.ident(s.Cond), Body: p.block(s.Then)}
		if len(s.Else) > 0 {
			is.Else = p.block(s.Else)
		}
		return is
	case *Return:
		return &ast.ReturnStmt{Results: []ast.Expr{p.operand(s.X)}}
	case *Sink:
		return &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []ast.Expr{p.ident(s.X)}}
	}
	panic(fmt.Sprintf("tir: unknown stmt %T", s))
}

// expr prints e as the right-hand side of a definition of type t. A bare
// constant of a type other than int or bool is converted explicitly, since an
// untyped constant would otherwise default to int.
func (p printer) expr(e Expr, t Type) ast.Expr {
	switch e := e.(type) {
	case *Atom:
		if c, ok := e.X.(Const); ok && c.T != Int && c.T != Bool {
			return &ast.CallExpr{Fun: ast.NewIdent(c.T.String()), Args: []ast.Expr{p.operand(c)}}
		}
		return p.operand(e.X)
	case *Binop:
		return &ast.BinaryExpr{X: p.operand(e.X), Op: e.Op, Y: p.operand(e.Y)}
	case *Unop:
		return &ast.UnaryExpr{Op: e.Op, X: p.operand(e.X)}
	case *Index:
		return &ast.IndexExpr{X: p.ident(e.X), Index: p.operand(e.I)}
	case *Len:
		return &ast.CallExpr{Fun: ast.NewIdent("len"), Args: []ast.Expr{p.ident(e.X)}}
	case *Call:
		var fun ast.Expr = ast.NewIdent(e.Name)
		if e.Pkg != "" {
			fun = &ast.SelectorExpr{X: ast.NewIdent(path.Base(e.Pkg)), Sel: ast.NewIdent(e.Name)}
		}
		var args []ast.Expr
		for _, a := range e.Args {
			args = append(args, p.operand(a))
		}
		return &ast.CallExpr{Fun: fun, Args: args}
	case *Convert:
		return &ast.CallExpr{Fun: ast.NewIdent(t.String()), Args: []ast.Expr{p.operand(e.X)}}
	}
	panic(fmt.Sprintf("tir: unknown expr %T", e))
}

func (p printer) operand(o Operand) ast.Expr {
	switch o := o.(type) {
	case Var:
		return p.ident(o)
	case Const:
		return constExpr(o)
	}
	panic(fmt.Sprintf("tir: unknown operand %T", o))
}

// constExpr prints a constant: true/false, signed decimal, or unsigned
// decimal below 1<<16 and lowercase hex above.
func constExpr(c Const) ast.Expr {
	switch {
	case c.T.IsBool():
		if c.Bits != 0 {
			return ast.NewIdent("true")
		}
		return ast.NewIdent("false")
	case c.T.IsUnsigned():
		if c.Bits < 1<<16 {
			return &ast.BasicLit{Kind: token.INT, Value: strconv.FormatUint(c.Bits, 10)}
		}
		return &ast.BasicLit{Kind: token.INT, Value: "0x" + strconv.FormatUint(c.Bits, 16)}
	default:
		v := c.Int64()
		if v < 0 {
			// uint64(-v) is right for math.MinInt64 too.
			return &ast.UnaryExpr{Op: token.SUB, X: &ast.BasicLit{Kind: token.INT, Value: strconv.FormatUint(uint64(-v), 10)}}
		}
		return &ast.BasicLit{Kind: token.INT, Value: strconv.FormatInt(v, 10)}
	}
}
