//go:build linux

package subprocess

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// launcherGroupEntries is the PIG_EXT_GROUP entries of the environment pid started with. /proc/<pid>/environ is the environment at exec, which cli.mjs's delete does not rewrite.
func launcherGroupEntries(t *testing.T, pid int) []string {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		t.Fatal(err)
	}
	var groups []string
	for kv := range strings.SplitSeq(string(data), "\x00") {
		if strings.HasPrefix(kv, "PIG_EXT_GROUP=") {
			groups = append(groups, kv)
		}
	}
	return groups
}

// Two Hosts in one registry share the isolated Node launcher that Host A spawned; its initial environment carries Host A's group (pig additive (D232)). This is the link between factoryLauncherEnv and cli.mjs: the connectExt call site.
func TestSharedIsolatedNodeLauncherStartsInTheSpawnersGroup(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	_, nodeIsolated, _ := sharedFixtures(t)
	cwd := t.TempDir()
	registry := NewCellRegistry()
	a, b := NewHost(cwd), NewHost(cwd)
	a.ShareCells(registry)
	b.ShareCells(registry)
	t.Cleanup(func() { a.Shutdown("test done"); b.Shutdown("test done") })
	loadShared(t, a, []ExtConfig{nodeIsolated})
	loadShared(t, b, []ExtConfig{nodeIsolated})
	pa, pb := a.MemberProcessID(nodeIsolated.Name), b.MemberProcessID(nodeIsolated.Name)
	if pa == 0 || pa != pb {
		t.Fatalf("pids %d and %d, want one shared process", pa, pb)
	}
	if want, got := "PIG_EXT_GROUP="+a.Group(), launcherGroupEntries(t, pa); len(got) != 1 || got[0] != want {
		t.Fatalf("the launcher started with %v, want [%s] (Host B is %s)", got, want, b.Group())
	}
}

// A PIG_EXT_GROUP the user exported reaches no launcher: a Host that shares no cells passes none, and one that does passes its own (pig additive (D232)).
func TestLauncherDropsAnExportedPigExtGroup(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	_, nodeIsolated, _ := sharedFixtures(t)
	t.Setenv("PIG_EXT_GROUP", "exported")
	solo := NewHost(t.TempDir())
	t.Cleanup(func() { solo.Shutdown("test done") })
	loadShared(t, solo, []ExtConfig{nodeIsolated})
	pid := solo.MemberProcessID(nodeIsolated.Name)
	if pid == 0 {
		t.Fatal("the isolated Node fixture has no process")
	}
	if got := launcherGroupEntries(t, pid); len(got) != 0 {
		t.Fatalf("a Host that shares no cells started its launcher with %v, want no PIG_EXT_GROUP", got)
	}
	sharing := NewHost(t.TempDir())
	sharing.ShareCells(NewCellRegistry())
	t.Cleanup(func() { sharing.Shutdown("test done") })
	loadShared(t, sharing, []ExtConfig{nodeIsolated})
	if want, got := "PIG_EXT_GROUP="+sharing.Group(), launcherGroupEntries(t, sharing.MemberProcessID(nodeIsolated.Name)); len(got) != 1 || got[0] != want {
		t.Fatalf("a sharing Host started its launcher with %v, want [%s]", got, want)
	}
}
