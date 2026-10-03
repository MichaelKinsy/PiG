package main

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func askProjectTrusted(t *testing.T, bridge *subprocess.UIBridge) bool {
	t.Helper()
	result, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: "isProjectTrusted"})
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Trusted bool `json:"trusted"`
	}
	if err := json.Unmarshal(result.Result, &answer); err != nil {
		t.Fatal(err)
	}
	return answer.Trusted
}

// Pi answers ctx.isProjectTrusted() from the Session's settings manager (agent-session.ts:3355), so a project is trusted only when that manager says so. Before a Session exists Pig's binding has no settings manager to ask; it answers false, because a project nothing has established as trusted must not read as trusted. It answered true.
func TestHeadlessIsProjectTrustedFailsClosedWithoutASession(t *testing.T) {
	bridge := subprocess.NewUIBridge(func() {})
	bindSessionExtensionActions(nil, bridge, func() *coding.Session { return nil }, extension.ContextActions{})
	if askProjectTrusted(t, bridge) {
		t.Fatal("isProjectTrusted answered true with no Session and no mode-supplied answer")
	}
}

// A mode that supplies its own answer keeps it, whether or not a Session exists.
func TestHeadlessIsProjectTrustedUsesTheModesAnswer(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		bridge := subprocess.NewUIBridge(func() {})
		bindSessionExtensionActions(nil, bridge, func() *coding.Session { return nil }, extension.ContextActions{IsProjectTrusted: func() bool { return trusted }})
		if got := askProjectTrusted(t, bridge); got != trusted {
			t.Errorf("mode answer %t reached the extension as %t", trusted, got)
		}
	}
}
