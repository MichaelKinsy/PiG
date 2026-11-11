package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/childsession"
)

// The async contract of the AgentSession members for the Lisp SDK: the child_abort_probe and child_order_probe tools of the Go fixture (testfixture/childsession/async_probes.go) as a Lisp extension (testdata/child-session-lisp). A member Pi declares `async` rides childSession.opAsync and never holds a later call; the abort of a running execute-bash is abort-bash from another thread or a cancelled :ctx.

func loadChildAsyncLisp(t *testing.T, wrap func(subprocess.ChildSession) subprocess.ChildSession) (extension.Extension, *coding.AgentSessionServices) {
	t.Helper()
	return loadChildAsyncLispIn(t, "strict", wrap)
}

// loadChildAsyncLispIn loads the child-session fixture isolated ("strict") or packed with a sibling ("shared-ok").
func loadChildAsyncLispIn(t *testing.T, isolation string, wrap func(subprocess.ChildSession) subprocess.ChildSession) (extension.Extension, *coding.AgentSessionServices) {
	t.Helper()
	host, services := newChildSessionHostWith(t, true, wrap)
	cfg := lispFixtureConfig(t, "child-session-lisp", childsession.Name)
	cfg.Isolation = isolation
	configs := []subprocess.ExtConfig{cfg}
	if isolation == "shared-ok" {
		peer := packedFlagFactory(t, findModuleRoot(t), "lisp", "child-session-peer", false)
		peer.Isolation = isolation
		configs = append(configs, peer)
	}
	loaded, failures := host.LoadAll(t.Context(), configs)
	if len(failures) != 0 || len(loaded) != len(configs) {
		t.Fatalf("load: %v", failures)
	}
	return loaded[0], services
}

func runChildAsyncLispProbe(t *testing.T, ext extension.Extension, tool, args string) map[string]any {
	t.Helper()
	return runChildTool(t, ext, tool, args)
}

// TestChildAgentSessionAbortEndsAwaitedMemberLisp: execute-bash "sleep 25" in one thread, abort-bash from another after 0.7 s: the abort must not queue behind the awaited command, and the command comes back cancelled long before it would end.
func TestChildAgentSessionAbortEndsAwaitedMemberLisp(t *testing.T) {
	ext, _ := loadChildAsyncLisp(t, nil)
	got := runChildAsyncLispProbe(t, ext, "child_abort_probe", `{}`)
	if got["returned"] != true || got["cancelled"] != true {
		t.Fatalf("an awaited member aborted from another thread did not return cancelled: %v", got)
	}
	if seconds, _ := got["seconds"].(float64); seconds > 8 {
		t.Fatalf("the abort waited behind the awaited member: %v s", seconds)
	}
}

// TestChildAgentSessionAbortNotHeldBehindAwaitedMemberLisp: with one awaited member held in the host for 3 s, an abort-bash sent 0.4 s later from another thread is served by the host before the member is released (heldProbe, child_session_held_test.go), isolated and packed with a sibling. Every member Pi declares `async` that reaches childSession.op and can be held without a run is listed.
func TestChildAgentSessionAbortNotHeldBehindAwaitedMemberLisp(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		for _, held := range heldCases() {
			member := held.member
			t.Run(isolation+"/"+held.name, func(t *testing.T) {
				probe := newHeldProbe(member, held.delay)
				ext, _ := loadChildAsyncLispIn(t, isolation, probe.wrap)
				probe.start(3 * time.Second)
				runChildAsyncLispProbe(t, ext, "child_order_probe", `{"first":"`+member+`"}`)
				probe.check(t)
			})
		}
	}
}

// TestChildAgentSessionCtxCancelLisp: the host cancels the tool's context while execute-bash "sleep 20" :ctx runs. The :ctx runs Pi's abort (abortBash), so the command comes back cancelled in seconds, and compact and navigate-tree given the cancelled :ctx refuse at once. A :ctx the SDK ignores leaves the command running its 20 s.
func TestChildAgentSessionCtxCancelLisp(t *testing.T) {
	ext, _ := loadChildAsyncLisp(t, nil)
	out := filepath.Join(t.TempDir(), "observed.json")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ext.Tools["child_ctx_cancel"].Definition.Execute(ctx, "call", json.RawMessage(`{"out":`+quoteJSON(out)+`}`), nil)
	}()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	var raw []byte
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			raw = b
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if raw == nil {
		t.Fatal("the tool did not finish within 12 s of its context's cancellation: :ctx did not run abortBash")
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	if bash, _ := got["bash"].(map[string]any); bash["cancelled"] != true {
		t.Errorf("execute-bash :ctx = %v, want cancelled", got["bash"])
	}
	if seconds, _ := got["seconds"].(float64); seconds > 8 {
		t.Errorf("execute-bash :ctx took %v s after the cancellation", seconds)
	}
	for _, key := range []string{"compact", "navigate"} {
		if text, _ := got[key].(string); !strings.Contains(text, "context cancelled") {
			t.Errorf("%s with a cancelled :ctx = %q, want the context-cancelled refusal", key, got[key])
		}
	}
	<-done
}

// TestChildAgentSessionContextUsageLisp: a session on a model with a context window answers a usage object, which the null of a model without one (child_queue) is not.
func TestChildAgentSessionContextUsageLisp(t *testing.T) {
	ext, services := loadChildAsyncLisp(t, nil)
	if err := services.Registry().RegisterExtensionProvider("windowed", extension.ProviderConfig{
		API: ai.APIOpenAICompletions, BaseURL: "https://windowed.invalid", APIKey: "windowed-key",
		Models:       []extension.ProviderModelConfig{{ID: "echo", Name: "Windowed", ContextWindow: 4096}},
		StreamSimple: childSentinelStream,
	}); err != nil {
		t.Fatal(err)
	}
	got := runChildTool(t, ext, "child_usage", `{"provider":"windowed","model":"echo"}`)
	usage, _ := got["usage"].(map[string]any)
	if usage == nil || usage["contextWindow"] != 4096.0 {
		t.Fatalf("usage = %v, want the model's context window 4096", got["usage"])
	}
	if !reflect.DeepEqual(mapKeys(usage), []string{"contextWindow", "percent", "tokens"}) {
		t.Errorf("usage keys = %v", mapKeys(usage))
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
