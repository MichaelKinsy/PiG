package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

var conformanceFlagNames = []string{"flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"}

func conformanceFlagDeclarations() map[string]extension.ExtensionFlag {
	return map[string]extension.ExtensionFlag{
		"flag-true":   {Name: "flag-true", Type: "boolean", Default: true},
		"flag-false":  {Name: "flag-false", Type: "boolean", Default: false},
		"flag-string": {Name: "flag-string", Type: "string", Default: "default"},
		"flag-empty":  {Name: "flag-empty", Type: "string", Default: ""},
		"flag-unset":  {Name: "flag-unset", Type: "string"},
	}
}

// runnerFlagAPI is the Go reference for extension.API.GetFlag: the production Runner's flag values, restricted to the flags the loaded
// extension declared (loader.ts:307-347), which is what the host answers for every SDK.
type runnerFlagAPI struct {
	extension.API
	runner *inproc.Runner
}

func (a runnerFlagAPI) GetFlag(name string) any {
	if _, declared := a.runner.Flags()[name]; !declared {
		return nil
	}
	return a.runner.GetFlagValues()[name]
}

// loader.ts:307-347 initializes first defaults, preserves falsy overrides, and
// restricts getFlag to the requesting extension's declarations (Pi getFlag,
// packages/coding-agent/src/core/extensions/types.ts:1669). The default phases read the Runner through extension.API and require it to
// equal the literal list, so neither the Runner nor the literal can drift alone. Every nonempty
// override differs from its SDK fallback; the empty override must not default.
func TestFlagValuesAcrossSDKs(t *testing.T) {
	t.Parallel()
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var api extension.API = runnerFlagAPI{runner: h.runner}
			runnerDefaults := make([]any, 0, len(conformanceFlagNames))
			for _, name := range conformanceFlagNames {
				runnerDefaults = append(runnerDefaults, api.GetFlag(name))
			}
			if literal := []any{true, false, "default", "", nil, nil}; !reflect.DeepEqual(runnerDefaults, literal) {
				t.Fatalf("the Runner's flag defaults = %#v, want %#v", runnerDefaults, literal)
			}
			cmd, ok := findCommand(h.runner, "flag-probe")
			if !ok {
				t.Fatal("flag-probe missing")
			}
			for _, phase := range []struct {
				name      string
				overrides map[string]any
				want      []any
			}{
				{"defaults", nil, runnerDefaults},
				{"overrides", map[string]any{"flag-true": false, "flag-false": true, "flag-string": "", "flag-empty": "configured", "flag-unset": "supplied", "unregistered": "must-not-leak"}, []any{false, true, "", "configured", "supplied", nil}},
				{"defaults-restored", nil, runnerDefaults},
				{"shared-default", nil, []any{true, false, "default", "", "peer-default", nil}},
			} {
				t.Run(phase.name, func(t *testing.T) {
					if phase.name == "shared-default" {
						if h.host == nil {
							h.runner.SetFlagValue("flag-unset", "peer-default")
						} else {
							path := filepath.Join(t.TempDir(), "flag-peer.mjs")
							if err := os.WriteFile(path, []byte(`export default function(pi) { pi.registerFlag("flag-unset", {type:"string", default:"peer-default"}); pi.registerFlag("flag-false", {type:"boolean", default:true}); }`), 0o600); err != nil {
								t.Fatal(err)
							}
							if _, err := h.host.Load(t.Context(), subprocess.ExtConfig{Name: "flag-peer", Source: path, Enabled: true}); err != nil {
								t.Fatal(err)
							}
						}
					}
					lookup := func(name string) any { return phase.overrides[name] }
					h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetFlagValue: lookup}, nil)
					if h.bridge != nil {
						h.bridge.SetHostAction("getFlag", func(_, name string) any { return lookup(name) })
					}
					h.ui.ClearRecorded()
					if err := cmd.Handler(h.runner.DispatchContext(context.Background()), ""); err != nil {
						t.Fatal(err)
					}
					waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 })
					var got []any
					message, hasLevel := strings.CutSuffix(h.ui.Recorded()[0], ":info")
					if !hasLevel {
						t.Fatalf("flag probe notification: %q", h.ui.Recorded()[0])
					}
					if err := json.Unmarshal([]byte(message), &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, phase.want) {
						t.Fatalf("flags = %#v, want %#v", got, phase.want)
					}
				})
			}
		})
	}
}
