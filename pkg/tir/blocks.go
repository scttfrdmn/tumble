// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

import (
	"go/ast"
	"go/parser"
	"go/token"
)

// Step is one step of a BlockPath: the statement At of the current block,
// and which of its blocks to enter: 0 for a loop body or an if's then block,
// 1 for an if's else block.
type Step struct {
	At     int
	Branch int
}

// BlockPath addresses a block nested in a function body; the empty path is
// the body itself.
type BlockPath []Step

// Block returns the block at p, or nil if p does not address one.
func (f *Func) Block(p BlockPath) *[]Stmt {
	b := &f.Body
	for _, st := range p {
		if st.At < 0 || st.At >= len(*b) {
			return nil
		}
		switch s := (*b)[st.At].(type) {
		case *Loop:
			if st.Branch != 0 {
				return nil
			}
			b = &s.Body
		case *If:
			switch st.Branch {
			case 0:
				b = &s.Then
			case 1:
				b = &s.Else
			default:
				return nil
			}
		default:
			return nil
		}
	}
	return b
}

// BlockPoint is a program point inside a nested block: the value of V just
// after statement At of the block at Path.
type BlockPoint struct {
	Path BlockPath
	At   int
	V    Var
}

// BlockPoints are f's points inside nested blocks (loop bodies and if
// branches at any depth), in pre-order: after a definition of a non-slice
// local in the block, and after a nested loop or if that reassigns a local
// defined earlier in the same block. A point is listed only if no later
// statement of the block reassigns its variable, so every later read in the
// block sees the point's value. Points at the top level are Points.
func BlockPoints(f *Func) []BlockPoint {
	var out []BlockPoint
	var walk func(p BlockPath, ss []Stmt)
	walk = func(p BlockPath, ss []Stmt) {
		if len(p) > 0 {
			local := map[int]bool{}
			lastWrite := map[int]int{}
			for i, s := range ss {
				for _, w := range Writes(s) {
					lastWrite[w.ID] = i
				}
			}
			for i, s := range ss {
				add := func(v Var) {
					if j, ok := lastWrite[v.ID]; (!ok || j <= i) && !v.T.Slice {
						out = append(out, BlockPoint{Path: append(BlockPath(nil), p...), At: i, V: v})
					}
				}
				switch s := s.(type) {
				case *Assign:
					if s.Define {
						local[s.Dst.ID] = true
						add(s.Dst)
					}
				case *Loop, *If:
					for _, w := range Writes(s) {
						if local[w.ID] {
							add(w)
						}
					}
				}
			}
		}
		for i, s := range ss {
			switch s := s.(type) {
			case *Loop:
				walk(append(append(BlockPath(nil), p...), Step{At: i}), s.Body)
			case *If:
				walk(append(append(BlockPath(nil), p...), Step{At: i}), s.Then)
				walk(append(append(BlockPath(nil), p...), Step{At: i, Branch: 1}), s.Else)
			}
		}
	}
	walk(nil, f.Body)
	return out
}

// VisibleAt are the variables in scope just after statement at of the block
// at p: parameters, then definitions before p's steps at each enclosing
// level (with each entered loop's induction variable), then the block's own
// definitions up to and including at. Order is definition order.
func (f *Func) VisibleAt(p BlockPath, at int) []Var {
	out := f.Params()
	b := f.Body
	defsOf := func(ss []Stmt) {
		for _, s := range ss {
			if a, ok := s.(*Assign); ok && a.Define {
				out = append(out, a.Dst)
			}
		}
	}
	for _, st := range p {
		defsOf(b[:st.At])
		switch s := b[st.At].(type) {
		case *Loop:
			out = append(out, s.Ind)
			b = s.Body
		case *If:
			if st.Branch == 0 {
				b = s.Then
			} else {
				b = s.Else
			}
		}
	}
	defsOf(b[:min(at+1, len(b))])
	return out
}

// Lines maps each statement of the function named name in src, printed by
// PrintFile from f, to its first and last line. The printer emits one Go
// statement per IR statement with the same nesting, so the two are walked in
// step.
func Lines(src []byte, name string, f *Func) map[Stmt][2]int {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "g.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
			decl = fd
		}
	}
	if decl == nil {
		return nil
	}
	out := map[Stmt][2]int{}
	var walk func(ss []Stmt, as []ast.Stmt)
	walk = func(ss []Stmt, as []ast.Stmt) {
		for i := 0; i < len(ss) && i < len(as); i++ {
			out[ss[i]] = [2]int{fset.Position(as[i].Pos()).Line, fset.Position(as[i].End()).Line}
			switch s := ss[i].(type) {
			case *Loop:
				if fs, ok := as[i].(*ast.ForStmt); ok {
					walk(s.Body, fs.Body.List)
				}
			case *If:
				if is, ok := as[i].(*ast.IfStmt); ok {
					walk(s.Then, is.Body.List)
					if eb, ok := is.Else.(*ast.BlockStmt); ok {
						walk(s.Else, eb.List)
					}
				}
			}
		}
	}
	walk(f.Body, decl.Body.List)
	return out
}
