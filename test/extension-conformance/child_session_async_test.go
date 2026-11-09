package extensionconformance

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/childsession"
)

// The async contract of the AgentSession members (docs/extension-api-parity.md, AGENTS.md "TypeScript async/Promise parity"): a member Pi declares `async` rides childSession.opAsync, starts in call order and never holds a later call; cancelling the ctx of a member that has an abort in Pi runs it. Three plumbing breaks fail these rows and no value row: opAsync dropped from startsAsync, no CallInitiated for opAsync, and an awaited member sent over the synchronous childSession.op.

// loadChildProbeHost loads the child session fixture, fused or as an isolated Go subprocess, on a host whose child sessions wrap is applied to.
func loadChildProbeHost(t *testing.T, placement string, wrap func(subprocess.ChildSession) subprocess.ChildSession) extension.Extension {
	t.Helper()
	host, _ := newChildSessionHostWith(t, true, wrap)
	if placement == "fused" {
		ext, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: childsession.Name, Path: "/ext/child-session.go", Enabled: true}, childsession.Extension().RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		return *ext
	}
	root := findModuleRoot(t)
	cfg := subprocess.ExtConfig{
		Name: childsession.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/child-session-go"), Enabled: true, Isolation: "strict",
		RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
		SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/child-session-go", Package: "example.com/child-session-go",
	}
	loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	return loaded[0]
}

func runChildProbe(t *testing.T, ext extension.Extension, tool, args string) map[string]any {
	t.Helper()
	result, err := ext.Tools[tool].Definition.Execute(t.Context(), "call", json.RawMessage(args), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
		t.Fatalf("%q: %v", result.Text(), err)
	}
	return got
}

// TestChildAgentSessionAwaitedMemberCancelledByCtx: ExecuteBash("sleep 25") cancelled by its ctx after 0.7 s runs Pi's abortBash, which must not queue behind the awaited command; Cancelled comes back long before the command would end.
func TestChildAgentSessionAwaitedMemberCancelledByCtx(t *testing.T) {
	for _, placement := range []string{"fused", "isolated"} {
		t.Run(placement, func(t *testing.T) {
			got := runChildProbe(t, loadChildProbeHost(t, placement, nil), "child_abort_probe", `{}`)
			if got["returned"] != true || got["cancelled"] != true {
				t.Fatalf("an awaited member cancelled by its ctx did not return cancelled: %v", got)
			}
			if seconds, _ := got["seconds"].(float64); seconds > 8 {
				t.Fatalf("the abort waited behind the awaited member: %v s", seconds)
			}
		})
	}
}

// TestChildAgentSessionAbortNotHeldBehindAwaitedMember: with one awaited member held in the host for 3 s, an AbortBash sent 0.4 s later from another goroutine is served by the host before the member is released (heldProbe, child_session_held_test.go). Every member Pi declares `async` that reaches childSession.op is listed (steer, followUp and prompt have calls of their own).
func TestChildAgentSessionAbortNotHeldBehindAwaitedMember(t *testing.T) {
	for _, placement := range []string{"fused", "isolated"} {
		for _, held := range heldCases() {
			member := held.member
			t.Run(placement+"/"+held.name, func(t *testing.T) {
				probe := newHeldProbe(member, held.delay)
				ext := loadChildProbeHost(t, placement, probe.wrap)
				probe.start(3 * time.Second)
				runChildProbe(t, ext, "child_order_probe", `{"first":"`+member+`"}`)
				probe.check(t)
			})
		}
	}
}
