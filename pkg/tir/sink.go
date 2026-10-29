// Copyright 2026 Scott Friedman
// SPDX-License-Identifier: Apache-2.0

package tir

// Sink makes f free of unused locals: it removes every existing sink and
// inserts _ = v directly after the definition of each local that is
// otherwise never read. Go rejects unused
// variables; sinking them keeps dead code as neutral material (PROJECT.md
// §4.2) instead of deleting it.
func (f *Func) Sink() {
	read := map[int]bool{} // read by a statement other than a sink
	Walk(f.Body, func(s Stmt, _ int) bool {
		if _, ok := s.(*Sink); !ok {
			for _, u := range Uses(s) {
				read[u.ID] = true
			}
		}
		return true
	})
	f.Body = sinkBlock(f.Body, read)
}

func sinkBlock(ss []Stmt, read map[int]bool) []Stmt {
	out := ss[:0:0]
	for _, s := range ss {
		if _, ok := s.(*Sink); ok {
			// Existing sinks are dropped; the definition below re-sinks
			// whatever still needs it, so each variable gets exactly one,
			// directly after its definition.
			continue
		}
		switch s := s.(type) {
		case *Loop:
			s.Body = sinkBlock(s.Body, read)
		case *If:
			s.Then = sinkBlock(s.Then, read)
			s.Else = sinkBlock(s.Else, read)
		}
		out = append(out, s)
		if a, ok := s.(*Assign); ok && a.Define && !read[a.Dst.ID] {
			out = append(out, &Sink{X: a.Dst})
		}
	}
	return out
}
