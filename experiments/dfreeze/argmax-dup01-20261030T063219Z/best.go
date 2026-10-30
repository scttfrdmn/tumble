package best

func Best(xs []int32) int {
	v0 := -1
	v1 := int32(-6)
	for i0 := 0; i0 < len(xs); i0++ {
		v2 := xs[i0]
		v3 := v0 < 0
		v4 := v2 > v1
		v5 := v4 || v3
		if v5 {
			v1 = v2
			v0 = i0
		}
	}
	return v0
	for i1 := v0; i1 < len(xs); i1++ {
		for i2 := 0; i2 < len(xs); i2++ {
			v1 = v1 + v1
		}
	}
	v6 := v1 >> 15
	v7 := v6 << 18
	v8 := 11 > v7
	_ = v8
	v9 := 38 + v0
	v10 := v7 + v7
	v11 := v1 + v10
	_ = v11
	return v9
}
