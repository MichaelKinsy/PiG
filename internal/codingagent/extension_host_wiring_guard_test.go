package codingagent

import (
	"io"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Interactive mode binds the Session to its extensions as interactive-mode.ts does through session.bindExtensions over agent-session.ts:3275-3408 _bindExtensionCore. The guard runs the production attach path, attachSubprocess and wireInprocContextActions, against a real bridge, and inspects the HostCallbacks it left behind. A field no mode binds is a capability every extension reaches only as the host's fallback, as HostCallbacks.GetSettings was when only a test could set it.
func TestInteractiveModeBindsEveryHostCallback(t *testing.T) {
	cwd := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: cwd, Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewFooterComponent(model, "", nil)
	m.agent = mustNewAgent(agent.AgentOptions{Model: model})
	m.newRunner = inproc.NewRunner(nil, cwd)
	// Production interactive mode always has a Session that owns its replacement actions.
	m.opts.SessionHandle = &replacementRecordingHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}}
	bridge := subprocess.NewUIBridge(func() {})
	m.opts.SubprocessUIBridge = bridge
	m.attachSubprocess()
	defer m.detachSubprocess()
	m.wireInprocContextActions()

	allowed := map[string]string{
		// Deprecated v0.2.0 fields that no extension call reads.
		"GetBranch":  "deprecated: extensions read the branch through sessionRead or the session mirror",
		"GetEntries": "deprecated: extensions read the entries through sessionRead or the session mirror",
	}
	field := reflect.ValueOf(bridge).Elem().FieldByName("actions")
	if !field.IsValid() || field.Kind() != reflect.Pointer || field.IsNil() {
		t.Fatal("interactive mode bound no HostCallbacks, or subprocess.UIBridge no longer keeps them in the actions field")
	}
	typ := reflect.TypeFor[subprocess.HostCallbacks]()
	var unbound []string
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if !field.Elem().Field(i).IsNil() {
			continue
		}
		// Exec and ExecContext are one capability: either binding serves the exec call.
		if (name == "Exec" && !field.Elem().FieldByName("ExecContext").IsNil()) || (name == "ExecContext" && !field.Elem().FieldByName("Exec").IsNil()) {
			continue
		}
		unbound = append(unbound, name)
		if _, ok := allowed[name]; !ok {
			t.Errorf("interactive mode leaves HostCallbacks.%s unbound", name)
		}
	}
	for name := range allowed {
		if !slices.Contains(unbound, name) {
			t.Errorf("interactive mode binds HostCallbacks.%s now; delete its allowance", name)
		}
	}
}
