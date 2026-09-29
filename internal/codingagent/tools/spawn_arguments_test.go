package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// spawnCall is one child_process.spawn(file, args) call.
type spawnCall struct {
	File string   `json:"file"`
	Args []string `json:"args"`
}

// nodeSpawnErrors returns the message each call makes Node's spawn throw, or
// "" when it does not throw.
func nodeSpawnErrors(t *testing.T, calls []spawnCall) []string {
	t.Helper()
	input, err := json.Marshal(calls)
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawn } = require("node:child_process");
const calls = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
process.stdout.write(JSON.stringify(calls.map(({ file, args }) => {
	try {
		spawn(file, args, { stdio: "ignore" }).on("error", () => {}).kill();
		return "";
	} catch (error) {
		return error.message;
	}
})));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var messages []string
	if err := json.Unmarshal(out, &messages); err != nil || len(messages) != len(calls) {
		t.Fatalf("decode %s: %v", out, err)
	}
	for i, message := range messages {
		if message == "" {
			t.Fatalf("Node's spawn accepted %+q", calls[i])
		}
	}
	return messages
}

func toolResultText(t *testing.T, content []ai.ToolResultMessageContent) string {
	t.Helper()
	if len(content) != 1 {
		t.Fatalf("content = %+v", content)
	}
	text, ok := content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("content = %+v", content)
	}
	return text.Text
}

// Pi's grep and find tools call spawn inside a try block whose catch rejects
// with the thrown error unchanged (grep.ts:168,311; find.ts:216,300); only the
// child's 'error' event adds "Failed to run ripgrep: " or "Failed to run fd: ".
// A NUL in the pattern makes Node's normalizeSpawnArguments throw
// ERR_INVALID_ARG_VALUE for that argument. Pi's bash tool rejects its exec with
// the same error (bash.ts:96). Node's spawn with the arguments Pi builds is the
// oracle.
func TestSearchAndShellToolsReportSpawnArgumentErrorsAsPiDoes(t *testing.T) {
	cwd := t.TempDir()
	pattern := "it's a\x00b"
	limit := findDefaultLimit
	// grep.ts:163-167 and find.ts:181-213 argument lists for a plain pattern.
	grepArgs := []string{"--json", "--line-number", "--color=never", "--hidden", "--", pattern, cwd}
	findArgs := []string{"--glob", "--color=never", "--hidden"}
	if !insideGitRepo(cwd) {
		findArgs = append(findArgs, "--no-require-git")
	}
	findArgs = append(findArgs, "--max-results", strconv.Itoa(limit), "--", pattern, cwd)
	want := nodeSpawnErrors(t, []spawnCall{
		{File: "rg", Args: grepArgs},
		{File: "fd", Args: findArgs},
		{File: "bash", Args: []string{"-c", pattern}},
	})
	params, err := json.Marshal(map[string]any{"pattern": pattern})
	if err != nil {
		t.Fatal(err)
	}

	grep, err := (&GrepTool{CWD: cwd, RgPath: "rg"}).Execute(context.Background(), "", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := toolResultText(t, grep.Content); !grep.IsError || got != want[0] {
		t.Errorf("grep = %q (error %v), want Pi's %q", got, grep.IsError, want[0])
	}

	find, err := (&FindTool{CWD: cwd, FdPath: "fd"}).Execute(context.Background(), "", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := toolResultText(t, find.Content); !find.IsError || got != want[1] {
		t.Errorf("find = %q (error %v), want Pi's %q", got, find.IsError, want[1])
	}

	shell := &LocalShellOperations{ShellName: "bash", ResolveShell: func() (ShellConfig, error) {
		return ShellConfig{Path: "bash", Args: []string{"-c"}}, nil
	}}
	if _, err := shell.Exec(context.Background(), pattern, cwd, BashOperationsExecOptions{}); err == nil || err.Error() != want[2] {
		t.Errorf("bash exec error = %v, want Pi's %q", err, want[2])
	}
}
