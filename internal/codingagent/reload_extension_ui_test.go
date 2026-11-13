package codingagent

import (
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi resets what extensions put on screen when a /reload begins and before a Session replacement invalidates the outgoing Session's
// extensions: handleReloadCommand calls resetExtensionUI before it shows the reload box (interactive-mode.ts:6628 at Pi 1.5.2), the
// constructor installs it as the runtime host's beforeSessionInvalidate (:643), and resetExtensionUI (:2592-2623) disposes the dialogs,
// removes the overlays (:2602) and the terminal input listeners (:2603), restores the built-in footer (:2604) and header (:2605), clears
// the widgets (:2606) and the statuses (:2607), forgets the editor component (:2610), writes the session's terminal title (:2613), and
// returns the working message, visibility and indicator (:2614-2616) and the hidden thinking label (:2622) to their defaults. The
// extensions that run afterwards set their own again: what an extension set before and does not set again is gone. Each test sets one
// of them from a host-process extension (the UI context) and from an extension process (the bridge), resets, and requires Pi's state;
// a UI bound afterwards (a TUI mode switch binds one) must not be shown what the bridge kept of the replaced extension processes.

// extensionUIResets are the two callers of Pi's resetExtensionUI.
var extensionUIResets = []struct {
	name string
	run  func(t *testing.T, m *InteractiveMode)
}{
	{"reload", func(t *testing.T, m *InteractiveMode) {
		t.Helper()
		if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
			t.Fatal(err)
		}
	}},
	{"session replacement", func(t *testing.T, m *InteractiveMode) {
		t.Helper()
		autocompleteOwnerCall(t, m, func() error { m.resetExtensionUIForReplacement(); return nil })
	}},
}

// extensionUIProbe is an interactive mode with the bridge of the extension processes bound to it.
type extensionUIProbe struct {
	m      *InteractiveMode
	bridge *subprocess.UIBridge
	ui     *ExtUIContext
}

// eachExtensionUIReset runs probe once for each caller of the reset.
func eachExtensionUIReset(t *testing.T, probe func(t *testing.T, p *extensionUIProbe, reset func())) {
	for _, by := range extensionUIResets {
		t.Run(by.name, func(t *testing.T) {
			m := newFullscreenProbe(t)
			bridge := subprocess.NewUIBridge(func() {})
			m.opts.SubprocessUIBridge = bridge
			m.attachSubprocess()
			t.Cleanup(m.detachSubprocess)
			p := &extensionUIProbe{m: m, bridge: bridge, ui: &ExtUIContext{m: m}}
			probe(t, p, func() {
				t.Helper()
				by.run(t, m)
				runPostedOwnerTasks(m)
			})
		})
	}
}

// call makes a UI call of an extension process.
func (p *extensionUIProbe) call(t *testing.T, method string, args any) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.bridge.HandleCall("proc", &subprocess.CallPayload{Method: method, Args: raw})
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if result != nil && result.Error != nil {
		t.Fatalf("%s: %s", method, result.Error.Message)
	}
	runPostedOwnerTasks(p.m)
}

// rebind binds the UI to the bridge again, as a TUI mode switch does.
func (p *extensionUIProbe) rebind() {
	p.bridge.SetUIContext(&ExtUIContext{m: p.m})
	runPostedOwnerTasks(p.m)
}

// extensionInputListeners counts the raw terminal input listeners extensions registered.
func (p *extensionUIProbe) extensionInputListeners() int {
	p.m.terminalInputMu.Lock()
	defer p.m.terminalInputMu.Unlock()
	count := 0
	for _, listener := range p.m.terminalInputListeners {
		if !listener.builtin {
			count++
		}
	}
	return count
}

// disposableLines is the footer or header component of a host-process extension.
type disposableLines struct {
	lines    []string
	disposed *int
}

func (d disposableLines) Render(int) []string { return d.lines }
func (d disposableLines) Invalidate()         {}
func (d disposableLines) Dispose()            { *d.disposed++ }

func TestExtensionUIResetClearsTheStatuses(t *testing.T) {
	eachExtensionUIReset(t, func(t *testing.T, p *extensionUIProbe, reset func()) {
		p.ui.SetStatus("host", "status of a host-process extension")
		p.call(t, "ui.setStatus", map[string]any{"key": "process", "text": "status of an extension process"})
		statuses := func() map[string]string { return p.m.statusLine.GetExtensionStatuses() }
		if got := statuses(); len(got) != 2 {
			t.Fatalf("before the reset the footer holds the statuses %v, want both", got)
		}
		reset()
		if got := statuses(); len(got) != 0 {
			t.Errorf("after the reset the footer still holds the statuses %v; Pi clears them (clearExtensionStatuses)", got)
		}
		p.rebind()
		if got := statuses(); len(got) != 0 {
			t.Errorf("a UI bound after the reset is shown the statuses %v of the replaced extension processes", got)
		}
	})
}

func TestExtensionUIResetRestoresTheFooterAndTheHeader(t *testing.T) {
	for _, slot := range []string{"footer", "header"} {
		for _, from := range []string{"host process", "extension process"} {
			t.Run(slot+" from a "+from, func(t *testing.T) {
				eachExtensionUIReset(t, func(t *testing.T, p *extensionUIProbe, reset func()) {
					text := slot + " of an extension"
					disposed := 0
					component := disposableLines{lines: []string{text}, disposed: &disposed}
					shown := p.m.extFooter
					switch {
					case slot == "footer" && from == "host process":
						p.ui.SetFooter(func(tui.TUI, *tui.Theme, extension.ReadonlyFooterDataProvider) extension.DisposableComponent {
							return component
						})
					case slot == "header" && from == "host process":
						shown = p.m.extHeader
						p.ui.SetHeader(func(tui.TUI, *tui.Theme) extension.DisposableComponent { return component })
					case slot == "footer":
						p.call(t, "ui.setFooter", map[string]any{"lines": []string{text}, "width": 40, "surfaceId": 1})
					default:
						shown = p.m.extHeader
						p.call(t, "ui.setHeader", map[string]any{"lines": []string{text}, "width": 40, "surfaceId": 1})
					}
					drawn := func() string { return strings.Join(shown.Render(40), "\n") }
					if !strings.Contains(drawn(), text) {
						t.Fatalf("before the reset the %s slot draws %q, want the extension's", slot, drawn())
					}
					reset()
					if strings.Contains(drawn(), text) {
						t.Errorf("after the reset the %s of the replaced extension is still drawn: %q; Pi restores its own", slot, drawn())
					}
					if from == "host process" && disposed != 1 {
						t.Errorf("the reset disposed the %s component %d times, want once", slot, disposed)
					}
					// An update the replaced extension's factory renders late is not installed: its surface is gone.
					if from == "extension process" {
						p.call(t, "ui.set"+strings.ToUpper(slot[:1])+slot[1:], map[string]any{"lines": []string{text}, "width": 40, "surfaceId": 1, "updateOnly": true})
					}
					p.rebind()
					if strings.Contains(drawn(), text) {
						t.Errorf("a UI bound after the reset is shown the %s of the replaced extension: %q", slot, drawn())
					}
				})
			})
		}
	}
}

func TestExtensionUIResetWritesTheSessionTitle(t *testing.T) {
	eachExtensionUIReset(t, func(t *testing.T, p *extensionUIProbe, reset func()) {
		terminal := recordTerminalCalls(t, p.m)
		p.ui.SetTitle("title of a host-process extension")
		p.call(t, "ui.setTitle", map[string]any{"title": "title of an extension process"})
		terminal.takeTitles()
		reset()
		want := tui.BuildTerminalTitle("", p.m.opts.CWD)
		if titles := terminal.takeTitles(); len(titles) == 0 || titles[len(titles)-1] != want {
			t.Errorf("the reset wrote the titles %q on the renderer's terminal, want the session's title %q last; Pi restores it there (updateTerminalTitle)", titles, want)
		}
	})
}

func TestExtensionUIResetRestoresTheWorkingStatus(t *testing.T) {
	eachExtensionUIReset(t, func(t *testing.T, p *extensionUIProbe, reset func()) {
		frames := []string{"<", ">"}
		p.call(t, "ui.setWorkingMessage", map[string]any{"message": "working message of an extension process"})
		p.ui.SetWorkingVisible(false)
		p.ui.SetWorkingIndicator(extension.WorkingIndicatorOptions{Frames: &frames})
		p.ui.SetHiddenThinkingLabel("label of a host-process extension")
		runPostedOwnerTasks(p.m)
		if p.m.workingMessage == nil || p.m.workingVisible || p.m.workingIndicatorOptions == nil || p.m.hiddenThinkingLabel == nil {
			t.Fatalf("before the reset: working message %v, visible %t, indicator %v, hidden thinking label %v; want each set",
				p.m.workingMessage, p.m.workingVisible, p.m.workingIndicatorOptions, p.m.hiddenThinkingLabel)
		}
		reset()
		if p.m.workingMessage != nil {
			t.Errorf("after the reset the working message is still %q, want the default", *p.m.workingMessage)
		}
		if !p.m.workingVisible {
			t.Error("after the reset the working indicator is still hidden")
		}
		if p.m.workingIndicatorOptions != nil {
			t.Errorf("after the reset the working indicator still has the extension's frames %v", p.m.workingIndicatorOptions.Frames)
		}
		if p.m.hiddenThinkingLabel != nil {
			t.Errorf("after the reset the hidden thinking label is still %q, want the default", *p.m.hiddenThinkingLabel)
		}
	})
}

func TestExtensionUIResetForgetsTheEditorComponent(t *testing.T) {
	eachExtensionUIReset(t, func(t *testing.T, p *extensionUIProbe, reset func()) {
		p.ui.SetEditorComponent(func(tui.TUI, tui.EditorTheme, extension.KeybindingsManager) tui.EditorComponent { return nil })
		runPostedOwnerTasks(p.m)
		if p.ui.GetEditorComponent() == nil {
			t.Fatal("before the reset getEditorComponent answers no factory")
		}
		reset()
		if p.ui.GetEditorComponent() != nil {
			t.Error("after the reset getEditorComponent still answers the factory of the replaced extension; Pi's setCustomEditorComponent(undefined) forgets it")
		}
	})
}

func TestExtensionUIResetRemovesTheTerminalInputListeners(t *testing.T) {
	eachExtensionUIReset(t, func(t *testing.T, p *extensionUIProbe, reset func()) {
		heard := 0
		p.ui.OnTerminalInput(func(string) extension.TerminalInputResult {
			heard++
			return extension.TerminalInputResult{}
		})
		p.m.notifyTerminalInput("x")
		if heard != 1 {
			t.Fatalf("before the reset the listener of a host-process extension heard %d inputs of 1", heard)
		}
		hostEnd, peer := net.Pipe()
		t.Cleanup(func() {
			_ = hostEnd.Close()
			_ = peer.Close()
		})
		// The extension process reads what the host sends it.
		go func() { _, _ = io.Copy(io.Discard, peer) }()
		p.bridge.RegisterExtConn("proc", subprocess.NewConn("proc", hostEnd), false)
		p.call(t, "ui.onTerminalInput", nil)
		if got := p.extensionInputListeners(); got != 2 {
			t.Fatalf("before the reset %d listeners of extensions take the terminal input, want the host-process one and the extension process's", got)
		}
		reset()
		if got := p.extensionInputListeners(); got != 0 {
			t.Fatalf("after the reset %d listeners of the replaced extensions still take the terminal input; Pi removes them (clearExtensionTerminalInputListeners)", got)
		}
		p.m.notifyTerminalInput("x")
		if heard != 1 {
			t.Errorf("after the reset the listener of the replaced host-process extension heard an input")
		}
	})
}

func TestExtensionUIResetRemovesTheOverlays(t *testing.T) {
	eachExtensionUIReset(t, func(t *testing.T, p *extensionUIProbe, reset func()) {
		removed := 0
		p.m.addExtensionOverlay(nil, func() {
			removed++
			p.m.deleteExtensionOverlay(nil)
		})
		reset()
		if removed != 1 {
			t.Errorf("the reset removed the overlay of a host-process extension %d times, want once", removed)
		}
	})
}
