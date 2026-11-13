package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
	"github.com/MichaelKinsy/PiG/tui"
)

// The surfaces rows of the reload leak test reload extensions that draw: every generation registers a message, an entry and a tool
// renderer, a message renderer and a tool result renderer whose view holds a loader the host animates, sets a widget of such a view when
// its session starts, and opens a dialog with a timeout that nobody answers. After every reload a row draws what the interactive mode
// draws: the components of the replaced generation once more at another width (a frame between the reload and the rebuilt transcript),
// then new components from the reloaded renderers, and it leaves a frame's requests in flight for the next reload. What a replaced
// generation left on the host is what the row's bounds see. Two kinds of row each draw what one thing a generation can leave behind
// hangs on, so a row names its cause: the views rows draw the views the host animates (a loader's timer), for messages without an
// identity; the keys rows draw the messages and entries the transcript keeps across reloads (the key that names a message to the
// extension's process) and hold the dialog open (its call and its countdown). The surfaces rows draw both, as a session does, so what
// the two leave behind together is under the bounds as well.

// reloadLeakSurfacesSource is the extension of a surfaces row; the verb is its name.
const reloadLeakSurfacesSource = `package surfaces

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

const name = %q

// working is a view whose loader the host animates.
func working() kit.View { return kit.View{Root: kit.NewLoader("Working")} }

func Extension() *sdk.Extension {
	e := sdk.New(name)
	e.Command("noop", "Return at once", func(sdk.Context, string) error { return nil })
	e.MessageRenderer("leak-message:"+name, func(_ sdk.Context, message map[string]any, _ sdk.MessageRenderOptions, width int) ([]string, error) {
		return []string{fmt.Sprintf("message:%%v:%%d", message["content"], width)}, nil
	})
	e.EntryRenderer("leak-entry:"+name, func(_ sdk.Context, entry map[string]any, _ sdk.EntryRenderOptions, width int) ([]string, error) {
		return []string{fmt.Sprintf("entry:%%v:%%d", entry["data"], width)}, nil
	})
	e.MessageViewRenderer("leak-loader:"+name, func(sdk.Context, map[string]any, sdk.MessageRenderOptions, int) (kit.View, error) {
		return working(), nil
	})
	tool := "leak_tool_" + strings.ReplaceAll(name, "-", "_")
	e.Tool(tool, "Draw a card", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(sdk.Context, map[string]any) (any, error) {
		return "ok", nil
	})
	e.SetToolRenderers(tool, sdk.ToolRenderers{
		Call: func(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			return []string{fmt.Sprintf("call:%%v:%%d", args["topic"], width)}, nil
		},
		ResultView: func(sdk.Context, sdk.ToolRenderResult, sdk.ToolRenderResultOptions, sdk.ToolRenderContext, int) (kit.View, error) {
			return working(), nil
		},
	})
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.SetWidget("leak-widget:"+name, working(), sdk.WidgetOptions{}); err != nil {
			return nil, err
		}
		// Nobody answers this dialog and the handler does not wait for it: it is open when the next reload replaces the extension.
		go func() {
			_, _, _ = ctx.SelectWithOptions("leak-dialog:"+name, []string{"a", "b"}, sdk.DialogOptions{Timeout: 600000})
		}()
		return nil, nil
	})
	return e
}
`

// reloadLeakSurfacesFactory writes the surfaces extension name into a directory of its own and returns its config: a Go factory, alone in
// its process (strict) or a member of a cell (shared-ok).
func reloadLeakSurfacesFactory(t *testing.T, name, isolation string) subprocess.ExtConfig {
	t.Helper()
	dir := t.TempDir()
	cfg := subprocess.ExtConfig{
		Name: name, Source: dir, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "go", Isolation: isolation, EntrypointKind: "factory",
		SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.test/" + name, Package: "example.test/" + name, Factory: "Extension",
	}
	for file, content := range map[string]string{
		"go.mod":       fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", cfg.ModulePath),
		"extension.go": fmt.Sprintf(reloadLeakSurfacesSource, name),
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

// reloadLeakSurfaceRows are the surfaces rows: of each kind, one extension alone in its process and two in one cell.
func reloadLeakSurfaceRows() []reloadLeakRow {
	var rows []reloadLeakRow
	for _, kind := range []string{"views", "keys", "surfaces"} {
		rows = append(rows,
			reloadLeakRow{sdk: sdkscenarios.Go, realization: "isolated-" + kind, strategy: subprocess.CellStrategyIsolated, command: "noop", surfaces: kind, configs: func(t *testing.T, _, _ string) []subprocess.ExtConfig {
				return []subprocess.ExtConfig{reloadLeakSurfacesFactory(t, "reload-leak-surfaces", "strict")}
			}},
			reloadLeakRow{sdk: sdkscenarios.Go, realization: "packed-" + kind, strategy: subprocess.CellStrategy("packed-go"), command: "noop", surfaces: kind, configs: func(t *testing.T, _, _ string) []subprocess.ExtConfig {
				return []subprocess.ExtConfig{reloadLeakSurfacesFactory(t, "reload-leak-surfaces-a", "shared-ok"), reloadLeakSurfacesFactory(t, "reload-leak-surfaces-b", "shared-ok")}
			}})
	}
	return rows
}

// reloadLeakWidth is the width the surfaces rows draw at.
const reloadLeakWidth = 72

// reloadLeakDialogUI is the host UI of a surfaces row. It holds each dialog until the dialog's call ends, as a terminal holds a dialog
// nobody answers, and gives a dialog that has a timeout the countdown the interactive dialog owns: ExtUIContext.runDialog
// (internal/codingagent) starts the selector's tui.CountdownTimer and disposes it when the dialog's context ends.
type reloadLeakDialogUI struct{ *recordingUI }

func (u reloadLeakDialogUI) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	if opts.Timeout != nil && *opts.Timeout > 0 {
		countdown := tui.NewCountdownTimer(time.Duration(*opts.Timeout*float64(time.Millisecond)), nil, func(int) {}, func() {})
		defer countdown.Dispose()
	}
	return u.recordingUI.Select(ctx, title, options, opts)
}

// reloadLeakSurfaces is what a surfaces row keeps of its extensions between two reloads, as the interactive mode keeps the transcript.
type reloadLeakSurfaces struct {
	// kind is the row's kind: "views", "keys", or "surfaces" for both.
	kind    string
	bridge  *subprocess.UIBridge
	dialogs *dialogGate
	// identities are the identities of the row's messages and entries: a message keeps its identity when the transcript is rebuilt.
	identities map[string]*extension.RenderIdentity
	// shown are the components drawn after the latest reload. The next reload replaces the extensions under them.
	shown []interface{ Render(int) []string }
}

func newReloadLeakSurfaces(kind string, bridge *subprocess.UIBridge, ui *recordingUI) *reloadLeakSurfaces {
	ui.dialogs = newDialogGate()
	return &reloadLeakSurfaces{kind: kind, bridge: bridge, dialogs: ui.dialogs, identities: map[string]*extension.RenderIdentity{}}
}

func (s *reloadLeakSurfaces) identity(of string) *extension.RenderIdentity {
	if s.identities[of] == nil {
		s.identities[of] = extension.NewRenderIdentity()
	}
	return s.identities[of]
}

// draw is what the interactive mode draws around one reload, after the reloaded extensions heard session_start.
func (s *reloadLeakSurfaces) draw(t *testing.T, n int, runner *inproc.Runner, loaded []extension.Extension) {
	t.Helper()
	// await waits until reached holds; missed says what was there instead when the test's wait budget ran out.
	// One wait budget for this draw, released when it returns: a context of testbudget.Context stays with the test until it ends, and
	// a hundred reloads of them, one for every wait, were what the rows still gained (about 850 bytes each) once the host held nothing.
	budget, release := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer release()
	await := func(reached func() bool, missed func() string) {
		t.Helper()
		if !awaitCondition(budget, reached) {
			t.Fatalf("reload %d: %s (wait budget %s)", n, missed(), testbudget.Env)
		}
	}
	// A frame drawn before the transcript is rebuilt draws the components of the replaced extensions at the terminal's new width: each
	// asks its extension, whose connection the reload ended.
	for _, component := range s.shown {
		component.Render(reloadLeakWidth + 1)
	}
	// The rebuilt transcript drops them.
	clear(s.shown)
	s.shown = s.shown[:0]
	show := func(what string, component extension.Component, want string) {
		t.Helper()
		drawn, ok := component.(interface{ Render(int) []string })
		if !ok {
			t.Fatalf("reload %d: %s is %T, which draws nothing", n, what, component)
		}
		var lines []string
		await(func() bool {
			lines = drawn.Render(reloadLeakWidth)
			return strings.Contains(strings.Join(lines, "\n"), want)
		}, func() string { return fmt.Sprintf("%s draws %q, want %q in it", what, lines, want) })
		s.shown = append(s.shown, drawn)
	}
	message := func(customType, want string, identity *extension.RenderIdentity) {
		t.Helper()
		renderer := runner.MessageRenderer(customType)
		if renderer == nil {
			t.Fatalf("reload %d: no message renderer %s", n, customType)
		}
		show(customType, renderer(extension.CustomMessage{CustomType: customType, Content: "body", Display: true}, extension.MessageRenderOptions{Identity: identity}, nil), want)
	}
	views, keys := s.kind != "keys", s.kind != "views"
	var dialogs []string
	for _, ext := range loaded {
		name := ext.Name
		if keys {
			message("leak-message:"+name, fmt.Sprintf("message:body:%d", reloadLeakWidth), s.identity("leak-message:"+name))
			entryType := "leak-entry:" + name
			entryRenderer := runner.EntryRenderer(entryType)
			if entryRenderer == nil {
				t.Fatalf("reload %d: no entry renderer %s", n, entryType)
			}
			show(entryType, entryRenderer(extension.CustomEntry{CustomType: entryType, Data: "data"}, extension.EntryRenderOptions{Identity: s.identity(entryType)}, nil), fmt.Sprintf("entry:data:%d", reloadLeakWidth))
			dialogs = append(dialogs, "leak-dialog:"+name)
		}
		if !views {
			continue
		}
		message("leak-loader:"+name, "Working", nil)
		toolName := "leak_tool_" + strings.ReplaceAll(name, "-", "_")
		tool, ok := runner.GetToolDefinition(toolName)
		if !ok || tool.RenderCall == nil || tool.RenderResult == nil {
			t.Fatalf("reload %d: tool %s = %+v, want it with both renderers", n, toolName, tool)
		}
		card := extension.ToolRenderContext{
			Args: json.RawMessage(`{"topic":"alpha"}`), ToolCallID: "leak-call:" + name, Card: "leak-card:" + name,
			State: map[string]any{}, Invalidate: func() {}, ExecutionStarted: true, ArgsComplete: true,
		}
		show(toolName+" call", tool.RenderCall(json.RawMessage(`{"topic":"alpha"}`), nil, card), fmt.Sprintf("call:alpha:%d", reloadLeakWidth))
		result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}
		show(toolName+" result", tool.RenderResult(result, extension.ToolRenderResultOptions{}, nil, card), "Working")

		widget := "leak-widget:" + name
		var widgetLines []string
		await(func() bool {
			proxy := s.bridge.GetWidget(name, widget)
			if proxy == nil {
				return false
			}
			widgetLines = proxy.Render(reloadLeakWidth)
			return strings.Contains(strings.Join(widgetLines, "\n"), "Working")
		}, func() string { return fmt.Sprintf("the widget %s draws %q, want its loader", widget, widgetLines) })
	}
	if keys {
		// Each reloaded extension opened its dialog, and the dialogs of the replaced ones ended with them.
		slices.Sort(dialogs)
		var open []string
		await(func() bool {
			open = s.dialogs.openTitles()
			slices.Sort(open)
			return slices.Equal(open, dialogs)
		}, func() string { return fmt.Sprintf("the host UI holds the dialogs %q, want %q", open, dialogs) })
	}
	// The requests of this frame are in flight when the next reload begins.
	for _, component := range s.shown {
		component.Render(reloadLeakWidth + 2)
	}
}
