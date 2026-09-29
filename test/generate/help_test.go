package generate_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestHelpGeneratorIsolatesAgentAndPreservesOutputOnFailure(t *testing.T) {
	script, err := os.ReadFile("../../automation/gen/gen-help.sh")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.CommandContext(t.Context(), "node", "-p", "process.execPath").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		repeat int
		fail   bool
	}{
		{name: "isolated success"},
		{name: "complete output before immediate exit", repeat: 40000},
		{name: "failed generation preserves committed output", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fail := tc.fail
			root := t.TempDir()
			put := func(name, body string) {
				t.Helper()
				file := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			put("automation/gen/gen-help.sh", string(script))
			const pkg = "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/"
			put(pkg+"package.json", `{"version":"fixture","piConfig":{"name":"pi","configDir":".pi"}}`)
			cli := `if (process.env.PIG_CODING_AGENT_DIR === process.env.POISON_AGENT_DIR) { throw new Error("inherited live agent directory"); }
if (process.cwd() === process.env.POISON_AGENT_DIR) { throw new Error("inherited project directory"); }
if (process.env.FORCE_COLOR !== "0") { throw new Error("inherited color setting"); }
console.log("pig fixture help");
`
			// Pi's main.ts prints help and immediately calls process.exit(0). A pipe can lose queued output even though the process succeeds.
			if tc.repeat > 0 {
				cli += fmt.Sprintf("process.stdout.write('help line\\n'.repeat(%d)); process.exit(0);\n", tc.repeat)
			}
			if fail {
				cli = "process.exit(42);\n"
			}
			put(pkg+"dist/cli.js", cli)
			put("cmd/pig/help_upstream.txt", "previous help\n")
			// On Windows a bare "bash" can resolve to the WSL launcher in System32; testenv.Bash is Git for Windows bash.
			cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/gen/gen-help.sh"))
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(strings.TrimSpace(string(node)))+string(os.PathListSeparator)+os.Getenv("PATH"), "PIG_CODING_AGENT_DIR="+root, "PI_CODING_AGENT_DIR="+root, "POISON_AGENT_DIR="+root, "FORCE_COLOR=1")
			output, runErr := cmd.CombinedOutput()
			if fail && runErr == nil {
				t.Fatal("failed generator returned success")
			}
			if !fail && runErr != nil {
				t.Fatalf("generate: %v\n%s", runErr, output)
			}
			want := "pig fixture help\n" + strings.Repeat("help line\n", tc.repeat)
			if fail {
				want = "previous help\n"
			}
			got, err := os.ReadFile(filepath.Join(root, "cmd/pig/help_upstream.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("help length = %d, want %d; complete output differs", len(got), len(want))
			}
		})
	}
}
