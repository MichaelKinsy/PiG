//go:build windows

package nodespawn

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

const directoryHelper = "PIG_NODESPAWN_DIRECTORY_HELPER"

// directoryReport is what a started test binary reports: its working
// directory and its image.
type directoryReport struct {
	Cwd   string `json:"cwd"`
	Image string `json:"image"`
}

// reportDirectory writes the process's directoryReport to stdout and exits.
func reportDirectory() {
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetCurrentDirectory(uint32(len(buffer)), &buffer[0])
	image, imageErr := os.Executable()
	if err != nil || imageErr != nil || json.NewEncoder(os.Stdout).Encode(directoryReport{Cwd: windows.UTF16ToString(buffer[:n]), Image: image}) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// cwdOutcome is what spawn(file, [], { cwd }) does: throw, emit an error, or
// start a program that reports its working directory and image.
type cwdOutcome struct {
	Thrown  bool            `json:"thrown"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Report  directoryReport `json:"report"`
}

// directoryOfLength creates a directory under base whose path has length
// ASCII characters.
func directoryOfLength(t *testing.T, base string, length int) string {
	t.Helper()
	path := base
	for len(path)+len(`\directory-with-a-long-name`)+2 < length {
		path = filepath.Join(path, "directory-with-a-long-name")
	}
	path = filepath.Join(path, strings.Repeat("x", length-len(path)-1))
	if len(path) != length {
		t.Fatalf("directory %q has length %d, want %d", path, len(path), length)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// libuv's uv_spawn shortens a cwd of MAX_PATH (260) or more UTF-16 units with
// GetShortPathNameW and uses the result for the program search and as the
// child's cwd (src/win/process.c). node.exe does not declare longPathAware
// (src/res/node.exe.extra.manifest, Node 24.19.0), so in Pi's process the call fails for a
// long path without the \\?\ prefix and spawn emits ENOENT, while a prefixed
// path is shortened and the child runs in the short, prefixed directory.
// Node's spawn is the oracle; copies of the test binary report the directory
// and image they run in. CreateProcessW itself rejects a 259-unit working
// directory and the prefixed short path of a program found in a prefixed
// cwd, for libuv and os/exec alike; for those two cases only, os/exec's
// failure is reported as libuv reports CreateProcessW's.
func TestSetProgramUsesLibuvsWorkingDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	deep := directoryOfLength(t, base, 300)
	copyFile(t, exe, filepath.Join(deep, "tool.exe"))
	at260 := directoryOfLength(t, base, 260)
	at259 := directoryOfLength(t, base, 259)
	prefixed := `\\?\` + deep
	type cwdCase struct {
		Cwd         string `json:"cwd"`
		File        string `json:"file"`
		createFails bool
	}
	cases := []cwdCase{
		{Cwd: deep, File: exe},
		{Cwd: deep, File: "tool"},
		{Cwd: prefixed, File: exe},
		{Cwd: prefixed, File: "tool", createFails: true},
		{Cwd: prefixed + `\missing`, File: exe},
		{Cwd: at260, File: exe},
		{Cwd: at259, File: exe, createFails: true},
		{Cwd: base, File: exe},
	}
	env := append(os.Environ(), directoryHelper+"=1")
	childEnv := map[string]string{}
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok && name != "" {
			childEnv[name] = value
		}
	}
	input, err := json.Marshal(map[string]any{"env": childEnv, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawn } = require("node:child_process");
const { env, cases } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const outcome = ({ cwd, file }) => new Promise((resolve) => {
	let child;
	try {
		child = spawn(file, [], { cwd, env, stdio: ["ignore", "pipe", "ignore"] });
	} catch (error) {
		resolve({ thrown: true, code: error.code, message: error.message });
		return;
	}
	let stdout = "";
	child.stdout.on("error", () => {});
	child.stdout.setEncoding("utf8");
	child.stdout.on("data", (chunk) => { stdout += chunk; });
	child.on("error", (error) => resolve({ thrown: false, code: error.code, message: error.message }));
	child.on("close", (status) => resolve(status === 0 ? { report: JSON.parse(stdout) } : { code: "status " + status, message: stdout }));
});
(async () => {
	const outcomes = [];
	for (const c of cases) outcomes.push(await outcome(c));
	process.stdout.write(JSON.stringify(outcomes));
})();
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want []cwdOutcome
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(cases) {
		t.Fatalf("decode node outcomes %q: %v", out, err)
	}
	if want[0].Code != "ENOENT" || !strings.HasPrefix(want[2].Report.Cwd, `\\?\`) || len(want[2].Report.Cwd) >= len(prefixed) {
		t.Fatalf("Node outcomes %+v: want ENOENT for the long cwd and a shortened prefixed cwd", want)
	}
	for i, c := range cases {
		cmd := exec.Command(c.File)
		cmd.Dir = c.Cwd
		cmd.Env = env
		SetProgram(cmd)
		SetCommandLine(cmd)
		var got cwdOutcome
		output, err := cmd.Output()
		var spawnErr *Error
		if c.createFails && err != nil && !errors.As(err, &spawnErr) {
			err = spawnFailure(c.File, err)
		}
		switch {
		case errors.As(err, &spawnErr):
			got = cwdOutcome{Thrown: spawnErr.Thrown, Code: spawnErr.Code, Message: spawnErr.Error()}
		case err != nil:
			got = cwdOutcome{Code: "go", Message: err.Error()}
		default:
			if err := json.Unmarshal(output, &got.Report); err != nil {
				t.Fatalf("case %d: decode %q: %v", i, output, err)
			}
		}
		if got != want[i] {
			t.Errorf("case %d (cwd %d units, file %q)\n got %+v\nwant %+v", i, len(c.Cwd), filepath.Base(c.File), got, want[i])
		}
	}
}
