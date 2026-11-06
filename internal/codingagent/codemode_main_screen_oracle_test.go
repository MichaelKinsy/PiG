package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// funcComponent is a tui.Component drawn by a function.
type funcComponent struct{ render func(width int) []string }

func (c funcComponent) Render(width int) []string { return c.render(width) }
func (c funcComponent) Invalidate()               {}

// A codemode call row whose arguments the card cuts inside a surrogate pair keeps a lone surrogate, which Pi measures as nothing and
// writes as U+FFFD. The card pads the row to the terminal width, so measuring it after the write's encoding (one cell wider) crashed
// the main screen with "Rendered line N exceeds terminal width" where Pi draws the card. Both sides draw the card, after a header
// row, through the main screen's differential render, and the bytes written (or the error thrown) are compared with Pi's.
func TestCodemodeCardCutInsideSurrogatePairMatchesPiMainScreen(t *testing.T) {
	probes, names := codemodeSurrogateCutProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/codemode_main_screen.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Error  *string `json:"error"`
		Output string  `json:"output"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	cut := 0
	driveCodemodeCards(t, probes, func(i int, f toolComponentFixture) {
		var out bytes.Buffer
		ui := tui.NewWithOutput(&out, probes[i].Width, 40)
		ui.SetLogDirectory(t.TempDir())
		shown := false
		ui.Add(funcComponent{func(width int) []string {
			if !shown {
				return []string{"header"}
			}
			return append([]string{"header"}, f.card.Render(width)...)
		}})
		ui.Render()
		out.Reset()
		shown = true
		var gotError *string
		func() {
			defer func() {
				if value := recover(); value != nil {
					first, _, _ := strings.Cut(fmt.Sprint(value), "\n")
					gotError = &first
				}
			}()
			ui.Render()
		}()
		want := expected[i]
		if (gotError == nil) != (want.Error == nil) || (gotError != nil && *gotError != *want.Error) {
			t.Errorf("%s: Pig error %v, Pi error %v", names[i], optionalErrorText(gotError), optionalErrorText(want.Error))
			return
		}
		if gotError == nil && out.String() != want.Output {
			at := 0
			for at < len(out.String()) && at < len(want.Output) && out.String()[at] == want.Output[at] {
				at++
			}
			from := max(at-40, 0)
			t.Errorf("%s: first difference at byte %d\n  Pig %q\n  Pi  %q", names[i], at, out.String()[from:min(at+60, out.Len())], want.Output[from:min(at+60, len(want.Output))])
		}
		if strings.Contains(want.Output, "\ufffd") {
			cut++
		}
	})
	if cut == 0 {
		t.Fatal("no probe cut inside a surrogate pair: the corpus does not reach the case")
	}
}

func optionalErrorText(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

// codemodeSurrogateCutProbes are calls whose arguments the collapsed card cuts at 77 UTF-16 units, at every parity of the cut
// against the emoji (a pair split, a pair kept), at widths that fit and exactly fill the padded row.
func codemodeSurrogateCutProbes() ([]toolCardProbe, []string) {
	var probes []toolCardProbe
	var names []string
	for _, width := range []int{120, 100, 80, 40} {
		for _, prefix := range []int{0, 1, 2, 3, 4, 40} {
			for _, expanded := range []bool{false, true} {
				args := strings.Repeat("a", prefix) + strings.Repeat("😀", 41)
				calls := []any{map[string]any{"name": "read", "args": args, "status": "ok", "durationMs": 12}}
				probes = append(probes, toolCardProbe{
					Tool: "codemode", Args: map[string]any{"code": "return 1"}, Expanded: expanded, Width: width, CWD: "/tmp",
					Result: &toolCardResult{Content: cardText("Script completed\nWall time 0.1 seconds\nOutput:\nok"), Details: map[string]any{"calls": calls}},
				})
				names = append(names, fmt.Sprintf("prefix=%d width=%d expanded=%v", prefix, width, expanded))
			}
		}
	}
	return probes, names
}
