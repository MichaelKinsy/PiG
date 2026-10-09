//go:build !pig_strip_node_extensions

package subprocess

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The real subprocess return must retain the property distinctions used by Pi runner.ts:136-159.
func TestNodeUserBashResultValidation(t *testing.T) {
	source, err := filepath.Abs("../../../../test/parity/scenarios/extensions-runtime/testdata/ext/user-bash-validation.mjs")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test done") })
	ext, err := host.Load(t.Context(), ExtConfig{Name: "user-bash-validation", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{*ext}, root)
	var reported []*extension.ExtensionError
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
	for _, command := range []string{"valid", "undefined-exit", "missing-exit", "null-exit", "null-path", "both-with-null-operations", "empty", "incomplete", "throws", "observe"} {
		t.Run(command, func(t *testing.T) {
			reported = nil
			result, err := runner.EmitUserBash(t.Context(), extension.UserBashEvent{Type: "user_bash", Command: command, Cwd: root})
			switch command {
			case "valid":
				if err != nil || result == nil {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				value, ok := result.Result.(map[string]any)
				if !ok || value["output"] != "handled" || value["exitCode"] != float64(0) || value["cancelled"] != false || value["truncated"] != false {
					t.Fatalf("result=%+v", result)
				}
			case "undefined-exit":
				if err != nil || result == nil {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				exitCode, present := result.Result.(map[string]any)["exitCode"]
				if !present || exitCode != nil {
					t.Fatalf("exitCode=%v present=%v", exitCode, present)
				}
			case "observe":
				if err != nil || result != nil {
					t.Fatalf("result=%+v error=%v", result, err)
				}
			default:
				want := "Invalid user_bash handler result"
				if command == "throws" {
					want = "Routing failed"
				}
				if err == nil || !strings.Contains(err.Error(), want) || result != nil || len(reported) != 1 || !strings.Contains(reported[0].Error, want) {
					t.Fatalf("result=%+v error=%v reported=%+v", result, err, reported)
				}
			}
		})
	}
}
