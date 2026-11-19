package sdk

import "testing"

// BenchmarkContextActiveReads reads the theme and the terminal size of a context that is not stale: the members that fail with the stale message after a session replacement
// must cost one atomic load on the active one, with no host call and no allocation.
func BenchmarkContextActiveReads(b *testing.B) {
	ctx := Context{ext: New("active-reads")}
	b.ReportAllocs()
	for b.Loop() {
		_ = ctx.Width()
		_ = ctx.Height()
		_ = ctx.UITheme()
	}
}
