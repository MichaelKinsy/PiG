package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// sharedGoFactory writes a Go factory extension whose probe tool reports its process id, and returns its config.
func sharedGoFactory(t *testing.T, name string) ExtConfig {
	t.Helper()
	dir := t.TempDir()
	modulePath := "example.com/shared" + name
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package ext

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New(EXTENSION_NAME)
	e.Tool("probe", "probe", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]string{"content": fmt.Sprintf("{\"calls\":1,\"pid\":%d}", os.Getpid())}, nil
	})
	return e
}
`
	source = strings.Replace(source, "EXTENSION_NAME", strconv.Quote(name), 1)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return packedFactoryConfig(name, dir, modulePath, "shared-go-"+name)
}

// memberShare is the process share hosting the named member of h.
func memberShare(t *testing.T, h *Host, name string) *processShare {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	me := h.exts[name]
	if me == nil {
		t.Fatalf("extension %s is not loaded", name)
	}
	if me.packedProcess != nil {
		return me.packedProcess.share
	}
	return me.share
}

func loadShared(t *testing.T, h *Host, configs []ExtConfig) {
	t.Helper()
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func sharedFixtures(t *testing.T) (nodePacked, nodeIsolated, goPacked ExtConfig) {
	t.Helper()
	root := t.TempDir()
	nodePacked, _ = retainedNodeExtension(t, root, "shared-node.mjs", false)
	nodeIsolated, _ = retainedNodeExtension(t, root, "shared-isolated.mjs", false)
	nodeIsolated.Isolation = "isolated"
	return nodePacked, nodeIsolated, sharedGoFactory(t, "shared-go")
}

func TestSharedCellsAdmitASecondHostIntoTheRunningProcess(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	nodePacked, nodeIsolated, goPacked := sharedFixtures(t)
	configs := []ExtConfig{nodePacked, nodeIsolated, goPacked}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	a, b := NewHost(cwd), NewHost(cwd)
	a.ShareCells(registry)
	b.ShareCells(registry)
	if a.Group() == "" || a.Group() == b.Group() {
		t.Fatalf("groups = %q and %q, want two distinct non-empty ids", a.Group(), b.Group())
	}
	t.Cleanup(func() { a.Shutdown("test done"); b.Shutdown("test done") })
	loadShared(t, a, configs)
	loadShared(t, b, configs)
	shares := map[string]*processShare{}
	for _, cfg := range configs {
		pa, pb := retainedProbeOf(t, a, cfg.Name), retainedProbeOf(t, b, cfg.Name)
		if pa.Pid != pb.Pid {
			t.Errorf("%s: pids %d and %d, want one shared process", cfg.Name, pa.Pid, pb.Pid)
		}
		shares[cfg.Name] = memberShare(t, b, cfg.Name)
		if memberShare(t, a, cfg.Name) != shares[cfg.Name] {
			t.Errorf("%s: Hosts hold different process shares", cfg.Name)
		}
	}
	a.Shutdown("host A closed")
	for _, cfg := range configs {
		select {
		case <-shares[cfg.Name].exitedChan():
			t.Fatalf("%s: process ended while Host B still used it", cfg.Name)
		default:
		}
		if got := retainedProbeOf(t, b, cfg.Name); got.Pid == 0 {
			t.Errorf("%s: Host B probe = %+v", cfg.Name, got)
		}
	}
	b.Shutdown("host B closed")
	for _, cfg := range configs {
		select {
		case <-shares[cfg.Name].exitedChan():
		case <-time.After(30 * time.Second):
			t.Errorf("%s: process still running after the last Host closed", cfg.Name)
		}
	}
}

func TestHostsWithoutARegistryKeepOneProcessEach(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	nodePacked, nodeIsolated, goPacked := sharedFixtures(t)
	configs := []ExtConfig{nodePacked, nodeIsolated, goPacked}
	cwd := t.TempDir()
	a, b := NewHost(cwd), NewHost(cwd)
	if a.Group() != "" {
		t.Fatalf("Group() = %q without a registry, want empty", a.Group())
	}
	t.Cleanup(func() { a.Shutdown("test done"); b.Shutdown("test done") })
	loadShared(t, a, configs)
	loadShared(t, b, configs)
	for _, cfg := range configs {
		if pa, pb := retainedProbeOf(t, a, cfg.Name), retainedProbeOf(t, b, cfg.Name); pa.Pid == pb.Pid {
			t.Errorf("%s: both Hosts run in pid %d without a registry", cfg.Name, pa.Pid)
		}
	}
}

func TestSharedCellsSkipADeadProcess(t *testing.T) {
	// A packed Go process that dies quarantines its cell and restarts only on an explicit reload, so the reload below does not race an automatic recovery (a Node cell recovers by itself).
	cfg := sharedGoFactory(t, "shared-dead")
	configs := []ExtConfig{cfg}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	hosts := []*Host{NewHost(cwd), NewHost(cwd)}
	crashed := make(chan string, 8)
	for _, h := range hosts {
		h.ShareCells(registry)
		h.SetCrashHandler(func(name string, _ time.Duration, _ bool, _ string) { crashed <- name })
		t.Cleanup(func() { h.Shutdown("test done") })
		loadShared(t, h, configs)
	}
	first := retainedProbeOf(t, hosts[0], cfg.Name)
	if other := retainedProbeOf(t, hosts[1], cfg.Name); other.Pid != first.Pid {
		t.Fatalf("pids %d and %d before the crash, want one shared process", first.Pid, other.Pid)
	}
	if err := killTestProcess(first.Pid); err != nil {
		t.Fatal(err)
	}
	for range hosts {
		select {
		case <-crashed:
		case <-time.After(30 * time.Second):
			t.Fatal("a Host did not see the shared process die")
		}
	}
	for _, h := range hosts {
		if _, err := h.Reload(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	a, b := retainedProbeOf(t, hosts[0], cfg.Name), retainedProbeOf(t, hosts[1], cfg.Name)
	if a.Pid == first.Pid || a.Pid != b.Pid {
		t.Fatalf("after the crash pids = %d and %d (old %d), want one new shared process", a.Pid, b.Pid, first.Pid)
	}
}

func TestSharedCellsStartTheSameCellOnceUnderARace(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	nodePacked, _, goPacked := sharedFixtures(t)
	configs := []ExtConfig{nodePacked, goPacked}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	hosts := []*Host{NewHost(cwd), NewHost(cwd)}
	var wg sync.WaitGroup
	for _, h := range hosts {
		h.ShareCells(registry)
		t.Cleanup(func() { h.Shutdown("test done") })
		own := slices.Clone(configs) // LoadAll fills content hashes in place
		h.SetConfigLoader(func() ([]ExtConfig, error) { return own, nil })
		wg.Go(func() {
			if _, errs := h.LoadAll(t.Context(), own); len(errs) > 0 {
				t.Error(errs)
			}
		})
	}
	wg.Wait()
	for _, cfg := range configs {
		if a, b := retainedProbeOf(t, hosts[0], cfg.Name), retainedProbeOf(t, hosts[1], cfg.Name); a.Pid != b.Pid {
			t.Errorf("%s: pids %d and %d, want exactly one process spawned", cfg.Name, a.Pid, b.Pid)
		}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.procs) != len(configs) {
		t.Errorf("registry holds %d processes, want %d", len(registry.procs), len(configs))
	}
}

// readyGroupExtSource records the raw ready frame it receives in a file.
const readyGroupExtSource = `package main

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"strings"
)

func frame(conn net.Conn, data []byte) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	conn.Write(hdr[:])
	conn.Write(data)
}

func main() {
	conn, err := net.Dial("unix", os.Getenv("PIG_EXT_SOCKET"))
	if err != nil {
		os.Exit(1)
	}
	frame(conn, []byte(` + "`" + `{"type":"register","register":{"name":"readygroup"}}` + "`" + `))
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(conn, data); err != nil {
			return
		}
		if strings.Contains(string(data), "\"type\":\"ready\"") {
			os.WriteFile(OUT, data, 0o644)
		}
	}
}
`

func TestReadyCarriesTheHostGroup(t *testing.T) {
	shortSockDir(t)
	for _, shared := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			srcDir, out := t.TempDir(), filepath.Join(t.TempDir(), "ready.json")
			if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module readygroup\ngo 1.26\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(strings.Replace(readyGroupExtSource, "OUT", strconv.Quote(out), 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			if shared {
				h.ShareCells(NewCellRegistry())
			}
			t.Cleanup(func() { h.Shutdown("test done") })
			if _, err := h.Load(t.Context(), ExtConfig{Name: "readygroup", Source: srcDir, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			var data []byte
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				if data, _ = os.ReadFile(out); len(data) > 0 {
					break
				}
			}
			var env struct {
				Ready map[string]json.RawMessage `json:"ready"`
			}
			if err := json.Unmarshal(data, &env); err != nil || env.Ready == nil {
				t.Fatalf("ready frame %q: %v", data, err)
			}
			group, has := env.Ready["group"]
			if shared {
				if want := strconv.Quote(h.Group()); string(group) != want {
					t.Errorf("ready.group = %s, want %s", group, want)
				}
			} else if has {
				t.Errorf("ready.group = %s for a Host that shares no cells, want it absent", group)
			}
			if r := (&withSessionRoute{target: h}).ready(); r.Group != h.Group() {
				t.Errorf("with_session ready group = %q, want %q", r.Group, h.Group())
			}
		})
	}
}

func TestSharedCellsOutliveTheSpawnersLoadContext(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	nodePacked, nodeIsolated, goPacked := sharedFixtures(t)
	configs := []ExtConfig{nodePacked, nodeIsolated, goPacked}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	t.Cleanup(registry.Close)
	a, b := NewHost(cwd), NewHost(cwd)
	a.ShareCells(registry)
	b.ShareCells(registry)
	t.Cleanup(func() { a.Shutdown("test done"); b.Shutdown("test done") })
	ctxA, cancelA := context.WithCancel(t.Context())
	defer cancelA()
	a.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	if _, errs := a.LoadAll(ctxA, slices.Clone(configs)); len(errs) > 0 {
		t.Fatal(errs)
	}
	loadShared(t, b, slices.Clone(configs))
	pids := map[string]int{}
	shares := map[string]*processShare{}
	for _, cfg := range configs {
		pids[cfg.Name] = retainedProbeOf(t, b, cfg.Name).Pid
		shares[cfg.Name] = memberShare(t, b, cfg.Name)
	}
	cancelA()
	a.Shutdown("host A closed")
	time.Sleep(500 * time.Millisecond) // a wrongly cancelled process needs a moment to die
	for _, cfg := range configs {
		if got := retainedProbeOf(t, b, cfg.Name).Pid; got != pids[cfg.Name] {
			t.Errorf("%s: pid %d after the spawner's context ended, want %d", cfg.Name, got, pids[cfg.Name])
		}
	}
	b.Shutdown("host B closed")
	for _, cfg := range configs {
		select {
		case <-shares[cfg.Name].exitedChan():
		case <-time.After(30 * time.Second):
			t.Errorf("%s: process still running after the last Host closed", cfg.Name)
		}
	}
}

func TestSharedCellsNeverShareAQuarantinedCell(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	cfg, _ := retainedNodeExtension(t, t.TempDir(), "shared-quarantined.mjs", false)
	isolated := cfg
	isolated.Isolation = "isolated"
	cwd := t.TempDir()
	registry := NewCellRegistry()
	t.Cleanup(registry.Close)
	b, a := NewHost(cwd), NewHost(cwd)
	for _, h := range []*Host{a, b} {
		h.ShareCells(registry)
		t.Cleanup(func() { h.Shutdown("test done") })
	}
	// Host B runs the extension isolated, which registers its process under the isolated Node key; Host A's quarantined cell starts isolated under that same key.
	loadShared(t, b, []ExtConfig{isolated})
	hashed := cfg
	fillContentHash(&hashed) // LoadAll plans the config with its content hash
	cells := a.planCells([]ExtConfig{hashed})
	if len(cells) != 1 || cells[0].Strategy != CellStrategyPackedNode {
		t.Fatalf("plan = %+v, want one packed Node cell", cells)
	}
	a.quarantinedCells = map[string]string{cells[0].Key: "crashed"}
	loadShared(t, a, []ExtConfig{cfg})
	if pa, pb := retainedProbeOf(t, a, cfg.Name), retainedProbeOf(t, b, cfg.Name); pa.Pid == pb.Pid {
		t.Fatalf("a quarantined cell joined another Host's process (pid %d)", pa.Pid)
	}
}
