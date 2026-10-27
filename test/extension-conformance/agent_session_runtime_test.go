package extensionconformance

import (
	_ "embed"
	"encoding/json"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// agentSessionRuntimeProbe is the extension every session of the agent_session_runtime row loads (testdata/agent-session-runtime.mjs is the Node script that defines the expectation).
//
//go:embed testdata/agent-session-runtime-probe.mjs
var agentSessionRuntimeProbe string

// sessionGraphAgentSessionRuntimeWant is the report of Pi's own createAgentSessionRuntime and AgentSessionRuntime (the Node row).
//
//go:embed testdata/session-graph-agent-session-runtime.json
var sessionGraphAgentSessionRuntimeWant string

// createAgentSessionRuntime and AgentSessionRuntime (agent-session-runtime.ts) for every SDK, against the report of Pi's own run (the Node row): a factory that builds cwd-bound services and a session, and every replacement method (newSession with a parent and a setup, fork before and at an entry, switchSession, importFromJsonl) with the session_before_switch, session_before_fork and session_shutdown events a handler sees, a handler that cancels, the rebind and before-invalidate callbacks, refreshContext after a setup, and dispose.
func TestSessionGraphAgentSessionRuntimeAcrossSDKs(t *testing.T) {
	// Not parallel: the agent directory is the process environment's, as in the services row.
	newRig := agentSessionRuntimeRig(t)
	eachSessionGraphSDKWith(t, newRig, agentSessionRuntimeRow)
}

// TestPackedSessionGraphAgentSessionRuntimeAcrossSDKs is the agent_session_runtime row in the packed cells of the Go, Rust and Python SDKs.
func TestPackedSessionGraphAgentSessionRuntimeAcrossSDKs(t *testing.T) {
	newRig := agentSessionRuntimeRig(t)
	eachSessionGraphPackedWith(t, newRig, agentSessionRuntimeRow)
}

// agentSessionRuntimeRig is the rig builder of the row: the process environment's agent directory is a temporary one.
func agentSessionRuntimeRig(t *testing.T) func(testing.TB) *sessionGraphRig {
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	return func(t testing.TB) *sessionGraphRig {
		r := newSessionGraphRigWithChildren(t)
		r.host.SetHostInfo(subprocess.HostInfo{AgentDir: agentDir, ConfigDirName: ".pig", Version: coding.UpstreamPackageVersion})
		return r
	}
}

func agentSessionRuntimeRow(t *testing.T, r *sessionGraphRig, _ string) {
	report, root := r.script("agent_session_runtime")
	got := normalizeSessionGraph(t, report, root)
	if path := os.Getenv("SESSION_GRAPH_PRINT"); path != "" {
		encoded, _ := json.MarshalIndent(got, "", "\t")
		_ = os.WriteFile(path, encoded, 0o600)
	}
	requireSessionGraphReport(t, got, sessionGraphAgentSessionRuntimeWant)
}
