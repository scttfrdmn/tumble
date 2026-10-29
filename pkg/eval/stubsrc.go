// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// sigShape is the Go spelling of a function signature's parameter and
// result types, which the generated harness and drivers are written in.
type sigShape struct {
	params  []string
	results []string
}

func shapeOf(fn *ssa.Function) (sigShape, error) {
	var s sigShape
	sig := fn.Signature
	for i := range sig.Params().Len() {
		t := sig.Params().At(i).Type()
		if !supported(t) {
			return s, fmt.Errorf("eval: %s: parameter type %s not supported by the stub", fn.Name(), t)
		}
		s.params = append(s.params, types.TypeString(t, nil))
	}
	for i := range sig.Results().Len() {
		t := sig.Results().At(i).Type()
		if !supported(t) {
			return s, fmt.Errorf("eval: %s: result type %s not supported by the stub", fn.Name(), t)
		}
		s.results = append(s.results, types.TypeString(t, nil))
	}
	return s, nil
}

// supported are the v0 kernel types: integers, bools, and slices of them.
func supported(t types.Type) bool {
	if sl, ok := t.(*types.Slice); ok {
		t = sl.Elem()
	}
	b, ok := t.(*types.Basic)
	return ok && b.Info()&(types.IsInteger|types.IsBoolean) != 0 && b.Info()&types.IsUntyped == 0
}

func (s sigShape) funcType() string {
	r := strings.Join(s.results, ", ")
	if len(s.results) > 1 {
		r = "(" + r + ")"
	}
	return fmt.Sprintf("func(%s) %s", strings.Join(s.params, ", "), r)
}

func (s sigShape) equal(o sigShape) bool {
	return slices.Equal(s.params, o.params) && slices.Equal(s.results, o.results)
}

// rewriteGen returns the generation source as package main. With fuel set,
// every loop body first spends one unit of tumbleFuel and panics with
// tumbleOutOfFuel when it runs out: native execution of a genome terminates
// even when its loop bounds come from data.
func rewriteGen(src []byte, fuel bool) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, GenFile, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	f.Name = ast.NewIdent("main")
	if fuel {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ForStmt:
				n.Body.List = append([]ast.Stmt{fuelStmt()}, n.Body.List...)
			case *ast.RangeStmt:
				n.Body.List = append([]ast.Stmt{fuelStmt()}, n.Body.List...)
			}
			return true
		})
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fuelStmt is: if tumbleFuel--; tumbleFuel < 0 { panic(tumbleOutOfFuel{}) }
// (written on one line by the printer, which is fine for a build input).
func fuelStmt() ast.Stmt {
	fuel := func() *ast.Ident { return ast.NewIdent("tumbleFuel") }
	return &ast.IfStmt{
		Init: &ast.IncDecStmt{X: fuel(), Tok: token.DEC},
		Cond: &ast.BinaryExpr{X: fuel(), Op: token.LSS, Y: &ast.BasicLit{Kind: token.INT, Value: "0"}},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{
			Fun:  ast.NewIdent("panic"),
			Args: []ast.Expr{&ast.CompositeLit{Type: ast.NewIdent("tumbleOutOfFuel")}},
		}}}},
	}
}

// harnessMain is the native harness: it reads {fuel, vectors} as JSON on
// stdin, runs every listed function on a fresh copy of every vector, and
// writes {name: [{out, fault}]} as JSON on stdout. Outputs are printed with
// fmt.Sprint and parsed back by type.
func harnessMain(shape sigShape, names []string) []byte {
	var b strings.Builder
	b.WriteString(`package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type tumbleOutOfFuel struct{}

var tumbleFuel int64

type tumbleArgs struct {
`)
	for i, p := range shape.params {
		fmt.Fprintf(&b, "\tA%d %s `json:\"a%d\"`\n", i, p, i)
	}
	b.WriteString(`}

type tumbleOut struct {
	Out   []string ` + "`json:\"out\"`" + `
	Fault string   ` + "`json:\"fault,omitempty\"`" + `
}

var tumbleFns = []struct {
	name string
	f    ` + shape.funcType() + `
}{
`)
	for _, n := range names {
		fmt.Fprintf(&b, "\t{%q, %s},\n", n, n)
	}
	b.WriteString(`}

func main() {
	var in struct {
		Fuel    int64        ` + "`json:\"fuel\"`" + `
		Vectors []tumbleArgs ` + "`json:\"vectors\"`" + `
	}
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		panic(err)
	}
	res := map[string][]tumbleOut{}
	for _, fn := range tumbleFns {
		outs := make([]tumbleOut, len(in.Vectors))
		for i, v := range in.Vectors {
			outs[i] = tumbleCall(fn.f, v, in.Fuel)
		}
		res[fn.name] = outs
	}
	if err := json.NewEncoder(os.Stdout).Encode(res); err != nil {
		panic(err)
	}
}

func tumbleCall(f ` + shape.funcType() + `, v tumbleArgs, fuel int64) (o tumbleOut) {
`)
	var args, rets []string
	for i, p := range shape.params {
		if strings.HasPrefix(p, "[]") {
			fmt.Fprintf(&b, "\ta%d := append(%s(nil), v.A%d...)\n", i, p, i)
		} else {
			fmt.Fprintf(&b, "\ta%d := v.A%d\n", i, i)
		}
		args = append(args, fmt.Sprintf("a%d", i))
	}
	for i := range shape.results {
		rets = append(rets, fmt.Sprintf("r%d", i))
	}
	b.WriteString(`	tumbleFuel = fuel
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(tumbleOutOfFuel); ok {
				o.Fault = "fuel"
			} else {
				o.Fault = fmt.Sprint(r)
			}
		}
	}()
`)
	fmt.Fprintf(&b, "\t%s := f(%s)\n", strings.Join(rets, ", "), strings.Join(args, ", "))
	for _, r := range rets {
		fmt.Fprintf(&b, "\to.Out = append(o.Out, fmt.Sprint(%s))\n", r)
	}
	b.WriteString("\treturn o\n}\n")
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		panic(fmt.Sprintf("eval: generated harness does not parse: %v\n%s", err, b.String()))
	}
	return out
}

// driverMain is a giri driver: main calls tumbleV0, tumbleV1, ..., and
// tumbleVi calls name on input vector i. giri's stack traces name each
// frame's function (with its definition line, not the call line), so a
// finding is attributed to vector i by the tumbleVi frame in its trace.
func driverMain(name string, in []Input) []byte {
	var b strings.Builder
	b.WriteString("package main\n\nfunc main() {\n")
	for i := range in {
		fmt.Fprintf(&b, "\ttumbleV%d()\n", i)
	}
	b.WriteString("}\n")
	for i, v := range in {
		var args []string
		for _, a := range v.Args {
			args = append(args, goLiteral(a))
		}
		fmt.Fprintf(&b, "\nfunc tumbleV%d() {\n\t_ = %s(%s)\n}\n", i, name, strings.Join(args, ", "))
	}
	return []byte(b.String())
}

// baselineFunc is a function of the given shape that returns zero values: a
// driver calling it measures the driver's own steps.
func baselineFunc(name string, shape sigShape) []byte {
	var b strings.Builder
	var params []string
	for i, p := range shape.params {
		params = append(params, fmt.Sprintf("a%d %s", i, p))
	}
	r := strings.Join(shape.results, ", ")
	if len(shape.results) > 1 {
		r = "(" + r + ")"
	}
	fmt.Fprintf(&b, "package main\n\nfunc %s(%s) %s {\n", name, strings.Join(params, ", "), r)
	var zs []string
	for i, t := range shape.results {
		fmt.Fprintf(&b, "\tvar z%d %s\n", i, t)
		zs = append(zs, fmt.Sprintf("z%d", i))
	}
	for i := range shape.params {
		fmt.Fprintf(&b, "\t_ = a%d\n", i)
	}
	fmt.Fprintf(&b, "\treturn %s\n}\n", strings.Join(zs, ", "))
	return []byte(b.String())
}

// goLiteral writes an input value as a Go literal.
func goLiteral(a any) string {
	switch a := a.(type) {
	case []uint64:
		return sliceLit("uint64", a)
	case []int:
		return sliceLit("int", a)
	case []int32:
		return sliceLit("int32", a)
	case []int64:
		return sliceLit("int64", a)
	case []uint32:
		return sliceLit("uint32", a)
	case []bool:
		return sliceLit("bool", a)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, bool:
		return fmt.Sprintf("%T(%v)", a, a)
	}
	panic(fmt.Sprintf("eval: no literal for %T", a))
}

func sliceLit[T any](elem string, xs []T) string {
	var parts []string
	for _, x := range xs {
		parts = append(parts, fmt.Sprint(x))
	}
	return fmt.Sprintf("[]%s{%s}", elem, strings.Join(parts, ", "))
}

// writeModule writes a main module named mod with the given files to dir.
func writeModule(dir, mod string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files["go.mod"] = fmt.Appendf(nil, "module %s\n\ngo 1.22\n", mod)
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			return err
		}
	}
	return nil
}
