package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi 0.87.1 reload() clears the extension factory cache and invokes every factory again inside one Node process (resource-loader.ts, extensions/loader.ts). jiti (moduleCache: false) re-evaluates a .ts module; a .mjs module stays in Node's ESM cache, so its module-level state survives. PiG re-invokes the factory in the retained runtime process and applies the same loader rules.
const retainedStateSource = `import {appendFileSync} from "node:fs";
let moduleFactoryCalls%[1]s = 0;
appendFileSync(%[2]q, "module\n");
export default function (pi%[3]s) {
  moduleFactoryCalls++;
  appendFileSync(%[2]q, "factory\n");
  pi.registerTool({
    name: "probe",
    label: "probe",
    description: "module state probe",
    parameters: {type: "object", properties: {}},
    async execute() { return {content: [{type: "text", text: JSON.stringify({calls: moduleFactoryCalls, pid: process.pid})}]}; },
  });
}
`

type retainedProbe struct {
	Calls int `json:"calls"`
	Pid   int `json:"pid"`
}

func retainedProbeOf(t *testing.T, h *Host, name string) retainedProbe {
	t.Helper()
	for _, ext := range h.Extensions() {
		if ext.Name != name {
			continue
		}
		result, err := ext.Tools["probe"].Definition.Execute(t.Context(), "probe-call", json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatalf("probe %s: %v", name, err)
		}
		var probe retainedProbe
		if err := json.Unmarshal([]byte(result.Text()), &probe); err != nil {
			t.Fatal(err)
		}
		return probe
	}
	t.Fatalf("extension %s is not loaded", name)
	return retainedProbe{}
}

func retainedLogCounts(t *testing.T, path string) (modules, factories int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		switch line {
		case "module":
			modules++
		case "factory":
			factories++
		}
	}
	return modules, factories
}

func retainedNodeExtension(t *testing.T, root, file string, typed bool) (ExtConfig, string) {
	t.Helper()
	log := filepath.Join(root, file+".log")
	suffix, param := "", ""
	if typed {
		suffix, param = ": number", ": any"
	}
	entry := filepath.Join(root, file)
	if err := os.WriteFile(entry, fmt.Appendf(nil, retainedStateSource, suffix, log, param), 0o644); err != nil {
		t.Fatal(err)
	}
	name := strings.TrimSuffix(file, filepath.Ext(file))
	return ExtConfig{Name: name, Source: entry, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}, log
}

// The probed Pi behavior: two reloads evaluate a .ts module three times and an .mjs module once, with a factory call for every load.
func TestNodeReloadReinvokesFactoriesInTheRetainedProcessWithPiLoaderRules(t *testing.T) {
	skipWithoutNodeExtensions(t)
	t.Parallel()
	nodeCellRequireNode(t)
	root := t.TempDir()
	mjs, mjsLog := retainedNodeExtension(t, root, "keeps-module.mjs", false)
	ts, tsLog := retainedNodeExtension(t, root, "reevaluates-module.ts", true)
	configs := []ExtConfig{mjs, ts}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	first := retainedProbeOf(t, h, mjs.Name)
	if first.Calls != 1 {
		t.Fatalf("first load calls = %d, want 1", first.Calls)
	}
	for reload := 1; reload <= 2; reload++ {
		if _, err := h.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		keeps := retainedProbeOf(t, h, mjs.Name)
		if keeps.Pid != first.Pid {
			t.Fatalf("reload %d replaced the extension process %d with %d", reload, first.Pid, keeps.Pid)
		}
		if keeps.Calls != reload+1 {
			t.Errorf("reload %d: .mjs module-level state = %d factory calls, want %d (native ESM cache keeps the module)", reload, keeps.Calls, reload+1)
		}
		if reevaluated := retainedProbeOf(t, h, ts.Name); reevaluated.Calls != 1 || reevaluated.Pid != first.Pid {
			t.Errorf("reload %d: .ts probe = %+v, want state re-evaluated by jiti in pid %d", reload, reevaluated, first.Pid)
		}
	}
	if modules, factories := retainedLogCounts(t, mjsLog); modules != 1 || factories != 3 {
		t.Errorf(".mjs evaluated %d times with %d factory calls, want 1 and 3 (Pi 0.87.1 probe)", modules, factories)
	}
	if modules, factories := retainedLogCounts(t, tsLog); modules != 3 || factories != 3 {
		t.Errorf(".ts evaluated %d times with %d factory calls, want 3 and 3 (Pi 0.87.1 probe)", modules, factories)
	}
	if processes := nodeCellProcessesForMarker(t, root); len(processes) != 1 {
		t.Errorf("Node processes = %v, want the one retained process", processes)
	}
}

// A retired generation leaves the shared event bus with its runtime: a listener registered by the old factory must not hear an emit after the reload (Pi's runtime.trackEventBusSubscription).
func TestNodeReloadRetiresTheOldGenerationBusListeners(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	entry := filepath.Join(root, "bus.mjs")
	source := `let heard = 0;
export default function (pi) {
  pi.events.on("ping", () => { heard++; });
  pi.registerTool({name: "probe", label: "probe", description: "bus probe", parameters: {type: "object", properties: {}},
    async execute() { pi.events.emit("ping", {}); await new Promise((resolve) => setTimeout(resolve, 20)); return {content: [{type: "text", text: JSON.stringify({calls: heard, pid: process.pid})}]}; }});
}
`
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	configs := []ExtConfig{{Name: "bus", Source: entry, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := retainedProbeOf(t, h, "bus").Calls; got != 1 {
		t.Fatalf("before reload heard %d, want 1", got)
	}
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		// The module keeps its counter (native ESM cache), so one live listener adds exactly one per emit: 1 before, then 2 and 3.
		probe := retainedProbeOf(t, h, "bus")
		if probe.Calls == 2 {
			break
		}
		if probe.Calls > 2 || time.Now().After(deadline) {
			t.Fatalf("after reload one emit was heard %d total times, want 2 (old listener gone, new one live)", probe.Calls)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A strictly isolated Node extension keeps its own process, and /reload still re-invokes its factory in that process.
func TestNodeIsolatedReloadReinvokesFactoryInTheRetainedProcess(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	mjs, log := retainedNodeExtension(t, root, "isolated-keeps.mjs", false)
	mjs.Isolation = "isolated"
	configs := []ExtConfig{mjs}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	first := retainedProbeOf(t, h, mjs.Name)
	for reload := 1; reload <= 2; reload++ {
		if _, err := h.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := retainedProbeOf(t, h, mjs.Name); got.Pid != first.Pid || got.Calls != reload+1 {
			t.Fatalf("reload %d: probe = %+v, want pid %d with %d factory calls", reload, got, first.Pid, reload+1)
		}
	}
	if modules, factories := retainedLogCounts(t, log); modules != 1 || factories != 3 {
		t.Errorf("isolated .mjs evaluated %d times with %d factory calls, want 1 and 3", modules, factories)
	}
}

// Pi re-invokes extension factories in the same runtime on every Session replacement (agent-session-runtime.ts creates a new resource loader; the module-level extension cache survives). A replacement Host claims the processes its predecessor parked in the shared RuntimeRetention.
func TestSessionReplacementHostClaimsTheParkedProcess(t *testing.T) {
	skipWithoutNodeExtensions(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			nodeCellRequireNode(t)
			root := t.TempDir()
			mjs, log := retainedNodeExtension(t, root, "survives.mjs", false)
			mjs.Isolation = isolation
			configs := []ExtConfig{mjs}
			retention := NewRuntimeRetention()
			t.Cleanup(retention.Close)
			first := NewHost(t.TempDir())
			first.SetRuntimeRetention(retention)
			t.Cleanup(func() { first.Shutdown("test done") })
			if _, errs := first.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			before := retainedProbeOf(t, first, mjs.Name)

			// The outgoing Session parks its processes, then its Host shuts down.
			first.Retain()
			first.Shutdown("session replaced")

			second := NewHost(t.TempDir())
			second.SetRuntimeRetention(retention)
			t.Cleanup(func() { second.Shutdown("test done") })
			if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			after := retainedProbeOf(t, second, mjs.Name)
			if after.Pid != before.Pid || after.Calls != 2 {
				t.Fatalf("replacement probe = %+v, want the parked process %d with its module state (2 factory calls)", after, before.Pid)
			}
			if modules, factories := retainedLogCounts(t, log); modules != 1 || factories != 2 {
				t.Errorf("module evaluated %d times with %d factory calls, want 1 and 2 (Pi keeps the factory cache across a Session replacement in one cwd)", modules, factories)
			}
		})
	}
}

// Pi's reload() clears the factory cache once and then caches every factory it loads (resource-loader.ts reload, loader.ts loadExtensionsInternal), so a later same-cwd Session replacement calls each cached .ts factory without re-evaluating its module. A Node cell admits its members one at a time, and each reload admission must not evict the factory an earlier member of the same reload cached.
func TestSessionReplacementAfterReloadReusesEveryCachedFactory(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	firstTS, firstLog := retainedNodeExtension(t, root, "first-cached.ts", true)
	secondTS, secondLog := retainedNodeExtension(t, root, "second-cached.ts", true)
	configs := []ExtConfig{firstTS, secondTS}
	cwd := t.TempDir()
	retention := NewRuntimeRetention()
	t.Cleanup(retention.Close)
	first := NewHost(cwd)
	first.SetRuntimeRetention(retention)
	t.Cleanup(func() { first.Shutdown("test done") })
	first.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := first.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, err := first.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := retainedProbeOf(t, first, firstTS.Name)
	first.Retain()
	first.Shutdown("session replaced")

	second := NewHost(cwd)
	second.SetRuntimeRetention(retention)
	t.Cleanup(func() { second.Shutdown("test done") })
	if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	for _, ext := range []ExtConfig{firstTS, secondTS} {
		if got := retainedProbeOf(t, second, ext.Name); got.Pid != before.Pid || got.Calls != 2 {
			t.Errorf("%s after replacement = %+v, want pid %d with 2 calls on the module the reload evaluated", ext.Name, got, before.Pid)
		}
	}
	for _, log := range []string{firstLog, secondLog} {
		if modules, factories := retainedLogCounts(t, log); modules != 2 || factories != 3 {
			t.Errorf("%s: module evaluated %d times with %d factory calls, want 2 and 3 (load, reload, cached replacement)", filepath.Base(log), modules, factories)
		}
	}
}

// Pi keys its factory cache by the resolved configured cwd (loader.ts useExtensionCacheCwd uses resolvePath, not realpath). An isolated Node process's first generation must use the Host's cwd too: through a symlinked cwd the kernel reports the physical path, and comparing that with the next admission's configured cwd would clear the cache on a same-cwd Session replacement.
func TestIsolatedSessionReplacementKeepsTheCacheThroughASymlinkedCwd(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	ts, log := retainedNodeExtension(t, root, "isolated-cached.ts", true)
	ts.Isolation = "isolated"
	configs := []ExtConfig{ts}
	cwd := filepath.Join(t.TempDir(), "linked-cwd")
	testenv.RequireDirectoryLink(t, t.TempDir(), cwd)
	retention := NewRuntimeRetention()
	t.Cleanup(retention.Close)
	first := NewHost(cwd)
	first.SetRuntimeRetention(retention)
	t.Cleanup(func() { first.Shutdown("test done") })
	if _, errs := first.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	before := retainedProbeOf(t, first, ts.Name)
	first.Retain()
	first.Shutdown("session replaced")

	second := NewHost(cwd)
	second.SetRuntimeRetention(retention)
	t.Cleanup(func() { second.Shutdown("test done") })
	if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := retainedProbeOf(t, second, ts.Name); got.Pid != before.Pid || got.Calls != 2 {
		t.Errorf("replacement probe = %+v, want pid %d calling the cached factory (2 calls)", got, before.Pid)
	}
	if modules, factories := retainedLogCounts(t, log); modules != 1 || factories != 2 {
		t.Errorf("module evaluated %d times with %d factory calls, want 1 and 2", modules, factories)
	}
}

// A process the replacement Session's plan does not claim ends with the replacement's load instead of staying parked.
func TestSessionReplacementReleasesUnclaimedParkedProcesses(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	dropped, _ := retainedNodeExtension(t, root, "dropped.mjs", false)
	dropped.Isolation = "isolated"
	other, _ := retainedNodeExtension(t, root, "other.mjs", false)
	other.Isolation = "isolated"
	retention := NewRuntimeRetention()
	t.Cleanup(retention.Close)
	first := NewHost(t.TempDir())
	first.SetRuntimeRetention(retention)
	t.Cleanup(func() { first.Shutdown("test done") })
	if _, errs := first.LoadAll(t.Context(), []ExtConfig{dropped}); len(errs) > 0 {
		t.Fatal(errs)
	}
	first.mu.Lock()
	share := first.exts[dropped.Name].share
	first.mu.Unlock()
	first.Retain()
	first.Shutdown("session replaced")
	if !share.alive() {
		t.Fatal("the parked process ended with its Host")
	}
	second := NewHost(t.TempDir())
	second.SetRuntimeRetention(retention)
	t.Cleanup(func() { second.Shutdown("test done") })
	if _, errs := second.LoadAll(t.Context(), []ExtConfig{other}); len(errs) > 0 {
		t.Fatal(errs)
	}
	if share.alive() {
		t.Fatal("the unclaimed parked process survived the replacement's load")
	}
	deadline := time.Now().Add(10 * time.Second)
	for !share.exited() {
		if time.Now().After(deadline) {
			t.Fatal("the released process did not exit")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Crash recovery (D20/D56) owns the process a reload retained: the crash still quarantines only the culprit, restarts the healthy members together in a fresh process, and never replays the interrupted callback.
func TestNodeCrashAfterRetainedReloadRecoversWithoutReplay(t *testing.T) {
	skipWithoutNodeExtensions(t)
	t.Parallel()
	h, configs, trace := nodeRecoveryFixture(t, 3)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	h.mu.Lock()
	before := pidOf(h.exts["member1"])
	h.mu.Unlock()
	for range 2 {
		if _, err := h.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	h.mu.Lock()
	bad := h.exts["member1"]
	oldPID := pidOf(bad)
	staleHandler := bad.ext.EventHandlers("session_start")[0]
	h.mu.Unlock()
	if oldPID != before {
		t.Fatalf("reloads replaced the process %d with %d", before, oldPID)
	}
	if _, err := bad.ext.Tools["member1"].Definition.Execute(t.Context(), "interrupted", json.RawMessage(`{"crash":true}`), nil); err == nil {
		t.Fatal("interrupted callback succeeded")
	}
	waitRecovered(t, h, []string{"member0", "member1", "member2"}, oldPID)
	if got := recoveryBus(t, h, "member0"); got != "member0,member1,member2 | member0+member2 member1" {
		t.Fatalf("healthy bus split: %s", got)
	}
	h.mu.Lock()
	a, b, c := pidOf(h.exts["member0"]), pidOf(h.exts["member1"]), pidOf(h.exts["member2"])
	h.mu.Unlock()
	if a != c || a == b || a == oldPID {
		t.Fatalf("recovery placement after a retained reload: %d %d %d (old %d)", a, b, c, oldPID)
	}
	if _, err := staleHandler(); err == nil {
		t.Fatal("old-generation callback reached the recovered process")
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "crash\n") != 1 {
		t.Fatalf("interrupted callback replayed: %s", data)
	}
	// The recovered processes are retained by the next reload in turn.
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	again := pidOf(h.exts["member0"])
	h.mu.Unlock()
	if again != a {
		t.Fatalf("reload after recovery replaced the recovered process %d with %d", a, again)
	}
}

// A strictly isolated extension that crashes after a retained reload restarts under its supervisor in a fresh process, and a later reload retains that one.
func TestNodeIsolatedCrashAfterRetainedReloadRestarts(t *testing.T) {
	skipWithoutNodeExtensions(t)
	t.Parallel()
	nodeCellRequireNode(t)
	root := t.TempDir()
	entry := filepath.Join(root, "isolated-crash.mjs")
	source := `export default function (pi) {
  pi.registerTool({name: "probe", label: "probe", description: "crash probe", parameters: {type: "object", properties: {}},
    async execute(_id, args) { if (args.crash) process.exit(23); return {content: [{type: "text", text: JSON.stringify({calls: 1, pid: process.pid})}]}; }});
}
`
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	config := ExtConfig{Name: "isolated-crash", Source: entry, Enabled: true, Isolation: "isolated", RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	recordCrashNotices(t, h)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
	if _, errs := h.LoadAll(t.Context(), []ExtConfig{config}); len(errs) > 0 {
		t.Fatal(errs)
	}
	first := retainedProbeOf(t, h, config.Name).Pid
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	crashing := h.exts[config.Name]
	h.mu.Unlock()
	if pidOf(crashing) != first {
		t.Fatalf("reload replaced the isolated process %d with %d", first, pidOf(crashing))
	}
	crashed := retainedShareOf(h, config.Name)
	if _, err := crashing.ext.Tools["probe"].Definition.Execute(t.Context(), "crash", json.RawMessage(`{"crash":true}`), nil); err == nil {
		t.Fatal("interrupted callback succeeded")
	}
	waitRecovered(t, h, []string{config.Name}, first)
	// The supervisor's restart drops the dead process's reference; reap cannot end a process a state still holds.
	if refs := crashed.refs.Load(); refs != 0 {
		t.Fatalf("crashed generation retained %d process references after the supervisor restart", refs)
	}
	// The supervisor restarts the extension in place; read only its process, as waitRecovered does.
	h.mu.Lock()
	restarted := pidOf(h.exts[config.Name])
	h.mu.Unlock()
	if restarted == first {
		t.Fatalf("the crashed process %d was not replaced", first)
	}
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := retainedProbeOf(t, h, config.Name).Pid; got != restarted {
		t.Fatalf("reload after the restart replaced the restarted process %d with %d", restarted, got)
	}
}

// A Session replacement can build the successor's Host while the outgoing Host is still held open by a running command handler, and retire the outgoing Host afterwards. The successor claims the live process, and the late Retain parks nothing: the process ends with the successor's Host, not never.
func TestSessionReplacementSuccessorLoadsBeforeTheOutgoingHostRetires(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	mjs, _ := retainedNodeExtension(t, root, "overlap.mjs", false)
	configs := []ExtConfig{mjs}
	retention := NewRuntimeRetention()
	t.Cleanup(retention.Close)
	first := NewHost(t.TempDir())
	first.SetRuntimeRetention(retention)
	t.Cleanup(func() { first.Shutdown("test done") })
	if _, errs := first.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	before := retainedProbeOf(t, first, mjs.Name)
	share := retainedShareOf(first, mjs.Name)

	second := NewHost(t.TempDir())
	second.SetRuntimeRetention(retention)
	t.Cleanup(func() { second.Shutdown("test done") })
	if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	after := retainedProbeOf(t, second, mjs.Name)
	if after.Pid != before.Pid || after.Calls != 2 {
		t.Fatalf("successor probe = %+v, want the live process %d with its module state", after, before.Pid)
	}

	first.Retain()
	first.Shutdown("session replaced")
	if got := retainedProbeOf(t, second, mjs.Name); got.Pid != before.Pid {
		t.Fatalf("retiring the outgoing Host ended the process the successor holds: %+v", got)
	}
	second.Shutdown("session ended")
	deadline := time.Now().Add(10 * time.Second)
	for share != nil && !share.exited() {
		if time.Now().After(deadline) {
			t.Fatal("the process outlived both Hosts: the late Retain left a parked reference")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// retainedShareOf returns the process share the named extension runs in.
func retainedShareOf(h *Host, name string) *processShare {
	h.mu.Lock()
	defer h.mu.Unlock()
	me := h.exts[name]
	if me.packedProcess != nil {
		return me.packedProcess.share
	}
	return me.share
}

// Isolated Node extensions start concurrently, and each start claims from the retention every live process registered so far. A process must be complete when it becomes claimable: the race detector reports a claim that reads a spawner's exit channel while that spawner is still assigning it.
func TestConcurrentIsolatedStartsClaimOnlyCompleteProcesses(t *testing.T) {
	skipWithoutNodeExtensions(t)
	t.Parallel()
	nodeCellRequireNode(t)
	root := t.TempDir()
	var configs []ExtConfig
	for i := range 6 {
		config, _ := retainedNodeExtension(t, root, fmt.Sprintf("concurrent-%d.mjs", i), false)
		config.Isolation = "isolated"
		configs = append(configs, config)
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	for range 3 {
		if _, err := h.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, config := range configs {
		if got := retainedProbeOf(t, h, config.Name); got.Calls != 4 {
			t.Errorf("%s = %+v, want 4 factory calls in its retained process", config.Name, got)
		}
	}
}

// Reload returns only after the retiring generation has finished its teardown. The old generation runs a handler that ignores the abort and ends 300ms after it starts; its runtime closes the connection only once that handler has drained, so a Retire that did not wait would return with the handler still running.
func TestNodeReloadWaitsForTheOldGenerationToDrainItsHandlers(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	marker := filepath.Join(root, "drained")
	started := filepath.Join(root, "started")
	entry := filepath.Join(root, "drain.mjs")
	source := fmt.Sprintf(`import {appendFileSync} from "node:fs";
export default function (pi) {
  pi.registerTool({name: "probe", label: "probe", description: "probe", parameters: {type: "object", properties: {}},
    async execute() { return {content: [{type: "text", text: JSON.stringify({calls: 1, pid: process.pid})}]}; }});
  pi.registerTool({name: "slow", label: "slow", description: "ignores abort", parameters: {type: "object", properties: {}},
    async execute() { appendFileSync(%q, "x"); await new Promise((resolve) => setTimeout(resolve, 300)); appendFileSync(%q, "x"); return {content: [{type: "text", text: "done"}]}; }});
}
`, started, marker)
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	configs := []ExtConfig{{Name: "drain", Source: entry, Enabled: true, Isolation: "isolated", RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
	var slow extension.ToolDefinition
	for _, ext := range h.Extensions() {
		slow = ext.Tools["slow"].Definition
	}
	running := make(chan struct{})
	go func() {
		close(running)
		_, _ = slow.Execute(t.Context(), "slow-call", json.RawMessage(`{}`), nil)
	}()
	<-running
	// The handler must be inside the old generation's runtime before it is retired; a call that reached the successor would outlive Reload for another reason.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the slow handler never started")
		}
	}
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Reload returned before the retiring generation drained its handler: %v", err)
	}
}

// Pi /reload keeps a native ESM .js entry (a "type": "module" package) in Node's module cache: jiti imports it natively, so an edit, even a syntax error, is not seen, and the factory runs again with the module's state. Probed with Pi 0.87.1's own loadExtensionsCached after clearExtensionCache per reload: old-1, old-2, old-3 and no load error. PiG evaluates an edited ES module entry again (D93), so an edit runs, an edit that breaks the module fails its load as an edited TypeScript extension's does in Pi, and the next valid edit loads; an unedited reload keeps the module and its state.
func TestNodeReloadEvaluatesAnEditedTypeModuleJsEntryAgain(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	root := t.TempDir()
	dir := filepath.Join(root, "ext")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module","main":"main.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(source string) {
		t.Helper()
		path := filepath.Join(dir, "main.js")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		// A later modification time keeps the edit visible on a file system with a coarse clock.
		later := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
	}
	version := func(marker string) string {
		return `let calls = 0;
export default function (pi) {
  calls++;
  const seen = calls;
  pi.registerTool({name: "probe", label: "probe", description: "probe", parameters: {type: "object", properties: {}},
    async execute() { return {content: [{type: "text", text: "` + marker + `-" + seen}]}; }});
}
`
	}
	write(version("old"))
	config := ExtConfig{Name: "esm-js", Source: dir, Enabled: true}
	h := NewHost(root)
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
	if _, errs := h.LoadAll(t.Context(), []ExtConfig{config}); len(errs) > 0 {
		t.Fatal(errs)
	}
	probe := func() string {
		t.Helper()
		extensions := h.Extensions()
		if len(extensions) != 1 {
			t.Fatalf("loaded extensions = %d, want 1", len(extensions))
		}
		result, err := extensions[0].Tools["probe"].Definition.Execute(t.Context(), "probe-call", json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		return result.Text()
	}
	reload := func() []string {
		t.Helper()
		if _, err := h.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
		return h.LastReloadReport().Issues
	}
	if got := probe(); got != "old-1" {
		t.Fatalf("load probe = %q, want old-1", got)
	}
	if issues := reload(); len(issues) != 0 {
		t.Fatalf("unedited reload issues = %q", issues)
	}
	if got := probe(); got != "old-2" {
		t.Fatalf("unedited reload probe = %q, want old-2 (the module keeps its state)", got)
	}
	write(version("new"))
	if issues := reload(); len(issues) != 0 {
		t.Fatalf("edited reload issues = %q", issues)
	}
	if got := probe(); got != "new-1" {
		t.Fatalf("edited reload probe = %q, want new-1 (the edited module evaluated again)", got)
	}
	write("export default function register(pi) { pi.registerShortcut(\"ctrl+shift+right\", { handler: () => {\n")
	if issues := reload(); len(issues) != 1 || !strings.Contains(issues[0], "Failed to load extension") {
		t.Fatalf("reload of a broken edit: issues = %q, want the extension's load failure", issues)
	}
	if extensions := h.Extensions(); len(extensions) != 0 {
		t.Fatalf("after a broken edit %d extensions stay loaded, want none", len(extensions))
	}
	write(version("fixed"))
	if issues := reload(); len(issues) != 0 {
		t.Fatalf("reload after the fix: issues = %q", issues)
	}
	if got := probe(); got != "fixed-1" {
		t.Fatalf("reload after the fix probe = %q, want fixed-1", got)
	}
}
