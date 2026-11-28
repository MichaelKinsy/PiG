package cli

// pi: packages/coding-agent/src/cli/agent-dir-arg.ts
// Ports packages/coding-agent/test/agent-dir-arg.test.ts (Pi dd71a6fc7)
// and packages/coding-agent/test/args.test.ts resumeFlags (Pi 145785f0d).

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestApplyAgentDirArg(t *testing.T) {
	envName := codingagent.AgentDirEnvName()

	t.Run("unchanged without --agent-dir", func(t *testing.T) {
		t.Setenv(envName, "")
		_ = os.Unsetenv(envName)
		rest, dir, errMsg := ApplyAgentDirArg([]string{"--session", "abc"})
		if errMsg != "" || dir != "" || !reflect.DeepEqual(rest, []string{"--session", "abc"}) {
			t.Fatalf("got (%v, %q, %q)", rest, dir, errMsg)
		}
		if _, ok := os.LookupEnv(envName); ok {
			t.Fatalf("%s should remain unset", envName)
		}
	})

	t.Run("takes flag out in any position", func(t *testing.T) {
		t.Setenv(envName, "")
		rest, dir, errMsg := ApplyAgentDirArg([]string{"--agent-dir", "/tmp/agent", "install", "npm:foo"})
		if errMsg != "" || dir != "/tmp/agent" || !reflect.DeepEqual(rest, []string{"install", "npm:foo"}) {
			t.Fatalf("got (%v, %q, %q)", rest, dir, errMsg)
		}
		rest, dir, errMsg = ApplyAgentDirArg([]string{"--session", "abc", "--agent-dir", "/tmp/agent"})
		if errMsg != "" || dir != "/tmp/agent" || !reflect.DeepEqual(rest, []string{"--session", "abc"}) {
			t.Fatalf("got (%v, %q, %q)", rest, dir, errMsg)
		}
	})

	t.Run("last --agent-dir wins", func(t *testing.T) {
		t.Setenv(envName, "")
		ApplyAgentDirArg([]string{"--agent-dir", "/a", "--agent-dir", "/b"})
		want, _ := filepath.Abs(codingagent.ExpandTildePath("/b"))
		if got := os.Getenv(envName); got != want {
			t.Fatalf("env = %q, want %q", got, want)
		}
	})

	t.Run("sets absolute path and expands tilde", func(t *testing.T) {
		t.Setenv(envName, "")
		ApplyAgentDirArg([]string{"--agent-dir", "relative/agent"})
		wantRel, _ := filepath.Abs("relative/agent")
		if got := os.Getenv(envName); got != wantRel {
			t.Fatalf("relative env = %q, want %q", got, wantRel)
		}

		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			ApplyAgentDirArg([]string{"--agent-dir", "~/custom-agent"})
			wantHome := filepath.Join(home, "custom-agent")
			if got := os.Getenv(envName); got != wantHome {
				t.Fatalf("tilde env = %q, want %q", got, wantHome)
			}
		}
	})

	t.Run("leaves arguments after -- as messages", func(t *testing.T) {
		t.Setenv(envName, "")
		_ = os.Unsetenv(envName)
		rest, dir, errMsg := ApplyAgentDirArg([]string{"-p", "--", "--agent-dir", "/a"})
		if errMsg != "" || dir != "" || !reflect.DeepEqual(rest, []string{"-p", "--", "--agent-dir", "/a"}) {
			t.Fatalf("got (%v, %q, %q)", rest, dir, errMsg)
		}
		if _, ok := os.LookupEnv(envName); ok {
			t.Fatalf("%s should remain unset", envName)
		}
	})

	t.Run("reports missing directory", func(t *testing.T) {
		_, _, errMsg := ApplyAgentDirArg([]string{"--agent-dir"})
		if errMsg != "--agent-dir requires a directory" {
			t.Fatalf("errMsg = %q, want --agent-dir requires a directory", errMsg)
		}
	})
}

func TestParseArgsResumeFlags(t *testing.T) {
	t.Run("records restriction and resource flags as typed in order", func(t *testing.T) {
		in := []string{
			"-t", "read,grep",
			"--no-extensions",
			"-e", "./ext.ts",
			"--extension", "/abs/other.ts",
			"--skill", "./skills",
			"--append-system-prompt", "Be terse",
			"-na",
		}
		got := parseArgs(in).ResumeFlags
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("ResumeFlags = %v, want %v", got, in)
		}
	})

	t.Run("leaves out session, picker, secret, cosmetic, and message args", func(t *testing.T) {
		got := parseArgs([]string{
			"--model", "sonnet",
			"--thinking", "high",
			"--api-key", "secret",
			"--session", "abc",
			"-c",
			"--offline",
			"--tools", "read",
			"@notes.md",
			"hello",
			"--",
			"--no-tools",
		}).ResumeFlags
		want := []string{"--tools", "read"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ResumeFlags = %v, want %v", got, want)
		}
	})
}
