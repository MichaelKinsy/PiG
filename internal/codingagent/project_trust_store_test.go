package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trust-manager.ts ProjectTrustStore: the nearest stored ancestor decides (a deeper entry overrides a shallower one, a distrust below a trust stays a distrust), Set with
// a nil decision deletes the entry, SetMany applies one batch, and the store persists to trust.json in the agent directory so a second store over it sees the same decisions.
func TestProjectTrustStoreNearestAncestorWinsAndPersists(t *testing.T) {
	agentDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "work")
	child := filepath.Join(root, "a", "b")
	store := NewProjectTrustStore(agentDir)
	decision := func(s *ProjectTrustStore, cwd string) string {
		t.Helper()
		got, err := s.GetEntry(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			if d, _ := s.Get(cwd); d != nil {
				t.Fatalf("Get(%s) = %v with no entry", cwd, *d)
			}
			return "none"
		}
		d, _ := s.Get(cwd)
		if d == nil || *d != got.Decision {
			t.Fatalf("Get(%s) disagrees with GetEntry %+v", cwd, got)
		}
		return map[bool]string{true: "trust", false: "distrust"}[got.Decision] + "@" + strings.TrimPrefix(got.Path, root)
	}

	if got := decision(store, child); got != "none" {
		t.Fatalf("an empty store decided %q", got)
	}
	yes, no := true, false
	if err := store.Set(root, &yes); err != nil {
		t.Fatal(err)
	}
	if got := decision(store, child); got != "trust@" {
		t.Fatalf("a trusted ancestor decided %q for its descendant", got)
	}
	if err := store.Set(filepath.Join(root, "a"), &no); err != nil {
		t.Fatal(err)
	}
	if got := decision(store, child); got != "distrust@"+string(filepath.Separator)+"a" {
		t.Fatalf("the nearer distrust decided %q", got)
	}
	if got := decision(store, filepath.Join(root, "z")); got != "trust@" {
		t.Fatalf("a sibling of the distrusted directory decided %q", got)
	}

	// A second store over the same agent directory reads the persisted decisions; trust.json holds them.
	if got := decision(NewProjectTrustStore(agentDir), child); got != "distrust@"+string(filepath.Separator)+"a" {
		t.Fatalf("a second store decided %q", got)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "trust.json")); err != nil {
		t.Fatalf("trust.json: %v", err)
	}

	// SetMany applies the whole batch: a nil decision deletes its entry.
	if err := store.SetMany([]ProjectTrustUpdate{{Path: filepath.Join(root, "a"), Decision: nil}, {Path: child, Decision: &no}}); err != nil {
		t.Fatal(err)
	}
	if got := decision(store, filepath.Join(child, "deeper")); got != "distrust@"+string(filepath.Separator)+filepath.Join("a", "b") {
		t.Fatalf("after the batch, a descendant of the new entry decided %q", got)
	}
	if got := decision(store, filepath.Join(root, "a")); got != "trust@" {
		t.Fatalf("after deleting the distrust, its directory decided %q, want the ancestor's trust", got)
	}
}
