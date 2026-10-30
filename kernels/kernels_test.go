// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package kernels

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestProblems(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) { testProblem(t, name) })
	}
}

func testProblem(t *testing.T, name string) {
	k, err := Get(name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := k.Problem()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Reference.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := p.Grammar.Admits(p.Reference); err != nil {
		t.Fatalf("reference outside its own grammar: %v", err)
	}
	if len(p.Selection) != len(p.Want) || len(p.Selection) == 0 {
		t.Fatalf("%d vectors, %d outputs", len(p.Selection), len(p.Want))
	}
}

func TestReferenceArgsAfter(t *testing.T) {
	k, _ := Get("popcount")
	p, err := k.Problem()
	if err != nil {
		t.Fatal(err)
	}
	for i, in := range p.Selection {
		if !reflect.DeepEqual(p.WantAfter[i], in.Args) {
			t.Fatalf("vector %d: reference left %#v, want its input unchanged", i, p.WantAfter[i])
		}
	}
}

// TestKernelsImportOnlyStdlib: the native stage copies a kernel's directory
// as a standalone module, so a kernel package may import only the standard
// library (a module-internal import breaks native verification).
func TestKernelsImportOnlyStdlib(t *testing.T) {
	for _, name := range Names() {
		k, _ := Get(name)
		pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedImports | packages.NeedName, Dir: "."}, "./"+filepath.Base(k.Native.Dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pkgs {
			for path := range p.Imports {
				if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
					t.Errorf("%s imports %s; kernels may import only the standard library", name, path)
				}
			}
		}
	}
}
