package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// TestConformance_HostPathsAgreeAcrossSDKs: Pi exports getPackageDir() and getShellConfig(customShellPath) (coding-agent config.ts, utils/shell.ts). Every SDK answers
// getShellConfig the same: a custom path that exists is that shell with `-c`, one that does not is the "Custom shell path not found" error, and with none it is the shell
// the host resolved (the Go, Rust and Python SDKs get it from PIG_EXT_HOST_INFO, Node runs Pi's own resolution on the same machine). The Go, Rust and Python SDKs return the
// host's getPackageDir(); a Node extension returns the directory of the Pi package its runtime ships, which is where PiG's host has no value to hand over (see the host
// info's PackageDir). The host's package directory here is a value no SDK could produce as a fallback.
func TestConformance_HostPathsAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	// Not parallel: it owns conformanceHostInfo and the process environment while it runs.
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_USE_PI_DIRS", "")
	packageDir := filepath.Join(t.TempDir(), "host-package-dir")
	shell := func() *subprocess.ShellInfo {
		config, err := shellconfig.Default()
		if err != nil {
			return &subprocess.ShellInfo{Error: err.Error()}
		}
		return &subprocess.ShellInfo{Shell: config.Path, Args: config.Args, CommandTransport: config.CommandTransport}
	}
	conformanceHostInfo = subprocess.HostInfo{AgentDir: agentDir, ConfigDirName: ".pig", Version: coding.UpstreamPackageVersion, PackageDir: packageDir, Shell: shell}
	t.Cleanup(func() { conformanceHostInfo = subprocess.HostInfo{} })

	existing := filepath.Join(t.TempDir(), "custom-shell")
	if err := os.WriteFile(existing, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	report := func(info *subprocess.ShellInfo) map[string]any {
		if info.Error != "" {
			return map[string]any{"error": info.Error}
		}
		out := map[string]any{"shell": info.Shell, "args": info.Args}
		if info.CommandTransport != "" {
			out["commandTransport"] = info.CommandTransport
		}
		return out
	}
	wantDefault := report(shell())
	wantCustom := report(&subprocess.ShellInfo{Shell: existing, Args: []string{"-c"}})
	wantMissing := map[string]any{"error": "Custom shell path not found: " + missing}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "fused-go" {
				// The fused extension runs in this process, so the host must put the entry in this process's environment before the factory runs.
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
			if !h.runner.ExecuteCommand(context.Background(), "host_paths_probe", existing+"|"+missing) {
				t.Fatal("fixture has no host_paths_probe command")
			}
			var reported string
			pollUntilConformance(t, "host_paths_probe: no report", func() bool {
				for _, n := range (*h.notify)[before:] {
					if rest, ok := strings.CutPrefix(n, "host_paths:"); ok {
						reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			var got struct {
				PackageDir string         `json:"packageDir"`
				Default    map[string]any `json:"default"`
				Custom     map[string]any `json:"custom"`
				Missing    map[string]any `json:"missing"`
			}
			if err := json.Unmarshal([]byte(reported), &got); err != nil {
				t.Fatalf("%s is not JSON: %v", reported, err)
			}
			if strings.Contains(tc.name, "node") {
				if _, err := os.Stat(filepath.Join(got.PackageDir, "package.json")); err != nil {
					t.Errorf("a Node extension's getPackageDir() = %q, which holds no package.json: %v", got.PackageDir, err)
				}
			} else if got.PackageDir != packageDir {
				t.Errorf("getPackageDir() = %q, want the host's %q", got.PackageDir, packageDir)
			}
			// JSON decoding makes args []any in both, so normalise the expectation through JSON too.
			normalise := func(value map[string]any) map[string]any {
				encoded, _ := json.Marshal(value)
				var out map[string]any
				_ = json.Unmarshal(encoded, &out)
				return out
			}
			for name, pair := range map[string][2]map[string]any{"default": {got.Default, wantDefault}, "custom": {got.Custom, wantCustom}, "missing": {got.Missing, wantMissing}} {
				if !reflect.DeepEqual(pair[0], normalise(pair[1])) {
					t.Errorf("getShellConfig %s = %v, want %v", name, pair[0], pair[1])
				}
			}
		})
	}
}
