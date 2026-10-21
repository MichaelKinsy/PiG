package factoryload_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
)

const objectSchema = `{"type":"object"}`

// Pi's extension runtime stubs refreshTools as a no-op until the runner binds (loader.ts createExtensionRuntime), so registerTool during a
// factory run never reaches the session. A reload runs the factories again on a runtime the Session is already bound to: a registration
// must not refresh the live Session then, or the Session runs the prepareLoadout hook of the tool the previous load registered, which reads
// the API that the new load of the same path just retired (issue #267).
func TestRegisterToolDuringLoadDoesNotRefreshTheBoundSession(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	runtime.GetSettings = func() extension.Settings { return extension.Settings{} }
	bus := extension.CreateEventBus()

	var hooks []extension.ToolPrepareLoadoutFunc
	refreshes := 0
	// The bound Session's refresh runs the hook of every registered tool, as applyToolLoadout does for an active tool.
	runtime.RefreshTools = func() error {
		refreshes++
		for _, hook := range hooks {
			var failure error
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						failure = fmt.Errorf("%v", recovered)
					}
				}()
				hook(extension.ToolLoadout{})
			}()
			if failure != nil {
				return failure
			}
		}
		return nil
	}

	first := func(pi extension.API) error {
		hook := func(extension.ToolLoadout) *extension.ToolLoadoutChanges {
			pi.GetSettings()
			return nil
		}
		pi.RegisterTool(extension.ToolDefinition{Name: "first", Parameters: []byte(objectSchema), PrepareLoadout: hook})
		// The Session's registry keeps the tool of the first load until the reload's rebuild replaces it.
		if len(hooks) == 0 {
			hooks = append(hooks, hook)
		}
		return nil
	}
	if _, err := factoryload.LoadExtensionFromFactory(first, ".", bus, runtime, "builtin:first"); err != nil {
		t.Fatalf("startup load: %v", err)
	}
	if refreshes != 0 {
		t.Errorf("startup load refreshed the Session %d times, want 0 (refreshTools is a no-op while a factory runs)", refreshes)
	}

	// The reload: the same path loads again, which retires the first load's API, then another extension registers a tool.
	if _, err := factoryload.LoadExtensionFromFactory(first, ".", bus, runtime, "builtin:first"); err != nil {
		t.Fatalf("reload of first: %v", err)
	}
	second := func(pi extension.API) error {
		pi.RegisterTool(extension.ToolDefinition{Name: "second", Parameters: []byte(objectSchema)})
		return nil
	}
	if _, err := factoryload.LoadExtensionFromFactory(second, ".", bus, runtime, "builtin:second"); err != nil {
		t.Fatalf("reload of second: %v", err)
	}
	if refreshes != 0 {
		t.Errorf("reload refreshed the bound Session %d times while factories ran, want 0", refreshes)
	}
}

// After the load the runner is bound, so a registration from a captured API refreshes the Session once, as Pi's bound runtime does.
func TestRegisterToolAfterLoadRefreshesTheSession(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	refreshes := 0
	runtime.RefreshTools = func() error { refreshes++; return nil }
	var captured extension.API
	_, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		captured = pi
		return nil
	}, ".", extension.CreateEventBus(), runtime, "<v>")
	if err != nil {
		t.Fatal(err)
	}
	captured.RegisterTool(extension.ToolDefinition{Name: "late", Parameters: []byte(objectSchema)})
	if refreshes != 1 {
		t.Fatalf("a registration after load refreshed %d times, want 1", refreshes)
	}
}
