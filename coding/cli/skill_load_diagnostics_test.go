package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// skills.ts loadSkills returns [...allDiagnostics, ...collisionDiagnostics]: each path's load and validation warnings, then the name collisions. Interactive mode lists them under [Skill conflicts]; no mode writes them to stderr (Pi 1.1.0 print mode with --skill on a misnamed skill prints only the answer).
func TestLoadSkillsReturnsValidationWarningsBeforeCollisionsAndWritesNothing(t *testing.T) {
	root := t.TempDir()
	write := func(dir, body string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	first := write(filepath.Join(root, "a", "same"), "---\nname: same\ndescription: first\n---\nBody")
	second := write(filepath.Join(root, "b", "same"), "---\nname: same\ndescription: second\n---\nBody")
	misnamed := write(filepath.Join(root, "c", "mismatch"), "---\nname: Bad--Name\ndescription: Badly named\n---\nBody")
	var diagnostics []extension.ResourceDiagnostic
	var err error
	stderr := captureStderr(t, func() {
		_, diagnostics, err = loadSkills([]string{first, second, misnamed}, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Fatalf("loadSkills wrote to stderr:\n%s", stderr)
	}
	var types []string
	for _, diagnostic := range diagnostics {
		types = append(types, diagnostic.Type)
	}
	want := []string{"warning", "warning", "collision"}
	if len(types) != len(want) {
		t.Fatalf("diagnostic types = %q, want %q (%+v)", types, want, diagnostics)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("diagnostic types = %q, want %q (%+v)", types, want, diagnostics)
		}
	}
	if path := filepath.Join(misnamed, "SKILL.md"); diagnostics[0].Path != path || diagnostics[0].Message != "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)" {
		t.Fatalf("first warning = %+v", diagnostics[0])
	}
}
