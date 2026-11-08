package surfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// client/manifest.json records the Pi commit and the hash of every file vendored under client/pi (D601): a file edited by
// hand, or one the manifest does not list, fails here (automation/gen/vendor-chord-surfaces-client.sh regenerates both). The
// commit is the pin's (`.upstream/pin`), unless the manifest names it as an explicit `catchup`, as the plugins component's
// vendored tree is checked.
func TestClientVendoredFilesAreExactlyTheManifests(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("client", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Commit  string `json:"commit"`
		Pin     string `json:"pin"`
		Catchup string `json:"catchup"`
		Files   []struct {
			Path   string `json:"path"`
			Sha256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(text, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Commit) != 40 {
		t.Fatalf("commit = %q", manifest.Commit)
	}
	pinFile, err := os.ReadFile(filepath.Join("..", "..", "..", ".upstream", "pin"))
	if err != nil {
		t.Fatal(err)
	}
	var pin string
	for line := range strings.Lines(string(pinFile)) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "commit="); ok {
			pin = value
		}
	}
	if pin == "" {
		t.Fatal(".upstream/pin names no commit")
	}
	if manifest.Pin != pin {
		t.Errorf("the pin moved to %s since the copy was made against %s: make vendor-chord-surfaces-client", pin, manifest.Pin)
	}
	if manifest.Commit != pin && manifest.Catchup != manifest.Commit {
		t.Errorf("the copy is Pi %s, the pin %s, and the manifest names no catch-up for it (catchup = %q)", manifest.Commit, pin, manifest.Catchup)
	}
	if manifest.Commit == pin && manifest.Catchup != "" {
		t.Errorf("the copy is the pin's, yet the manifest names the catch-up %s", manifest.Catchup)
	}
	listed := map[string]bool{}
	for _, file := range manifest.Files {
		listed[file.Path] = true
		content, err := os.ReadFile(filepath.Join("client", filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != file.Sha256 {
			t.Errorf("%s: sha256 %s, manifest %s", file.Path, got, file.Sha256)
		}
	}
	err = filepath.WalkDir(filepath.Join("client", "pi"), func(path string, entry fs.DirEntry, err error) error {
		relative, _ := filepath.Rel("client", path)
		if err == nil && !entry.IsDir() && !listed[filepath.ToSlash(relative)] {
			t.Errorf("%s is vendored but not in the manifest", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
