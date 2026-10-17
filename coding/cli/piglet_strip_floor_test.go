package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// The functional floor (D92) holds at startup as Piglet validation does: a
// Piglet whose effective strip removes every built-in tool and /quit is
// refused like any other invalid requested Piglet (the resolution warning,
// then exit 1 before a session). A Piglet that strips /quit alone or every
// tool alone loads and answers.
func TestPigletStripFloorAtStartup(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	tools := strings.Join(pigstrip.Known(pigstrip.ListTools), ", ")
	write := func(name, source string) string {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("chat", "name: chat\nstrip:\n  tools: ["+tools+"]\n")
	floor := "a Piglet can't strip every tool and /quit (strip.tools: " + tools + "; strip.commands: /quit); keep at least one tool or /quit"
	for _, tc := range []struct {
		name, source string
		refused      bool
	}{
		{"every tool and quit", "name: stuck\nextends:\n  source: local:./chat.yaml\nstrip:\n  commands: [/quit]\n", true},
		{"quit alone", "name: noquit\nstrip:\n  commands: [/quit]\n", false},
		{"every tool alone", "name: chatonly\nextends:\n  source: local:./chat.yaml\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := write(strings.ReplaceAll(tc.name, " ", "-"), tc.source)
			home := t.TempDir()
			command := exec.CommandContext(t.Context(), binary, "--piglet", path, "--model", "test-faux/faux-1", "--no-session", "-p", "What is 20+22?")
			command.Dir = t.TempDir()
			command.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_OFFLINE=1")
			var stdout, stderr strings.Builder
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			warning := "warning: piglet " + path + ": "
			if tc.refused {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.Len() != 0 {
					t.Fatalf("err = %v, stdout = %q, want exit 1 with no answer\nstderr:\n%s", err, stdout.String(), stderr.String())
				}
				if !strings.Contains(stderr.String(), warning) || !strings.Contains(stderr.String(), floor) || !strings.Contains(stderr.String(), "pig: the requested Piglet could not be loaded") {
					t.Fatalf("stderr does not refuse the Piglet with the floor rule:\n%s", stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v\nstderr:\n%s", err, stderr.String())
			}
			if strings.TrimSpace(stdout.String()) != "42" || strings.Contains(stderr.String(), warning) {
				t.Fatalf("allowed Piglet: stdout = %q\nstderr:\n%s", stdout.String(), stderr.String())
			}
		})
	}
}
