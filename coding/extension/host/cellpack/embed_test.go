package cellpack

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// extract must land each embedded binary at dest/<Binary>, executable, and the
// resolver built from the same manifest must then hit it. This exercises the
// whole embed→extract→resolve pipeline without a real build.
func TestExtractThenResolve(t *testing.T) {
	m := Manifest{PigCoreVersion: "test", Cells: []CellEntry{{
		Language: "go", Key: "go-core", OS: runtime.GOOS, Arch: runtime.GOARCH,
		Binary:     "go/aaaa/runner",
		Extensions: []ExtEntry{{Name: "context-info", Hash: "h1"}, {Name: "subagent", Hash: "h2"}},
	}}}
	fsys := fstest.MapFS{
		"cells/manifest.json":  {Data: mustJSON(t, m)},
		"cells/go/aaaa/runner": {Data: []byte("BINARY")},
	}

	if got, err := loadManifest(fsys); err != nil || len(got.Cells) != 1 {
		t.Fatalf("loadManifest = %+v, %v", got, err)
	}

	dest := t.TempDir()
	if err := extract(fsys, m, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	// Windows starts only files with an executable extension; elsewhere the
	// execute bit makes the binary runnable.
	want := extractedAt(dest, "go/aaaa/runner")
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("extracted binary missing: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode()&0o100 == 0 {
		t.Errorf("extracted binary not executable: mode %v", fi.Mode())
	}

	r := NewResolver(m, dest)
	got, ok := r.ResolvePrebuilt(runtimecell.PrebuiltRequest{
		Language: "go", Key: "go-core", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Extensions: []runtimecell.PrebuiltExtension{{Name: "context-info", Hash: "h1"}, {Name: "subagent", Hash: "h2"}},
	})
	if !ok || got != want {
		t.Errorf("resolve = %q, %v; want extracted path", got, ok)
	}
}

func TestEmbeddedDefaultManifestIsEmpty(t *testing.T) {
	m, err := loadManifest(cells)
	if err != nil {
		t.Fatalf("embedded default manifest must parse: %v", err)
	}
	if len(m.Cells) != 0 {
		t.Errorf("committed default manifest must be empty, got %d cells", len(m.Cells))
	}
}

// The manifest stock pig embeds is the one every start reads: it must come back as the decoder would give it, without the decoder.
func TestStockManifestFastPathEqualsTheDecoder(t *testing.T) {
	got, err := loadManifest(cells)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := loadManifest(fstest.MapFS{"cells/manifest.json": {Data: []byte(`{"cells": []}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, decoded) {
		t.Errorf("stock manifest = %#v, decoder gives %#v", got, decoded)
	}
	if raw, err := fs.ReadFile(cells, "cells/manifest.json"); err != nil || string(bytes.TrimSpace(raw)) != stockManifest {
		t.Errorf("cells/manifest.json = %q, %v; stockManifest names %q", raw, err, stockManifest)
	}
}

// Reading the stock manifest is two allocations (the file's bytes, the embedded copy's); through the decoder it was nine.
func TestLoadManifestOfTheStockManifestDoesNotDecode(t *testing.T) {
	if got := testing.AllocsPerRun(20, func() {
		if _, err := loadManifest(cells); err != nil {
			t.Fatal(err)
		}
	}); got > 3 {
		t.Errorf("loadManifest of the stock manifest allocates %v times, want at most 3", got)
	}
}

// Only the whitespace JSON allows may surround the stock manifest on the fast path: a form feed, a vertical tab, NEL or a no-break space before it is not
// JSON whitespace, and the decoder rejects it as it always did. After it the decoder, which reads one value and ignores the rest, accepts it, and so does
// the fallback.
func TestStockManifestFastPathTakesOnlyJSONWhitespace(t *testing.T) {
	for _, padding := range []string{"", " ", "\t", "\r\n", " \t\r\n "} {
		if _, err := loadManifest(fstest.MapFS{"cells/manifest.json": {Data: []byte(padding + stockManifest + padding)}}); err != nil {
			t.Errorf("%q around the stock manifest: %v", padding, err)
		}
	}
	for _, padding := range []string{"\f", "\v", "\u0085", "\u00a0"} {
		if _, err := loadManifest(fstest.MapFS{"cells/manifest.json": {Data: []byte(padding + stockManifest)}}); err == nil {
			t.Errorf("manifest %q loaded; the decoder rejects it", padding+stockManifest)
		}
		if _, err := loadManifest(fstest.MapFS{"cells/manifest.json": {Data: []byte(stockManifest + padding)}}); err != nil {
			t.Errorf("manifest %q: %v; the decoder accepts it", stockManifest+padding, err)
		}
	}
}

func TestLoadManifestRejectsRetiredStaticAuthenticationMetadata(t *testing.T) {
	retiredField := "auth" + "Targets"
	data := []byte(`{"pigCoreVersion":"","cells":[],"` + retiredField + `":[]}`)
	_, err := loadManifest(fstest.MapFS{"cells/manifest.json": {Data: data}})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("loadManifest error = %v, want unknown retired field", err)
	}
}

// Regression: the extraction directory followed <home>/.pig whatever PIG_HOME said, so a Termux or relocated PiG home held the cells outside its own root.
func TestExtractDirFollowsPigHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "relocated")
	t.Setenv("PIG_HOME", home)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "elsewhere"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	dir, err := extractDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "piglet-binary-cells"); dir != want {
		t.Fatalf("extractDir() = %q, want %q", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("extractDir() did not create %q: %v", dir, err)
	}
}

// The host starts a cell with cmd.Dir set to the session directory, so a relative PIG_HOME must not leave a cell path that resolves against it.
func TestExtractDirIsAbsoluteForARelativePigHome(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("PIG_HOME", "relative-home")
	dir, err := extractDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "relative-home", "piglet-binary-cells"); dir != want {
		t.Fatalf("extractDir() = %q, want %q", dir, want)
	}
}
