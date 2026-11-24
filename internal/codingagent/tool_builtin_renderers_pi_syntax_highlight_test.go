//go:build !windows && !pig_strip_syntax_highlight

package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestBuiltInRenderersMatchPi renders every case of the committed Pi 1.5.2+562da75f9 probe
// (testdata/pi-render-probe/render-probe.mjs, run against Pi's built renderers)
// through PiG's built-in read, write and edit renderers and compares the rows.
// Pi: packages/coding-agent/src/core/extensions/types.ts:463 (ToolRenderResultOptions.isPartial).
func TestBuiltInRenderersMatchPi(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pi-render-probe", "builtin-renderers.golden.json"))
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
	cwd, home := shortTempDir(t), shortTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(cwd, "e.txt"), []byte("alpha\nbeta\ngamma\ndelta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subst := func(s string) string { return strings.NewReplacer("{{CWD}}", cwd, "{{HOME}}", home).Replace(s) }
	unsubst := func(s string) string { return strings.NewReplacer(cwd, "{{CWD}}", home, "{{HOME}}").Replace(s) }
	t.Setenv("PI_HYPERLINKS", "1")
	t.Setenv("PI_IMAGE_PROTOCOL", "none")
	// Pi's probe recorded truecolor SGR sequences; a CI shell without COLORTERM detects 256 colors.
	t.Setenv("PI_TRUE_COLOR", "1")
	// Cleanups run last-first: the capability cache drops the pinned mode, then the previous theme is rebuilt in the
	// restored environment, so no later test inherits this truecolor theme.
	previousTheme := tui.ActiveTheme().Name
	t.Cleanup(func() { tui.SetThemeByName(previousTheme) })
	tui.ResetCapabilitiesCache()
	t.Cleanup(tui.ResetCapabilitiesCache)
	tui.SetTheme("dark")
	// Pi's edit preview settles in a promise continuation, after the render that follows renderCall and before the probe's
	// "wait" step ends. Each case holds PiG's preview until its "wait" step, so the first frame never races the preview.
	var previewGate atomic.Pointer[chan struct{}]
	compute := computeEditsDiff
	computeEditsDiff = func(path string, edits []tools.EditReplacement, cwd string) tools.EditDiffOutcome {
		if gate := previewGate.Load(); gate != nil {
			<-*gate
		}
		return compute(path, edits, cwd)
	}
	t.Cleanup(func() { computeEditsDiff = compute })

	for _, c := range golden.Cases {
		t.Run(c.Name, func(t *testing.T) {
			gate := make(chan struct{})
			previewGate.Store(&gate)
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(gate) }) }
			defer release()
			state := map[string]any{}
			last := map[string]extension.Component{}
			invalidated := make(chan struct{}, 64)
			var got [][]string
			var prev piRenderStep
			for _, step := range c.Steps {
				stepPrev := prev
				prev = step
				args := json.RawMessage(subst(string(step.Args)))
				ctx := extension.ToolRenderContext{
					Args: args, ToolCallID: "c", State: state, Cwd: cwd, ExecutionStarted: true, ArgsComplete: true, ShowImages: true, OutputPad: 1, // the 1.0.4 oracle renders every edit box with a fixed one-cell inset (outputPad 1)
					Expanded: step.Ctx.Expanded, IsPartial: step.Ctx.IsPartial, IsError: step.Ctx.IsError,
					Invalidate: func() {
						select {
						case invalidated <- struct{}{}:
						default:
						}
					},
				}
				if step.Ctx.ArgsComplete != nil {
					ctx.ArgsComplete = *step.Ctx.ArgsComplete
				}
				if step.Ctx.ShowImages != nil {
					ctx.ShowImages = *step.Ctx.ShowImages
				}
				call, result := builtInToolRenderers(step.Tool)
				var comp extension.Component
				switch step.Op {
				case "wait":
					release()
					// The probe sleeps; PiG's preview invalidates the card, so wait for that when the previous call computed one.
					if previous := stepPrev; previous.Op == "call" && previous.Tool == "edit" && previewExpected(previous) {
						select {
						case <-invalidated:
						case <-time.After(10 * time.Second):
							t.Fatal("the edit preview never invalidated the card")
						}
					}
					got = append(got, nil)
					continue
				case "call":
					ctx.LastComponent = last["call:"+step.Tool]
					comp = call(args, nil, ctx)
					last["call:"+step.Tool] = comp
				case "result":
					ctx.LastComponent = last["result:"+step.Tool]
					comp = result(piRenderResult(step), extension.ToolRenderResultOptions{Expanded: step.Ctx.Expanded, IsPartial: step.Ctx.IsPartial}, nil, ctx)
					last["result:"+step.Tool] = comp
				case "render":
					comp = last[step.Of+":"+step.Tool]
				}
				rows := comp.(interface{ Render(int) []string }).Render(golden.Width)
				for i := range rows {
					rows[i] = unsubst(rows[i])
				}
				got = append(got, rows)
			}
			if len(got) != len(c.Lines) {
				t.Fatalf("rendered %d steps, golden has %d", len(got), len(c.Lines))
			}
			for i := range got {
				// A row that names the probe's directories pads to a different width when the directory names differ.
				for j := range got[i] {
					if j < len(c.Lines[i]) && strings.Contains(c.Lines[i][j], "{{") {
						got[i][j], c.Lines[i][j] = strings.TrimRight(got[i][j], " "), strings.TrimRight(c.Lines[i][j], " ")
					}
				}
				if strings.Join(got[i], "\n") != strings.Join(c.Lines[i], "\n") {
					t.Errorf("step %d (%s)\n got: %q\nwant: %q", i, c.Steps[i].Op, got[i], c.Lines[i])
				}
			}
		})
	}
}
