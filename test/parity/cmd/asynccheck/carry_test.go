package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

// A leap keeps a reviewed async row only for a source file that is byte-identical in the two mirrors; a changed file's Promise
// ordering may differ, so it is pending again, and so is a file the earlier manifest never reviewed.
func TestPendingManifestCarriesOnlyUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	before, now := filepath.Join(dir, "before"), filepath.Join(dir, "now")
	for root, files := range map[string]map[string]string{
		before: {"same.ts": "await a;", "edited.ts": "await a;", "gone.ts": "x"},
		now:    {"same.ts": "await a;", "edited.ts": "await b;", "new.ts": "await c;"},
	} {
		for name, body := range files {
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest := filepath.Join(dir, "previous.toml")
	reviewed := func(name string) string {
		return "\n[[files]]\npath = \"" + name + "\"\ndisposition = \"deferred\"\ncontracts = [\"awaited\"]\nevidence = [\"test:a_test.go\"]\nrationale = \"why \\\"quoted\\\"\"\n"
	}
	if err := os.WriteFile(manifest, []byte("version = \"1.1.0\"\n"+reviewed("same.ts")+reviewed("edited.ts")+reviewed("gone.ts")), 0o644); err != nil {
		t.Fatal(err)
	}
	text := pendingManifest("1.1.0+x", []string{"edited.ts", "new.ts", "same.ts"}, carryUnchanged(manifest, before, now))
	var got asyncManifest
	if _, err := toml.Decode(text, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	want := map[string]string{"edited.ts": "pending", "new.ts": "pending", "same.ts": "deferred"}
	if got.Version != "1.1.0+x" || len(got.Files) != 3 {
		t.Fatalf("manifest = %+v", got)
	}
	for _, row := range got.Files {
		if row.Disposition != want[row.Path] {
			t.Errorf("%s = %s, want %s", row.Path, row.Disposition, want[row.Path])
		}
	}
	if kept := got.Files[2]; len(kept.Contracts) != 1 || kept.Rationale != `why "quoted"` || len(kept.Evidence) != 1 {
		t.Errorf("carried row not kept verbatim: %+v", kept)
	}
}
