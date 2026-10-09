package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// upstream: packages/durable/src/env/node.ts exec: `const cwd = options?.cwd ? resolvePath(this.cwd, options.cwd) : this.cwd`: a relative ShellExecOptions.Cwd resolves against the environment's cwd, an absolute one is used as is, and a missing one is reported before spawning.
// Pi source: packages/durable/src/env/node.ts (exec)
// mutation-checked: not resolving options.Cwd against the environment cwd fails it
// mutation-checked: dropping the reads and writes of ShellExecOptions.Cwd fails it
// Pi: packages/durable/src/env/index.ts:173 (cwd)
func TestShellExecOptionsCwdOverridesTheEnvironmentCwd(t *testing.T) {
	skipOnWindows(t)
	env, root := newTestEnv(t)
	mustDo(t, os.MkdirAll(filepath.Join(root, "sub"), 0o700))
	other := t.TempDir()
	want := func(dir string) string {
		resolved, err := filepath.EvalSymlinks(dir)
		mustDo(t, err)
		return resolved
	}
	for _, c := range []struct{ cwd, expected string }{{"sub", want(filepath.Join(root, "sub"))}, {other, want(other)}, {"", want(root)}} {
		_, output, err := collectShellOutput(env, `printf '%s' "$PWD"`, &durableenv.ShellExecOptions{Cwd: c.cwd}, background)
		mustDo(t, err)
		if output != c.expected {
			t.Fatalf("Cwd %q ran in %q, want %q", c.cwd, output, c.expected)
		}
	}
	if _, _, err := collectShellOutput(env, "true", &durableenv.ShellExecOptions{Cwd: "missing"}, background); err == nil {
		t.Fatal("a missing Cwd must fail before spawning")
	}
}

// upstream: packages/durable/test/env-node.test.ts "cleanup terminates active shell processes": Shell.Cleanup kills every command the environment still runs, so the running Exec settles.
// Pi: packages/durable/src/env/index.ts:321 (cleanup)
// Pi source: packages/durable/src/env/node.ts (cleanup)
// mutation-checked: a Cleanup that kills nothing leaves the command running and fails it
func TestShellCleanupKillsRunningCommands(t *testing.T) {
	skipOnWindows(t)
	env, _ := newTestEnv(t)
	var shell durableenv.Shell = env
	done := make(chan error, 1)
	go func() {
		_, err := shell.Exec(background, "touch started; sleep 60", nil)
		done <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); !must(env.Exists(background, "started")); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the command did not start within 10s")
		}
	}
	mustDo(t, shell.Cleanup(background))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Exec after Cleanup: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not settle within 3s of Cleanup")
	}
	mustDo(t, shell.Cleanup(background))
}
