package subprocess

import (
	"path/filepath"
	"testing"
)

// TestRegisteredProviderConfigAcrossProcessesMatchesPi runs the author and reader of one configuration Provider in separate Node processes. Pi 0.87.1 gives every reader the author's single effective root (model-runtime.ts:438-443,753-766): repeated reads are identical, a reader's root write is shared, the author's child writes are visible, an author root write is not, and a partial re-registration merges over that root with the author's original children and functions.
func TestRegisteredProviderConfigAcrossProcessesMatchesPi(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir, err := filepath.Abs("testdata/registered-provider-config")
	if err != nil {
		t.Fatal(err)
	}
	h := newTestHost(t)
	h.SetUIBridge(NewUIBridge(func() {}))
	t.Cleanup(func() { h.Shutdown("test done") })
	exts, errs := h.LoadAll(t.Context(), []ExtConfig{
		{Name: "config-author", Source: filepath.Join(dir, "author.mjs"), Enabled: true, Isolation: "isolated"},
		{Name: "config-reader", Source: filepath.Join(dir, "reader.mjs"), Enabled: true, Isolation: "isolated"},
	})
	if len(errs) != 0 || len(exts) != 2 {
		t.Fatalf("LoadAll = %d extensions, %v", len(exts), errs)
	}
	author, reader := exts[0], exts[1]
	for _, step := range []struct {
		ext     string
		command string
	}{
		{"reader", "reader-first"},
		{"author", "author-after-reader-write"},
		{"reader", "reader-after-author-write"},
		{"author", "author-after-reader-merge"},
	} {
		ext := reader
		if step.ext == "author" {
			ext = author
		}
		if err := ext.Commands[step.command].Handler(t.Context(), ""); err != nil {
			t.Fatalf("%s: %v", step.command, err)
		}
	}
}
