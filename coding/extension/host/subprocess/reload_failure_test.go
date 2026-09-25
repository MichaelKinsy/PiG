package subprocess

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestReload_MixedCellFailureIsolatesFailingExtension verifies that when one
// cell fails to stage, the other cells still reload and the failure is
// reported, as upstream reload loads each extension on its own.
func TestReload_MixedCellFailureIsolatesFailingExtension(t *testing.T) {
	rootA := writePackedFactoryModule(t, "example.com/mixfail/a", "mix-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/mixfail/b", "mix-b", "tool_b")
	good := []ExtConfig{
		packedFactoryConfig("mix-a", rootA, "example.com/mixfail/a", "ha"),
		packedFactoryConfig("mix-b", rootB, "example.com/mixfail/b", "hb"),
	}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return good, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	h.mu.Lock()
	originalA := h.exts["mix-a"]
	originalB := h.exts["mix-b"]
	h.mu.Unlock()
	if originalA == nil || originalB == nil {
		t.Fatal("expected packed extensions live after first reload")
	}
	originalCell := originalA.packedCellKey

	// Reload with the unchanged packed cell plus an isolated subprocess
	// extension that points at a non-existent binary so stageIsolated fails.
	ghostPath := "/definitely/does/not/exist/" + t.Name()
	bad := append([]ExtConfig(nil), good...)
	bad = append(bad, ExtConfig{Name: "ghost", Enabled: true, Path: ghostPath})
	h.SetConfigLoader(func() ([]ExtConfig, error) { return bad, nil })

	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("reload failed as a whole on one failing extension: %v", err)
	}

	h.mu.Lock()
	stillA := h.exts["mix-a"]
	stillB := h.exts["mix-b"]
	_, hasGhost := h.exts["ghost"]
	h.mu.Unlock()
	if hasGhost {
		t.Errorf("ghost extension must not appear in registry after it failed to load")
	}
	if stillA == nil || stillB == nil {
		t.Fatalf("healthy packed extensions must remain loaded, got A=%v B=%v", stillA, stillB)
	}
	if stillA == originalA || stillB == originalB {
		t.Error("reload reused unchanged packed extension state; want fresh factory instances")
	}
	if stillA.packedCellKey != originalCell {
		t.Errorf("unchanged packed cell key changed: was %q, now %q", originalCell, stillA.packedCellKey)
	}

	rep := h.LastReloadReport()
	if rep == nil {
		t.Fatal("nil LastReloadReport after reload")
	}
	if rep.Error != "" {
		t.Errorf("ReloadReport.Error = %q, want empty", rep.Error)
	}
	if len(rep.Issues) != 1 || !strings.HasPrefix(rep.Issues[0], ghostPath+": Failed to load extension: ") {
		t.Errorf("ReloadReport.Issues = %q, want the ghost failure", rep.Issues)
	}
}

func TestReloadReturnsConfiguredOrderForMixedPackedAndIsolatedCells(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	rootA := writePackedFactoryModule(t, "example.com/mixorder/a", "mix-order-a", "tool_a")
	rootB := writePackedFactoryModule(t, "example.com/mixorder/b", "mix-order-b", "tool_b")
	isolated := startupNodeFixture(t, root, "middle-node", `export default function () {}`)
	configs := []ExtConfig{
		packedFactoryConfig("mix-order-b", rootB, "example.com/mixorder/b", "hb"),
		isolated,
		packedFactoryConfig("mix-order-a", rootA, "example.com/mixorder/a", "ha"),
	}
	host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	host.SetConfigLoader(func() ([]ExtConfig, error) { return append([]ExtConfig(nil), configs...), nil })
	t.Cleanup(func() { host.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()

	loaded, err := host.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(loaded))
	for i := range loaded {
		got[i] = loaded[i].Name
	}
	want := []string{"mix-order-b", "middle-node", "mix-order-a"}
	if !slices.Equal(got, want) {
		t.Fatalf("reload order = %v, want %v", got, want)
	}
}
