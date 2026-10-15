//go:build !race

package jsstring

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// PF1 N6: JavaScript's .length is O(1); a 50 KB ASCII tool result must not cost a rune decode per byte. The word scan is about 10x the rune loop; the test asks for 3x.
func TestLengthCountsASCIIByWord(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU budget")
	}
	text := strings.Repeat("export const value = items.map((item) => item.id);\n", 1000)
	fast, _ := fastestRun(func() { lengthSink += Length(text) })
	slow, _ := fastestRun(func() { lengthSink += lengthByRune(text) })
	t.Logf("Length of %d ASCII bytes: %d ns/op; rune loop %d ns/op", len(text), fast, slow)
	testenv.TimingGateFatal(t, 3*fast > slow, "Length of %d ASCII bytes takes %d ns, the rune loop %d ns: want at least 3x faster", len(text), fast, slow)
}
