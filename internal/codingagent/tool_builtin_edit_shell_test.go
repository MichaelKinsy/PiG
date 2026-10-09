// The golden rows are Pi 1.0.4 rendering on a POSIX host: paths and file URLs differ on Windows.

//go:build !windows

package codingagent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// The stock edit tool has no extension definition, yet Pi's edit card draws its own box (core/tools/edit.ts renderShell:
// "self"): ToolExecutionComponent.render returns a blank row and the call renderer's rows, with no content box around them.
// The rows are Pi 1.0.4's, from the probe's "edit:ok" call once its preview diff settled.
// Pi: packages/coding-agent/src/modes/interactive/components/tool-execution.ts:175 (ToolExecutionComponent.setArgsComplete).
func TestStockEditCardDrawsItsOwnShell(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pi104-render-probe", "builtin-renderers.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Width int            `json:"width"`
		Cases []piRenderCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	var want []string
	var args json.RawMessage
	for _, c := range golden.Cases {
		if c.Name == "edit:ok" {
			want, args = append([]string{""}, c.Lines[2]...), c.Steps[0].Args
		}
	}
	if want == nil {
		t.Fatal("golden has no edit:ok case")
	}
	cwd := shortTempDir(t)
	if err := os.WriteFile(filepath.Join(cwd, "e.txt"), []byte("alpha\nbeta\ngamma\ndelta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_HYPERLINKS", "1")
	// Pi's probe recorded truecolor SGR sequences; a CI shell without COLORTERM detects 256 colors.
	t.Setenv("PI_TRUE_COLOR", "1")
	// Cleanups run last-first: the capability cache drops the pinned mode, then the previous theme is rebuilt in the
	// restored environment, so no later test inherits this truecolor theme.
	previousTheme := tui.ActiveTheme().Name
	t.Cleanup(func() { tui.SetThemeByName(previousTheme) })
	tui.ResetCapabilitiesCache()
	t.Cleanup(tui.ResetCapabilitiesCache)
	tui.SetTheme("dark")
	m := &InteractiveMode{opts: InteractiveModeOptions{CWD: cwd}, chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, golden.Width, 40)}
	renders := make(chan func(), 4)
	m.tuiInst.SetRenderDispatcher(func(render func()) { renders <- render })
	t.Cleanup(m.tuiInst.CancelPendingRender)
	m.newRunner = inproc.NewRunner(nil, cwd)
	card := newToolCardForTest("edit", tui.HeaderForTool("edit", args, cwd))
	card.Cwd = cwd
	card.SetHeaderArgs(args)
	m.applyToolPresentation(card, "c", "edit", args)
	m.chatContainer.Add(card)
	m.tuiInst.Add(m.chatContainer)
	m.tuiInst.Render()
	card.SetArgsComplete()
	m.tuiInst.Render()
	select {
	case render := <-renders:
		render()
	case <-time.After(10 * time.Second):
		t.Fatal("the edit preview never requested a render")
	}
	got := card.Render(golden.Width)
	for i := range got {
		got[i] = strings.ReplaceAll(got[i], cwd, "{{CWD}}")
	}
	for i := range want {
		// The golden pads rows naming the probe's directory to its own path width.
		if strings.Contains(want[i], "{{") && i < len(got) {
			want[i], got[i] = strings.TrimRight(want[i], " "), strings.TrimRight(got[i], " ")
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stock edit card\n got: %q\nwant: %q", got, want)
	}
}
