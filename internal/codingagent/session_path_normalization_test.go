package codingagent

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// Pi's SessionManager.open, forkFrom and setSessionFile, ProjectTrustStore and
// normalizeCwd call utils/paths.ts resolvePath, which expands "~" and converts
// a file:// URL before path.resolve (session-manager.ts:1030,1764,1818,
// trust-manager.ts:41,213).
func TestPiResolvePathSitesNormalizeTheirInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	source := filepath.Join(home, "source.jsonl")
	header := `{"type":"session","version":3,"id":"source-session-id","timestamp":"2026-01-01T00:00:00Z","cwd":"/project"}` + "\n"
	if err := os.WriteFile(source, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(source)}).String()
	if filepath.VolumeName(source) != "" {
		fileURL = (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(source)}).String()
	}
	sm := NewSessionManagerWithDir("/project", filepath.Join(home, "sessions"))

	for _, input := range []string{"~" + string(filepath.Separator) + "source.jsonl", fileURL} {
		opened, err := sm.Open(input)
		if err != nil || opened.Path() != source {
			t.Errorf("Open(%q) = %v, %v; want the session at %q", input, opened, err, source)
		}
		loaded, err := sm.Load(input)
		if err != nil || loaded.Path() != source {
			t.Errorf("Load(%q) = %v, %v; want the session at %q", input, loaded, err, source)
		}
		fork, err := sm.ForkFromFile(input, "fork-id")
		if err != nil || fork.ParentSession() != source {
			t.Errorf("ForkFromFile(%q) = %v, %v; want parentSession %q", input, fork, err, source)
		}
	}

	if got, want := NewProjectTrustStore("~"+string(filepath.Separator)+"agent").trustPath, filepath.Join(home, "agent", "trust.json"); got != want {
		t.Errorf("NewProjectTrustStore(~/agent) path = %q, want %q", got, want)
	}
	if got, want := normalizeTrustCwd("~"), CanonicalizePath(home); got != want {
		t.Errorf("normalizeTrustCwd(~) = %q, want %q", got, want)
	}
}
