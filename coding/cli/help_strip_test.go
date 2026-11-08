package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): a Piglet that strips self-update lists `pig update [source]` in --help, without the self targets
// that Pi 1.1.0 args.ts:293 lists as `update [source|self|pi]` and that Stock PiG prints as `[source|self|pig]`.
func TestHelpWithoutSelfUpdateListsOnlyTheSourceTarget(t *testing.T) {
	var stock bytes.Buffer
	printHelp(&stock, false)
	if !strings.Contains(stock.String(), "  pig update [source|self|pig]   Update pig, extensions, or model catalogs\n") {
		t.Fatalf("stock help lacks Pi's update line:\n%s", stock.String())
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.SelfUpdate))
	var stripped bytes.Buffer
	printHelp(&stripped, false)
	if !strings.Contains(stripped.String(), "  pig update [source]           Update extensions or model catalogs\n") || strings.Contains(stripped.String(), "source|self") {
		t.Fatalf("help without self-update:\n%s", stripped.String())
	}
}

// pig additive (D92): the --tools examples drop whole when one of their tools is stripped, also when the tool carries the + or - modifier of the
// default tools (`pig --tools +codemode`), so a build without codemode does not show an example for it.
func TestHelpWithoutCodemodeDropsTheToolModifierExample(t *testing.T) {
	const example, comment = "  pig --tools +codemode\n", "# Add codemode to the default tools\n"
	var stock bytes.Buffer
	printHelp(&stock, false)
	if !strings.Contains(stock.String(), example) || !strings.Contains(stock.String(), comment) {
		t.Fatalf("stock help lacks the +codemode example:\n%s", stock.String())
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListExtensions, "codemode"))
	var stripped bytes.Buffer
	printHelp(&stripped, false)
	if strings.Contains(stripped.String(), example) || strings.Contains(stripped.String(), comment) {
		t.Fatalf("help without codemode keeps its +codemode example:\n%s", stripped.String())
	}
	if !strings.Contains(stripped.String(), "pig --tools read,grep,find,ls -p") {
		t.Fatalf("help without codemode lost an example that names no stripped tool:\n%s", stripped.String())
	}
	modified := []string{"  pig --tools +write\n", "  pig --tools -write,+read\n"}
	for _, line := range modified {
		if _, keep := stripHelpLine(line); !keep {
			t.Fatalf("stripHelpLine dropped %q with write kept", line)
		}
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListTools, "write"))
	for _, line := range modified {
		if _, keep := stripHelpLine(line); keep {
			t.Fatalf("stripHelpLine kept %q with write stripped", line)
		}
	}
}
