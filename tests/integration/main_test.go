//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMain builds ./cmd/pig once into a process-lifetime temp dir
// and stashes the path in binaryPath. Without this, each test's
// t.TempDir() got cleaned up between subtests and the second test
// in a run found no binary.
func TestMain(m *testing.M) {
	os.Exit(runIntegrationTests(m))
}

func runIntegrationTests(m *testing.M) int {
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration: can't find repo root:", err)
		return 2
	}
	tmp, err := os.MkdirTemp("", "pig-integration-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration: mktmp:", err)
		return 2
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			fmt.Fprintln(os.Stderr, "remove integration fixtures:", err)
		}
	}()
	tmuxHomeRoot = tmp
	// Keep the private server alive between tests. Otherwise killing the last session races the next new-session while the server exits under load.
	keeper := "parity-integration-keeper-" + randID()
	if out, err := tmuxCommand("new-session", "-d", "-s", keeper).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: start private tmux server: %v\n%s", err, out)
		return 2
	}
	defer func() {
		if out, err := tmuxCommand("kill-session", "-t", keeper).CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "integration: remove tmux keeper: %v\n%s", err, out)
		}
	}()
	bin := filepath.Join(tmp, "pig-it")
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", bin, "./cmd/pig")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: build failed: %v\n%s", err, out)
		return 2
	}
	binaryPath = bin
	return m.Run()
}
