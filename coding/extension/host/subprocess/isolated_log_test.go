//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Pi loads in-process (loader.ts:538-550); the isolated subprocess diagnostic is owned by PiG, just like a packed log.
func TestIsolatedStderrLogLifecycle(t *testing.T) {
	configs := packedLogTestConfigs(t, "node")
	for i := range configs {
		configs[i].Isolation = "strict"
		configs[i].SupervisorConfig = SupervisorConfig{MaxCrashes: 1}
	}
	root := t.TempDir()
	tmp := privatePackedLogTemp(t)
	for range 2 {
		h := NewHostWithConfigRoot(t.TempDir(), root)
		t.Cleanup(func() { h.Shutdown("test done") })
		h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
		for range 3 {
			members := reloadIsolatedLogTest(t, h, configs)
			assertIsolatedLogs(t, tmp, members[0].stderrLogPath, members[1].stderrLogPath)
		}
		h.Shutdown("normal shutdown")
		assertIsolatedLogs(t, tmp)
	}

	h := NewHostWithConfigRoot(t.TempDir(), root)
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	reported := make(chan string, 1)
	h.SetCrashHandler(func(_ string, _ time.Duration, _ bool, reason string) { reported <- reason })
	members := reloadIsolatedLogTest(t, h, configs)
	crashed := members[0]
	crashLog := crashed.stderrLogPath
	if err := crashed.proc.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-reported:
		if !strings.HasSuffix(reason, "(stderr: "+crashLog+")") {
			t.Fatalf("diagnostic lacks log path: %s", reason)
		}
	case <-t.Context().Done():
		t.Fatal("crash was not reported")
	}
	members = reloadIsolatedLogTest(t, h, configs)
	assertIsolatedLogs(t, tmp, crashLog, members[0].stderrLogPath, members[1].stderrLogPath)
	h.Shutdown("after crash and reload")
	assertIsolatedLogs(t, tmp, crashLog)
}

func TestIsolatedStderrLogCancellation(t *testing.T) {
	tmp := privatePackedLogTemp(t)
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := h.Load(ctx, packedLogTestConfigs(t, "node")[0]); err != nil {
		t.Fatal(err)
	}
	cancel()
	h.Shutdown("parent cancelled")
	assertIsolatedLogs(t, tmp)
}

func TestIsolatedStderrLogLoadFailure(t *testing.T) {
	entry := filepath.Join(t.TempDir(), "index.mjs")
	if err := os.WriteFile(entry, []byte(`export default function() { throw new Error("isolated-log-failure"); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := privatePackedLogTemp(t)
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	_, err := h.Load(t.Context(), ExtConfig{Name: "broken", Source: entry, Enabled: true})
	loadErr, ok := errors.AsType[*LoadError](err)
	if !ok || loadErr.StderrLog == "" {
		t.Fatalf("load error lacks log: %v", err)
	}
	h.Shutdown("failed load")
	assertIsolatedLogs(t, tmp, loadErr.StderrLog)
	data, err := os.ReadFile(loadErr.StderrLog)
	if err != nil || !strings.Contains(string(data), "isolated-log-failure") {
		t.Fatalf("log content = %q, error = %v", data, err)
	}
}

func assertIsolatedLogs(t *testing.T, dir string, want ...string) {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(dir, "pig-ext-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("isolated logs = %v, want %v", got, want)
	}
}

func reloadIsolatedLogTest(t *testing.T, h *Host, configs []ExtConfig) []*managedExt {
	t.Helper()
	loaded, err := h.Reload(t.Context())
	if err != nil || len(loaded) != len(configs) {
		t.Fatalf("reload: %v, extensions = %d, report = %+v", err, len(loaded), h.LastReloadReport())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var members []*managedExt
	for _, cfg := range configs {
		me := h.exts[cfg.Name]
		if me == nil || me.packedProcess != nil || me.stderrLogPath == "" {
			t.Fatalf("%s: missing isolated process/log", cfg.Name)
		}
		members = append(members, me)
	}
	return members
}
