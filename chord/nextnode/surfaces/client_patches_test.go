package surfaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Not upstream's: PiG's patches over the vendored chord trees (automation/gen/vendor-chord-patches.mjs): a patch applies
// exactly or the vendoring fails, a patch needs an approved record, and a manifest's `patches` are the patch files as they
// are.

// chordPatches is where the patches live, patchTool what applies them.
const (
	chordPatches = "../../../automation/gen/chord-patches"
	patchTool    = "../../../automation/gen/vendor-chord-patches.mjs"
)

// vendoredPatch is an entry of a manifest's `patches`.
type vendoredPatch struct {
	Name   string `json:"name"`
	Sha256 string `json:"sha256"`
}

// patchProblems is what is wrong with the patches a manifest lists against the patch files of dir: a file that is gone, or
// one that is no longer the file the manifest hashed.
func patchProblems(dir string, listed []vendoredPatch) []string {
	var problems []string
	for _, patch := range listed {
		content, err := os.ReadFile(filepath.Join(dir, patch.Name))
		if err != nil {
			problems = append(problems, "patch "+patch.Name+": "+err.Error())
			continue
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != patch.Sha256 {
			problems = append(problems, "patch "+patch.Name+": sha256 "+got+", manifest "+patch.Sha256)
		}
	}
	return problems
}

// manifestPatches is the `patches` of the manifest at path; the test fails when the manifest has no such list.
func manifestPatches(t *testing.T, path string) []vendoredPatch {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Patches *[]vendoredPatch `json:"patches"`
	}
	if err := json.Unmarshal(text, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Patches == nil {
		t.Fatalf("%s has no patches list (an empty one when there is none)", path)
	}
	return *manifest.Patches
}

// patchFixture is a small tree, a records file with an approved record D900 and an unapproved D901, and an empty patch dir.
func patchFixture(t *testing.T) (dir, root, records string) {
	t.Helper()
	base := t.TempDir()
	dir, root, records = filepath.Join(base, "patches"), filepath.Join(base, "tree"), filepath.Join(base, "records.md")
	for path, text := range map[string]string{
		filepath.Join(root, "packages", "chord", "src", "a.ts"): "one\ntwo\nthree\nfour\n",
		filepath.Join(dir, ".keep"):                             "",
		records:                                                 "# Records\n\n## D900 A record\n\nWhat: a line.\n\nSCRUTINIZED:approved 2026-01-01\n\n## D901 Another\n\nWhat: nobody approved it.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, root, records
}

// patchRun writes the patch name with text into dir and applies the patches of dir to the tree at root: the tool's output,
// the tree's file afterwards, and the tool's error.
func patchRun(t *testing.T, dir, root, records, name, text string) (output, file string, err error) {
	t.Helper()
	node, lookErr := exec.LookPath("node")
	if lookErr != nil {
		t.Skip("no node on the PATH")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "--no-warnings", patchTool, dir, root, records).CombinedOutput()
	content, readErr := os.ReadFile(filepath.Join(root, "packages", "chord", "src", "a.ts"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(out), string(content), err
}

const patchHeader = "--- a/packages/chord/src/a.ts\n+++ b/packages/chord/src/a.ts\n"

func TestAChordPatchAppliesExactlyWhereItSaysAndIsListedWithItsHash(t *testing.T) {
	dir, root, records := patchFixture(t)
	text := "D900: two is louder\n" + patchHeader + "@@ -1,3 +1,4 @@\n one\n-two\n+TWO\n+and a half\n three\n"
	output, file, err := patchRun(t, dir, root, records, "D900-louder.patch", text)
	if err != nil {
		t.Fatalf("the patch did not apply: %v\n%s", err, output)
	}
	if file != "one\nTWO\nand a half\nthree\nfour\n" {
		t.Fatalf("the patched file = %q", file)
	}
	sum := sha256.Sum256([]byte(text))
	var applied []vendoredPatch
	if err := json.Unmarshal([]byte(output), &applied); err != nil || len(applied) != 1 || applied[0] != (vendoredPatch{Name: "D900-louder.patch", Sha256: hex.EncodeToString(sum[:])}) {
		t.Fatalf("applied = %s (%v), want the patch with the sha256 of its file", output, err)
	}
	if problems := patchProblems(dir, applied); len(problems) != 0 {
		t.Fatalf("the list the tool wrote has problems: %v", problems)
	}
}

func TestAChordPatchThatDoesNotApplyExactlyFailsTheVendoring(t *testing.T) {
	for name, hunk := range map[string]string{
		"a context line that is not the file's":   "@@ -1,3 +1,3 @@\n ONE\n-two\n+TWO\n three\n",
		"a removed line that is not the file's":   "@@ -1,3 +1,3 @@\n one\n-2\n+TWO\n three\n",
		"lines that are the file's, a line later": "@@ -2,3 +2,3 @@\n one\n-two\n+TWO\n three\n",
		"lines past the file's end":               "@@ -4,3 +4,3 @@\n four\n-five\n+FIVE\n six\n",
		"fewer lines than its header counts":      "@@ -1,4 +1,4 @@\n one\n-two\n+TWO\n three\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir, root, records := patchFixture(t)
			output, file, err := patchRun(t, dir, root, records, "D900-louder.patch", "D900: two is louder\n"+patchHeader+hunk)
			if err == nil {
				t.Fatalf("the patch applied: %s", output)
			}
			if !strings.Contains(output, "D900-louder.patch") || file != "one\ntwo\nthree\nfour\n" {
				t.Fatalf("output = %s, file = %q: want the patch named and the file untouched", output, file)
			}
		})
	}
}

func TestAChordPatchWithoutAnApprovedRecordIsRefused(t *testing.T) {
	hunk := patchHeader + "@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"
	for name, patch := range map[string][2]string{
		"a record that is not approved":             {"D901-louder.patch", "D901: two is louder\n" + hunk},
		"a record that does not exist":              {"D902-louder.patch", "D902: two is louder\n" + hunk},
		"a first line that names another record":    {"D900-louder.patch", "D901: two is louder\n" + hunk},
		"a name that names no record":               {"louder.patch", "D900: two is louder\n" + hunk},
		"a heading's number that only starts alike": {"D90-louder.patch", "D90: two is louder\n" + hunk},
	} {
		t.Run(name, func(t *testing.T) {
			dir, root, records := patchFixture(t)
			output, file, err := patchRun(t, dir, root, records, patch[0], patch[1])
			if err == nil {
				t.Fatalf("the patch applied: %s", output)
			}
			if !strings.Contains(output, patch[0]) || file != "one\ntwo\nthree\nfour\n" {
				t.Fatalf("output = %s, file = %q: want the patch named and the file untouched", output, file)
			}
		})
	}
}

func TestAListedChordPatchWhoseFileChangedOrIsGoneIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "D900-kept.patch"), []byte("D900: kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("D900: kept\n"))
	kept := vendoredPatch{Name: "D900-kept.patch", Sha256: hex.EncodeToString(sum[:])}
	if problems := patchProblems(dir, []vendoredPatch{kept}); len(problems) != 0 {
		t.Fatalf("a patch that is its file has problems: %v", problems)
	}
	changed := vendoredPatch{Name: "D900-kept.patch", Sha256: strings.Repeat("0", 64)}
	if problems := patchProblems(dir, []vendoredPatch{changed}); len(problems) != 1 || !strings.Contains(problems[0], "sha256") {
		t.Fatalf("a patch whose file changed: problems = %v, want its sha256 named", problems)
	}
	gone := vendoredPatch{Name: "D900-gone.patch", Sha256: kept.Sha256}
	if problems := patchProblems(dir, []vendoredPatch{gone}); len(problems) != 1 || !strings.Contains(problems[0], "D900-gone.patch") {
		t.Fatalf("a patch whose file is gone: problems = %v", problems)
	}
}

// Every patch of the directory is applied to a vendored tree: one no manifest lists changes nothing and says it does.
func TestEveryChordPatchIsListedByAVendoredTreesManifest(t *testing.T) {
	listed := map[string]bool{}
	for _, manifest := range []string{filepath.Join("client", "manifest.json"), filepath.Join("..", "plugins", "node", "manifest.json")} {
		for _, patch := range manifestPatches(t, manifest) {
			listed[patch.Name] = true
		}
	}
	entries, err := os.ReadDir(chordPatches)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".patch") && !listed[entry.Name()] {
			t.Errorf("%s is listed by no vendored tree's manifest: make vendor-chord-surfaces-client vendor-chord-plugins-node", entry.Name())
		}
	}
}
