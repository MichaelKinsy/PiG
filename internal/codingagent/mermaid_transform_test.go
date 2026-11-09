//go:build !pig_strip_mermaid

package codingagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestMermaidTransformMatchesUpstream diffs pig's Mermaid markdown transformer
// against pi's own createMermaidMarkdownTransformer (compiled mermaid.js, no
// theme → plain art) over render / gating / warnings / width / identity cases.
func TestMermaidTransformMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/mermaid-transform-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()

	var total, changed int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var g struct {
			Input struct {
				MD             string `json:"md"`
				Mode           string `json:"mode"`
				MessageType    string `json:"messageType"`
				IsStreaming    bool   `json:"isStreaming"`
				AvailableWidth int    `json:"availableWidth"`
			} `json:"input"`
			Out string `json:"out"`
		}
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("bad golden %q: %v", sc.Bytes(), err)
		}
		total++
		if g.Out != g.Input.MD {
			changed++
		}

		transformer := createMermaidMarkdownTransformer(func() string { return g.Input.Mode }, nil)
		got := transformer(g.Input.MD, extension.MarkdownTransformContext{
			MessageType:    extension.MarkdownMessageType(g.Input.MessageType),
			IsStreaming:    g.Input.IsStreaming,
			AvailableWidth: g.Input.AvailableWidth,
		})
		if got != g.Out {
			t.Errorf("transform mismatch (mode=%s msg=%s stream=%v w=%d)\n  md:  %q\n  go:  %q\n  pi:  %q",
				g.Input.Mode, g.Input.MessageType, g.Input.IsStreaming, g.Input.AvailableWidth, g.Input.MD, got, g.Out)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total == 0 {
		t.Fatal("no goldens")
	}
	t.Logf("checked %d byte-exact transform cases (%d rewrote the markdown)", total, changed)
}

// TestMermaidThemedTransformMatchesUpstream diffs the themed transformer
// (themedLines: the role to theme-color map, the slice(2, -1) of each escape,
// the bold title, classDef over the theme, hyperlinks only on node text and
// only on terminals with hyperlinks, the themed warning) against Pi's own
// createMermaidMarkdownTransformer driven with a {getFgAnsi, fg} theme built
// from Go's ActiveTheme palette (truecolor and 256) and the terminal's
// hyperlink capability. The goldens carry the palette they were generated
// with; a theme change that moves it needs a regeneration.
func TestMermaidThemedTransformMatchesUpstream(t *testing.T) {
	checkMermaidThemedGoldens(t)
}

// TestMermaidThemedTransformIgnoresTheActiveTheme runs the themed goldens with each theme and color depth another test can leave active
// (the process-global theme is shared by every test in the package, and a test that selects the light theme, or a theme by name in a 256-color
// environment, leaves it selected for whichever test runs next under -shuffle or a narrower -run). The goldens name their own palette and color
// depth, so the result must not depend on which theme was active.
func TestMermaidThemedTransformIgnoresTheActiveTheme(t *testing.T) {
	for _, name := range []string{"dark", "light"} {
		for _, truecolor := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/truecolor=%v", name, truecolor), func(t *testing.T) {
				restoreStartupTheme(t)
				tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: truecolor})
				tui.SetThemeByName(name)
				checkMermaidThemedGoldens(t)
			})
		}
	}
}

func checkMermaidThemedGoldens(t *testing.T) {
	t.Helper()
	f, err := os.Open("testdata/mermaid-transform-themed-golden.jsonl")
	if err != nil {
		t.Fatalf("open goldens: %v", err)
	}
	defer func() { _ = f.Close() }()
	t.Cleanup(tui.ResetCapabilitiesCache)

	var total, styled, linked int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var g struct {
			Input struct {
				MD             string `json:"md"`
				Mode           string `json:"mode"`
				MessageType    string `json:"messageType"`
				IsStreaming    bool   `json:"isStreaming"`
				AvailableWidth int    `json:"availableWidth"`
				Theme          struct {
					ColorMode  string               `json:"colorMode"`
					Hyperlinks bool                 `json:"hyperlinks"`
					Palette    map[string]string    `json:"palette"`
					Fg         map[string][2]string `json:"fg"`
				} `json:"theme"`
			} `json:"input"`
			Out string `json:"out"`
		}
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatalf("bad golden %q: %v", sc.Bytes(), err)
		}
		total++
		// The active theme is created in the color depth the environment reports (COLORTERM) when an earlier test selects a theme by name, so the mode the golden names is set here and not assumed.
		theme := tui.ActiveTheme().WithColorMode(tui.TerminalColorModeTrueColor)
		if g.Input.Theme.ColorMode == "256" {
			theme = theme.WithColorMode(tui.TerminalColorMode256)
		}
		for color, want := range g.Input.Theme.Palette {
			if got := theme.GetFgAnsi(color); got != want {
				t.Fatalf("theme %s %s is %q, the golden was generated with %q: regenerate the goldens", g.Input.Theme.ColorMode, color, got, want)
			}
			prefix, suffix, _ := strings.Cut(theme.Fg(color, "\x00"), "\x00")
			if want := g.Input.Theme.Fg[color]; prefix != want[0] || suffix != want[1] {
				t.Fatalf("theme %s Fg(%s) wraps with %q, %q, the golden was generated with %q: regenerate the goldens", g.Input.Theme.ColorMode, color, prefix, suffix, want)
			}
		}
		if strings.Contains(g.Out, "\x1b[") {
			styled++
		}
		if strings.Contains(g.Out, "\x1b]8;;") {
			linked++
		}
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true, Hyperlinks: g.Input.Theme.Hyperlinks})
		got := createMermaidMarkdownTransformer(func() string { return g.Input.Mode }, theme)(g.Input.MD, extension.MarkdownTransformContext{
			MessageType:    extension.MarkdownMessageType(g.Input.MessageType),
			IsStreaming:    g.Input.IsStreaming,
			AvailableWidth: g.Input.AvailableWidth,
		})
		if got != g.Out {
			t.Errorf("themed transform mismatch (theme=%s links=%v msg=%s stream=%v)\n  md:  %q\n  go:  %q\n  pi:  %q",
				g.Input.Theme.ColorMode, g.Input.Theme.Hyperlinks, g.Input.MessageType, g.Input.IsStreaming, g.Input.MD, got, g.Out)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total == 0 || styled == 0 || linked == 0 {
		t.Fatalf("goldens cover nothing: %d cases, %d styled, %d with OSC 8", total, styled, linked)
	}
	t.Logf("checked %d byte-exact themed transform cases (%d styled, %d with OSC 8)", total, styled, linked)
}
