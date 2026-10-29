// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"math"
	"runtime"
	"testing"
)

func TestSpearman(t *testing.T) {
	cases := []struct {
		xs, ys []float64
		want   float64
	}{
		{[]float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}, 1},
		{[]float64{1, 2, 3, 4}, []float64{40, 30, 20, 10}, -1},
		{[]float64{1, 2, 3, 4, 5}, []float64{5, 6, 7, 8, 7}, 0.8207826816681233},
		{[]float64{1, 1, 2, 2}, []float64{1, 2, 3, 4}, 0.8944271909999159},
	}
	for _, c := range cases {
		if got := Spearman(c.xs, c.ys); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("Spearman(%v, %v) = %v, want %v", c.xs, c.ys, got, c.want)
		}
	}
	if !math.IsNaN(Spearman([]float64{1, 2}, []float64{1, 2})) || !math.IsNaN(Spearman([]float64{1, 1, 1}, []float64{1, 2, 3})) {
		t.Error("Spearman defined with fewer than 3 pairs or a constant side")
	}
}

func TestLoadAverage(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("load average read on darwin and linux only")
	}
	if l := LoadAverage(); l < 0 {
		t.Errorf("load average not read: %v", l)
	}
}
