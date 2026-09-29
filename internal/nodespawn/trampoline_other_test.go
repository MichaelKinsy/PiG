//go:build !windows

package nodespawn

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// spawnOutcomes runs Node's spawnSync(file, args, { env }) for each env
// option, whose properties are given in order, and returns its outcomes.
func spawnOutcomes(t *testing.T, file string, args []string, envs [][]EnvProperty) []execvpOutcome {
	t.Helper()
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	fileJSON, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	objects := make([]string, len(envs))
	for i, env := range envs {
		objects[i] = orderedObject(t, env)
	}
	node := exec.CommandContext(t.Context(), nodeBinary, "-e", `
const { spawnSync } = require("node:child_process");
const { file, args, envs } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
process.stdout.write(JSON.stringify(envs.map((env) => {
  const result = spawnSync(file, args, { env, encoding: "utf8" });
  if (result.error) return { code: result.error.code };
  return { status: result.status, stdout: result.stdout };
})));
`)
	node.Stdin = strings.NewReader(`{"file":` + string(fileJSON) + `,"args":` + string(argsJSON) + `,"envs":[` + strings.Join(objects, ",") + `]}`)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var outcomes []execvpOutcome
	if err := json.Unmarshal(out, &outcomes); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return outcomes
}

// requireSpawnOutcomes compares the outcome of SetEnvProperties and
// SetProgram for each env option with Node's.
func requireSpawnOutcomes(t *testing.T, file string, args []string, envs [][]EnvProperty) {
	t.Helper()
	want := spawnOutcomes(t, file, args, envs)
	for i, env := range envs {
		cmd := exec.Command(file, args...)
		SetEnvProperties(cmd, env)
		SetProgram(cmd)
		output, err := cmd.Output()
		got := execvpOutcome{Stdout: string(output)}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			got.Status = exitErr.ExitCode()
		} else if err != nil {
			got = execvpOutcome{Code: goErrorCode(err)}
		}
		if got != want[i] {
			t.Errorf("env %d: got %+v (error %v); Node's is %+v", i, got, err, want[i])
		}
	}
}

// libuv's execvp finds the program in the PATH that the child's getenv sees,
// which is the first entry that starts with "PATH=": glibc's getenv in
// uv__process_child_init (src/unix/process.c, libuv 1.52.1), and
// uv__spawn_find_path_in_env on macOS. A property whose name starts with
// "PATH=" is such an entry, and it reaches the child beside PATH.
func TestSetEnvPropertiesSearchesThePathTheChildSees(t *testing.T) {
	requireSpawnOutcomes(t, "sh", []string{"-c", "printf ran"}, [][]EnvProperty{
		{{"PATH", "/usr/bin:/bin"}, {"PATH=X", "y"}},
		{{"PATH=X", "y"}, {"PATH", "/usr/bin:/bin"}},
		{{"PATH=", "/usr/bin:/bin"}, {"PATH", "/nonexistent"}},
	})
}

// An environment far larger than one string passes through the
// trampoline as Node passes it: the kernel limits each string of argv and
// envp (MAX_ARG_STRLEN) and their sum, not the environment as one string.
func TestSetEnvPropertiesTrampolinePassesALargeEnvironment(t *testing.T) {
	env := []EnvProperty{{"PATH", "/usr/bin:/bin"}, {"PIG_A", "one"}, {"PIG_A=B", "two"}}
	for i := range 64 {
		env = append(env, EnvProperty{"PIG_LARGE_" + strconv.Itoa(i), strings.Repeat("x", 4096)})
	}
	requireSpawnOutcomes(t, "sh", []string{"-c", `printf '%s' "$PIG_A"`}, [][]EnvProperty{env})
}
