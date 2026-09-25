//go:build parity

package runner

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// printModeDriver runs `<bin> [base args] --print <prompt> --model <m>`
// and captures combined output + exit code + wall clock.
type printModeDriver struct{}

func (printModeDriver) Name() string { return "print-mode" }

func (printModeDriver) Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) Result {
	t.Helper()

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
		env = append(env, resolveScenarioEnvVars(sc.SourcePath, pigEnv)...)
		args = append(args, sc.Env.PigArgs...)
		for _, ext := range sc.Env.PigExtensions {
			resolved := resolveExtensionPath(sc.SourcePath, ext)
			args = append(args, "-e", resolved)
		}
	case "pi":
		env = append(env, resolveScenarioEnvVars(sc.SourcePath, piEnv)...)
		args = append(args, sc.Env.PiArgs...)
		for _, ext := range sc.Env.PiExtensions {
			resolved := resolveExtensionPath(sc.SourcePath, ext)
			args = append(args, "-e", resolved)
		}
	}

	args = append(args, "--print", sc.Print.Prompt)
	if sc.Model != "" {
		args = append(args, "--model", sc.Model)
	}

	timeout := time.Duration(sc.Print.TimeoutSeconds) * time.Second
	cwd := resolveScenarioCWD(sc.SourcePath, sc.Print.CWD)

	// Snapshot the scenario cwd, or the default cwd fixture, to a temp
	// directory so the binary neither mutates committed testdata (e.g. the
	// edit tool modifies files in-place) nor runs inside the checkout. The
	// temp dir is cleaned up via t.Cleanup.
	if cwd == "" {
		cwd = defaultCWDFixture()
	}
	{
		tmp, err := mkdirTempFixed("parity-snap-cwd-")
		if err != nil {
			return Result{Err: fmt.Errorf("snapshot print cwd: %w", err)}
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(tmp); err != nil {
				t.Error(err)
			}
		})
		if err := copyDir(cwd, tmp); err != nil {
			return Result{Err: fmt.Errorf("copy print cwd: %w", err)}
		}
		cwd = tmp
	}

	out, code, ms, err := runCmd(ctx, bin.Path, args, env, timeout, cwd)
	return Result{
		Output:    out,
		ExitCode:  code,
		RuntimeMs: ms,
		Err:       err,
	}
}
