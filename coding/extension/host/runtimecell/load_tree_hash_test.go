package runtimecell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeSDKTreeFixture writes a small tree in the place of an SDK: sources in the root and in a package below it, and a file that is not a source.
func writeSDKTreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":         "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n",
		"sdk.go":         "package sdk\n",
		"json/encode.go": "package json\n",
		"README.md":      "the SDK\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The key a load looks for a built cell under is the key the cell's build published it under. With one reading of the SDK tree shared by the
// cells of the load (WithLoadTreeHashes), every key is byte for byte the one computed from a reading of its own: for a cell of one extension,
// for thirteen cells of one extension each, and for one cell that packs five.
func TestLoadTreeHashGivesTheKeysOfAReadingPerCell(t *testing.T) {
	sdkRoot := writeSDKTreeFixture(t)
	cacheRoot := t.TempDir()
	extensions := make([]GoExtension, 13)
	for i := range extensions {
		name := fmt.Sprintf("ext%02d", i)
		extensions[i] = GoExtension{Name: name, Root: t.TempDir(), ModulePath: "example.com/" + name, Package: "example.com/" + name, Factory: "Extension", Hash: "source of " + name}
	}
	each := make([][]GoExtension, len(extensions))
	for i := range extensions {
		each[i] = extensions[i : i+1]
	}
	for _, tc := range []struct {
		name  string
		cells [][]GoExtension
	}{
		{"one extension", each[:1]},
		{"thirteen extensions, a cell each", each},
		{"one packed cell of five", [][]GoExtension{extensions[:5]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			load := WithLoadTreeHashes(t.Context())
			for i, cell := range tc.cells {
				normalized, err := normalizeGoExtensions(cell, true)
				if err != nil {
					t.Fatal(err)
				}
				key := fmt.Sprintf("cell-%d", i)
				own := goPackedCellHash(cacheRoot, key, normalized, sdkRoot)
				shared := goPackedCellHashOfSDK(cacheRoot, key, normalized, loadTreeHash(load, sdkRoot))
				if strings.HasPrefix(own, "error:") || shared != own {
					t.Fatalf("cell %d: key %q from the load's reading of the SDK, %q from a reading of its own", i, shared, own)
				}
			}
		})
	}
}

// A load reads an SDK tree once, whichever of its cells asks first and however many ask at once: a source that changes while the load runs
// does not change the hash its cells share. The next load reads the tree again and sees the change, and a caller outside a load always reads it.
func TestLoadTreeHashIsReadOnceForALoadAndAgainForTheNext(t *testing.T) {
	sdkRoot := writeSDKTreeFixture(t)
	askAtOnce := func(load func() string) []string {
		got := make([]string, 13)
		var cells sync.WaitGroup
		for i := range got {
			cells.Go(func() { got[i] = load() })
		}
		cells.Wait()
		return got
	}
	first := WithLoadTreeHashes(t.Context())
	before := hashTree(sdkRoot)
	if strings.HasPrefix(before, "error:") {
		t.Fatal(before)
	}
	for i, hash := range askAtOnce(func() string { return loadTreeHash(first, sdkRoot) }) {
		if hash != before {
			t.Fatalf("cell %d of the first load: %q, want the tree's hash %q", i, hash, before)
		}
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "sdk.go"), []byte("package sdk\n\nfunc Changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := hashTree(sdkRoot)
	if strings.HasPrefix(changed, "error:") || changed == before {
		t.Fatalf("the changed tree hashes to %q, as before the change or not at all", changed)
	}
	for i, hash := range askAtOnce(func() string { return loadTreeHash(first, sdkRoot) }) {
		if hash != before {
			t.Errorf("cell %d of the first load read the tree again after the change: %q, want the load's %q", i, hash, before)
		}
	}
	second := WithLoadTreeHashes(t.Context())
	for i, hash := range askAtOnce(func() string { return loadTreeHash(second, sdkRoot) }) {
		if hash != changed {
			t.Errorf("cell %d of the next load: %q, want the changed tree's %q", i, hash, changed)
		}
	}
	if hash := loadTreeHash(t.Context(), sdkRoot); hash != changed {
		t.Errorf("outside a load: %q, want the changed tree's %q", hash, changed)
	}
}

// A source of the SDK that changes while a load runs, after the load's first cell read the tree: a cell that has to build does not use the
// load's reading. It hashes the tree again under its build lock and publishes under that key, so the image is stored under the key of the
// tree it was compiled from. The next cell of the same load, which looks under the load's older key and finds nothing, reaches the image by
// the same path.
func TestACellBuiltDuringALoadIsPublishedUnderItsOwnReadingOfTheSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a packed Go cell")
	}
	extension := goExtensionFixture(t, "latecell", `sdk.New("latecell")`)
	sdkRoot := filepath.Join(t.TempDir(), "sdk")
	if err := os.CopyFS(sdkRoot, os.DirFS(os.Getenv("PIG_SDK_GO_ROOT"))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", sdkRoot)
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	normalized, err := normalizeGoExtensions([]GoExtension{extension}, true)
	if err != nil {
		t.Fatal(err)
	}
	found, err := findSDKRootWithPreferred(normalized, "")
	if err != nil {
		t.Fatal(err)
	}
	load := WithLoadTreeHashes(t.Context())
	atFirstCell := goPackedCellHashOfSDK(cacheRoot, "late-cell", normalized, loadTreeHash(load, found))
	source, err := os.OpenFile(filepath.Join(sdkRoot, "extension.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.WriteString("\n// edited while the load ran\n"); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	built, err := buildGoPackedCell(load, cacheRoot, "late-cell", []GoExtension{extension}, "")
	if err != nil {
		t.Fatal(err)
	}
	own := goPackedCellHash(cacheRoot, "late-cell", normalized, found)
	if strings.HasPrefix(atFirstCell, "error:") || strings.HasPrefix(own, "error:") || own == atFirstCell {
		t.Fatalf("the edit did not change the cell's key: %q before it, %q after", atFirstCell, own)
	}
	if built.Cached || built.Hash != own {
		t.Fatalf("the cell built during the load: cached %v under %q; want a build published under its own reading %q, not the load's %q", built.Cached, built.Hash, own, atFirstCell)
	}
	again, err := buildGoPackedCell(load, cacheRoot, "late-cell", []GoExtension{extension}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !again.Cached || again.Hash != own {
		t.Fatalf("the next cell of the load: cached %v under %q; want the image published under %q", again.Cached, again.Hash, own)
	}
}
