package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

// /sprite create asks the model to write the sprite extension into the global extensions directory of the agent directory
// the CLI uses (AgentDir), so PIG_HOME, XDG_CONFIG_HOME, PIG_CODING_AGENT_DIR and shared-mode runs load it on /reload
// instead of the model writing it under ~/.pig/agent.
func TestSpriteCreatePromptNamesTheRunsExtensionsDirectory(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"default", nil},
		{"PIG_HOME", map[string]string{"PIG_HOME": filepath.Join(home, "pighome")}},
		{"PIG_HOME tilde", map[string]string{"PIG_HOME": "~/tilde-home"}},
		{"XDG_CONFIG_HOME", map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, "xdg")}},
		{"PIG_HOME over XDG_CONFIG_HOME", map[string]string{"PIG_HOME": filepath.Join(home, "pighome"), "XDG_CONFIG_HOME": filepath.Join(home, "xdg")}},
		{"PIG_CODING_AGENT_DIR", map[string]string{"PIG_CODING_AGENT_DIR": "~/custom-agent", "PIG_HOME": filepath.Join(home, "pighome")}},
		{"shared mode", map[string]string{"PIG_USE_PI_DIRS": "1", "PIG_CODING_AGENT_DIR": filepath.Join(home, "ignored")}},
		{"shared mode PI_CODING_AGENT_DIR", map[string]string{"PIG_USE_PI_DIRS": "1", "PI_CODING_AGENT_DIR": filepath.Join(home, "pi-agent")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			for _, name := range []string{"PIG_HOME", "XDG_CONFIG_HOME", "PIG_CODING_AGENT_DIR", "PIG_USE_PI_DIRS", "PI_CODING_AGENT_DIR"} {
				t.Setenv(name, tc.env[name])
			}
			want := "`" + filepath.Join(AgentDir(), "extensions", "<id>.ts") + "`"
			if prompt := piglogin.CreatePrompt(); !strings.Contains(prompt, want) {
				t.Fatalf("prompt does not name %s:\n%s", want, prompt)
			}
		})
	}
}
