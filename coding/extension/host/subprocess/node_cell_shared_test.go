package subprocess

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// sharedCellsConfig is the shared-cells.mjs factory as a Node member.
func sharedCellsConfig(t *testing.T) ExtConfig {
	t.Helper()
	entry, err := filepath.Abs(filepath.Join("testdata", "shared-cells.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	return ExtConfig{Name: "shared-cells", Source: entry, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}
}

// callShared runs tool of the shared-cells member of h and returns its text.
func callShared(t *testing.T, h *Host, tool, params string) string {
	t.Helper()
	for _, ext := range h.Extensions() {
		if ext.Name != "shared-cells" {
			continue
		}
		result, err := ext.Tools[tool].Definition.Execute(t.Context(), tool+"-call", json.RawMessage(params), nil)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return result.Text()
	}
	t.Fatal("shared-cells is not loaded")
	return ""
}

// twoSharedNodeHosts loads shared-cells in two Hosts of one registry.
func twoSharedNodeHosts(t *testing.T) (a, b *Host) {
	t.Helper()
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	configs := []ExtConfig{sharedCellsConfig(t)}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	a, b = NewHost(cwd), NewHost(cwd)
	a.ShareCells(registry)
	b.ShareCells(registry)
	t.Cleanup(func() { a.Shutdown("test done"); b.Shutdown("test done"); registry.Close() })
	loadShared(t, a, configs)
	loadShared(t, b, configs)
	if memberShare(t, a, "shared-cells") != memberShare(t, b, "shared-cells") || a.MemberProcessID("shared-cells") != b.MemberProcessID("shared-cells") || a.MemberProcessID("shared-cells") == 0 {
		t.Fatal("the Hosts did not share one process")
	}
	return a, b
}

func TestSharedNodeCellKeepsEachHostsEventBusApart(t *testing.T) {
	a, b := twoSharedNodeHosts(t)
	callShared(t, a, "ping", `{"name":"A"}`)
	if got := callShared(t, a, "received", `{}`); got != "received=A" {
		t.Errorf("Host A heard %q, want its own emit", got)
	}
	if got := callShared(t, b, "received", `{}`); got != "received=" {
		t.Errorf("Host B heard %q, want nothing from Host A", got)
	}
}

func TestSharedNodeCellKeepsVirtualWritesPerHost(t *testing.T) {
	a, b := twoSharedNodeHosts(t)
	callShared(t, a, "virtual", `{"name":"A"}`)
	if got := callShared(t, a, "models", `{}`); got != "models=A" {
		t.Errorf("Host A's registry shows %q, want shared/A", got)
	}
	if got := callShared(t, b, "models", `{}`); got != "models=" {
		t.Errorf("Host B's registry shows %q, want no shared/A", got)
	}
	if got := len(b.Runtime().PendingVirtualModelRegistrations()); got != 0 {
		t.Errorf("Host B holds %d virtual model registrations, want 0", got)
	}
}

func TestSharedNodeCellKeepsServingTheOtherHostAfterOneCloses(t *testing.T) {
	a, b := twoSharedNodeHosts(t)
	a.EndInput()
	a.Shutdown("host A closed")
	if got := callShared(t, b, "counter", `{}`); got != "1" {
		t.Errorf("Host B counter = %q, want 1 from the still-running process", got)
	}
	if b.MemberProcessID("shared-cells") == 0 {
		t.Error("Host B's member has no process")
	}
}

func TestSharedNodeCellCountersAreInstanceLocal(t *testing.T) {
	a, b := twoSharedNodeHosts(t)
	got := []string{callShared(t, a, "counter", `{}`), callShared(t, a, "counter", `{}`), callShared(t, b, "counter", `{}`)}
	if strings.Join(got, ",") != "1,2,1" {
		t.Errorf("counters = %v, want 1,2,1", got)
	}
}

func TestFactoryAdmissionCarriesTheGroupOnlyWhenSet(t *testing.T) {
	plain, err := factoryAdmission{Name: "ext", Socket: "/s", ReloadPass: 2}.line()
	if err != nil {
		t.Fatal(err)
	}
	if want := "admit\text\t/s\t\t\t2\n"; plain != want {
		t.Errorf("line without a group = %q, want %q", plain, want)
	}
	grouped, err := factoryAdmission{Name: "ext", Socket: "/s", ReloadPass: 2, Group: "g7"}.line()
	if err != nil {
		t.Fatal(err)
	}
	if want := "admit\text\t/s\t\t\t2\tg7\n"; grouped != want {
		t.Errorf("line with a group = %q, want %q", grouped, want)
	}
}

// beforeAdmit runs hook before the first admit line reaches the process, as the point where another Host's last generation can end.
type beforeAdmit struct {
	io.WriteCloser
	once sync.Once
	hook func()
}

func (w *beforeAdmit) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), "admit\t") {
		w.once.Do(w.hook)
	}
	return w.WriteCloser.Write(p)
}

// Host A's last generation ends while Host B, which already joined the process, has not yet been admitted: the runner exits itself, and B starts the cell on a fresh process instead of running without it (D232).
func TestSharedNodeCellAdmitsAHostWhoseJoinedProcessExitsBeforeAdmission(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	configs := []ExtConfig{sharedCellsConfig(t)}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	a, b := NewHost(cwd), NewHost(cwd)
	a.ShareCells(registry)
	b.ShareCells(registry)
	t.Cleanup(func() { a.Shutdown("test done"); b.Shutdown("test done"); registry.Close() })
	loadShared(t, a, configs)
	old := memberShare(t, a, "shared-cells")
	oldPid := a.MemberProcessID("shared-cells")
	old.admitMu.Lock()
	old.admit = &beforeAdmit{WriteCloser: old.admit, hook: func() {
		a.EndInput()
		a.Shutdown("host A closed")
		select {
		case <-old.exitedChan():
		case <-time.After(30 * time.Second):
			t.Error("the shared process did not exit after its last generation ended")
		}
	}}
	old.admitMu.Unlock()
	loadShared(t, b, configs)
	if got := callShared(t, b, "counter", `{}`); got != "1" {
		t.Fatalf("Host B counter = %q, want 1 from a fresh instance", got)
	}
	share := memberShare(t, b, "shared-cells")
	if share == old || b.MemberProcessID("shared-cells") == oldPid || b.MemberProcessID("shared-cells") == 0 {
		t.Fatalf("Host B did not start a fresh process (old %d, new %d)", oldPid, b.MemberProcessID("shared-cells"))
	}
	registry.mu.Lock()
	entries, registered := len(registry.procs), registry.procs[b.cellRegistryKey(share.key)]
	registry.mu.Unlock()
	if entries != 1 || registered != share {
		t.Fatalf("registry holds %d entries, want the one fresh process", entries)
	}
}

// Twelve Hosts join, end their input and shut down in turn while one stays: the process-wide beforeExit listener count does not grow with them, and the process's stderr shows no EventEmitter warning (D232).
func TestSharedNodeCellKeepsOneBeforeExitListenerAcrossSessions(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	configs := []ExtConfig{sharedCellsConfig(t)}
	cwd := t.TempDir()
	registry := NewCellRegistry()
	anchor := NewHost(cwd)
	anchor.ShareCells(registry)
	t.Cleanup(func() { anchor.Shutdown("test done"); registry.Close() })
	loadShared(t, anchor, configs)
	pid := anchor.MemberProcessID("shared-cells")
	for i := range 12 {
		h := NewHost(cwd)
		h.ShareCells(registry)
		loadShared(t, h, configs)
		if got := h.MemberProcessID("shared-cells"); got != pid {
			t.Fatalf("session %d runs in process %d, want the shared %d", i+1, got, pid)
		}
		if got := callShared(t, h, "counter", `{}`); got != "1" {
			t.Fatalf("session %d counter = %q", i+1, got)
		}
		h.EndInput()
		h.Shutdown("session done")
	}
	anchor.EndInput()
	if got := callShared(t, anchor, "listeners", `{}`); got != "beforeExit=1" {
		t.Errorf("after 12 sessions the shared process holds %s, want beforeExit=1", got)
	}
	logPath := memberShare(t, anchor, "shared-cells").origin.(*packedProcessState).stderrLog.path
	if log, err := os.ReadFile(logPath); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(log), "MaxListenersExceededWarning") {
		t.Errorf("the shared process warned:\n%s", log)
	}
}

// The registry drops a process's entry when its last reference releases (D232).
func TestSharedCellsForgetAProcessWhenItsLastHostLeaves(t *testing.T) {
	a, b := twoSharedNodeHosts(t)
	registry := a.cells
	count := func() int {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		return len(registry.procs)
	}
	if got := count(); got != 1 {
		t.Fatalf("registry entries with two Hosts = %d, want 1", got)
	}
	a.Shutdown("host A closed")
	if got := count(); got != 1 {
		t.Fatalf("registry entries after one Host left = %d, want 1", got)
	}
	b.Shutdown("host B closed")
	if got := count(); got != 0 {
		t.Fatalf("registry entries after the last Host left = %d, want 0", got)
	}
}
