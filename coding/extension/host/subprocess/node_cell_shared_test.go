package subprocess

import (
	"encoding/json"
	"path/filepath"
	"strings"
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
	if memberShare(t, a, "shared-cells") != memberShare(t, b, "shared-cells") {
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

func TestSharedNodeCellDrainsOnlyTheClosingHost(t *testing.T) {
	a, b := twoSharedNodeHosts(t)
	share := memberShare(t, b, "shared-cells")
	a.EndInput()
	a.Shutdown("host A closed")
	time.Sleep(500 * time.Millisecond)
	select {
	case <-share.exitedChan():
		t.Fatal("the process ended when Host A's input ended")
	default:
	}
	if got := callShared(t, b, "counter", `{}`); got != "1" {
		t.Errorf("Host B counter = %q, want 1", got)
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
