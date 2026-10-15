package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// packages/coding-agent/test/suite/regressions/2860-replaced-session-context.test.ts:109,123 bind `newSession: async (options) => runtime.newSession(options)` and `switchSession: async (sessionPath, options) => runtime.switchSession(sessionPath, options)` as the command context actions, and the test's third case (:243-290) drives replacement through them: a new Session is created (cancelled false), prompted, and switched back to by path.
// Runtime.ExtensionCommandActions is that binding for Go's print, RPC and interactive modes. This test calls its plain NewSession and SwitchSession (the context-free actions) with Pi's sequence: root prompt, NewSession(nil) -> not cancelled and a new Session file, prompt "target", SwitchSession(root) -> the root Session file and conversation, SwitchSession(target) -> the target conversation.
// mutation-checked: NewSession returning cancelled without replacing, NewSession reporting success without replacing, and SwitchSession switching to the current path each fail it.
func TestRuntimeCommandActionsNewAndSwitchSessionReplaceTheSessionUpstream(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	h.provider.responses = []scriptedResponse{fauxReply("root reply", ai.StopReasonStop, 0), fauxReply("target reply", ai.StopReasonStop, 0)}
	runtimePrompt(t, h.runtime, "root")
	rootPath := h.runtime.Session().Path()

	var actions extension.CommandActions = h.runtime.ExtensionCommandActions(h.runtime.Session())
	result, err := actions.NewSession(nil)
	if err != nil || result.Cancelled {
		t.Fatalf("NewSession = %+v, %v; want not cancelled", result, err)
	}
	targetPath := h.runtime.Session().Path()
	if targetPath == rootPath {
		t.Fatalf("NewSession kept the Session file %s", rootPath)
	}
	runtimePrompt(t, h.runtime, "target")

	actions = h.runtime.ExtensionCommandActions(h.runtime.Session())
	if result, err := actions.SwitchSession(rootPath, nil); err != nil || result.Cancelled {
		t.Fatalf("SwitchSession(root) = %+v, %v", result, err)
	}
	if got := h.runtime.Session().Path(); got != rootPath {
		t.Fatalf("after SwitchSession(root) path = %s, want %s", got, rootPath)
	}
	if got, want := replacedConversation(h.runtime.Session()), []string{"user:root", "assistant:root reply"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("root conversation = %v, want %v", got, want)
	}

	actions = h.runtime.ExtensionCommandActions(h.runtime.Session())
	if _, err := actions.SwitchSession(targetPath, nil); err != nil {
		t.Fatal(err)
	}
	if got := h.runtime.Session().Path(); got != targetPath {
		t.Fatalf("after SwitchSession(target) path = %s, want %s", got, targetPath)
	}
	if got, want := replacedConversation(h.runtime.Session()), []string{"user:target", "assistant:target reply"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("target conversation = %v, want %v", got, want)
	}
}
