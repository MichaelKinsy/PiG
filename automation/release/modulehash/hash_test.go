// SPDX-License-Identifier: MIT

package modulehash

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestHashesMatchPublishedTrackedFilesAndOverlay(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, t.TempDir())
	}
	dir := filepath.Join(root, "sdk")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":            "module example.com/sdk\n\ngo 1.26.0\n",
		"sdk.go":            "package sdk\nconst Value = 1\n",
		"nested/go.mod":     "module example.com/nested\n",
		"nested/ignored.go": "nested module files do not ship in parent\n",
	}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "sdk"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("not published"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v0.2.1", "v0.3.0"} {
		for _, overlaid := range []bool{false, true} {
			t.Run(version+"/overlay="+map[bool]string{false: "false", true: "true"}[overlaid], func(t *testing.T) {
				mod := module.Version{Path: "example.com/sdk", Version: version}
				overlay := map[string][]byte{}
				if overlaid {
					overlay["sdk/sdk.go"] = []byte("package sdk\nconst Value = 123456789\n")
				}
				zipHash, modHash, err := Hashes(root, "sdk", mod, overlay)
				if err != nil {
					t.Fatal(err)
				}
				prefix := mod.Path + "@" + mod.Version + "/"
				// Independent Go h1 oracle with the exact publication file set:
				// untracked files and the nested module are absent by contract.
				open := func(path string) (io.ReadCloser, error) {
					rel := strings.TrimPrefix(path, prefix)
					data := files[rel]
					if planned, ok := overlay["sdk/"+rel]; ok {
						data = string(planned)
					}
					return io.NopCloser(strings.NewReader(data)), nil
				}
				wantZip, err := dirhash.Hash1([]string{prefix + "go.mod", prefix + "sdk.go"}, open)
				if err != nil {
					t.Fatal(err)
				}
				wantMod, err := dirhash.Hash1([]string{"go.mod"}, open)
				if err != nil {
					t.Fatal(err)
				}
				if zipHash != wantZip || modHash != wantMod {
					t.Fatalf("got %s / %s, want %s / %s", zipHash, modHash, wantZip, wantMod)
				}
			})
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "sdk.go"))
	if err != nil || string(data) != files["sdk.go"] {
		t.Fatalf("overlay modified source: %q, %v", data, err)
	}
}
