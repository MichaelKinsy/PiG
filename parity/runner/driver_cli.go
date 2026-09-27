//go:build parity

package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// cliModeDriver runs `<bin> [base args] [cli.args...]` and captures combined output + exit code through a regular file. Used for flag-only invocations like --list-models, --version, --diagnose, etc. that exit immediately.
type cliModeDriver struct{}

// posixEnvLauncher is the pig_bin or pi_bin that runs a PATH program such as
// go or node: /usr/bin/env PROGRAM ARGS. Windows has no /usr/bin/env, so there
// the driver looks PROGRAM up on PATH itself.
const posixEnvLauncher = "/usr/bin/env"

// envLauncherProgram finds the program /usr/bin/env would run on Windows,
// searching the child's effective PATH as env does. bash and sh are Git for
// Windows' shells: bash.exe on PATH is usually the WSL launcher, which runs the
// script in a Linux distribution without the host's toolchains or files.
func envLauncherProgram(t *testing.T, name, pathList string) (string, error) {
	switch name {
	case "bash":
		return testenv.Bash(t), nil
	case "sh":
		return testenv.Sh(t), nil
	}
	return lookPathIn(name, pathList)
}

// lookPathIn resolves name against pathList instead of the runner's own PATH.
func lookPathIn(name, pathList string) (string, error) {
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" {
			continue
		}
		if path, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: not found on the scenario PATH", name)
}

// effectivePath returns the PATH a child started with environ sees: the last
// assignment wins, matched case-insensitively as on Windows.
func effectivePath(environ []string) string {
	value := ""
	for _, kv := range environ {
		if key, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, "PATH") {
			value = v
		}
	}
	return value
}

func (cliModeDriver) Name() string { return "cli-mode" }

// isShebangScript reports whether the file at path starts with a #! line.
func isShebangScript(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	prefix := make([]byte, 2)
	n, _ := io.ReadFull(file, prefix)
	return n == 2 && string(prefix) == "#!"
}

func (cliModeDriver) Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) Result {
	t.Helper()

	// Allocate per-binary tempdir for {{TEMP}} substitution.
	tc := newTokenContext(t, "cli-"+sc.Name+"-"+bin.Label)
	scenarioBin := ""
	if bin.Label == "pig" && sc.Env.PigBin != "" {
		scenarioBin = tc.expand(sc.Env.PigBin)
	}
	if bin.Label == "pi" && sc.Env.PiBin != "" {
		scenarioBin = tc.expand(sc.Env.PiBin)
	}
	envLauncher := runtime.GOOS == "windows" && scenarioBin == posixEnvLauncher
	if scenarioBin != "" && !envLauncher {
		bin.Path = resolveScenarioCWD(sc.SourcePath, scenarioBin)
	}

	// Snapshot agent dirs so binary writes never mutate committed testdata.
	preserveAuth, injectAuth := scenarioAuthModes(sc)
	pigEnv, piEnv := snapshotAgentDirs(t, sc.SourcePath, sc.Env.Pig, sc.Env.Pi, preserveAuth, injectAuth)

	args := []string{}
	if !sc.Env.OverrideBaseArgs {
		args = append(args, bin.Args...)
	}

	// Apply per-binary env overrides from the scenario's [env] section.
	env := snapshotBinaryEnv(t, sc.SourcePath, bin.Env, preserveAuth, injectAuth)
	switch bin.Label {
	case "pig":
		env = append(env, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, pigEnv))...)
		args = append(args, tc.expandSlice(sc.Env.PigArgs)...)
		for _, ext := range sc.Env.PigExtensions {
			resolved := resolveExtensionPath(sc.SourcePath, ext)
			args = append(args, "-e", resolved)
		}
	case "pi":
		env = append(env, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, piEnv))...)
		args = append(args, tc.expandSlice(sc.Env.PiArgs)...)
		for _, ext := range sc.Env.PiExtensions {
			resolved := resolveExtensionPath(sc.SourcePath, ext)
			args = append(args, "-e", resolved)
		}
	}

	cliArgs := sc.CLI.Args
	if bin.Label == "pig" && sc.CLI.PigArgs != nil {
		cliArgs = sc.CLI.PigArgs
	}
	if bin.Label == "pi" && sc.CLI.PiArgs != nil {
		cliArgs = sc.CLI.PiArgs
	}
	args = append(args, tc.expandSlice(cliArgs)...)
	if envLauncher {
		// Run the program named by the first argument from PATH, as env does.
		if len(args) == 0 {
			return Result{Err: fmt.Errorf("%s names no program to run", posixEnvLauncher)}
		}
		program, err := envLauncherProgram(t, args[0], effectivePath(append(hermeticEnviron(), env...)))
		if err != nil {
			return Result{Err: err}
		}
		bin.Path, args = program, args[1:]
	} else if runtime.GOOS == "windows" && scenarioBin != "" && isShebangScript(bin.Path) {
		// Windows runs a file by its extension, not its #! line; run the
		// interpreter that line names, as testenv.ScriptCommand does.
		script := testenv.ScriptCommand(t, bin.Path, args...)
		bin.Path, args = script.Path, script.Args[1:]
	}

	timeout := time.Duration(sc.CLI.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cwd := resolveScenarioCWD(sc.SourcePath, tc.expand(sc.CLI.CWD))
	if sc.CLI.SnapshotCWD {
		snap, err := snapshotCWD(t, cwd)
		if err != nil {
			return Result{Err: err}
		}
		cwd = snap
	} else if cwd == "" {
		snap, err := defaultCWD(t)
		if err != nil {
			return Result{Err: err}
		}
		cwd = snap
	}
	// Node writes synchronously to regular files but asynchronously to POSIX pipes. Pi's one-shot commands call process.exit after logging; capture both binaries in a file so the oracle cannot discard queued output.
	outputFile, err := os.CreateTemp(tc.tempRoot, "combined-output-*")
	if err != nil {
		return Result{Err: err}
	}
	defer func() { _ = outputFile.Close() }()
	out, code, ms, err := runCmdWithInput(ctx, bin.Path, args, env, tc.expandSlice(sc.CLI.InputLines), time.Duration(sc.CLI.SettleSeconds)*time.Second, timeout, cwd, outputFile)
	res := Result{
		Output:    out,
		ExitCode:  code,
		RuntimeMs: ms,
		Err:       err,
	}
	if sc.CLI.ArtifactPath != "" {
		path := resolveScenarioCWD(sc.SourcePath, tc.expand(sc.CLI.ArtifactPath))
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			res.ArtifactErr = readErr
		} else {
			res.Artifact = string(data)
		}
	}
	return res
}
