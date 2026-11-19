package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestAdaptiveFramePacingSelected(t *testing.T) {
	for _, tc := range []struct {
		setting, env string
		want         bool
	}{
		{"adaptive", "", true},
		{"pi", "", false},
		{"", "", false},
		{"faster", "", false},
		{"adaptive", "pi", false},
		{"adaptive", "0", false},
		{"pi", "adaptive", true},
		{"pi", "1", false},
		{"adaptive", "1", true},
		{"adaptive", "PI", true},
	} {
		getenv := func(name string) string {
			if name == framePacingEnv {
				return tc.env
			}
			return ""
		}
		if got := adaptiveFramePacingSelected(tc.setting, getenv); got != tc.want {
			t.Errorf("setting %q, %s=%q: adaptive = %v, want %v", tc.setting, framePacingEnv, tc.env, got, tc.want)
		}
	}
}

// The gate is open while a run is active and closed at the first frame after the run ended, however it ended: its queue drained, it failed, or it was aborted.
func TestFramePacingGateClosesOneFrameAfterTheRunEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(m *InteractiveMode, finish chan<- error)
	}{
		{"the last queued message drained", func(_ *InteractiveMode, finish chan<- error) { finish <- nil }},
		{"error", func(_ *InteractiveMode, finish chan<- error) { finish <- errors.New("provider failed") }},
		{"abort", func(m *InteractiveMode, _ chan<- error) { m.abortFn() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, ctx, _ := newLifecycleMode(t)
			m.framePacingInstalled = true
			m.runScheduledRender()
			if m.framePacingAllowed.Load() {
				t.Fatal("the gate is open before any run")
			}
			started, finish, aborted := make(chan struct{}), make(chan error), m.abortCtx.Done()
			m.runTurn(ctx, "", func(runCtx context.Context) ([]agent.AgentMessage, error) {
				close(started)
				select {
				case err := <-finish:
					return nil, err
				case <-runCtx.Done():
					return nil, runCtx.Err()
				case <-aborted:
					return nil, context.Canceled
				}
			})
			m.queueMu.Lock()
			settled := m.turnSettled
			m.queueMu.Unlock()
			<-started
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate is closed while the run is active")
			}
			tc.end(m, finish)
			<-settled
			m.runScheduledRender()
			if m.framePacingAllowed.Load() {
				t.Fatal("the gate is open one frame after the run ended")
			}
		})
	}
}

// A compaction closes the gate at the next frame though the run that started it is still active, and its end opens it again.
func TestFramePacingGateIsClosedDuringCompaction(t *testing.T) {
	m, _, _ := newLifecycleMode(t)
	m.framePacingInstalled = true
	m.turnActive.Store(true)
	m.runScheduledRender()
	if !m.framePacingAllowed.Load() {
		t.Fatal("the gate is closed while the run is active")
	}
	m.handleAgentEvent(agent.CompactionStartEvent{})
	if !m.isCompacting {
		t.Fatal("compaction_start did not mark the mode compacting")
	}
	m.runScheduledRender()
	if m.framePacingAllowed.Load() {
		t.Fatal("the gate is open one frame after compaction started")
	}
	m.isCompacting = false
	m.runScheduledRender()
	if !m.framePacingAllowed.Load() {
		t.Fatal("the gate stayed closed after the compaction")
	}
}

type pacingTestComponent struct{}

func (pacingTestComponent) Render(int) []string { return nil }
func (pacingTestComponent) Invalidate()         {}
func (pacingTestComponent) Dispose()            {}

// Each extension surface alone, mounted while a run streams, closes the gate at the next frame, and so does an open overlay; the frame after the surface is unmounted opens it again.
func TestFramePacingGateIsClosedWhileAnExtensionDrawsOnScreen(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mount   func(m *InteractiveMode)
		unmount func(m *InteractiveMode)
	}{
		{"dialog", func(m *InteractiveMode) { m.extensionDialogs = []*extensionDialog{{}} }, func(m *InteractiveMode) { m.extensionDialogs = nil }},
		{"overlay of an extension", func(m *InteractiveMode) { m.extensionOverlays = []*extensionOverlay{{}} }, func(m *InteractiveMode) { m.extensionOverlays = nil }},
		{"open overlay", func(m *InteractiveMode) { m.tuiInst.ShowOverlay(tui.NewText("overlay"), tui.OverlayOptions{}) }, func(m *InteractiveMode) { m.tuiInst.HideOverlay() }},
		{"header", func(m *InteractiveMode) { m.extHeaderComponent = pacingTestComponent{} }, func(m *InteractiveMode) { m.extHeaderComponent = nil }},
		{"footer", func(m *InteractiveMode) { m.extFooterComponent = pacingTestComponent{} }, func(m *InteractiveMode) { m.extFooterComponent = nil }},
		{"editor", func(m *InteractiveMode) {
			m.editorFactory = func(tui.TUI, tui.EditorTheme, extension.KeybindingsManager) tui.EditorComponent { return nil }
		}, func(m *InteractiveMode) { m.editorFactory = nil }},
		{"widget of the host process", func(m *InteractiveMode) { m.inprocessWidgets = []inprocessWidget{{}} }, func(m *InteractiveMode) { m.inprocessWidgets = nil }},
		{"widget of an extension process", func(m *InteractiveMode) { m.bridgeWidgets = map[string]*subprocess.PushProxy{"w": nil} }, func(m *InteractiveMode) { delete(m.bridgeWidgets, "w") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newLifecycleMode(t)
			m.framePacingInstalled = true
			m.turnActive.Store(true)
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate is closed while the run is active and nothing of an extension is mounted")
			}
			tc.mount(m)
			m.runScheduledRender()
			if m.framePacingAllowed.Load() {
				t.Fatal("the gate is open one frame after the mount")
			}
			tc.unmount(m)
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate stayed closed one frame after the unmount")
			}
		})
	}
}

// pacingTestExtension registers what an extension can draw inside the transcript: a tool with a call renderer, a tool without renderers, a message renderer and an entry renderer.
func pacingTestExtension() extension.Extension {
	component := func() extension.Component { return pacingTestComponent{} }
	return extension.Extension{
		Name: "pacing-test",
		Tools: map[string]extension.RegisteredTool{
			"drawn": {Definition: extension.ToolDefinition{Name: "drawn", RenderCall: func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
				return component()
			}}},
			"plain": {Definition: extension.ToolDefinition{Name: "plain"}},
		},
		ToolOrder: []string{"drawn", "plain"},
		MessageRenderers: map[string]extension.MessageRenderer{"note": func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
			return component()
		}},
		EntryRenderers: map[string]extension.EntryRenderer{"mark": func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
			return component()
		}},
	}
}

// Something an extension's renderer draws inside the transcript, added while a run streams, closes the gate at the next frame: a card of a tool whose definition has a renderer, a custom message and a custom entry with a registered renderer. The frame after the transcript is emptied opens it again. What no extension draws leaves the gate open: a built-in tool's card, the card of an extension's tool without renderers, a message and an entry without a registered renderer.
func TestFramePacingGateIsClosedWhileAnExtensionRendersInsideTheTranscript(t *testing.T) {
	for _, tc := range []struct {
		name   string
		add    func(m *InteractiveMode)
		closes bool
	}{
		{"card of a tool with a renderer", func(m *InteractiveMode) { m.newToolCard("drawn", "call-1", json.RawMessage(`{}`)) }, true},
		{"card bound to a tool with a renderer", func(m *InteractiveMode) {
			m.bindToolCard(m.newToolCard("read", "call-1", json.RawMessage(`{}`)), "call-1", "drawn", json.RawMessage(`{}`))
		}, true},
		{"message with a renderer", func(m *InteractiveMode) {
			m.appendCustomMessage(CustomMessageEntry{CustomType: "note", Content: "text", Display: true}, messageMeta{})
		}, true},
		{"entry with a renderer", func(m *InteractiveMode) { m.addCustomEntryToChat(CustomEntry{CustomType: "mark"}) }, true},
		{"card of a built-in tool", func(m *InteractiveMode) { m.newToolCard("read", "call-1", json.RawMessage(`{}`)) }, false},
		{"card of an extension's tool without renderers", func(m *InteractiveMode) { m.newToolCard("plain", "call-1", json.RawMessage(`{}`)) }, false},
		{"message without a renderer", func(m *InteractiveMode) {
			m.appendCustomMessage(CustomMessageEntry{CustomType: "other", Content: "text", Display: true}, messageMeta{})
		}, false},
		{"entry without a renderer", func(m *InteractiveMode) { m.addCustomEntryToChat(CustomEntry{CustomType: "other"}) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newLifecycleMode(t)
			m.newRunner = inproc.NewRunner([]extension.Extension{pacingTestExtension()}, m.opts.AgentDir)
			m.framePacingInstalled = true
			m.turnActive.Store(true)
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate is closed while the run is active and the transcript is empty")
			}
			tc.add(m)
			m.runScheduledRender()
			if got := !m.framePacingAllowed.Load(); got != tc.closes {
				t.Fatalf("one frame after it was added the gate is closed = %v, want %v", got, tc.closes)
			}
			m.rebuildChatFrom(nil, false)
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate stayed closed one frame after the transcript was emptied")
			}
		})
	}
}

// Each place that empties the transcript besides a rebuild reopens the gate one frame later: the redraw after a compaction and a command's Clear.
func TestFramePacingGateReopensWhereverTheTranscriptIsEmptied(t *testing.T) {
	for _, tc := range []struct {
		name  string
		empty func(m *InteractiveMode)
	}{
		{"redraw after a compaction", func(m *InteractiveMode) { m.renderCompactionResult(agent.CompactionEndEvent{}, nil) }},
		{"a command's Clear", func(m *InteractiveMode) { m.buildSlashContext(context.Background()).Clear() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newLifecycleMode(t)
			m.newRunner = inproc.NewRunner([]extension.Extension{pacingTestExtension()}, m.opts.AgentDir)
			m.framePacingInstalled = true
			m.turnActive.Store(true)
			m.addCustomEntryToChat(CustomEntry{CustomType: "mark"})
			m.runScheduledRender()
			if m.framePacingAllowed.Load() {
				t.Fatal("the gate is open one frame after an extension's entry was added")
			}
			tc.empty(m)
			m.runScheduledRender()
			if !m.framePacingAllowed.Load() {
				t.Fatal("the gate stayed closed one frame after the transcript was emptied")
			}
		})
	}
}

// The redraw around a compaction entry an extension appended at a boundary empties the transcript as well: the gate an extension's renderer closed reopens one frame later.
func TestFramePacingGateReopensAfterABoundaryCompactionRedraw(t *testing.T) {
	p, ids := boundaryCompactionMode(t, func(sess *Session, ids map[string]string) {
		appendMessage(t, sess, ids, "old", userMsg("OLD QUESTION"))
		appendMessage(t, sess, ids, "kept", userMsg("KEPT QUESTION"))
		id, err := sess.AppendCompaction("BOUNDARY SUMMARY", ids["kept"], 1234, nil, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids["compaction"] = id
	})
	m := p.InteractiveMode
	m.toolsExpanded = true
	m.framePacingInstalled = true
	m.turnActive.Store(true)
	m.runScheduledRender()
	if !m.framePacingAllowed.Load() {
		t.Fatal("the gate is closed while the run is active and no extension draws")
	}
	m.extensionRenderersMounted()
	m.runScheduledRender()
	if m.framePacingAllowed.Load() {
		t.Fatal("the gate is open one frame after an extension's renderer was noted")
	}
	p.handleAgentEvent(appendedEvent(t, p, ids["compaction"]))
	if got := p.chat(); !strings.Contains(got, "BOUNDARY SUMMARY") || strings.Contains(got, "OLD QUESTION") {
		t.Fatalf("chat = %q, want the transcript redrawn around the compaction's summary", got)
	}
	m.runScheduledRender()
	if !m.framePacingAllowed.Load() {
		t.Fatal("the gate stayed closed one frame after the boundary compaction redrew the transcript")
	}
}

// A tool renderer resolver of an extension closes the gate when it answers for the tool, with renderers of its own or by changing the ones it was handed, and not when it hands the resolution on.
func TestFramePacingGateFollowsToolRendererResolvers(t *testing.T) {
	render := func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
		return pacingTestComponent{}
	}
	for _, tc := range []struct {
		name     string
		resolver extension.ToolRendererResolver
		closes   bool
	}{
		{"hands on", func(_ string, next func() *extension.ToolRenderers) *extension.ToolRenderers { return next() }, false},
		{"hands on after asking twice", func(_ string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
			next()
			return next()
		}, false},
		{"answers with its own renderers", func(string, func() *extension.ToolRenderers) *extension.ToolRenderers {
			return &extension.ToolRenderers{RenderCall: render}
		}, true},
		{"answers with a copy of the ones it was handed", func(_ string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
			copied := *next()
			return &copied
		}, true},
		{"changes the ones it was handed", func(_ string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
			handed := next()
			handed.RenderCall = render
			return handed
		}, true},
		{"changes the shell of the ones it was handed", func(_ string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
			handed := next()
			handed.RenderShell = extension.ToolRenderShellSelf
			return handed
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newLifecycleMode(t)
			m.newRunner = inproc.NewRunner([]extension.Extension{{Name: "resolver", ToolRenderers: []extension.ToolRendererResolver{tc.resolver}}}, m.opts.AgentDir)
			m.framePacingInstalled = true
			m.turnActive.Store(true)
			if m.resolveToolRenderers("read") == nil {
				t.Fatal("the built-in tool resolved to no renderers")
			}
			m.runScheduledRender()
			if got := !m.framePacingAllowed.Load(); got != tc.closes {
				t.Fatalf("one frame after the resolution the gate is closed = %v, want %v", got, tc.closes)
			}
		})
	}
}

// Without the policy the gate is never written: a run with Pi's interval carries nothing of the policy.
func TestFramePacingGateIsUntouchedWithoutThePolicy(t *testing.T) {
	m, _, _ := newLifecycleMode(t)
	m.turnActive.Store(true)
	m.runScheduledRender()
	if m.framePacingAllowed.Load() {
		t.Fatal("the gate was opened though no policy is installed")
	}
}

// pacingRecorder is a renderer that keeps the policies it is given.
type pacingRecorder struct {
	tui.TUI
	policies []*tui.FramePacing
}

func (r *pacingRecorder) SetFramePacing(pacing *tui.FramePacing) {
	r.policies = append(r.policies, pacing)
}

// The default is decided in one constant, so both of its values are tested here: with either, a setting or a PIG_FRAME_PACING that names a policy wins, and a run that names none gets the default. Under "pi" such a run does not select the adaptive policy, under "adaptive" it does.
func TestFramePacingSelectionUnderEitherDefault(t *testing.T) {
	for _, byDefault := range []string{TerminalFramePacingPi, TerminalFramePacingAdaptive} {
		unselected := byDefault == TerminalFramePacingAdaptive
		for _, tc := range []struct {
			setting, env string
			want         bool
		}{
			{"", "", unselected}, {"Pi", "", unselected}, {"0", "", unselected}, {"faster", "", unselected}, {"", "1", unselected}, {"", "PI", unselected},
			{"adaptive", "", true}, {"pi", "", false},
			{"", "adaptive", true}, {"", "pi", false}, {"", "0", false},
			{"adaptive", "pi", false}, {"adaptive", "0", false}, {"pi", "adaptive", true}, {"pi", "1", false}, {"adaptive", "1", true},
		} {
			setting := terminalFramePacingOr(tc.setting, byDefault)
			if setting != TerminalFramePacingPi && setting != TerminalFramePacingAdaptive {
				t.Errorf("default %q: terminal.framePacing %q reads as %q, which names no frame pacing", byDefault, tc.setting, setting)
			}
			getenv := func(name string) string {
				if name == framePacingEnv {
					return tc.env
				}
				return ""
			}
			if got := adaptiveFramePacingSelected(setting, getenv); got != tc.want {
				t.Errorf("default %q, terminal.framePacing %q, %s=%q: adaptive = %v, want %v", byDefault, tc.setting, framePacingEnv, tc.env, got, tc.want)
			}
		}
	}
}

// A run that selects no frame pacing has Pi's interval: the settings read "pi" and the renderer is given no policy, so nothing of the adaptive policy exists in the run. The setting and PIG_FRAME_PACING each opt one run in.
func TestARunThatSelectsNoFramePacingHasPisInterval(t *testing.T) {
	const shipped = TerminalFramePacingPi
	if terminalFramePacingDefault != shipped {
		t.Fatalf("the default frame pacing is %q, want %q", terminalFramePacingDefault, shipped)
	}
	if got := (Settings{}).GetTerminalFramePacing(); got != shipped {
		t.Fatalf("settings without terminal.framePacing read %q, want %q", got, shipped)
	}
	if got := NewSettingsManager(t.TempDir(), t.TempDir()).GetTerminalFramePacing(); got != shipped {
		t.Fatalf("with no settings file terminal.framePacing reads %q, want %q", got, shipped)
	}
	for _, tc := range []struct {
		setting, env string
		policies     int
	}{{"", "", 0}, {"faster", "", 0}, {"", "1", 0}, {"adaptive", "", 1}, {"", "adaptive", 1}} {
		t.Setenv(framePacingEnv, tc.env)
		m, _, _ := newLifecycleMode(t)
		m.opts.Settings.TerminalFramePacing = tc.setting
		renderer := &pacingRecorder{TUI: m.tuiInst}
		m.tuiInst = renderer
		m.installFramePacing()
		if got := len(renderer.policies); got != tc.policies || m.framePacingInstalled != (tc.policies == 1) {
			t.Errorf("terminal.framePacing %q, %s=%q: the renderer was given %d policies (installed %v), want %d", tc.setting, framePacingEnv, tc.env, got, m.framePacingInstalled, tc.policies)
		}
	}
}

// The policy is installed at start, once, only when the run selects it: the environment decides when it names a policy, else the terminal.framePacing setting, and a missing or invalid setting is the default. A run that has Pi's interval gives the renderer nothing.
func TestInstallFramePacingFollowsTheSelection(t *testing.T) {
	unselected := terminalFramePacingDefault == TerminalFramePacingAdaptive
	for _, tc := range []struct {
		setting, env string
		want         bool
	}{
		{"", "", unselected}, {"adaptive", "", true}, {"pi", "", false}, {"Pi", "", unselected}, {"0", "", unselected}, {"faster", "", unselected},
		{"", "pi", false}, {"", "0", false}, {"adaptive", "pi", false}, {"adaptive", "0", false}, {"", "adaptive", true},
		{"pi", "adaptive", true}, {"pi", "1", false}, {"pi", "PI", false}, {"adaptive", "1", true},
	} {
		t.Setenv(framePacingEnv, tc.env)
		m, _, _ := newLifecycleMode(t)
		m.opts.Settings.TerminalFramePacing = tc.setting
		renderer := &pacingRecorder{TUI: m.tuiInst}
		m.tuiInst = renderer
		m.installFramePacing()
		m.installFramePacing()
		if m.framePacingInstalled != tc.want {
			t.Errorf("terminal.framePacing %q, %s=%q: policy installed = %v, want %v", tc.setting, framePacingEnv, tc.env, m.framePacingInstalled, tc.want)
		}
		if got := len(renderer.policies); (got == 1) != tc.want || got > 1 || (got == 1 && renderer.policies[0] == nil) {
			t.Errorf("terminal.framePacing %q, %s=%q: the renderer was given %d policies %v, want one policy only when the run selects the adaptive one", tc.setting, framePacingEnv, tc.env, got, renderer.policies)
		}
	}
}

// terminal.framePacing is the default unless it names the other pacing, is read from the global file only, and a rewrite of the settings file keeps it.
func TestTerminalFramePacingSetting(t *testing.T) {
	byDefault, other := terminalFramePacingDefault, TerminalFramePacingAdaptive
	if byDefault == TerminalFramePacingAdaptive {
		other = TerminalFramePacingPi
	}
	agentDir, cwd := t.TempDir(), t.TempDir()
	if got := NewSettingsManager(cwd, agentDir).GetTerminalFramePacing(); got != byDefault {
		t.Fatalf("with no settings file = %q, want the default %q", got, byDefault)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSettingsFixture(t, filepath.Join(cwd, ".pig", "settings.json"), `{"terminal":{"framePacing":"`+other+`","showTerminalProgress":true}}`)
	project := NewSettingsManager(cwd, agentDir)
	if got, merged := project.GetTerminalFramePacing(), project.Get().GetTerminalFramePacing(); got != byDefault || merged != byDefault {
		t.Fatalf("a project file selected %q (merged view %q)", got, merged)
	}
	if !project.GetShowTerminalProgress() {
		t.Fatalf("the project file's other terminal keys were not read")
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	for value, want := range map[string]string{"adaptive": TerminalFramePacingAdaptive, "pi": TerminalFramePacingPi, "": byDefault, "Pi": byDefault, "0": byDefault, "fast": byDefault} {
		writeSettingsFixture(t, settingsPath, `{"terminal":{"framePacing":"`+value+`"}}`)
		sm := NewSettingsManager(cwd, agentDir)
		if got, merged := sm.GetTerminalFramePacing(), sm.Get().GetTerminalFramePacing(); got != want || merged != want {
			t.Errorf("terminal.framePacing %q reads as %q (merged view %q), want %q", value, got, merged, want)
		}
	}
	writeSettingsFixture(t, settingsPath, `{"terminal":{"framePacing":"`+other+`","screenWriter":"pi","clearOnShrink":true}}`)
	sm := NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateGlobal(func(s *Settings) { s.Theme = "light" }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"framePacing": "`+other+`"`) && !strings.Contains(string(data), `"framePacing":"`+other+`"`) {
		t.Fatalf("the rewritten settings file lost terminal.framePacing: %s", data)
	}
	if got := NewSettingsManager(cwd, agentDir).GetTerminalFramePacing(); got != other {
		t.Fatalf("after the rewrite the setting reads %q, want %q", got, other)
	}
	if got := NewSettingsManager(cwd, agentDir).GetTerminalScreenWriter(); got != TerminalScreenWriterPi {
		t.Fatalf("after the rewrite terminal.screenWriter reads %q, want %q", got, TerminalScreenWriterPi)
	}
	// A setter that changes the key writes the new value: the file's own copy of the key is not what keeps it.
	if err := NewSettingsManager(cwd, agentDir).UpdateGlobal(func(s *Settings) { s.TerminalFramePacing = byDefault }); err != nil {
		t.Fatal(err)
	}
	if data, err = os.ReadFile(settingsPath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"framePacing": "`+byDefault+`"`) && !strings.Contains(string(data), `"framePacing":"`+byDefault+`"`) {
		t.Fatalf("a setter that changed terminal.framePacing to %q left the file without it: %s", byDefault, data)
	}
}

// The adaptive policy without the screen writer is the one selection that does nothing. Each half is decided as the run decides it: PIG_FRAME_PACING before terminal.framePacing, PIG_TUI_SCREEN_WRITER before terminal.screenWriter, and a missing or invalid setting is its default.
func TestAdaptiveFramePacingLacksScreenWriter(t *testing.T) {
	pacingUnset := terminalFramePacingDefault == TerminalFramePacingAdaptive
	writerUnset := TerminalScreenWriterDefault == TerminalScreenWriterWriter
	for _, tc := range []struct {
		pacing, pacingEnv, writer, writerEnv string
		want                                 bool
	}{
		{"adaptive", "", "pi", "", true},
		{"adaptive", "", "", "", !writerUnset},
		{"adaptive", "", "Writer", "", !writerUnset},
		{"", "adaptive", "", "", !writerUnset},
		{"pi", "adaptive", "pi", "", true},
		{"adaptive", "", "writer", "pi", true},
		{"adaptive", "", "writer", "0", true},
		{"", "adaptive", "writer", "pi", true},
		{"adaptive", "", "writer", "", false},
		{"", "adaptive", "", "writer", false},
		{"", "adaptive", "", "1", false},
		{"adaptive", "", "pi", "writer", false},
		{"pi", "adaptive", "writer", "", false},
		{"adaptive", "pi", "pi", "", false},
		{"adaptive", "0", "", "", false},
		{"pi", "", "pi", "", false},
		{"pi", "", "writer", "", false},
		{"pi", "", "", "writer", false},
		{"", "", "pi", "", pacingUnset},
		{"faster", "1", "pi", "", pacingUnset},
		{"", "", "writer", "", false},
	} {
		getenv := func(name string) string {
			switch name {
			case framePacingEnv:
				return tc.pacingEnv
			case "PIG_TUI_SCREEN_WRITER":
				return tc.writerEnv
			}
			return ""
		}
		settings := Settings{TerminalFramePacing: tc.pacing, TerminalScreenWriter: tc.writer}
		if got := adaptiveFramePacingLacksScreenWriter(settings.GetTerminalFramePacing(), settings.GetTerminalScreenWriter(), getenv); got != tc.want {
			t.Errorf("terminal.framePacing %q, %s=%q, terminal.screenWriter %q, PIG_TUI_SCREEN_WRITER=%q: adaptive without the screen writer = %v, want %v", tc.pacing, framePacingEnv, tc.pacingEnv, tc.writer, tc.writerEnv, got, tc.want)
		}
	}
}

// Interactive mode says once, among the notices of its start, that the adaptive frame pacing the run selects does nothing without the screen writer: the warning is in the transcript and on the screen one time. A run that selects both, by any mix of the settings file and the environment, only the screen writer, or neither shows nothing of it.
func TestStartupNoticeWhenAdaptivePacingLacksTheScreenWriter(t *testing.T) {
	writerUnset := 1
	if TerminalScreenWriterDefault == TerminalScreenWriterWriter {
		writerUnset = 0
	}
	for _, tc := range []struct {
		name, terminal, pacingEnv, writerEnv string
		notices                              int
	}{
		{"the setting selects adaptive, the writer is unset", `{"framePacing":"adaptive"}`, "", "", writerUnset},
		{"the setting selects adaptive, the writer is pi", `{"framePacing":"adaptive","screenWriter":"pi"}`, "", "", 1},
		{"the environment selects adaptive, the writer is unset", "", "adaptive", "", writerUnset},
		{"the environment selects adaptive over a setting of pi", `{"framePacing":"pi","screenWriter":"pi"}`, "adaptive", "", 1},
		{"the environment turns off the writer the setting selects", `{"framePacing":"adaptive","screenWriter":"writer"}`, "", "pi", 1},
		{"both by the settings file", `{"framePacing":"adaptive","screenWriter":"writer"}`, "", "", 0},
		{"both by the environment", "", "adaptive", "writer", 0},
		{"adaptive by the setting, the writer by the environment", `{"framePacing":"adaptive"}`, "", "writer", 0},
		{"adaptive by the environment, the writer by the setting", `{"screenWriter":"writer"}`, "adaptive", "", 0},
		{"the environment turns off the pacing the setting selects", `{"framePacing":"adaptive"}`, "pi", "", 0},
		{"only the writer", `{"screenWriter":"writer"}`, "", "", 0},
		{"neither", "", "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(framePacingEnv, tc.pacingEnv)
			r := startScreenWriterRun(t, tc.writerEnv, tc.terminal, "fullscreen", nil)
			r.mode.showInitNotices()
			// The notices ask for a frame, as Pi's showWarning does (interactive-mode.ts:4818); the frame draws them when the owner loop runs it.
			r.mode.runRequestedFrame()
			if got := strings.Count(reloadChatText(r.mode), "Warning: "+framePacingWithoutScreenWriterNotice); got != tc.notices {
				t.Errorf("the transcript holds the notice %d times, want %d:\n%s", got, tc.notices, reloadChatText(r.mode))
			}
			written, err := os.ReadFile(r.written)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(stripANSITest(string(written)), "Warning: Adaptive frame pacing"); got != tc.notices {
				t.Errorf("the frames drew the notice's first row %d times, want %d", got, tc.notices)
			}
		})
	}
}
