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
			v1 = v2
			v0 = i0
		}
	}
	return v0
	v6 := v1 & 12
	v7 := int(v6)
	v8 := v7 + v7
	v9 := v8 << 14
	v10 := v9 << 14
	v11 := v10 << 14
	v12 := min(11, v11)
	return v12
}
