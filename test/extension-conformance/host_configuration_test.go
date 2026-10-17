package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestConformance_HostConfigurationAgreesAcrossSDKs: Pi exports getAgentDir(), CONFIG_DIR_NAME and VERSION (coding-agent index.ts, config.ts) and its extensions
// call them while their factory runs, so an SDK needs them before the host's ready message. The Node SDK answers from its environment (shims/pig-config.mjs); the
// other SDKs return what the host resolved and started them with in PIG_EXT_HOST_INFO. Every SDK, isolated and packed, must give the same three values, and the
// host's own values must be the ones a Node extension sees (PiG's tree, D2). factoryAgentDir is getAgentDir() as each factory saw it while it ran; a fused
// extension's factory runs in this process, so the host must have put the entry in this process's environment by then.
func TestConformance_HostConfigurationAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	// Not parallel: it owns conformanceHostInfo and the process environment while it runs.
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	info := subprocess.HostInfo{AgentDir: agentDir, ConfigDirName: ".pig", Version: coding.UpstreamPackageVersion}
	conformanceHostInfo = info
	t.Cleanup(func() { conformanceHostInfo = subprocess.HostInfo{} })
	wantJSON, err := json.Marshal(map[string]string{"agentDir": info.AgentDir, "configDirName": info.ConfigDirName, "version": info.Version, "factoryAgentDir": info.AgentDir})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "fused-go" {
				// The fused extension runs in this process, so the host must put the entry in this process's environment before the factory runs.
				// The empty value restores the variable after the run and leaves the SDK nothing to read unless the host delivers it.
				t.Setenv("PIG_EXT_HOST_INFO", "")
			}
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			before := len(*h.notify)
			if !h.runner.ExecuteCommand(context.Background(), "host_info_probe", "") {
				t.Fatal("fixture has no host_info_probe command")
			}
			var reported string
			pollUntilConformance(t, "host_info_probe: no report", func() bool {
				for _, n := range (*h.notify)[before:] {
					if rest, ok := strings.CutPrefix(n, "host_info:"); ok {
						reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			var got, want map[string]string
			if err := json.Unmarshal([]byte(reported), &got); err != nil {
				t.Fatalf("%s is not JSON: %v", reported, err)
			}
			_ = json.Unmarshal(wantJSON, &want)
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s = %q, want %q (%s)", key, got[key], value, reported)
				}
			}
		})
	}
}
