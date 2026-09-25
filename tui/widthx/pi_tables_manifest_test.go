package widthx

// Provenance lock for unicode_tables.go: the tables must come from the pinned
// inputs in gen/inputs.sha256 and must not be edited after generation.
//   - every in-repo input (vendored get-east-asian-width, the generator)
//     hashes as the manifest pins;
//   - the tables header records the digest of this exact manifest;
//   - unicode_tables.go hashes as gen/output.sha256 recorded at generation;
//   - with PIG_UCD_DIR set to a UCD 17.0.0 tree and a matching node, the
//     generator (which re-verifies the UCD hashes) reproduces the file byte
//     for byte.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestUnicodeTablesDeriveFromPinnedInputs(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("gen", "inputs.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{"eaw": filepath.Join("testdata", "pi", "get-east-asian-width"), "gen": "gen"}
	checked, ucd := 0, 0
	for line := range strings.SplitSeq(string(manifest), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "node-unicode" {
			continue
		}
		root, rel, _ := strings.Cut(f[1], ":")
		if root == "ucd" {
			ucd++
			continue
		}
		dir, ok := roots[root]
		if !ok {
			t.Fatalf("manifest names unknown root %q", root)
		}
		if got := fileSHA256(t, filepath.Join(dir, rel)); got != f[0] {
			t.Errorf("%s: sha256 %s, pinned %s", f[1], got, f[0])
		}
		checked++
	}
	if checked < 6 || ucd < 4 {
		t.Fatalf("manifest lists %d in-repo and %d UCD inputs; want at least 6 and 4", checked, ucd)
	}
	sum := sha256.Sum256(manifest)
	header := "// Inputs: gen/inputs.sha256 sha256 " + hex.EncodeToString(sum[:]) + "."
	tables, err := os.ReadFile("unicode_tables.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(tables, []byte(header+"\n")) {
		t.Errorf("unicode_tables.go was not generated from the current gen/inputs.sha256; regenerate with gen/gen_tables.mjs")
	}
	out, err := os.ReadFile(filepath.Join("gen", "output.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Fields(string(out))[0]; fileSHA256(t, "unicode_tables.go") != want {
		t.Errorf("unicode_tables.go differs from the generator output recorded in gen/output.sha256 (hand-edited?)")
	}

	dir := os.Getenv("PIG_UCD_DIR")
	node, nodeErr := exec.LookPath("node")
	if dir == "" || nodeErr != nil {
		t.Log("PIG_UCD_DIR or node unavailable; skipped byte-for-byte regeneration")
		return
	}
	tmp := t.TempDir()
	gen := filepath.Join(tmp, "gen")
	if err := os.CopyFS(gen, os.DirFS("gen")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(tmp, "testdata", "pi", "get-east-asian-width"), os.DirFS(roots["eaw"])); err != nil {
		t.Fatal(err)
	}
	regenerated, err := exec.Command(node, filepath.Join(gen, "gen_tables.mjs"), dir).Output()
	if err != nil {
		t.Fatalf("regenerate tables: %v", err)
	}
	if !bytes.Equal(regenerated, tables) {
		t.Fatal("regenerating from the pinned inputs does not reproduce unicode_tables.go")
	}
}
