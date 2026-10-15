package inproc_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// runner.ts:868-871: getActiveTools asserts the runner is active, then reads the runtime's action, which stays
// `notInitialized` until bindCore (loader.ts:157-159).
func TestRunnerGetActiveToolsReadsTheBoundAction(t *testing.T) {
	r := inproc.NewRunner(nil, "/tmp")
	if _, err := r.GetActiveTools(); !errors.Is(err, extension.ErrRuntimeNotInitialized) {
		t.Fatalf("before bindCore: err = %v, want ErrRuntimeNotInitialized", err)
	}
	active := []string{"read", "bash"}
	r.BindCore(extension.ExtensionActions{GetActiveTools: func() []string { return slices.Clone(active) }}, extension.ContextActions{}, nil)
	got, err := r.GetActiveTools()
	if err != nil || !slices.Equal(got, []string{"read", "bash"}) {
		t.Fatalf("GetActiveTools = %v, %v", got, err)
	}
	active = []string{"edit"}
	if got, _ = r.GetActiveTools(); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("a later change is read at call time: %v", got)
	}
	r.Invalidate("stale for test")
	if _, err := r.GetActiveTools(); err == nil || err.Error() != "stale for test" {
		t.Fatalf("stale runner: err = %v, want the stale message", err)
	}
}

type registryStub struct{ extension.ModelRegistry }

// runner.ts:843-845 and 363, 399-406: getModelRegistry returns the registry given to the runner; a mode binding that
// omits one keeps the Session's, and one that supplies another replaces it.
func TestRunnerGetModelRegistryFollowsTheBinding(t *testing.T) {
	r := inproc.NewRunner(nil, "/tmp")
	if got := r.GetModelRegistry(); got != nil {
		t.Fatalf("before any binding: %v, want nil", got)
	}
	first := &registryStub{}
	r.BindTools(extension.ContextActions{ModelRegistry: first})
	if got := r.GetModelRegistry(); got != extension.ModelRegistry(first) {
		t.Fatalf("after BindTools: %v, want the bound registry", got)
	}
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	if got := r.GetModelRegistry(); got != extension.ModelRegistry(first) {
		t.Fatalf("BindCore without a registry keeps the Session's: %v", got)
	}
	second := &registryStub{}
	r.BindTools(extension.ContextActions{ModelRegistry: second})
	if got := r.GetModelRegistry(); got != extension.ModelRegistry(second) {
		t.Fatalf("a rebinding replaces it: %v", got)
	}
}

// ToolsVersion changes whenever Tools can return a different list: a tool registered after load in any extension, a replaced definition, and the extension count. It stays put across reads.
func TestRunnerToolsVersionTracksEveryRegistryMutation(t *testing.T) {
	exts := []extension.Extension{{Path: "a"}, {Path: "b"}}
	for i := range exts {
		exts[i].InitializeToolRegistry()
	}
	r := inproc.NewRunner(exts, "/tmp")
	seen := map[uint64]string{r.ToolsVersion(): "start"}
	step := func(what string, mutate func()) {
		t.Helper()
		before := r.ToolsVersion()
		_ = r.Tools()
		if r.ToolsVersion() != before {
			t.Fatalf("%s: reading the tools moved the version", what)
		}
		mutate()
		got := r.ToolsVersion()
		if prior, dup := seen[got]; dup {
			t.Fatalf("%s: version %d repeats the one after %q", what, got, prior)
		}
		seen[got] = what
	}
	step("register in the first extension", func() {
		exts[0].SetRegisteredTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "one"}})
	})
	step("register in the second extension", func() {
		exts[1].SetRegisteredTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "two"}})
	})
	step("replace a definition in place", func() {
		exts[0].SetRegisteredTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "one", Description: "changed"}})
	})
}
