// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A rename changes two paths: the one that disappears and the one that appears. The classifier reads both, so moving
// an OS-specific file to a neutral name still asks for the full run, and moving a product file into the prose paths
// still runs the product suites. A copy leaves its source unchanged; a deletion changes the deleted path.
func TestChangeClassifierReadsBothSidesOfARename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := t.TempDir()
	runGitIn(t, repo, "init", "-q")
	// The repository's own setting must not hide a source either.
	runGitIn(t, repo, "config", "diff.renames", "copies")
	write := func(path, text string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	body := strings.Repeat("a line of a file that git recognises after it moved\n", 20)
	write("README.md", "readme\n")
	write("tui/clipboard_windows.go", "package tui // windows\n"+body)
	write("tui/keys_darwin.go", "package tui // darwin\n"+body)
	write("tui/neutral.go", "package tui // neutral\n"+body)
	write("coding/session.go", "package coding\n"+body)
	runGitIn(t, repo, "add", "-A")
	runGitIn(t, repo, "commit", "-q", "-m", "base")
	base := runGitIn(t, repo, "rev-parse", "HEAD")
	// change makes one commit on top of base and returns it.
	change := func(message string, edit func()) string {
		t.Helper()
		runGitIn(t, repo, "checkout", "-q", "--detach", base)
		edit()
		runGitIn(t, repo, "add", "-A")
		runGitIn(t, repo, "commit", "-q", "-m", message)
		return runGitIn(t, repo, "rev-parse", "HEAD")
	}
	move := func(from, to string) func() {
		return func() {
			if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(filepath.FromSlash(to))), 0o755); err != nil {
				t.Fatal(err)
			}
			runGitIn(t, repo, "mv", from, to)
		}
	}
	copyOf := func(from, to string) func() {
		return func() {
			raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(from)))
			if err != nil {
				t.Fatal(err)
			}
			write(to, string(raw))
		}
	}
	script := filepath.Join(repoRoot(t), "automation", "ci", "classify-changes.sh")
	for _, tc := range []struct {
		name       string
		edit       func()
		osSpecific string
		product    string
	}{
		{"a Windows file renamed to a neutral name", move("tui/clipboard_windows.go", "tui/clipboard.go"), "true", "true"},
		{"a neutral file renamed to a Windows name", move("tui/neutral.go", "tui/neutral_windows.go"), "true", "true"},
		{"a neutral file renamed to a neutral name", move("tui/neutral.go", "tui/other.go"), "false", "true"},
		{"a macOS file copied to a neutral name", copyOf("tui/keys_darwin.go", "tui/keys.go"), "false", "true"},
		{"a neutral file copied to a macOS name", copyOf("tui/neutral.go", "tui/neutral_darwin.go"), "true", "true"},
		{"a Windows file deleted", func() { runGitIn(t, repo, "rm", "-q", "tui/clipboard_windows.go") }, "true", "true"},
		{"a product file renamed into the prose paths", move("coding/session.go", "docs/media/session.txt"), "false", "true"},
		{"prose renamed within the prose paths", move("README.md", "docs/media/readme.txt"), "false", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := change(tc.name, tc.edit)
			cmd := testenv.ScriptCommand(t, script, "pull_request", base, head)
			cmd.Dir = repo
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			want := []string{"product=" + tc.product, "os-specific=" + tc.osSpecific}
			if strings.Join(lines, " ") != strings.Join(want, " ") {
				t.Errorf("classified %q, want %q", lines, want)
			}
		})
	}
}
