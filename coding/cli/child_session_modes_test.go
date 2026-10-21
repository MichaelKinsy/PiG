//go:build unix

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// An extension's createAgentSession builds a child over the model runtime of the mode's build, in every mode (sdk.ts createAgentSession; createAgentSessionServices is not mode-specific). The fixture registers a provider, creates a child over it from a command, prompts it and records what the child answered.
func TestExtensionChildSessionsAcrossModes(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	fixture := buildGoSDKFixture(t, "child-session-modes-go")
	setup := func(t *testing.T) (env []string, log string, args []string) {
		dir := t.TempDir()
		log = filepath.Join(dir, "child.log")
		env = []string{"HOME=" + dir, "PIG_HOME=" + filepath.Join(dir, "home"), "PIG_CODING_AGENT_DIR=" + filepath.Join(dir, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_TEST_CHILD_LOG=" + log}
		args = []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", fixture}
		return env, log, args
	}
	want := "final:child answer\n"
	for _, mode := range []string{"print", "json"} {
		t.Run(mode, func(t *testing.T) {
			env, log, args := setup(t)
			if mode == "json" {
				args = append(args, "--mode", "json")
			}
			cmd := exec.CommandContext(testbudget.Context(t), bin, append(args, "-p", "/child")...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), env...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("pig %s mode: %v\n%s", mode, err, out)
			}
			if got, _ := os.ReadFile(log); string(got) != want {
				t.Fatalf("the child answered %q, want %q", got, want)
			}
		})
	}
	t.Run("rpc", func(t *testing.T) {
		env, log, args := setup(t)
		p := startRPCProcessAt(t, t.TempDir(), env, args...)
		p.send(`{"id":"child","type":"prompt","message":"/child"}`)
		// The command ends with a notification, after the child answered.
		p.await("the child command's notification", func(record rpcRecord) bool {
			return record["type"] == "extension_ui_request" && record["method"] == "notify" && record["message"] == "child done"
		})
		got, _ := os.ReadFile(log)
		if string(got) != want {
			t.Fatalf("the child answered %q, want %q\n%s", got, want, p.stderr.String())
		}
	})
}
