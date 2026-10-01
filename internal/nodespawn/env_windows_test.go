//go:build windows

package nodespawn

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Node's normalizeSpawnArguments keeps the first, in UTF-16 order, of the env
// names whose String.prototype.toUpperCase values are equal
// (lib/child_process.js). toUpperCase applies Unicode's full case mapping, so
// "ß" uppercases to "SS", "ﬁ" to "FI", and "ŉ" to "ʼN": PIG_SS drops PIG_ß,
// PIG_FI drops PIG_ﬁ, and PIG_ŉ drops PIG_ʼN, which sorts after it. Node's
// spawn with that env object is the oracle; the test binary reports the
// environment it received. Both environments are compared sorted, since
// os/exec orders the block of non-ASCII names differently from libuv.
func TestSetEnvDropsNamesThatUppercaseAlikeAsNodeDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		testenv.EnvironHelper + "=1",
		"PIG_ß=sharp", "PIG_SS=double",
		"PIG_ﬁ=ligature", "PIG_FI=letters",
		"PIG_ʼN=apostrophe", "PIG_ŉ=preceded",
		"PIG_Ω=omega", "PIG_ω=small-omega",
	}
	object := map[string]string{}
	for _, pair := range env {
		name, value, _ := strings.Cut(pair, "=")
		object[name] = value
	}
	input, err := json.Marshal(map[string]any{"file": exe, "env": object})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const { file, env } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const result = spawnSync(file, [], { env, encoding: "utf8" });
if (result.error || result.status !== 0) throw result.error ?? new Error(result.stderr);
process.stdout.write(result.stdout);
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if slices.Contains(want, "PIG_ß=sharp") || !slices.Contains(want, "PIG_SS=double") {
		t.Fatalf("Node's child environment %q: want PIG_SS without PIG_ß", want)
	}
	cmd := exec.Command(exe)
	SetEnv(cmd, env)
	SetProgram(cmd)
	SetCommandLine(cmd)
	stdout := output(t, cmd)
	var got []string
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("child environment\n got %q\nwant %q", got, want)
	}
}

// libuv's find_path (src/win/process.c, libuv 1.52.1) searches for a bare name
// in the PATH of the first entry of the block that starts with "PATH="
// regardless of case, which is also the child's PATH, and the entry of a
// property named "PATH=Z" starts so. The block keeps both "PATH" and "PATH=Z"
// in the order of libuv's qsort. Node's spawn of the name from cwd is the
// oracle: it either starts the program, which exits 0, or emits ENOENT.
func TestSetProgramSearchesThePathOfTheFirstPathEntryAsLibuvDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, cwd := t.TempDir(), t.TempDir()
	copyFile(t, exe, filepath.Join(dir, "pigprobe.exe"))
	if err := os.Mkdir(filepath.Join(cwd, "Z=dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, exe, filepath.Join(cwd, "Z=dir", "pigprobe.exe"))
	helper := EnvProperty{testenv.EnvironHelper, "1"}
	for _, c := range []struct {
		name string
		env  []EnvProperty
	}{
		{"PATH names the program's directory", []EnvProperty{helper, {"PATH", dir}, {"PATH=Z", "nothing"}}},
		{"PATH=Z makes the PATH Z=dir", []EnvProperty{helper, {"PATH", "nothing"}, {"PATH=Z", "dir"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			input := []byte(`{"cwd":` + mustJSON(t, cwd) + `,"env":` + orderedObject(t, c.env) + `}`)
			node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const { cwd, env } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const result = spawnSync("pigprobe", [], { cwd, env, stdio: "ignore" });
process.stdout.write(result.error ? result.error.code : "exit " + result.status);
`)
			node.Stdin = bytes.NewReader(input)
			out, err := node.Output()
			if err != nil {
				t.Fatalf("node spawn: %v; output %s", err, out)
			}
			cmd := exec.Command("pigprobe")
			cmd.Dir = cwd
			SetEnvProperties(cmd, c.env)
			SetProgram(cmd)
			SetCommandLine(cmd)
			got := "exit 0"
			if err := Start(cmd); err != nil {
				spawnErr, ok := errors.AsType[*Error](err)
				if !ok {
					t.Fatalf("start: %v", err)
				}
				got = spawnErr.Code
			} else if err := cmd.Wait(); err != nil {
				t.Fatalf("wait: %v", err)
			}
			if want := string(out); got != want {
				t.Errorf("spawn of pigprobe: got %s, want %s", got, want)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// libuv's make_program_env (src/win/process.c, libuv 1.52.1) adds a required
// variable that the env lacks when GetEnvironmentVariableW(name, NULL, 0)
// returns a nonzero size. That size includes the terminating null, so a
// variable with an empty value returns 1 and libuv adds "TEMP=". Node's spawn
// with an env that lacks TEMP, while Pi's TEMP is empty, is the oracle.
func TestSetEnvAddsARequiredVariableWithAnEmptyValueAsLibuvDoes(t *testing.T) {
	t.Setenv("TEMP", "")
	requireSameChildEnvironment(t, []string{testenv.EnvironHelper + "=1"})
}
