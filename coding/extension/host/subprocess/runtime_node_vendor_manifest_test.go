package subprocess_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's native-module-path.ts resolves helpers from the package's native directory, not dist/native. The manifest must retain provenance for every helper copied alongside the complete TUI graph.
func TestNodeVendorManifestIncludesPinnedNativeAssets(t *testing.T) {
	shims, err := filepath.Abs(filepath.Join("runtime-node", "shims"))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := filepath.Abs(pinnedPiPackages)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "vendor-manifest.json")
	command := exec.CommandContext(t.Context(), "node", filepath.Join("..", "..", "..", "..", "automation", "gen", "vendor-node-manifest.mjs"), shims, agent, output)
	if log, err := command.CombinedOutput(); err != nil {
		t.Fatalf("manifest: %v\n%s", err, log)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files []struct {
			Path         string `json:"path"`
			Source       string `json:"source"`
			SourceSHA256 string `json:"sourceSha256"`
			SHA256       string `json:"sha256"`
			Package      struct {
				Name string `json:"name"`
			} `json:"package"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(agent, "node_modules", "@earendil-works", "pi-tui", "native")
	if err := filepath.WalkDir(native, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(native, path)
		if err != nil {
			return err
		}
		source := "native/" + filepath.ToSlash(rel)
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(contents))
		for _, record := range manifest.Files {
			if record.Path == "pi-dist/pi-tui/"+source {
				if record.Source != source || record.SourceSHA256 != digest || record.SHA256 != digest || record.Package.Name != "@earendil-works/pi-tui" {
					t.Errorf("native provenance for %s = %+v", source, record)
				}
				return nil
			}
		}
		t.Errorf("manifest omits pinned helper %s", source)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join(shims, "vendor-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, committed) {
		t.Fatal("vendor manifest differs from the reviewed generated snapshot: run automation/gen/vendor-pi-dist.sh")
	}
}
