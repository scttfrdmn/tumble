// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package eval

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// GenModule is the module path of a generation package. Its first path
// element has a dot: giri treats dotless import paths as the standard
// library and does not interpret calls into them (scttfrdmn/giri#249).
const GenModule = "tumble.gen/g"

// GenFile is the name of the generation's source file in its directory.
const GenFile = "gen.go"

// WriteGeneration writes a generation package to dir: a go.mod for
// GenModule and src as GenFile. src's package clause is the caller's.
func WriteGeneration(dir string, src []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mod := fmt.Sprintf("module %s\n\ngo 1.22\n", GenModule)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, GenFile), src, 0o644)
}

// ssaProgram is a loaded generation package.
type ssaProgram struct {
	dir   string
	pkg   *ssa.Package
	funcs map[string]*ssa.Function
}

func (p *ssaProgram) Funcs() map[string]*ssa.Function { return p.funcs }

// LoadSSA type-checks and builds the package in dir with go/ssa: one build
// for every function of the generation.
func LoadSSA(dir string) (Program, error) { return loadSSA(dir) }

func loadSSA(dir string) (*ssaProgram, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir: dir,
		Env: append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod"),
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(pkgs) > 0 || len(pkgs) != 1 {
		return nil, fmt.Errorf("eval: loading %s failed", dir)
	}
	prog, spkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()
	p := &ssaProgram{dir: dir, pkg: spkgs[0], funcs: map[string]*ssa.Function{}}
	for name, m := range p.pkg.Members {
		if fn, ok := m.(*ssa.Function); ok && name != "init" {
			p.funcs[name] = fn
		}
	}
	return p, nil
}
