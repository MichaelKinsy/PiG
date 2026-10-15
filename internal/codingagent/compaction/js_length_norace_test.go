//go:build !race

package compaction

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var jsLengthSink int

// PF1 N6: the compaction estimate measures the trailing tool result's text after every tool turn; a 50 KB ASCII text must not cost a rune decode per byte. The word scan is about 10x the rune loop; the test asks for 3x.
func TestJSLengthCountsASCIIByWord(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	text := strings.Repeat("export const value = items.map((item) => item.id);\n", 1000)
	fastest := func(run func()) int64 {
		best := int64(1 << 62)
		for range 5 {
			best = min(best, testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					run()
				}
			}).NsPerOp())
		}
		return best
	}
	fast := fastest(func() { jsLengthSink += jsLength(text) })
	slow := fastest(func() { jsLengthSink += jsLengthByRune(text) })
	t.Logf("jsLength of %d ASCII bytes: %d ns/op; rune loop %d ns/op", len(text), fast, slow)
	testenv.TimingGateFatal(t, 3*fast > slow, "jsLength of %d ASCII bytes takes %d ns, the rune loop %d ns: want at least 3x faster", len(text), fast, slow)
}
