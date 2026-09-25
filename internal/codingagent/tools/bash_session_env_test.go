// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func lookup(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			return v, true
		}
	}
	return "", false
}

func TestSessionEnvironmentMirrorsUpstream(t *testing.T) {
	t.Setenv("PI_MODEL", "parent-model")
	t.Setenv("PI_SESSION_FILE", "/parent/session.jsonl")
	t.Setenv("KEEP_ME", "1")
	ctx := agent.WithToolEnvironment(context.Background(), agent.ToolEnvironment{SessionID: "s1", Provider: "anthropic", Model: "claude-x", ThinkingLevel: "high"})

	env := sessionEnvironment(ctx, true, "")
	for name, want := range map[string]string{"PI_SESSION_ID": "s1", "PI_PROVIDER": "anthropic", "PI_MODEL": "claude-x", "PI_REASONING_LEVEL": "high", "KEEP_ME": "1"} {
		if got, _ := lookup(env, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if _, ok := lookup(env, "PI_SESSION_FILE"); ok {
		t.Error("an inherited PI_SESSION_FILE must be removed when the session has no file")
	}

	hidden := sessionEnvironment(ctx, false, "")
	for _, name := range sessionVariables {
		if _, ok := lookup(hidden, name); ok {
			t.Errorf("%s exported with exposure off", name)
		}
	}
	if _, ok := lookup(sessionEnvironment(context.Background(), true, ""), "PI_MODEL"); ok {
		t.Error("without a tool environment, inherited PI_MODEL must still be removed")
	}
}

func TestBashGuidelineFollowsExposure(t *testing.T) {
	if !slices.Contains((&BashTool{}).Schema().PromptGuidelines, sessionGuideline) {
		t.Error("the default bash tool must carry upstream's PI_* guideline")
	}
	if slices.Contains((&BashTool{HideSessionEnvironment: true}).Schema().PromptGuidelines, sessionGuideline) {
		t.Error("with the session environment hidden, the guideline must be absent")
	}
}

// Upstream getShellEnv prepends <agentDir>/bin to PATH once.
func TestGetShellEnvPrependsBinDir(t *testing.T) {
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", "/usr/bin"+sep+"/bin")
	if got, _ := lookup(GetShellEnv("/agent/bin"), "PATH"); got != "/agent/bin"+sep+"/usr/bin"+sep+"/bin" {
		t.Fatalf("PATH = %q", got)
	}
	t.Setenv("PATH", "/usr/bin"+sep+"/agent/bin")
	if got, _ := lookup(GetShellEnv("/agent/bin"), "PATH"); got != "/usr/bin"+sep+"/agent/bin" {
		t.Fatalf("PATH already listing the bin dir changed: %q", got)
	}
	t.Setenv("PATH", "")
	if got, _ := lookup(GetShellEnv("/agent/bin"), "PATH"); got != "/agent/bin" {
		t.Fatalf("empty PATH: %q", got)
	}
}

// The bash tool and user bash both run with the managed bin dir on PATH, so a
// tools-manager rg or fd is callable from commands.
func TestBashPathStartsWithAgentBinDir(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	var bash agent.AgentTool
	for _, tool := range CreateCodingTools(t.TempDir(), nil, binDir) {
		if tool.Name() == "bash" {
			bash = tool
		}
	}
	args, _ := json.Marshal(bashParams{Command: `printf %s "$PATH"`})
	res, err := bash.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("execute: %v %+v", err, res)
	}
	assertBinDirLeadsInheritedPath(t, "tool", binDir, res.Content)
	sh, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	ub, err := ExecuteBash(context.Background(), `printf %s "$PATH"`, t.TempDir(), sh, BashExecOptions{BinDir: binDir})
	if err != nil {
		t.Fatalf("user bash: %v", err)
	}
	assertBinDirLeadsInheritedPath(t, "user bash", binDir, ub.Output)
}

// assertBinDirLeadsInheritedPath checks the PATH a bash command printed.
// Upstream getShellEnv hands bash binDir followed by the inherited PATH. On
// Windows, Git for Windows' bash.exe launcher then puts its own entries first
// and rewrites the rest in MSYS form (C:\x becomes /c/x), so there the bin
// dir must precede the first inherited entry.
func assertBinDirLeadsInheritedPath(t *testing.T, label, binDir, printed string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if !strings.HasPrefix(printed, binDir+string(os.PathListSeparator)) {
			t.Fatalf("%s PATH = %q, want it to start with %q", label, printed, binDir)
		}
		return
	}
	inherited := ""
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry != "" {
			inherited = entry
			break
		}
	}
	sh, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(sh.Path, "-c", `cygpath -u "$1"; cygpath -u "$2"`, "cygpath", binDir, inherited).Output()
	if err != nil {
		t.Fatalf("cygpath: %v", err)
	}
	msys := strings.Fields(string(out))
	if len(msys) != 2 {
		t.Fatalf("cygpath output %q", out)
	}
	entries := strings.Split(printed, ":")
	bin, first := slices.Index(entries, msys[0]), slices.Index(entries, msys[1])
	if bin < 0 || first >= 0 && first < bin {
		t.Fatalf("%s PATH = %q, want %q before the inherited %q", label, printed, msys[0], msys[1])
	}
}
