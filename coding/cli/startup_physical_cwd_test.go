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

// Pi's main.ts takes the launch directory from process.cwd(), which is the physical path (libuv uv_cwd calls getcwd(3)); a $PWD that
// reaches the directory through a symlink does not change it. The session header, the system prompt and every session directory name
// therefore carry the physical path.
func TestStartupWorkingDirectoryIsThePhysicalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no $PWD")
	}
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	testenv.RequireDirectoryLink(t, real, link)
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), buildPigBinaryForSignalTest(t), "--mode", "json", "--model", "test-faux/faux-1", "--no-context-files", "--no-skills", "--session-dir", filepath.Join(home, "sessions"), "hello")
	cmd.Dir = link
	cmd.Env = append(os.Environ(), "PWD="+link, "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+home, "PI_CODING_AGENT_DIR="+home, "PIG_TEST_FAUX=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	first, _, _ := strings.Cut(stdout.String(), "\n")
	var header struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(first), &header); err != nil || header.Type != "session" {
		t.Fatalf("first line is not the session header: %q (%v)", first, err)
	}
	if header.Cwd != real {
		t.Fatalf("session header cwd = %q, want the physical %q", header.Cwd, real)
	}
}
