// Hand-written popcount genomes inside the v0 grammar, used by
// kernels/headroom_test.go to measure how much cheaper than the reference a
// correct genome can be (#77). Not evolved; the GA has not found these.
package candidates

import "math/bits"

func Ref(xs []uint64) int {
	n := 0
	for _, x := range xs {
		n += bits.OnesCount64(x)
	}
	return n
}

func Unroll2(xs []uint64) int {
	n := 0
	m := len(xs)
	h := m &^ 1
	for i := 0; i < h; i += 2 {
		a := xs[i]
		j := i + 1
		b := xs[j]
		n += bits.OnesCount64(a) + bits.OnesCount64(b)
	}
	if h < m {
		n += bits.OnesCount64(xs[h])
	}
	return n
}

func Unroll4(xs []uint64) int {
	n := 0
	m := len(xs)
	h := m &^ 3
	for i := 0; i < h; i += 4 {
		a := xs[i]
		b := xs[i+1]
		c := xs[i+2]
		d := xs[i+3]
		n += bits.OnesCount64(a) + bits.OnesCount64(b) + bits.OnesCount64(c) + bits.OnesCount64(d)
	}
	for i := h; i < m; i++ {
		n += bits.OnesCount64(xs[i])
	}
	return n
}
