//go:build pig_experimental

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// experimentalEntryArgsEnv carries the arguments TestExperimentalEntryHelper passes to runExperimentalCommand in a
// child test process, so the strip a Piglet records stays in that process.
const experimentalEntryArgsEnv = "PIG_TEST_EXPERIMENTAL_ENTRY_ARGS"

// TestExperimentalEntryHelper is the child of runExperimentalEntry: it runs runExperimentalCommand and prints whether
// the experimental entry handled the command, and its exit code.
func TestExperimentalEntryHelper(t *testing.T) {
	encoded := os.Getenv(experimentalEntryArgsEnv)
	if encoded == "" {
		t.Skip("runs only as the child of runExperimentalEntry")
	}
	var args []string
	if err := json.Unmarshal([]byte(encoded), &args); err != nil {
		t.Fatal(err)
	}
	handled, code := runExperimentalCommand(context.Background(), args)
	fmt.Printf("ENTRY handled=%t code=%d stripped=%t\n", handled, code, pigstrip.Active())
}

// runExperimentalEntry runs runExperimentalCommand(args) with experiments enabled in a child process, and returns
// its result line and stderr.
func runExperimentalEntry(t *testing.T, args ...string) (string, string) {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExperimentalEntryHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), experimentalEntryArgsEnv+"="+string(encoded), "PI_EXPERIMENTAL=1", "HOME="+home,
		"PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_OFFLINE=1", "NO_COLOR=1")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		if strings.HasPrefix(line, "ENTRY ") {
			return line, stderr.String()
		}
	}
	t.Fatalf("experimental entry child printed no result:\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	return "", ""
}

// The experimental entry honors --piglet as the stable CLI does: a Piglet that strips the experimental server leaves
// `server` to the stable CLI, and one that keeps it runs the experimental server command without the flag, which the
// experimental parser would reject as an existing CLI option.
func TestExperimentalEntryHonorsPigletFlag(t *testing.T) {
	dir := t.TempDir()
	strips := filepath.Join(dir, "strips.yaml")
	keeps := filepath.Join(dir, "keeps.yaml")
	for path, yaml := range map[string]string{
		strips: "name: strips\nstrip:\n  features: [experimental-server]\n",
		keeps:  "name: keeps\nstrip:\n  tools: [ls]\n",
	} {
		if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"server", "--piglet", strips}, {"server", "--piglet=" + strips}} {
		if got, stderr := runExperimentalEntry(t, args...); got != "ENTRY handled=false code=0 stripped=false" {
			t.Errorf("pig %s: %s, want the stable CLI\n%s", strings.Join(args, " "), got, stderr)
		}
	}
	// --provider without --model fails the server's own validation before any server starts.
	// The kept server applies no Piglet, so the entry records none of its strip list: stripped=false.
	got, stderr := runExperimentalEntry(t, "server", "--piglet", keeps, "--provider", "x")
	if got != "ENTRY handled=true code=1 stripped=false" || !strings.Contains(stderr, "--provider requires --model") || strings.Contains(stderr, "does not support existing CLI options") {
		t.Errorf("pig server --piglet keeps.yaml --provider x: %s\n%s\nwant only the server's --provider diagnostic", got, stderr)
	}
}
