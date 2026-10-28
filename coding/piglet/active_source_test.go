package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveSourceValue(t *testing.T) {
	withSource, err := ParseBytes([]byte("name: reviewer\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ActiveSourceValue(nil, false); got != "" {
		t.Errorf("nil Piglet = %q", got)
	}
	if got := ActiveSourceValue(withSource, false); got != "reviewer" {
		t.Errorf("Piglet without a source file = %q, want its name", got)
	}
	file := filepath.Join(t.TempDir(), "reviewer.yaml")
	if err := os.WriteFile(file, []byte("name: reviewer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	// Parse resolves symlinks, so a temp directory under /var on macOS comes back as /private/var.
	want, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := ActiveSourceValue(parsed, false); got != want {
		t.Errorf("Piglet from a file = %q, want %q", got, want)
	}
	if got := ActiveSourceValue(parsed, true); got != "reviewer" {
		t.Errorf("a Piglet Binary's Piglet = %q, want its name", got)
	}
}

func TestActiveSourceIsPath(t *testing.T) {
	for value, want := range map[string]bool{"/tmp/p/reviewer.yaml": true, "reviewer.yml": true, "rel/dir": true, "tern": false, "pig-tern": false} {
		if got := ActiveSourceIsPath(value); got != want {
			t.Errorf("ActiveSourceIsPath(%q) = %v, want %v", value, got, want)
		}
	}
}

// Bug 4: `pig piglet show` run from the agent's shell shows the Piglet of the session that started it.
func TestShowWithoutAnArgumentUsesTheActivePigletSource(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "reviewer.yaml")
	if err := os.WriteFile(file, []byte("name: reviewer\ndescription: Reviews code\ntools: [read]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ActiveSourceEnv, file)
	var stdout, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "show"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "reviewer") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	t.Setenv(ActiveSourceEnv, "")
	stdout.Reset()
	stderr.Reset()
	if code := RunCommand([]string{"piglet", "show"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "Usage: pig piglet show") {
		t.Fatalf("without a source: code=%d stderr=%s", code, stderr.String())
	}
}
