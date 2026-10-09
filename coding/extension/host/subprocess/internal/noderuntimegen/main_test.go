package main

import (
	"archive/zip"
	"crypto/sha256"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runMain(t *testing.T, args ...string) {
	t.Helper()
	previousArgs, previousFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = previousArgs, previousFlags })
	flag.CommandLine = flag.NewFlagSet("noderuntimegen", flag.PanicOnError)
	os.Args = append([]string{"noderuntimegen"}, args...)
	main()
}

// The command packages exactly the runtime sources (top-level *.mjs, harness/ without dot or underscore names, shims/)
// into a zstd zip, and writes the hash of the entry names and bytes as the generated Go constant; the same input gives
// byte-identical outputs, and a changed source changes the hash.
func TestMainPackagesRuntimeReproducibly(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"main.mjs":            "main",
		"notes.txt":           "ignored: not .mjs",
		"nested/other.mjs":    "ignored: not top level",
		"harness/run.mjs":     "run",
		"harness/_private.js": "ignored: underscore",
		"harness/.hidden.mjs": "ignored: dot",
		"shims/a/b.js":        "shim",
	})
	out := t.TempDir()
	archive, metadata := filepath.Join(out, "runtime.zip"), filepath.Join(out, "digest.go")
	runMain(t, "-root", root, "-archive", archive, "-metadata", metadata)

	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	reader.RegisterDecompressor(zstd.ZipMethodWinZip, zstd.ZipDecompressor())
	var names []string
	contents := map[string]string{}
	for _, file := range reader.File {
		names = append(names, file.Name)
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data := make([]byte, file.UncompressedSize64)
		if _, err := handle.Read(data); err != nil && err.Error() != "EOF" {
			t.Fatal(err)
		}
		_ = handle.Close()
		contents[file.Name] = string(data)
	}
	slices.Sort(names)
	want := []string{"runtime-node/harness/run.mjs", "runtime-node/main.mjs", "runtime-node/shims/a/b.js"}
	if !slices.Equal(names, want) {
		t.Fatalf("archive entries = %v, want %v", names, want)
	}

	digest := sha256.New()
	for _, name := range []string{"runtime-node/harness/run.mjs", "runtime-node/main.mjs", "runtime-node/shims/a/b.js"} {
		digest.Write([]byte(name))
		digest.Write([]byte(contents[name]))
	}
	generated, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "const nodeRuntimeHash = "+strconv.Quote(string(digest.Sum(nil)))) {
		t.Fatalf("generated constant does not carry the content hash:\n%s", generated)
	}

	firstArchive, _ := os.ReadFile(archive)
	runMain(t, "-root", root, "-archive", archive, "-metadata", metadata)
	if secondArchive, _ := os.ReadFile(archive); string(firstArchive) != string(secondArchive) {
		t.Fatal("second run changed the archive bytes")
	}

	writeTree(t, root, map[string]string{"main.mjs": "changed"})
	runMain(t, "-root", root, "-archive", archive, "-metadata", metadata)
	if changed, _ := os.ReadFile(metadata); string(changed) == string(generated) {
		t.Fatal("changing a runtime source did not change the generated hash")
	}
}
