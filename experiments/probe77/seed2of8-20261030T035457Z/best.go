package best

import (
	"math/bits"
)

func Best(xs []uint64) int {
	v0 := 0
	for i0 := 0; i0 < len(xs); i0++ {
		v1 := xs[i0]
		v2 := bits.OnesCount64(v1)
		v0 = v0 + v2
	}
	return v0
	for i1 := -2; i1 < len(xs); i1 += 3 {
		v0 = i1 + i1
		for i2 := 25; i2 < len(xs); i2++ {
			v0 = v0 ^ 16
		}
	}
	v3 := uint64(v0)
	v4 := v0 + v0
	for i3 := v0; i3 < len(xs); i3++ {
		v0 = min(v0, v4)
	}
	v5 := uint64(v0)
	_ = v5
	v6 := uint64(v4)
	_ = v6
	for i4 := v0; i4 < len(xs); i4++ {
		v4 = 29 & i4
	}
	v7 := uint64(v4)
	v8 := v3 + v7
	_ = v8
	return v4
	v9 := max(45, v3)
	_ = v9
	return v4
}
