package extensionconformance

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// labelRuntimeAPI is the Go reference for extension.API.SetLabel: the handler the Session binds into the shared runtime
// (agent-session.ts:3419 _bindExtensionCore setLabel, copied to runtime.setLabel by runner.ts:410-438).
type labelRuntimeAPI struct {
	extension.API
	runtime *extension.ExtensionRuntime
	t       *testing.T
}

func (a labelRuntimeAPI) SetLabel(entryID string, label string) {
	if err := a.runtime.SetLabel(entryID, &label); err != nil {
		a.t.Fatal(err)
	}
}

// TestConformanceAPISetLabelAppendsALabelEntry pins pi.setLabel (packages/coding-agent/src/core/extensions/types.ts:1721): the label is
// appended to the real Session as a label entry on the target entry (sessionManager.appendLabelChange), and a target that does not exist
// is rejected with "Entry <id> not found".
func TestConformanceAPISetLabelAppendsALabelEntry(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	runtime := extension.CreateExtensionRuntime()
	runner := inproc.NewRunner(nil, t.TempDir(), runtime)
	session, err := coding.NewSession(services, coding.SessionOptions{Runner: runner, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.AppendCustomEntry("conformance-target", map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	var api extension.API = labelRuntimeAPI{runtime: runtime, t: t}

	api.SetLabel(target, "milestone")

	found := false
	for _, entry := range session.Entries() {
		var label struct {
			Type     string `json:"type"`
			TargetID string `json:"targetId"`
			Label    string `json:"label"`
		}
		if err := json.Unmarshal(entry.Raw(), &label); err != nil {
			t.Fatal(err)
		}
		if label.Type == "label" && label.TargetID == target && label.Label == "milestone" {
			found = true
		}
	}
	if !found {
		t.Errorf("no label entry milestone on %s after setLabel; entries %s", target, mustJSON(t, session.Entries()))
	}
	if err := runtime.SetLabel("missing-entry", new("x")); err == nil || err.Error() != "Entry missing-entry not found" {
		t.Errorf("setLabel on a missing entry = %v, want \"Entry missing-entry not found\"", err)
	}
}
