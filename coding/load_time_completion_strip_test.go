//go:build !pig_strip_node_extensions

package coding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A Piglet that Stock PiG runs strips its APIs when the process starts (strip.apis). The completions an extension asks for while its
// module loads have no session to go through: they take pi-ai's compatibility layer, which builds the collection of built-in
// providers first. With an API stripped they are answered like any other. The strip is process state and the collection is built
// once per process, so each case runs in a process of its own: this test binary again, with the API to strip in its environment.
func TestLoadTimeCompletionsAnsweredWithAnAPIStrippedAtRunTime(t *testing.T) {
	const stripEnv = "PIG_TEST_LOAD_TIME_STRIPPED_API"
	if api := os.Getenv(stripEnv); api != "" {
		t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, api))
		module, answers := loadTimeCompletionModule(t)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		root := t.TempDir()
		cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
		for _, dir := range []string{cwd, agentDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		result, err := DiscoverAndLoadExtensions(ctx, []string{module}, cwd, agentDir)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		if len(result.Errors) != 0 || len(result.Extensions) != 1 {
			t.Fatalf("errors = %v, extensions = %d, want the module loaded", result.Errors, len(result.Extensions))
		}
		requireLoadTimeAnswers(t, answers)
		return
	}
	for _, api := range []string{"bedrock-converse-stream", "google-vertex", "mistral-conversations"} {
		t.Run(api, func(t *testing.T) {
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLoadTimeCompletionsAnsweredWithAnAPIStrippedAtRunTime$", "-test.count=1")
			cmd.Env = append(os.Environ(), stripEnv+"="+api)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("a process with %s stripped: %v\n%s", api, err, output)
			}
		})
	}
}
