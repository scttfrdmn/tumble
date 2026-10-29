// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package kernels

import (
	"reflect"
	"testing"
)

func TestPopcountProblem(t *testing.T) {
	k, err := Get("popcount")
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
