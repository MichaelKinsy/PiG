//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pi loads factories in-process (loader.ts:538-550) and replaces them on reload
// (agent-session.ts:3291-3314). Packed logs are host-owned diagnostics, not an
// extension resource: only a reported failure may leave one after teardown.
func TestPackedStderrLogLifecycle(t *testing.T) {
	for _, language := range []string{"node", "go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			configs := packedLogTestConfigs(t, language)
			configRoot := t.TempDir()
			tmp := privatePackedLogTemp(t)
			for range 2 {
				h := NewHostWithConfigRoot(t.TempDir(), configRoot)
				t.Cleanup(func() { h.Shutdown("test done") })
				h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
				for range 3 {
					process, logPath := reloadPackedLogTest(t, h, configs)
					assertPackedLogs(t, tmp, logPath)
					if process.stopping.Load() {
						t.Fatal("replacement process is stopping")
					}
				}
				h.Shutdown("normal shutdown")
				assertPackedLogs(t, tmp)
			}

			h := NewHostWithConfigRoot(t.TempDir(), configRoot)
			t.Cleanup(func() { h.Shutdown("test done") })
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			var mu sync.Mutex
			reasons := make(map[string]string)
			reported := make(chan struct{})
			h.SetCrashHandler(func(name string, _ time.Duration, _ bool, reason string) {
				mu.Lock()
				defer mu.Unlock()
				complete := len(reasons) == len(configs)
				reasons[name] = reason
				if !complete && len(reasons) == len(configs) {
					close(reported)
				}
			})
			process, crashLog := reloadPackedLogTest(t, h, configs)
			if err := process.cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-reported:
			case <-t.Context().Done():
				t.Fatal("crash was not reported")
			}
			<-process.watcherDone
			mu.Lock()
			for name, reason := range reasons {
				if !strings.HasSuffix(reason, "(stderr: "+crashLog+")") {
					t.Errorf("%s: diagnostic does not reference crash log: %s", name, reason)
				}
			}
			mu.Unlock()
			_, replacementLog := reloadPackedLogTest(t, h, configs)
			assertPackedLogs(t, tmp, crashLog, replacementLog)
			h.Shutdown("after crash and reload")
			assertPackedLogs(t, tmp, crashLog)
		})
	}
}

// privatePackedLogTemp gives the test its own temp directory for packed logs.
// shortSockDir keeps socket paths under the Unix socket length limit, which
// t.TempDir paths exceed on Windows runners.
func privatePackedLogTemp(t *testing.T) string {
	t.Helper()
	return shortSockDir(t)
}

func assertPackedLogs(t *testing.T, dir string, want ...string) {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(dir, "pig-packed-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("packed logs = %v, want %v", got, want)
	}
}

func reloadPackedLogTest(t *testing.T, h *Host, configs []ExtConfig) (*packedProcessState, string) {
	t.Helper()
	loaded, err := h.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(configs) {
		t.Fatalf("loaded %d extensions, want %d: %+v", len(loaded), len(configs), h.LastReloadReport())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var process *packedProcessState
	var logPath string
	for _, cfg := range configs {
		me := h.exts[cfg.Name]
		if me == nil || me.packedProcess == nil || me.stderrLogPath == "" {
			t.Fatalf("%s: missing packed process or stderr path", cfg.Name)
		}
		if process != nil && process != me.packedProcess {
			t.Fatalf("members do not share a packed process: quarantine=%v, keys=%s/%s", h.quarantinedCells, process.key, me.packedCellKey)
		}
		process, logPath = me.packedProcess, me.stderrLogPath
	}
	return process, logPath
}

func packedLogTestConfigs(t *testing.T, language string) []ExtConfig {
	t.Helper()
	var configs []ExtConfig
	for _, suffix := range []string{"a", "b"} {
		name := "log-" + suffix
		switch language {
		case "node":
			entry := filepath.Join(t.TempDir(), name+".mjs")
			if err := os.WriteFile(entry, []byte(`export default function(pi) { pi.registerCommand("`+name+`", {handler: async () => {}}); }`), 0o600); err != nil {
				t.Fatal(err)
			}
			configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true})
		case "go":
			module := "example.com/logtest/" + suffix
			root := writePackedFactoryModule(t, module, name, "tool_"+suffix)
			configs = append(configs, packedFactoryConfig(name, root, module, suffix))
		case "python":
			module := "logtest_" + suffix
			root := writePackedPythonFactoryModule(t, module, name, "tool_"+suffix)
			configs = append(configs, packedPythonFactoryConfig(name, root, module, suffix))
		case "rust":
			crate := "logtest_" + suffix
			root := writePackedRustFactoryCrate(t, crate, name, "tool_"+suffix)
			configs = append(configs, packedRustFactoryConfig(name, root, crate, suffix))
		}
	}
	return configs
}
