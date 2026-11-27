package cli

import (
	"os"
	"strings"
	"testing"
)

// d2ProductNameTexts maps the fragments of Pi's messages that name the product to PiG's (D2, "Product name in messages",
// docs/parity/divergences/D2.md). A test that states Pi's text compares with PiG's through d2Text, so the one difference
// between the two is this table.
var d2ProductNameTexts = [][2]string{
	{"Session file is not a valid pi session: ", "Session file is not a valid pig session: "},
	{"Files may be in use by another pi session; ", "Files may be in use by another pig session; "},
}

// d2Text is Pi's text with each mapped fragment replaced by PiG's. A text that holds no mapped fragment is a mistake in the
// test: it panics.
func d2Text(piText string) string {
	pigText := piText
	for _, pair := range d2ProductNameTexts {
		pigText = strings.ReplaceAll(pigText, pair[0], pair[1])
	}
	if pigText == piText {
		panic("d2Text: no mapped product-name fragment in " + piText)
	}
	return pigText
}

// The help names PiG's variables; Pi's PI_ names keep working and are not listed.
func TestHelpNamesPigEnvironmentVariables(t *testing.T) {
	data, err := os.ReadFile("help_upstream.txt")
	if err != nil {
		t.Fatal(err)
	}
	help := string(data)
	for _, want := range []string{"(same as PIG_OFFLINE=1)\n", "\n  PIG_OFFLINE  ", "\n  PIG_SELF_UPDATE_COMMAND  "} {
		if !strings.Contains(help, want) {
			t.Errorf("help_upstream.txt lacks %q", want)
		}
	}
	for line := range strings.SplitSeq(help, "\n") {
		if strings.HasPrefix(line, "  PI_") || strings.Contains(line, "PI_OFFLINE") {
			t.Errorf("help_upstream.txt names a PI_ variable: %q", line)
		}
	}
}
