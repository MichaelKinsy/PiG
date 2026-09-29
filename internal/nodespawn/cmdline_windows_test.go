//go:build windows

package nodespawn

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const commandLineHelper = "PIG_NODESPAWN_COMMAND_LINE_HELPER"

// TestMain lets the test binary report the command line CreateProcessW gave
// it, before any C runtime or MSYS2 parsing, how it was started, the image
// file it runs from, its working directory and image, or its environment.
func TestMain(m *testing.M) {
	if os.Getenv(commandLineHelper) == "1" {
		if err := json.NewEncoder(os.Stdout).Encode(windows.UTF16PtrToString(syscall.GetCommandLine())); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	testenv.ReportStartupIfRequested()
	if os.Getenv(imageHelper) == "1" {
		image, err := os.Executable()
		if err != nil || json.NewEncoder(os.Stdout).Encode(image) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv(directoryHelper) == "1" {
		reportDirectory()
	}
	testenv.ReportEnvironIfRequested()
	os.Exit(m.Run())
}

// nodeSpawnArguments are arguments whose command-line bytes depend on each
// quote_cmd_arg branch, including libuv's documented cases.
var nodeSpawnArguments = []string{
	"", "plain", "a b", "a\tb", "a\nb", `a"b`, `"`, `""`, `\`, `\\`, `\"`,
	`hello"world`, `hello""world`, `hello\world`, `hello\\world`, `hello\"world`,
	`hello\\"world`, `hello world\`, `trailing\`, `trailing space\ `, `a b\c`,
	`a\b c\\`, `printf '%s' "$x" ''`, `héllo "wörld"\`, "😀\"x",
}

func receivedCommandLine(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	cmd.Env = append(os.Environ(), commandLineHelper+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %v; output %s", cmd.Args, err, out)
	}
	var line string
	if err := json.Unmarshal(out, &line); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return line
}

// Node's child_process.spawn(file, args) without shell or
// windowsVerbatimArguments is the oracle: the child reports the exact
// command line, so the comparison covers every byte, argv[0] included.
func TestSetCommandLineMatchesNodeSpawn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"file": exe, "args": nodeSpawnArguments, "helper": commandLineHelper})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const { file, args, helper } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const result = spawnSync(file, args, { env: { ...process.env, [helper]: "1" }, encoding: "utf8", windowsHide: true });
if (result.error || result.status !== 0) {
	process.stderr.write(String(result.error ?? result.stderr));
	process.exit(1);
}
process.stdout.write(result.stdout);
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode node result %q: %v", out, err)
	}

	cmd := exec.CommandContext(t.Context(), exe, nodeSpawnArguments...)
	SetCommandLine(cmd)
	if got := receivedCommandLine(t, cmd); got != want {
		t.Fatalf("SetCommandLine command line\n got %q\nwant %q", got, want)
	}
	if got := CommandLine(append([]string{exe}, nodeSpawnArguments...)); got != want {
		t.Fatalf("CommandLine\n got %q\nwant %q", got, want)
	}
}

// SetCommandLine keeps the caller's other process attributes.
func TestSetCommandLineKeepsProcessAttributes(t *testing.T) {
	cmd := exec.Command("bash.exe", "-c", `a"b`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	SetCommandLine(cmd)
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CmdLine != `bash.exe -c "a\"b"` {
		t.Fatalf("SysProcAttr = %+v", cmd.SysProcAttr)
	}
}
