package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// runPigThroughLink runs pig in a new directory entered through a symbolic link, with $PWD naming the link as a shell's cd leaves it,
// and returns the directory's physical path and pig's stdout. setup prepares the physical directory first. Pi takes every
// working-directory default from process.cwd(), which is the physical path (libuv uv_cwd calls getcwd(3)) whatever $PWD says.
func runPigThroughLink(t *testing.T, setup func(real string), args ...string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no $PWD")
	}
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	testenv.RequireDirectoryLink(t, real, link)
	if setup != nil {
		setup(real)
	}
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), buildPigBinaryForSignalTest(t), args...)
	cmd.Dir = link
	cmd.Env = append(os.Environ(), "PWD="+link, "HOME="+home, "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+home, "PI_CODING_AGENT_DIR="+home, "PIG_TEST_FAUX=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return real, stdout.String()
}

// physicalTempDir is a new temporary directory under its physical path, the one process.cwd() reports once a test changes into it
// (macOS's temporary directory /var/… is /private/var/…). Package commands resolve against that path, as Pi's do.
func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sessionHeaderCwd(t *testing.T, stdout string) string {
	t.Helper()
	first, _, _ := strings.Cut(stdout, "\n")
	var header struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(first), &header); err != nil || header.Type != "session" {
		t.Fatalf("first line is not the session header: %q (%v)", first, err)
	}
	return header.Cwd
}

// Pi's main.ts takes the launch directory from process.cwd(); a $PWD that reaches the directory through a symlink does not change it.
// The session header, the system prompt, every session directory name and the <file name> of a relative @file argument (main.ts:593,
// file-processor.ts resolveReadPath) therefore carry the physical path.
func TestStartupWorkingDirectoryIsThePhysicalPath(t *testing.T) {
	sessions := filepath.Join(t.TempDir(), "sessions")
	real, stdout := runPigThroughLink(t, func(real string) { writeTestFile(t, filepath.Join(real, "a.txt"), "hi\n") },
		"--mode", "json", "--model", "test-faux/faux-1", "--no-context-files", "--no-skills", "--session-dir", sessions, "@a.txt", "hello")
	if cwd := sessionHeaderCwd(t, stdout); cwd != real {
		t.Fatalf("session header cwd = %q, want the physical %q", cwd, real)
	}
	if want := `<file name=\"` + filepath.Join(real, "a.txt") + `\">`; !strings.Contains(stdout, want) {
		t.Fatalf("@a.txt is not named by its physical path %s:\n%s", want, stdout)
	}
}

// SessionManager.open on an empty file starts a new session in `cwdOverride ?? header cwd ?? process.cwd()` (session-manager.ts:1782),
// so `--session <empty file>` runs in, and records, the physical directory.
func TestEmptySessionFileStartsInThePhysicalWorkingDirectory(t *testing.T) {
	session := filepath.Join(t.TempDir(), "empty.jsonl")
	writeTestFile(t, session, "")
	real, stdout := runPigThroughLink(t, nil, "--mode", "json", "--model", "test-faux/faux-1", "--no-context-files", "--no-skills", "--session", session, "hello")
	if cwd := sessionHeaderCwd(t, stdout); cwd != real {
		t.Fatalf("session header cwd = %q, want the physical %q", cwd, real)
	}
}

// package-manager-cli.ts:951 resolves project packages against process.cwd(), so `list` prints a project-local package's installed
// path through the physical directory.
func TestPackageListNamesProjectPackagesThroughThePhysicalWorkingDirectory(t *testing.T) {
	real, stdout := runPigThroughLink(t, func(real string) {
		writeTestFile(t, filepath.Join(real, ".pig", "settings.json"), `{"packages":["../ext"]}`)
		writeTestFile(t, filepath.Join(real, "ext", "extensions", "a.ts"), "export default function () {}\n")
	}, "list", "--approve")
	if want := "    " + filepath.Join(real, "ext") + "\n"; !strings.Contains(stdout, want) {
		t.Fatalf("list output does not name %q:\n%s", want, stdout)
	}
}
