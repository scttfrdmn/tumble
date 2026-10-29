// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package pop

import (
	"go/ast"
	"go/parser"
	"go/token"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/tumble/pkg/eval"
	"github.com/scttfrdmn/tumble/pkg/index"
	"github.com/scttfrdmn/tumble/pkg/tir"
)

// topLevelLines maps each top-level definition of the function named name in
// the printed generation source to its line: the printer puts every
// statement on its own line, and names locals canonically, so the printed
// name identifies the IR variable.
func topLevelLines(src []byte, name string, f *tir.Func) map[int]tir.Var {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gen.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	byName := map[string]tir.Var{}
	names := f.Names()
	for _, s := range f.Body {
		if a, ok := s.(*tir.Assign); ok && a.Define {
			byName[names[a.Dst.ID]] = a.Dst
		}
	}
	out := map[int]tir.Var{}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name {
			continue
		}
		for _, st := range fd.Body.List {
			as, ok := st.(*ast.AssignStmt)
			if !ok || as.Tok != token.DEFINE {
				continue
			}
			if id, ok := as.Lhs[0].(*ast.Ident); ok {
				if v, ok := byName[id.Name]; ok {
					out[fset.Position(as.Pos()).Line] = v
				}
			}
		}
	}
	return out
}

// VarFingerprints attributes trace fingerprints to f's top-level
// definitions: the value a definition's variable holds is the last value
// (in instruction order) the SSA function computes on that statement's line.
// A definition with no instruction of its own (a copy or a constant) gets
// no fingerprint. src is the printed generation file and name the genome's
// function in it.
func VarFingerprints(src []byte, name string, f *tir.Func, fn *ssa.Function, traces []eval.ValueTrace) map[int]index.Fingerprint {
	if len(traces) == 0 {
		return nil
	}
	lines := topLevelLines(src, name, f)
	order := map[ssa.Value]int{}
	n := 0
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if v, ok := ins.(ssa.Value); ok {
				order[v] = n
			}
			n++
		}
	}
	type pick struct {
		at int
		tr eval.ValueTrace
	}
	best := map[int]pick{} // var ID → latest value on its line
	fset := fn.Prog.Fset
	for _, tr := range traces {
		at, ok := order[tr.Value]
		if !ok || !tr.Value.Pos().IsValid() {
			continue
		}
		v, ok := lines[fset.Position(tr.Value.Pos()).Line]
		if !ok {
			continue
		}
		if cur, seen := best[v.ID]; !seen || at > cur.at {
			best[v.ID] = pick{at, tr}
		}
	}
	out := map[int]index.Fingerprint{}
	for id, p := range best {
		if fp, ok := index.FromTrace(p.tr); ok {
			out[id] = fp
		}
	}
	return out
}
