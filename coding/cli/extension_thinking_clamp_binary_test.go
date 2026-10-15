package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const thinkingClampExtension = `export default function (pi) {
  pi.registerCommand("probe", {
    handler: async (args) => {
      pi.setThinkingLevel(args);
      process.stderr.write("thinking=" + pi.getThinkingLevel() + "\n");
    },
  });
}
`

// In Pi 1.1.0 an extension's pi.setThinkingLevel takes effect as agent-session.ts setThinkingLevel clamps it to the model (models.ts clampThinkingLevel), so getThinkingLevel right after it reads the clamped level. The Node runtime stored the requested level.
func TestExtensionThinkingLevelIsClampedAsPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	extPath := filepath.Join(t.TempDir(), "probe.mjs")
	if err := os.WriteFile(extPath, []byte(thinkingClampExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model, level, want string
		env                []string
	}{
		{model: "test-faux/faux-1", level: "high", want: "off", env: []string{"PIG_TEST_FAUX=1"}},
		{model: "test-faux/faux-1", level: "bogus", want: "off", env: []string{"PIG_TEST_FAUX=1"}},
		{model: "openai/gpt-5", level: "max", want: "high", env: []string{"OPENAI_API_KEY=test"}},
		{model: "openai/gpt-5.1", level: "minimal", want: "low", env: []string{"OPENAI_API_KEY=test"}}, // Pi 1.2.0+b06c1f2c dropped openai/gpt-5.1-codex-max; gpt-5.1 also has no minimal
		{model: "openai/gpt-5", level: "minimal", want: "minimal", env: []string{"OPENAI_API_KEY=test"}},
	} {
		t.Run(tc.model+" "+tc.level, func(t *testing.T) {
			agentDir := t.TempDir()
			cmd := exec.Command(bin, "-p", "--no-session", "--no-extensions", "--offline", "-e", extPath, "--model", tc.model, "/probe "+tc.level)
			cmd.Dir = agentDir
			cmd.Env = append(append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir), tc.env...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := testenv.Run(t, cmd); err != nil {
				t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
			}
			want := "thinking=" + tc.want + "\n"
			if got := stderr.String(); !strings.Contains(got, want) {
				t.Fatalf("stderr = %q, want it to contain %q", got, want)
			}
		})
	}
}
