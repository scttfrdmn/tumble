package best

func Best(xs []int32) int {
	v0 := -1
	v1 := int32(-13)
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
	for i1 := 0; i1 < 20; i1++ {
		v1 = 9 + v1
	}
	v6 := max(v1, 25)
	v7 := v6 * 45
	return v0
	v8 := 58 - v7
	v9 := v8 << 17
	return v0
	v10 := 58 + v9
	return v0
	v11 := v10 - v6
	_ = v11
	v12 := 58 + v10
	_ = v12
	return -1
}
