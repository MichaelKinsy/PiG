//go:build parity

package runner

import (
	"context"
	"os"
	"testing"
	"time"
)

// cliModeDriver runs `<bin> [base args] [cli.args...]` and captures combined output + exit code through a regular file. Used for flag-only invocations like --list-models, --version, --diagnose, etc. that exit immediately.
type cliModeDriver struct{}

func (cliModeDriver) Name() string { return "cli-mode" }

func (cliModeDriver) Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) Result {
	t.Helper()

	// Allocate per-binary tempdir for {{TEMP}} substitution.
	tc := newTokenContext(t, "cli-"+sc.Name+"-"+bin.Label)
	if bin.Label == "pig" && sc.Env.PigBin != "" {
		bin.Path = resolveScenarioCWD(sc.SourcePath, tc.expand(sc.Env.PigBin))
	}
	if bin.Label == "pi" && sc.Env.PiBin != "" {
		bin.Path = resolveScenarioCWD(sc.SourcePath, tc.expand(sc.Env.PiBin))
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
