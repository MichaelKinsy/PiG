package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi segments words and graphemes with Intl.Segmenter (ICU 78, Unicode 17; tui-alt-screen.ts:1179, utils.ts). Stock Pig uses internal/wordsegmenter and widthx.FirstGrapheme for both, so the Unicode 15 rivo/uniseg segmenter, which splits Thai words and Devanagari conjuncts differently, must not be linked into it.
func TestStockPigDoesNotLinkUniseg(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "./cmd/pig")
	cmd.Dir = testenv.ModuleRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list Stock Pig dependencies: %v\n%s", err, output)
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(line, "github.com/rivo/uniseg") {
			t.Fatalf("Stock Pig links %s; segment with internal/wordsegmenter or widthx.FirstGrapheme", line)
		}
	}
}
