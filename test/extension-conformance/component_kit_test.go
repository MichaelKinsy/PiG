package extensionconformance

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Component kit conformance (D107, docs/plan/extension-component-kit.md §10).
//
// The reference is the kit probe's tree built from the tui ports directly,
// as an in-process Pi extension builds it (inproc-go). Every other cell
// sends the same tree as a view over the wire and the host draws it.

// kitWidths are the widths the rows must match byte for byte at.
var kitWidths = []int{30, 72, 120}

const (
	kitKeyDown  = "\x1b[B"
	kitKeyEnter = "\r"
	// kitOverride is the probe's accent override, distinguishable from the
	// default accent.
	kitOverride = "#d75f00"
	// kitResult is the probe's close value after kitKeys.
	kitResult = "selectionChange:3:k3,selectionChange:4:k4,selectionChange:0:k0,input:x,select:0:k0"
)

// kitKeys are the keys the row sends: down three times (wrapping to item 0),
// a key the list does not bind, and enter.
var kitKeys = []string{kitKeyDown, kitKeyDown, kitKeyDown, "x", kitKeyEnter}

// kitCell is one cell of the component kit rows. frontend attaches a
// frontend before the probe runs, for an SDK that sends views only while
// one draws (Node), and whose view is derived from its components (spec §9):
// its ids are the walk's and it has a focus only when the list is the
// component itself. Adding an SDK cell is one entry here.
type kitCell struct {
	name     string
	make     func(*testing.T) *harness
	extName  string
	frontend bool
}

// componentKitCells are the component kit cells: inproc-go (the reference)
// first, then each SDK cell in every mode it loads in. AGENTS.md: isolated
// (subprocess), packed and fused are identical.
func componentKitCells() []kitCell {
	return []kitCell{
		{name: "inproc-go", make: makeInprocGoHarness, extName: inprocFixtureName},
		{name: "subprocess-go", make: makeSubprocessGoHarness, extName: "sdk-fixture"},
		{name: "fused-go", make: makeFusedGoHarness, extName: "sdk-fixture"},
		{name: "packed-go", make: func(t *testing.T) *harness { return makePackedUIHarness(t, "go") }, extName: "sdk-fixture"},
		{name: "subprocess-rust", make: makeSubprocessRustHarness, extName: "rust-sdk-fixture"},
		{name: "packed-rust", make: func(t *testing.T) *harness { return makePackedUIHarness(t, "rust") }, extName: "rust-sdk-fixture"},
		{name: "subprocess-python", make: makeSubprocessPythonHarness, extName: "python-sdk-fixture"},
		{name: "packed-python", make: func(t *testing.T) *harness { return makePackedUIHarness(t, "python") }, extName: "python-sdk-fixture"},
		{name: "subprocess-node", make: makeSubprocessNodeHarness, extName: "node-sdk-fixture", frontend: true},
		{name: "subprocess-node-packed", make: makeSubprocessNodePackedHarness, extName: "node-sdk-fixture", frontend: true},
	}
}

// componentKitSDKCells are the cells whose extension runs behind the wire.
func componentKitSDKCells() []kitCell {
	return componentKitCells()[1:]
}

// startKitCell makes the cell's harness and wires its bridge as production
// does (coding/cli/extensions.go): the theme palette and terminal capabilities
// come from the tui package, so an SDK that renders lines itself draws with
// the host's tui.ActiveTheme(). The state reaches the extension before the
// row runs a command.
func startKitCell(t *testing.T, tc kitCell) *harness {
	t.Helper()
	h := tc.make(t)
	t.Cleanup(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	})
	if h.bridge == nil {
		return h
	}
	h.bridge.SetThemeFunc(func() any {
		palette := codingagent.ActiveExtensionTheme()
		// The rows are a terminal's, where chalk draws bold and the other
		// modifiers (its stdout is a TTY), as the tui ports always do. This
		// test's stdout is not a terminal.
		if fields, ok := palette.(map[string]any); ok {
			fields = maps.Clone(fields)
			fields["modifiers"] = true
			return fields
		}
		return palette
	})
	h.bridge.SetTerminalCapabilitiesFunc(func() subprocess.TerminalCapabilitiesPayload {
		caps := tui.GetCapabilities()
		return subprocess.TerminalCapabilitiesPayload{Images: string(caps.Images), TrueColor: caps.TrueColor, Hyperlinks: caps.Hyperlinks}
	})
	// The coding agent installs the resolved keybindings, which Pi's
	// components read for their key hints (a bash block's "ctrl+o").
	keybindings := codingagent.DefaultKeybindingsManager().ExtensionKeybindingTable()
	h.bridge.SetKeybindingsFunc(func() any { return keybindings })
	h.bridge.SetFrontend(tc.frontend)
	h.host.BroadcastStateUpdate()
	return h
}

// newKitProbeTree builds the kit probe's tree from the tui ports with the
// coding-agent theme functions, as an in-process Pi extension would.
func newKitProbeTree() (*tui.Container, *tui.SelectList, error) {
	color, err := tui.ParseColor(kitOverride)
	if err != nil {
		return nil, nil, err
	}
	th, err := tui.ActiveTheme().WithTokenColors(map[string]tui.Color{"accent": color})
	if err != nil {
		return nil, nil, err
	}
	fg := func(token string) func(string) string { return func(s string) string { return th.Fg(token, s) } }
	md := tui.MarkdownThemeFor(th)
	items := make([]tui.SelectItem, 5)
	for i := range items {
		items[i] = tui.SelectItem{Value: fmt.Sprintf("k%d", i), Label: fmt.Sprintf("Track %d", i), Description: fmt.Sprintf("Artist %d", i)}
	}
	list := tui.NewSelectList(items, 3, tui.SelectListThemeFor(th), tui.SelectListLayoutOptions{})
	list.SetSelectedIndex(2)
	one := 1
	return tui.NewContainer(
		tui.NewDynamicBorder(fg("accent")),
		tui.NewPaddedText("Kit probe", 2, 0, func(s string) string { return th.Bg("customMessageBg", s) }),
		tui.NewMarkdownWithOptions("- one\n- **two**", 1, 0, &md, nil, &tui.MarkdownOptions{}),
		tui.NewHStack([]tui.StackChild{
			{Component: tui.NewTruncatedText("left side", 0, 0), StackEntryOptions: tui.StackEntryOptions{Grow: &one}},
			{Component: tui.NewTruncatedText("right", 0, 0), StackEntryOptions: tui.StackEntryOptions{Grow: &one}},
		}, tui.StackOptions{Gap: &one}),
		tui.NewSpacer(1),
		list,
	), list, nil
}

func mustKitProbeTree(t *testing.T) (*tui.Container, *tui.SelectList) {
	t.Helper()
	root, list, err := newKitProbeTree()
	if err != nil {
		t.Fatal(err)
	}
	return root, list
}

// inprocKitProbe is inproc-go's kit-probe component: the tree, with the
// list's events and the keys it does not bind logged as the SDK fixtures log
// them, closing on select with the log joined by ",".
type inprocKitProbe struct {
	*tui.Container
	list *tui.SelectList
	log  []string
}

func newInprocKitProbe(_ extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, done func(any)) (extension.DisposableComponent, error) {
	root, list, err := newKitProbeTree()
	if err != nil {
		return nil, err
	}
	p := &inprocKitProbe{Container: root, list: list}
	list.OnSelectionChange = func(item tui.SelectItem) {
		p.log = append(p.log, fmt.Sprintf("selectionChange:%d:%s", list.SelectedIndex(), item.Value))
	}
	list.OnSelect = func(item tui.SelectItem) {
		p.log = append(p.log, fmt.Sprintf("select:%d:%s", list.SelectedIndex(), item.Value))
		done(strings.Join(p.log, ","))
	}
	return p, nil
}

// Dispose has nothing to release: the probe owns no timers or goroutines.
func (p *inprocKitProbe) Dispose() {}

func (p *inprocKitProbe) HandleInput(data string) {
	kb := tui.GetTUIKeybindings()
	if kb.Matches(data, tui.KBSelectUp) || kb.Matches(data, tui.KBSelectDown) ||
		kb.Matches(data, tui.KBSelectConfirm) || kb.Matches(data, tui.KBSelectCancel) {
		p.list.HandleInput(data)
		return
	}
	p.log = append(p.log, "input:"+data)
}

// kitSession is an open kit probe as the terminal sees it.
type kitSession struct {
	// rows returns the rows drawn at width: nil before the first view.
	rows func(width int) []string
	// frontendView returns the view's structure, or nil without one; nil
	// for inproc-go, whose component never crosses the wire.
	frontendView func(width int) *frontend.View
	// input sends a key as the terminal does.
	input func(key string)
	// mouse hands a fullscreen mouse event to the component as the
	// interactive mode's overlay does; nil for inproc-go.
	mouse func(event extension.RemoteMouseEvent) extension.ViewMouseResult
	// lines returns the last lines frame; nil for inproc-go.
	lines func() []string
}

// kitUI shows ui.custom calls to the row: an in-process factory's component,
// or a remote overlay drawn from the views the bridge hands its handle.
type kitUI struct {
	*recordingUI
	sessions chan *kitSession
}

func newKitUI(h *harness) *kitUI {
	ui := &kitUI{recordingUI: h.ui, sessions: make(chan *kitSession, 1)}
	h.runner.SetUIContext(ui)
	if h.bridge != nil {
		h.bridge.SetUIContext(ui)
	}
	return ui
}

func (u *kitUI) Custom(ctx context.Context, factory extension.CustomFactory, opts *extension.CustomOptions) (any, error) {
	if factory == nil {
		return u.recordingUI.Custom(ctx, factory, opts)
	}
	result := make(chan any, 1)
	built, err := factory(nil, nil, nil, func(v any) {
		select {
		case result <- v:
		default:
		}
	})
	if err != nil {
		return nil, err
	}
	component := built.(*inprocKitProbe)
	var mu sync.Mutex
	u.sessions <- &kitSession{
		rows: func(width int) []string {
			mu.Lock()
			defer mu.Unlock()
			return component.Render(width)
		},
		input: func(key string) {
			mu.Lock()
			defer mu.Unlock()
			component.HandleInput(key)
		},
	}
	select {
	case v := <-result:
		return v, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		return nil, errors.New("kit probe never closed")
	}
}

func (u *kitUI) RunRemoteOverlay(_ extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	handle := &kitOverlayHandle{closed: make(chan struct{})}
	onHandle(handle)
	session := &kitSession{rows: handle.rows, frontendView: handle.frontendView, input: host.OnInput, lines: handle.lineFrame}
	if mouse, ok := host.(extension.RemoteOverlayMouseHost); ok {
		session.mouse = mouse.OnMouse
	}
	u.sessions <- session
	select {
	case <-handle.closed:
		return handle.result, true
	case <-time.After(10 * time.Second):
		return nil, false
	}
}

// kitOverlayHandle is a remote overlay that draws the bridge's view surface,
// as the interactive mode's overlay does (extension.ViewTarget).
type kitOverlayHandle struct {
	mu     sync.Mutex
	view   extension.ViewSurface
	lines  []string
	result any
	closed chan struct{}
}

func (h *kitOverlayHandle) UpdateLines(lines []string) {
	h.mu.Lock()
	h.lines = slices.Clone(lines)
	h.mu.Unlock()
}

// lineFrame returns the last lines frame.
func (h *kitOverlayHandle) lineFrame() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.lines)
}

func (h *kitOverlayHandle) UpdateViewAt(view extension.ViewSurface, _ int) {
	h.mu.Lock()
	h.view = view
	h.mu.Unlock()
}

func (h *kitOverlayHandle) surface() extension.ViewSurface {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.view
}

// rows draws only the view: an SDK that sends no view shows nothing.
func (h *kitOverlayHandle) rows(width int) []string {
	if view := h.surface(); view != nil {
		return view.Render(width)
	}
	return nil
}

func (h *kitOverlayHandle) frontendView(width int) *frontend.View {
	if view := h.surface(); view != nil {
		return view.FrontendView(width)
	}
	return nil
}

func (h *kitOverlayHandle) Close(result any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.closed:
	default:
		h.result = result
		close(h.closed)
	}
}

var _ extension.ViewTarget = (*kitOverlayHandle)(nil)

// waitKitRows waits until rows(width) equals want at every kit width; a
// host-drawn view lays out at any width, and a Node frame comes at the width
// the host reported.
func waitKitRows(t *testing.T, h *harness, step string, rows func(width int) []string, want func(width int) []string) {
	t.Helper()
	for _, width := range kitWidths {
		if h.host != nil {
			h.host.NotifyWidth(width)
		}
		expected := want(width)
		deadline := time.Now().Add(6 * time.Second)
		for {
			got := rows(width)
			if slices.Equal(got, expected) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s at width %d:\n got %q\nwant %q", step, width, got, expected)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// kitCommandContext is a command context of h's runner, as the interactive
// mode dispatches a command with.
func kitCommandContext(t *testing.T, h *harness) context.Context {
	cc := h.runner.CreateCommandContext()
	return extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
}

func runKitCommand(t *testing.T, h *harness, name, args string) {
	t.Helper()
	command, ok := findCommand(h.runner, name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	if err := command.Handler(kitCommandContext(t, h), args); err != nil {
		t.Fatalf("%s %s: %v", name, args, err)
	}
}

// TestConformance_ComponentKitRendersPiComponents is the component kit's row
// (spec §10): kit-probe draws the same rows as the tui ports at every width,
// shows item 3 selected in the override accent, and answers the keys as Pi's
// SelectList does, with the events and the unbound key reaching the
// extension in order.
func TestConformance_ComponentKitRendersPiComponents(t *testing.T) {
	t.Parallel()
	override, err := tui.ParseColor(kitOverride)
	if err != nil {
		t.Fatal(err)
	}
	overridden, err := tui.ActiveTheme().WithTokenColors(map[string]tui.Color{"accent": override})
	if err != nil {
		t.Fatal(err)
	}
	overrideAccent, defaultAccent := overridden.GetFgAnsi("accent"), tui.ActiveTheme().GetFgAnsi("accent")
	if overrideAccent == "" || overrideAccent == defaultAccent {
		t.Fatalf("override accent %q is not distinguishable from the default %q", overrideAccent, defaultAccent)
	}
	for _, tc := range componentKitCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			ui := newKitUI(h)
			command, ok := findCommand(h.runner, "kit-probe")
			if !ok {
				t.Fatal("kit-probe is not registered")
			}
			h.ui.ClearRecorded()
			done := make(chan error, 1)
			go func() { done <- command.Handler(kitCommandContext(t, h), "") }()
			var session *kitSession
			select {
			case session = <-ui.sessions:
			case err := <-done:
				t.Fatalf("kit-probe returned before it showed the probe: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("kit-probe never showed the probe")
			}

			reference, list := mustKitProbeTree(t)
			waitKitRows(t, h, "first frame", session.rows, reference.Render)
			selected := slices.IndexFunc(session.rows(72), func(row string) bool { return strings.Contains(row, "Track 2") })
			if selected < 0 || !strings.Contains(session.rows(72)[selected], overrideAccent) {
				t.Fatalf("first frame does not show item 3 selected in %s: %q", kitOverride, session.rows(72))
			}
			checkFrontend := func(step string) {
				if session.frontendView == nil {
					return
				}
				view := session.frontendView(72)
				if view == nil || len(view.Root.Children) != 6 {
					t.Fatalf("%s: frontend view = %+v, want the probe's six children", step, view)
				}
				focus, tracks := "kit-tracks", view.Root.Children[5]
				if tc.frontend {
					// A derived view's list is a child of the component that
					// receives the keys: no focus (spec §9).
					focus = ""
				}
				if view.Theme["accent"] != kitOverride || view.Focus != focus || tracks.Kind != "select-list" || tracks.Selected != list.SelectedIndex() {
					t.Fatalf("%s: frontend view theme=%v focus=%q list=%s selected=%d, want accent %s, focus %q, a select-list, %d",
						step, view.Theme, view.Focus, tracks.Kind, tracks.Selected, kitOverride, focus, list.SelectedIndex())
				}
			}
			checkFrontend("first frame")

			for i, key := range kitKeys[:len(kitKeys)-1] {
				session.input(key)
				if key == kitKeyDown {
					list.HandleInput(key)
				}
				step := fmt.Sprintf("after key %d %q", i+1, key)
				waitKitRows(t, h, step, session.rows, reference.Render)
				checkFrontend(step)
			}
			session.input(kitKeyEnter)
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("kit-probe: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("kit-probe did not close on enter")
			}
			want := "kit=" + kitResult + ":info"
			if got := h.ui.Recorded(); !slices.Contains(got, want) {
				t.Fatalf("kit-probe notified %q, want %q", got, want)
			}
		})
	}
}

// TestConformance_ComponentKitWidgetHeaderFooterAndRenderers draws the kit
// probe's tree through every other view surface (spec §9, §10): a widget,
// the header, the footer, a tool's result renderer and a message renderer
// each show the tui ports' rows at every width.
func TestConformance_ComponentKitWidgetHeaderFooterAndRenderers(t *testing.T) {
	t.Parallel()
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			reference, _ := mustKitProbeTree(t)
			runKitCommand(t, h, "kit-surfaces", "")

			waitKitRows(t, h, "widget", func(width int) []string {
				if widget := h.bridge.GetWidget(tc.extName, "kit-probe"); widget != nil {
					return widget.Render(width)
				}
				return nil
			}, reference.Render)
			for _, header := range []bool{true, false} {
				name := map[bool]string{true: "header", false: "footer"}[header]
				waitKitRows(t, h, name, func(width int) []string {
					if framed := h.ui.SurfaceView(header); framed != nil {
						return framed.View.Render(width)
					}
					return nil
				}, reference.Render)
			}

			var tool extension.ToolDefinition
			waitFor(t, func() bool {
				definition, ok := h.runner.GetToolDefinition("kit_view_tool")
				tool = definition
				return ok && tool.RenderResult != nil
			})
			result, ok := tool.RenderResult(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "kit"}}}, extension.ToolRenderResultOptions{}, nil, extension.ToolRenderContext{
				Args: json.RawMessage(`{}`), ToolCallID: "kit-view-1", State: map[string]any{}, Invalidate: func() {}, ExecutionStarted: true, ArgsComplete: true,
			}).(interface{ Render(int) []string })
			if !ok {
				t.Fatal("kit_view_tool's result renderer returned no component")
			}
			waitKitRows(t, h, "tool result", result.Render, reference.Render)

			renderer := h.runner.MessageRenderer("kit-message")
			if renderer == nil {
				t.Fatal("kit-message renderer is not registered")
			}
			message, ok := renderer(extension.CustomMessage{CustomType: "kit-message", Content: "kit", Display: true}, extension.MessageRenderOptions{}, nil).(interface{ Render(int) []string })
			if !ok {
				t.Fatal("kit-message renderer returned no component")
			}
			waitKitRows(t, h, "message", message.Render, reference.Render)
		})
	}
}

// wireTap records the calls extensions send, through the host's inbound
// observer, while recording is on. stall, when set, holds the host's read
// loop that long on every frame, as a host under load reads slowly.
type wireTap struct {
	on    atomic.Bool
	stall atomic.Int64 // time.Duration
	mu    sync.Mutex
	calls []wireCall
}

type wireCall struct {
	method string
	args   json.RawMessage
}

func observeWire(host *subprocess.Host) *wireTap {
	tap := &wireTap{}
	host.SetInboundObserver(tap.observe)
	return tap
}

func (w *wireTap) observe(_ string, env *subprocess.Envelope) {
	if d := w.stall.Load(); d > 0 {
		time.Sleep(time.Duration(d))
	}
	if !w.on.Load() || env.Call == nil {
		return
	}
	call := wireCall{method: env.Call.Method, args: slices.Clone(env.Call.Args)}
	w.mu.Lock()
	w.calls = append(w.calls, call)
	w.mu.Unlock()
}

// take returns the calls named method recorded since the last take.
func (w *wireTap) take(method string) []wireCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []wireCall
	for _, call := range w.calls {
		if call.method == method {
			out = append(out, call)
		}
	}
	w.calls = nil
	return out
}

// kitImageFrame is the image transport of one ui.setWidget view frame.
type kitImageFrame struct {
	key    string
	images map[string]string // ref → base64 data
}

func kitImageFrames(t *testing.T, calls []wireCall) []kitImageFrame {
	t.Helper()
	var frames []kitImageFrame
	for _, call := range calls {
		var args struct {
			Key  string `json:"key"`
			View *struct {
				Images []struct {
					Ref  string `json:"ref"`
					Data string `json:"data"`
				} `json:"images"`
			} `json:"view"`
		}
		if err := json.Unmarshal(call.args, &args); err != nil {
			t.Fatalf("decode ui.setWidget %s: %v", call.args, err)
		}
		if args.Key != "kit-img" || args.View == nil {
			continue
		}
		frame := kitImageFrame{key: args.Key, images: map[string]string{}}
		for _, img := range args.View.Images {
			data, err := base64.StdEncoding.DecodeString(img.Data)
			sum := sha256.Sum256(data)
			if err != nil || hex.EncodeToString(sum[:]) != img.Ref {
				t.Fatalf("image %s carries bytes that do not hash to it", img.Ref)
			}
			frame.images[img.Ref] = img.Data
		}
		frames = append(frames, frame)
	}
	return frames
}

// TestConformance_ComponentKitImagesSendOnce pins the image transport (spec
// §7): a frame carries an image's bytes the first time the connection
// references it, a later frame names it only, and after the host evicts it
// (ui.view.evicted) the next frame carries the bytes again.
//
// Each kit-images step shows one frame, and the row reads it once the host
// draws it. A rendered frame is a replaceable snapshot that Node coalesces
// under socket backpressure (docs/extension-api-parity.md), so a step that
// replaced the widget several times would pin a frame schedule, not the
// transport. Drawn one at a time, every frame is a final view: every SDK
// sends it, so 64 new images reach the host and it evicts image 0. The host
// reads slowly throughout, so the row holds under load.
func TestConformance_ComponentKitImagesSendOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			// The rows are read at 40 cells; a Node frame comes at the width
			// the host reported.
			h.host.NotifyWidth(40)
			// step runs the kit-images step that shows text and returns the
			// frames it sent once the host draws text (no step's text
			// contains a later one's).
			step := func(text string) []kitImageFrame {
				t.Helper()
				runKitCommand(t, h, "kit-images", text)
				waitFor(t, func() bool {
					widget := h.bridge.GetWidget(tc.extName, "kit-img")
					return widget != nil && slices.ContainsFunc(widget.Render(40), func(row string) bool { return strings.Contains(row, text) })
				})
				return kitImageFrames(t, h.wire.take("ui.setWidget"))
			}
			h.wire.stall.Store(int64(time.Millisecond))
			h.wire.on.Store(true)

			first := step("a1")
			if len(first) != 1 || len(first[0].images) != 1 {
				t.Fatalf("a1 frames = %+v, want one carrying its image", first)
			}
			var ref string
			for r := range first[0].images {
				ref = r
			}
			if second := step("a2"); len(second) != 1 || len(second[0].images) != 0 {
				t.Fatalf("a2 frames = %+v, want one naming its image only", second)
			}

			sent := map[string]bool{ref: true}
			for n := 1; n <= 64; n++ {
				frames := step(fmt.Sprintf("b%d", n))
				if len(frames) != 1 || len(frames[0].images) != 1 {
					t.Fatalf("b%d frames = %+v, want one carrying its own new image", n, frames)
				}
				for r := range frames[0].images {
					if sent[r] {
						t.Fatalf("b%d sends the bytes of %s again", n, r)
					}
					sent[r] = true
				}
			}

			again := step("a3")
			if len(again) == 0 || again[len(again)-1].images[ref] == "" {
				t.Fatalf("after ui.view.evicted the frame naming %s carries no bytes: %+v", ref, again)
			}
		})
	}
}

// kitKindsPNG is kit-kinds' image, a 1×1 PNG every fixture embeds byte for
// byte, so the rows of each SDK draw the same image.
const kitKindsPNG = "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c48900000010494441547801010500faff002a005fff026a01892888e8cd0000000049454e44ae426082"

// kitKindsList is the list annotation of kit-kinds' lines node, as the wire
// carries it while a frontend draws (canonical JSON: sorted keys).
const kitKindsList = `{"items":[{"detail":"A","label":"track one"},{"detail":"B","label":"track two"}],"selectedIndex":1}`

// kitRows is a lines node's rows, drawn verbatim.
type kitRows []string

func (r kitRows) Render(int) []string { return r }
func (kitRows) Invalidate()           {}

// newKitKindsTree builds kit-kinds' tree from the tui ports with the
// coding-agent theme functions under the probe's accent override: a vstack
// of the kinds kit-probe does not draw (box, settings list, loader, image,
// lines).
func newKitKindsTree(t *testing.T) tui.Component {
	t.Helper()
	color, err := tui.ParseColor(kitOverride)
	if err != nil {
		t.Fatal(err)
	}
	th, err := tui.ActiveTheme().WithTokenColors(map[string]tui.Color{"accent": color})
	if err != nil {
		t.Fatal(err)
	}
	data, err := hex.DecodeString(kitKindsPNG)
	if err != nil {
		t.Fatal(err)
	}
	fg := func(token string) func(string) string { return func(s string) string { return th.Fg(token, s) } }
	box := tui.NewPaddedBox(1, 0, func(s string) string { return th.Bg("customMessageBg", s) })
	box.AddChild(tui.NewPaddedText("boxed", 0, 0, nil))
	settings := tui.NewSettingsList([]tui.SettingItem{
		{ID: "theme", Label: "Theme", Description: "Color theme", CurrentValue: "dark", Values: []string{"dark", "light"}},
		{ID: "wrap", Label: "Wrap", Description: "Wrap lines", CurrentValue: "on", Values: []string{"on", "off"}},
	}, 3, tui.SettingsListThemeFor(th), nil, nil)
	loader := tui.NewLoader(nil, fg("accent"), fg("muted"), "Working", nil)
	loader.SetIndicator(&tui.LoaderIndicatorOptions{Frames: []string{"*"}})
	image := tui.NewImage(base64.StdEncoding.EncodeToString(data), "image/png", tui.ImageTheme{FallbackColor: func(s string) string { return s }}, tui.ImageOptions{}, nil)
	gap := 1
	return tui.NewVStack([]tui.StackChild{
		{Component: box}, {Component: settings}, {Component: loader}, {Component: image},
		{Component: kitRows{"track one", "track two"}},
	}, tui.StackOptions{Gap: &gap})
}

// kitKindsFrameLists returns the list annotation of the lines node in each
// kit-kinds ui.setWidget frame as canonical JSON, "" for a frame without one.
func kitKindsFrameLists(t *testing.T, calls []wireCall) []string {
	t.Helper()
	var lists []string
	for _, call := range calls {
		var args struct {
			Key  string `json:"key"`
			View *struct {
				Root struct {
					Children []struct {
						Kind string `json:"kind"`
						List any    `json:"list"`
					} `json:"children"`
				} `json:"root"`
			} `json:"view"`
		}
		if err := json.Unmarshal(call.args, &args); err != nil {
			t.Fatalf("decode ui.setWidget %s: %v", call.args, err)
		}
		if args.Key != "kit-kinds" || args.View == nil {
			continue
		}
		children := args.View.Root.Children
		if len(children) != 5 || children[4].Kind != "lines" {
			t.Fatalf("kit-kinds frame %s does not end in its lines node", call.args)
		}
		if children[4].List == nil {
			lists = append(lists, "")
			continue
		}
		list, err := json.Marshal(children[4].List)
		if err != nil {
			t.Fatal(err)
		}
		lists = append(lists, string(list))
	}
	return lists
}

// kitImageID is the image id of a Kitty graphics escape, which each tui.Image
// draws its own random one of.
var kitImageID = regexp.MustCompile(`(\x1b_G[^;]*,i=)\d+`)

// kitImageIDs returns rows with every Kitty image id replaced by 0, so rows
// that differ only in the ids each image instance drew compare equal.
func kitImageIDs(rows []string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = kitImageID.ReplaceAllString(row, "${1}0")
	}
	return out
}

// TestConformance_ComponentKitEveryKind draws the kinds kit-probe does not
// (spec §2, §10): kit-kinds sets a widget of a box, a settings list, a
// loader, an image and a lines node under the probe's theme override, whose
// rows must equal the tui ports' at every width. The lines node's list
// annotation travels only while a frontend draws, with selectedIndex 1.
func TestConformance_ComponentKitEveryKind(t *testing.T) {
	t.Parallel()
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			tree := newKitKindsTree(t)
			reference := func(width int) []string { return kitImageIDs(tree.Render(width)) }
			widget := func(width int) []string {
				if widget := h.bridge.GetWidget(tc.extName, "kit-kinds"); widget != nil {
					return kitImageIDs(widget.Render(width))
				}
				return nil
			}
			// The row starts without a frontend in every cell.
			h.bridge.SetFrontend(false)
			h.host.BroadcastStateUpdate()
			h.wire.on.Store(true)

			runKitCommand(t, h, "kit-kinds", "")
			waitKitRows(t, h, "kit-kinds without a frontend", widget, reference)
			// Without a frontend an authoritative view goes out once with no
			// list; a cell whose view is derived (Node) sends no view at all
			// (spec §8), so no list either.
			lists := kitKindsFrameLists(t, h.wire.take("ui.setWidget"))
			if want := map[bool]int{false: 1, true: 0}[tc.frontend]; len(lists) != want || slices.ContainsFunc(lists, func(list string) bool { return list != "" }) {
				t.Fatalf("without a frontend the frames' list annotations = %q, want %d frames without one", lists, want)
			}

			h.bridge.SetFrontend(true)
			h.host.BroadcastStateUpdate()
			runKitCommand(t, h, "kit-kinds", "")
			// A derived view's widget is drawn again when the frontend
			// attaches, so such a cell may send more than one frame; every
			// frame carries the list.
			lists = kitKindsFrameLists(t, h.wire.take("ui.setWidget"))
			if len(lists) == 0 || (!tc.frontend && len(lists) != 1) || slices.ContainsFunc(lists, func(list string) bool { return list != kitKindsList }) {
				t.Fatalf("with a frontend the frames' list annotations = %q, want each frame with %s", lists, kitKindsList)
			}
			waitKitRows(t, h, "kit-kinds with a frontend", widget, reference)
		})
	}
}

// kitConversationCard is one of kit-conversation's tool cards, built as the
// main transcript builds it.
func kitConversationCard(t *testing.T, name, callID, args string, builtin bool) *tui.ToolExecutionComponent {
	t.Helper()
	var renderers codingagent.ToolRenderers
	if builtin {
		renderers = codingagent.CreateAllToolRenderers()[name]
	}
	card := codingagent.NewToolRendererCard(t.Context(), name, callID, "/work/kit", json.RawMessage(args), renderers, func() {})
	t.Cleanup(card.Dispose)
	if !builtin {
		card.Component.SetDefinition(&tui.ToolDefinitionRenderers{}, json.RawMessage(args))
	}
	card.Component.SetArgsComplete()
	card.Component.MarkExecutionStarted()
	return card.Component
}

func kitConversationResult(card *tui.ToolExecutionComponent, text string, isError, partial bool) {
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, IsError: isError}
	card.SetResultValue(result)
	if partial {
		card.SetStreaming(text)
		return
	}
	card.SetResult(text, isError, 0)
}

// newKitConversationTree builds kit-conversation's tree from the ports the
// main transcript draws Pi's conversation components with (spec §2.1), in
// the first frame's state or, with next, the second's.
func newKitConversationTree(t *testing.T, next bool) tui.Component {
	t.Helper()
	user := tui.NewUserMessageComponent("Fix **the** kit build\n\n- one\n- two", nil, 1, nil)
	reply := "Done. **Bold** reply\n\n1. a\n2. b"
	if next {
		reply += "\n\nThen more kit."
	}
	streaming := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	streaming.SetContent([]tui.AssistantSegment{{Thinking: true, Text: "Reading the *kit* file"}, {Text: reply}})
	streaming.SetTerminalError("stop", "")
	failed := tui.NewAssistantMessageComponent(nil, true, nil, "", nil, nil)
	failed.SetHiddenThinkingLabel("Pondering kit...")
	failed.SetOutputPad(0)
	failed.SetContent([]tui.AssistantSegment{{Thinking: true, Text: "secret"}, {Text: "Visible kit"}})
	failed.SetTerminalError("error", "kit-boom-7")
	ls := kitConversationCard(t, "ls", "call-1", `{"path":"src"}`, true)
	kitConversationResult(ls, "a.go\nb.go", false, false)
	grep := kitConversationCard(t, "grep", "call-2", `{"pattern":"TODO"}`, true)
	if next {
		kitConversationResult(grep, "x.go:1: TODO kit\ny.go:2: TODO kit", false, false)
	} else {
		kitConversationResult(grep, "x.go:1: TODO kit", false, true)
	}
	read := kitConversationCard(t, "read", "call-3", `{"path":"missing.txt"}`, true)
	kitConversationResult(read, "ENOENT: kit", true, false)
	read.SetExpanded(true)
	custom := kitConversationCard(t, "kit_tool", "call-4", `{"q":"x"}`, false)
	kitConversationResult(custom, "answer 42", false, false)
	two, zero := 2, 0
	exited := tui.NewBashExecutionComponent("ls -la", nil, false, 1)
	exited.AppendOutput("a.txt\nb.txt")
	exited.SetCompleteWithOutput(&two, false, false, exited.GetOutput(), "")
	if next {
		exited.SetExpanded(true)
	}
	numbers := make([]string, 25)
	for i := range numbers {
		numbers[i] = fmt.Sprint(i + 1)
	}
	seq := tui.NewBashExecutionComponent("seq 25", nil, true, 1)
	seq.AppendOutput(strings.Join(numbers, "\n"))
	seq.SetCompleteWithOutput(&zero, false, false, seq.GetOutput(), "")
	diff := tui.NewPaddedText(tui.RenderDiff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail"), 0, 0, nil)
	return tui.NewContainer(user, streaming, failed, ls, grep, read, custom, exited, seq, diff)
}

// TestConformance_ComponentKitConversationKinds draws Pi's conversation
// components (spec §2.1, §10): kit-conversation's rows equal, at every kit
// width, the rows of the ports the main transcript uses, in the first frame
// and after "next" updates the kept nodes. A Node cell without a frontend
// draws Pi's own components, so it compares Pi's bytes with the ports'. With
// a frontend the view carries the main transcript's nodes for each kind.
func TestConformance_ComponentKitConversationKinds(t *testing.T) {
	t.Parallel()
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			widget := func(width int) []string {
				if widget := h.bridge.GetWidget(tc.extName, "kit-conversation"); widget != nil {
					return widget.Render(width)
				}
				return nil
			}
			h.bridge.SetFrontend(false)
			h.host.BroadcastStateUpdate()
			first := newKitConversationTree(t, false)
			runKitCommand(t, h, "kit-conversation", "")
			waitKitRows(t, h, "kit-conversation", widget, first.Render)

			h.bridge.SetFrontend(true)
			h.host.BroadcastStateUpdate()
			runKitCommand(t, h, "kit-conversation", "")
			waitKitRows(t, h, "kit-conversation with a frontend", widget, first.Render)
			// A frame of lines (Node's) reports its view at the width it
			// was drawn at.
			h.host.NotifyWidth(72)
			var view *frontend.View
			waitFor(t, func() bool {
				if w := h.bridge.GetWidget(tc.extName, "kit-conversation"); w != nil {
					view = w.FrontendView(72)
				}
				return view != nil && len(view.Root.Children) == 10 && view.Root.Children[9].Diff != nil
			})
			checkKitConversationView(t, view)

			next := newKitConversationTree(t, true)
			runKitCommand(t, h, "kit-conversation", "next")
			waitKitRows(t, h, "kit-conversation next", widget, next.Render)
		})
	}
}

// checkKitConversationView checks each conversation kind's transcript nodes
// against values a fallback cannot produce.
func checkKitConversationView(t *testing.T, view *frontend.View) {
	t.Helper()
	children := view.Root.Children
	kinds := []frontend.ViewKind{frontend.ViewKindUserMessage, frontend.ViewKindAssistantMessage, frontend.ViewKindAssistantMessage,
		frontend.ViewKindToolExecution, frontend.ViewKindToolExecution, frontend.ViewKindToolExecution, frontend.ViewKindToolExecution,
		frontend.ViewKindBashExecution, frontend.ViewKindBashExecution, frontend.ViewKindDiff}
	for i, kind := range kinds {
		if children[i].Kind != kind {
			t.Fatalf("child %d is %q, want %q", i, children[i].Kind, kind)
		}
	}
	if got := children[0].Transcript; len(got) != 1 || !reflect.DeepEqual(got[0], frontend.MarkdownText{Role: frontend.RoleUser, Text: "Fix **the** kit build\n\n- one\n- two"}) {
		t.Fatalf("user message transcript = %#v", got)
	}
	if got := children[1].Transcript; len(got) != 2 || got[0] != (frontend.Thinking{Text: "Reading the *kit* file"}) || !reflect.DeepEqual(got[1], frontend.MarkdownText{Role: frontend.RoleAssistant, Text: "Done. **Bold** reply\n\n1. a\n2. b", Streaming: true}) {
		t.Fatalf("streaming message transcript = %#v", got)
	}
	if got := children[2].Transcript; len(got) != 3 || got[0] != (frontend.Thinking{Text: "secret", Hidden: true}) {
		t.Fatalf("failed message transcript = %#v", got)
	}
	for i, want := range []struct {
		name   string
		status frontend.ToolStatus
		output string
	}{{"ls", frontend.ToolDone, "a.go\nb.go"}, {"grep", frontend.ToolRunning, "x.go:1: TODO kit"}, {"read", frontend.ToolError, "ENOENT: kit"}, {"kit_tool", frontend.ToolDone, "answer 42"}} {
		got := children[3+i].Transcript
		card, ok := frontend.ToolCard{}, len(got) == 1
		if ok {
			card, ok = got[0].(frontend.ToolCard)
		}
		if !ok || card.Name != want.name || card.Status != want.status || card.Output != want.output || card.Arguments == nil {
			t.Fatalf("tool card %d transcript = %#v, want %s %s %q", i, got, want.name, want.status, want.output)
		}
	}
	if d := children[9].Diff; d.Path != "kit.go" || !strings.Contains(d.Text, "+2 new kit line") {
		t.Fatalf("diff = %#v", d)
	}
}
