//go:build windows

package crossspawn

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// directoryHelper makes the test binary report its working directory.
const directoryHelper = "PIG_CROSSSPAWN_DIRECTORY_HELPER"

// directoryOutcome is an emitted spawn error or the working directory the
// started receiver reports.
type directoryOutcome struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cwd     string `json:"cwd"`
}

// cross-spawn hands the resolved program to Node's spawn with the cwd
// option, so libuv's rule for a cwd of MAX_PATH or more UTF-16 units applies:
// in Pi's process a long path without the \\?\ prefix makes spawn emit
// ENOENT, and a prefixed one runs the child in its short form. The pinned
// cross-spawn is the oracle; the test binary is the receiver.
func TestWindowsCommandUsesLibuvsWorkingDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	receiver := filepath.Join(root, "bin", "receiver.exe")
	writeExecutable(t, receiver, string(data))
	deep := root
	for len(deep) < 300 {
		deep = filepath.Join(deep, "directory-with-a-long-name")
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := []string{deep, `\\?\` + deep, root}
	env := append(os.Environ(), directoryHelper+"=1")
	childEnv := map[string]string{}
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok && name != "" {
			childEnv[name] = value
		}
	}
	input, err := json.Marshal(map[string]any{
		"crossSpawn": filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "cross-spawn"),
		"receiver":   receiver,
		"env":        childEnv,
		"dirs":       dirs,
	})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const path = require("node:path");
const input = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const crossSpawn = require(path.resolve(input.crossSpawn));
const outcome = (cwd) => new Promise((resolve) => {
	const child = crossSpawn(input.receiver, [], { cwd, env: input.env, stdio: ["ignore", "pipe", "ignore"] });
	let stdout = "";
	child.stdout.on("error", () => {});
	child.stdout.setEncoding("utf8");
	child.stdout.on("data", (chunk) => { stdout += chunk; });
	child.on("error", (error) => resolve({ code: error.code, message: error.message }));
	child.on("close", (status) => resolve(status === 0 ? { cwd: JSON.parse(stdout) } : { code: "status " + status, message: stdout }));
});
(async () => {
	const outcomes = [];
	for (const cwd of input.dirs) outcomes.push(await outcome(cwd));
	process.stdout.write(JSON.stringify(outcomes));
})();
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node: %v; output %s", err, out)
	}
	var want []directoryOutcome
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(dirs) {
		t.Fatalf("decode node outcomes %s: %v", out, err)
	}
	if want[0].Code != "ENOENT" || !strings.HasPrefix(want[1].Cwd, `\\?\`) || len(want[1].Cwd) >= len(dirs[1]) {
		t.Fatalf("cross-spawn outcomes %+v: want ENOENT for the long cwd and a shortened prefixed cwd", want)
	}
	for i, dir := range dirs {
		cmd := Command(t.Context(), dir, receiver)
		cmd.Env = env
		var got directoryOutcome
		output, err := cmd.Output()
		var spawnErr *nodespawn.Error
		switch {
		case errors.As(err, &spawnErr):
			got = directoryOutcome{Code: spawnErr.Code, Message: spawnErr.Error()}
		case err != nil:
			got = directoryOutcome{Code: "go", Message: err.Error()}
		default:
			if err := json.Unmarshal(output, &got.Cwd); err != nil {
				t.Fatalf("dir %d: decode %q: %v", i, output, err)
			}
		}
		if got != want[i] {
			t.Errorf("dir %d (%d units)\n got %+v\nwant %+v", i, len(dir), got, want[i])
		}
	}
}
