package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func flaggedExtension(seen *[]string, manager *extension.ReadonlySessionManager, registry *extension.ModelRegistry) extension.Extension {
	ext := extension.Extension{Path: "flagged", Flags: map[string]extension.ExtensionFlag{
		"fresh-flag":  {Type: "boolean", Default: true},
		"shared-flag": {Type: "string", Default: "from-extension"},
	}}
	ext.Handlers = map[string][]extension.HandlerFn{"session_start": {func(args ...any) (any, error) {
		ctx := extension.FromContext(args[1].(context.Context))
		*manager, _ = ctx.SessionManager()
		*registry, _ = ctx.ModelRegistry()
		*seen = append(*seen, "session_start")
		return nil, nil
	}}}
	return ext
}

// runner.ts:233-242 (the ExtensionRunner constructor) through its wiring: without a runtime the runner owns a fresh one seeded with the extensions' flag defaults, a supplied runtime is shared and keeps the value a flag already has, and a runner built without a SessionManager or ModelRegistry hands handlers none until a binding supplies them.
func TestNewExtensionRunnerSeedsFlagsAndLeavesUnboundContextEmpty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime *extension.ExtensionRuntime
		want    map[string]any
	}{
		{"fresh runtime", nil, map[string]any{"fresh-flag": true, "shared-flag": "from-extension"}},
		{"shared runtime keeps existing values", func() *extension.ExtensionRuntime {
			shared := extension.CreateExtensionRuntime()
			shared.FlagValues["shared-flag"] = "set-by-user"
			return shared
		}(), map[string]any{"fresh-flag": true, "shared-flag": "set-by-user"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			var manager extension.ReadonlySessionManager
			var registry extension.ModelRegistry
			runner := inproc.NewExtensionRunner([]extension.Extension{flaggedExtension(&seen, &manager, &registry)}, tc.runtime, "/work", nil, nil)
			if runner.Runtime() == nil || (tc.runtime != nil && runner.Runtime() != tc.runtime) {
				t.Fatalf("Runtime() = %p, supplied %p", runner.Runtime(), tc.runtime)
			}
			for name, want := range tc.want {
				if got := runner.Runtime().FlagValues[name]; got != want {
					t.Fatalf("flag %s = %v, want %v", name, got, want)
				}
			}
			if _, err := runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
				t.Fatal(err)
			}
			if len(seen) != 1 || manager != nil || registry != nil {
				t.Fatalf("handler ran %v times with manager=%v registry=%v, want one run with neither", seen, manager, registry)
			}
		})
	}
}
