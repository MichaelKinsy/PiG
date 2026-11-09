package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type extensionErrorProbe struct {
	Path   string  `json:"path"`
	Error  string  `json:"error"`
	Stack  *string `json:"stack"`
	Widths []int   `json:"widths"`
}

type extensionErrorRendering struct {
	Dark     [][]string `json:"dark"`
	Light    [][]string `json:"light"`
	Children int        `json:"children"`
}

func extensionErrorProbes() []extensionErrorProbe {
	stacks := map[string]*string{
		"no stack":                nil,
		"empty stack":             new(""),
		"single line":             new("Error: boom"),
		"trailing newline":        new("Error: boom\n"),
		"two frames":              new("Error: boom\n    at first (a.js:1:1)\n    at second (b.js:2:2)"),
		"tab frame trailing pad":  new("Error: boom\n\tat tabbed (a.js:1:1)   \n   \nat last (b.js:2:2)"),
		"blank frames only":       new("Error: boom\n\n\n"),
		"crlf":                    new("Error: boom\r\n    at crlf (a.js:1:1)\r\n    at crlf2 (a.js:2:2)\r\n"),
		"unicode whitespace":      new("Error: boom\n\u00a0\u2003at nbsp (a.js:1:1)\u3000\n\ufeffat bom (a.js:2:2)\ufeff\n\u0085at nel (a.js:3:3)\u0085"),
		"wide and combining":      new("Error: boom\n    at 日本語のとても長い関数名 (ファイル.js:1:1)\n    at e\u0301\u0301 (😀.js:2:2)"),
		"long frame wraps":        new("Error: boom\n    at someVeryLongFunctionName (/a/very/long/path/to/some/module/file-name.js:123:456)"),
		"ansi in frame":           new("Error: boom\n    \x1b[31mat red\x1b[0m (a.js:1:1)"),
		"first line only is cut":  new("first\nsecond\nthird"),
		"message with newlines":   new("Error: a\nb\n    at c (d.js:1:1)"),
		"one frame no indent":     new("Error\nat x"),
		"stack of only newlines":  new("\n\n"),
		"stack with only newline": new("\n"),
	}
	names := make([]string, 0, len(stacks))
	for name := range stacks {
		names = append(names, name)
	}
	slices.Sort(names)
	var probes []extensionErrorProbe
	for _, name := range names {
		for _, message := range []string{"failed", "a much longer failure message that has to wrap at the narrow widths of the probe", "日本語 failure"} {
			probes = append(probes, extensionErrorProbe{Path: "/ext/" + name + "/index.js", Error: message, Stack: stacks[name], Widths: []int{100, 40, 12, 3}})
		}
	}
	return probes
}

// InteractiveMode.showExtensionError (interactive-mode.ts) against the pinned Pi, which draws the message in the error colour and below it renderStack's block: the stack
// without its first line, every line trimmed (JavaScript whitespace, so U+FEFF goes and U+0085 stays), prefixed with two spaces and dimmed, in one padded text that
// takes the active theme when the tree is invalidated. The corpus covers a missing, empty, one-line and many-line stack, CRLF, blank frames, Unicode whitespace, wide and wrapping
// frames, and a theme change after the rows are added, at four widths.
func TestExtensionErrorStackMatchesPi(t *testing.T) {
	probes := extensionErrorProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/extension_error_stack.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []extensionErrorRendering
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(probes) {
		t.Fatalf("Pi answered %d probes, want %d", len(want), len(probes))
	}
	// The probes select the light theme and leave it selected; restoreStartupTheme puts the previous theme and capabilities back.
	restoreStartupTheme(t)
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	failures := 0
	for i, probe := range probes {
		tui.SetTheme("dark")
		m := newExtensionErrorProbe(t)
		stack := ""
		if probe.Stack != nil {
			stack = *probe.Stack
		}
		m.showExtensionError(probe.Path, probe.Error, stack)
		render := func(theme string) [][]string {
			tui.SetTheme(theme)
			m.chatContainer.Invalidate()
			rows := make([][]string, len(probe.Widths))
			for k, width := range probe.Widths {
				rows[k] = m.chatContainer.Render(width)
			}
			return rows
		}
		got := extensionErrorRendering{Dark: render("dark"), Light: render("light"), Children: m.chatContainer.ChildCount()}
		if !slices.EqualFunc(got.Dark, want[i].Dark, slices.Equal) || !slices.EqualFunc(got.Light, want[i].Light, slices.Equal) || got.Children != want[i].Children {
			if failures++; failures <= 4 {
				variant, gotRows, wantRows := "dark", got.Dark, want[i].Dark
				if slices.EqualFunc(got.Dark, want[i].Dark, slices.Equal) {
					variant, gotRows, wantRows = "light", got.Light, want[i].Light
				}
				k := 0
				for k < len(gotRows)-1 && slices.Equal(gotRows[k], wantRows[k]) {
					k++
				}
				t.Errorf("probe %d (%q, stack %q) %s width %d:\n  Pig %q children=%d\n  Pi  %q children=%d", i, probe.Path, stack, variant, probe.Widths[k], gotRows[k], got.Children, wantRows[k], want[i].Children)
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
