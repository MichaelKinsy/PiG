package cli

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// .upstream/v0.87.1/packages/coding-agent/test/package-manager-ssh.test.ts:75
func TestBareSCPPackageSourceRemainsLocalAtCLIAndIdentityBoundaries(t *testing.T) {
	const source = "git@github.com:user/repo"
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PIG_HOME", t.TempDir())
	if kind := packagemanager.DetectSourceKind(source); kind != "local" {
		t.Fatalf("source kind=%q", kind)
	}
	if identity := packagemanager.PackageSourceIdentity(cwd, source); identity != "local:"+filepath.Join(cwd, source) {
		t.Fatalf("identity=%q", identity)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", source}) })
	if code != 1 || stdout != "Installing "+source+"...\n" || stderr != "Error: Path does not exist: "+filepath.Join(cwd, source)+"\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
