//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type subagentTrustUI struct {
	*mockUIContext
	confirmations atomic.Int32
}

func (ui *subagentTrustUI) Confirm(context.Context, string, string, extension.ExtensionUIDialogOptions) (bool, error) {
	ui.confirmations.Add(1)
	return false, nil
}

func TestSubagentProjectTrustUpstream(t *testing.T) {
	for _, tc := range []struct {
		name     string
		trusted  bool
		confirms int32
		canceled bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8261-subagent-project-trust.test.ts:69
		{"skips per-call confirmation for trusted projects", true, 0, false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8261-subagent-project-trust.test.ts:76
		{"keeps confirmation for untrusted interactive projects", false, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shortSockDir(t)
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
			cwd := t.TempDir()
			agentsDir := filepath.Join(cwd, ".pig", "agents")
			if err := os.MkdirAll(agentsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentsDir, "project-agent.md"), []byte("---\nname: project-agent\ndescription: Project test agent\n---\n\nHandle the delegated task.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			host := NewHost(cwd)
			defer host.Shutdown("test done")
			ui := &subagentTrustUI{mockUIContext: &mockUIContext{}}
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			bridge.SetHostAction("isProjectTrusted", func() bool { return tc.trusted })
			host.SetUIBridge(bridge)
			source, err := filepath.Abs("../../../../.upstream/current/packages/coding-agent/examples/extensions/subagent/index.ts")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			loaded, err := host.Load(ctx, ExtConfig{Name: "subagent", Source: source, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			tool, ok := loaded.Tools["subagent"]
			if !ok {
				t.Fatal("subagent tool was not registered")
			}
			params, err := json.Marshal(map[string]any{"agent": "project-agent", "task": "Test project trust", "agentScope": "project", "cwd": filepath.Join(cwd, "missing-cwd")})
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Definition.Execute(ctx, "trust-call", params, nil)
			if err != nil {
				t.Fatal(err)
			}
			toolResult, ok := result, true
			if !ok {
				t.Fatalf("tool result type=%T", result)
			}
			if calls := ui.confirmations.Load(); calls != tc.confirms {
				t.Fatalf("confirm calls=%d, want %d; result=%+v", calls, tc.confirms, toolResult)
			}
			if tc.canceled && !strings.Contains(toolResult.Text(), "Canceled: project-local agents not approved.") {
				t.Fatalf("missing rejection: %+v", toolResult)
			}
			if !tc.canceled && strings.Contains(toolResult.Text(), "Canceled:") {
				t.Fatalf("trusted project canceled: %+v", toolResult)
			}
		})
	}
}
