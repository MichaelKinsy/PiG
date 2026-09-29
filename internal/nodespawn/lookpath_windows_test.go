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
)

const imageHelper = "PIG_NODESPAWN_IMAGE_HELPER"

// spawnOutcome is what Node's spawn(file, [], { shell: false }) does: throw,
// emit an error, or start a program that reports its image path.
type spawnOutcome struct {
	Thrown  bool   `json:"thrown"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Image   string `json:"image"`
}

func copyFile(t *testing.T, source, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Node's child_process.spawn(file, [], { cwd, env, shell: false }) is the
// oracle. Copies of the test binary report the image they run from, so the
// comparison names the exact file libuv chose: .com before .exe, cwd before
// PATH unless NoDefaultCurrentDirectoryInExePath is set, quoted PATH entries,
// and no PATHEXT, so npm finds no npm.cmd. Batch file names throw EINVAL
// before the search; a name that matches nothing emits ENOENT.
func TestLookPathMatchesNodeSpawn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	quoted := filepath.Join(root, "path;semi")
	plain := filepath.Join(root, "plain")
	for _, program := range []string{
		filepath.Join(cwd, "tool.exe"), filepath.Join(cwd, "both.com"), filepath.Join(cwd, "both.exe"),
		filepath.Join(cwd, "my.tool"), filepath.Join(cwd, "sub", "rel.exe"), filepath.Join(cwd, "shadow.exe"),
		filepath.Join(quoted, "semi.exe"), filepath.Join(plain, "onpath.exe"), filepath.Join(plain, "shadow.exe"),
		filepath.Join(plain, "dir.com"),
	} {
		copyFile(t, exe, program)
	}
	for _, script := range []string{filepath.Join(cwd, "npm.cmd"), filepath.Join(cwd, "script.cmd"), filepath.Join(plain, "npm.cmd"), filepath.Join(plain, "only.bat")} {
		writeFile(t, script, "@echo batch\r\n")
	}
	if err := os.MkdirAll(filepath.Join(cwd, "dir.exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + `"` + quoted + `";` + plain + ";", "SystemRoot=" + os.Getenv("SystemRoot"), imageHelper + "=1"}
	files := []string{
		"npm", "npm.cmd", "script.cmd", "script.CMD ", "only", "only.bat", "tool", "TOOL", "tool.exe", "tool.", "both",
		"my.tool", `sub\rel`, "sub/rel", `.\tool`, "onpath", "semi", "shadow", "dir", "missing",
		filepath.Join(cwd, "tool"), filepath.Join(cwd, "tool.exe"), filepath.Join(cwd, "both"),
		"x.cmd\nfoo", "x.exe\n.cmd\nz",
	}
	for _, searchCwd := range []bool{true, false} {
		t.Run(map[bool]string{true: "cwd searched", false: "NoDefaultCurrentDirectoryInExePath"}[searchCwd], func(t *testing.T) {
			t.Setenv("NoDefaultCurrentDirectoryInExePath", "1")
			if searchCwd {
				if err := os.Unsetenv("NoDefaultCurrentDirectoryInExePath"); err != nil {
					t.Fatal(err)
				}
			}
			want := nodeSpawnOutcomes(t, cwd, env, files)
			for i, file := range files {
				program, err := LookPath(file, cwd, env)
				var got spawnOutcome
				var spawnErr *Error
				switch {
				case errors.As(err, &spawnErr):
					got = spawnOutcome{Thrown: spawnErr.Thrown, Code: spawnErr.Code, Message: spawnErr.Error()}
				case err != nil:
					t.Fatalf("LookPath(%q): %v", file, err)
				default:
					got = spawnOutcome{Image: program}
				}
				if want[i].Image != "" && got.Image != "" {
					if !sameFile(t, got.Image, want[i].Image) {
						t.Errorf("LookPath(%q) = %q, Node ran %q", file, got.Image, want[i].Image)
					}
					continue
				}
				if got != want[i] {
					t.Errorf("LookPath(%q) = %+v, want Node's %+v", file, got, want[i])
				}
			}
		})
	}
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	aInfo, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(aInfo, bInfo)
}

func nodeSpawnOutcomes(t *testing.T, cwd string, env, files []string) []spawnOutcome {
	t.Helper()
	childEnv := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		childEnv[name] = value
	}
	input, err := json.Marshal(map[string]any{"cwd": cwd, "env": childEnv, "files": files})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawn } = require("node:child_process");
const { cwd, env, files } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const outcome = (file) => new Promise((resolve) => {
	let child;
	try {
		child = spawn(file, [], { cwd, env, shell: false, stdio: ["ignore", "pipe", "ignore"] });
	} catch (error) {
		resolve({ thrown: true, code: error.code, message: error.message, image: "" });
		return;
	}
	let stdout = "";
	child.stdout.setEncoding("utf8");
	child.stdout.on("data", (chunk) => { stdout += chunk; });
	child.on("error", (error) => resolve({ thrown: false, code: error.code, message: error.message, image: "" }));
	child.on("close", (status) => resolve(status === 0 ? { thrown: false, code: "", message: "", image: JSON.parse(stdout) } : { thrown: false, code: "status " + status, message: stdout, image: "" }));
});
(async () => {
	const outcomes = [];
	for (const file of files) outcomes.push(await outcome(file));
	process.stdout.write(JSON.stringify(outcomes));
})();
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var outcomes []spawnOutcome
	if err := json.Unmarshal(out, &outcomes); err != nil || len(outcomes) != len(files) {
		t.Fatalf("decode node outcomes %q: %v", out, err)
	}
	return outcomes
}
