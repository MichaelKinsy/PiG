// SPDX-License-Identifier: MIT

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func resultText(t *testing.T, result agent.AgentToolResult) string {
	t.Helper()
	var text strings.Builder
	for _, block := range result.Content {
		if content, ok := block.(ai.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	return text.String()
}

// packages/coding-agent/test/agent-session-dynamic-tools.test.ts:30 "exposes session state before custom bash spawn hooks and supports opting out": the spawn hook receives the session variables in its env, and a tool created without session exposure never sees them.
func TestBashSpawnHookSeesSessionEnvironmentAndSupportsOptingOut(t *testing.T) {
	t.Setenv("PI_MODEL", "inherited-model")
	ctx := agent.WithToolEnvironment(t.Context(), agent.ToolEnvironment{SessionID: "bash-env-test", SessionFile: "/sessions/bash-env-test.jsonl", Provider: "anthropic", Model: "claude-sonnet-4-5", ThinkingLevel: "high"})
	params := json.RawMessage(`{"command":"printf ok"}`)
	for _, tc := range []struct {
		name       string
		hideEnv    bool
		wantValues map[string]string
	}{
		{"exposed", false, map[string]string{"PI_SESSION_ID": "bash-env-test", "PI_SESSION_FILE": "/sessions/bash-env-test.jsonl", "PI_PROVIDER": "anthropic", "PI_MODEL": "claude-sonnet-4-5", "PI_REASONING_LEVEL": "high"}},
		{"opted out", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			tool := &BashTool{CWD: t.TempDir(), HideSessionEnvironment: tc.hideEnv, SpawnHook: func(spawn BashSpawnContext) BashSpawnContext {
				seen = slices.Clone(spawn.Env)
				return spawn
			}}
			result, err := tool.Execute(ctx, "bash-env", params, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := resultText(t, result); got != "ok" {
				t.Fatalf("output = %q, want ok", got)
			}
			if seen == nil {
				t.Fatal("the spawn hook did not run")
			}
			for _, name := range sessionVariables {
				got, ok := lookup(seen, name)
				want, wantOK := tc.wantValues[name]
				if ok != wantOK || got != want {
					t.Errorf("%s = %q (present %v), want %q (present %v)", name, got, ok, want, wantOK)
				}
			}
		})
	}
}

// bash.ts:resolveSpawnContext: the hook receives the command with the prefix, the working directory and the environment, and the context it returns is what runs.
func TestBashSpawnHookAdjustsCommandCwdAndEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture command is a POSIX shell script")
	}
	base, other := t.TempDir(), t.TempDir()
	other, err := filepath.EvalSymlinks(other)
	if err != nil {
		t.Fatal(err)
	}
	var received BashSpawnContext
	var hook BashSpawnHook = func(spawn BashSpawnContext) BashSpawnContext {
		received = spawn
		spawn.Command += "\nprintf '%s|%s|%s' \"$PREFIX\" \"$FROM_HOOK\" \"$(pwd -P)\""
		spawn.CWD = other
		spawn.Env = append(slices.Clone(spawn.Env), "FROM_HOOK=hooked")
		return spawn
	}
	tool := &BashTool{CWD: base, CommandPrefix: "PREFIX=prefixed", SpawnHook: hook}
	result, err := tool.Execute(t.Context(), "bash-hook", json.RawMessage(`{"command":"true"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if received.Command != "PREFIX=prefixed\ntrue" || received.CWD != base {
		t.Fatalf("hook received command %q cwd %q, want the prefixed command in %q", received.Command, received.CWD, base)
	}
	if got, want := resultText(t, result), "prefixed|hooked|"+other; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// powershell.ts:24-25: PowerShellSpawnContext and PowerShellSpawnHook are the bash types, and PowerShellToolOptions picks spawnHook from BashToolOptions.
func TestPowerShellSpawnHookUsesTheBashTypes(t *testing.T) {
	spawn := PowerShellSpawnContext(BashSpawnContext{Command: "Get-Location", CWD: os.TempDir()})
	hook := PowerShellSpawnHook(func(context BashSpawnContext) BashSpawnContext { return context })
	tool := &PowerShellTool{CWD: spawn.CWD, SpawnHook: hook}
	if got := tool.shellConfig().spawnHook; got == nil {
		t.Fatal("the PowerShell tool dropped its spawn hook")
	}

}

// Pi core/tools/bash.ts BashSpawnHook: a named hook value is what BashTool.SpawnHook and the PowerShell alias accept.
func TestBashSpawnHookTypeIsSharedByPowerShell(t *testing.T) {
	var hook BashSpawnHook = func(spawn BashSpawnContext) BashSpawnContext { spawn.Command += " # hooked"; return spawn }
	powershell := PowerShellSpawnHook(hook)
	got := powershell(BashSpawnContext{Command: "echo"})
	if got.Command != "echo # hooked" {
		t.Fatalf("hook result %+v", got)
	}
}
