//go:build parity

package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// extensionHostDriver runs Pig's production extension validation path and
// captures the structured registration report. Upstream pi has no equivalent
// subprocess-only runtime-cell host, so the pi side is intentionally a no-op;
// assertions are evaluated by AssertSpec.Extensions against the pig JSON.
type extensionHostDriver struct{}

func (extensionHostDriver) Name() string { return "extension-host" }

func (extensionHostDriver) Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) Result {
	t.Helper()
	if bin.Label != "pig" {
		return Result{Output: `{"valid":true,"skipped":"extension-host is pig-only"}` + "\n", ExitCode: 0, RuntimeMs: 0, ReadyOK: true}
	}
	if len(sc.ExtensionHost.Sources) == 0 {
		return Result{Err: fmt.Errorf("extension-host scenario %q must declare [extension_host].sources", sc.Name)}
	}
	tc := newTokenContext(t, "extension-host-"+sc.Name)
	preserveAuth, injectAuth := scenarioAuthModes(sc)
	pigEnv, _ := snapshotAgentDirs(t, sc.SourcePath, sc.Env.Pig, nil, preserveAuth, injectAuth)
	env := snapshotBinaryEnv(t, sc.SourcePath, bin.Env, preserveAuth, injectAuth)
	env = append(env, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, pigEnv))...)

	args := []string{"install"}
	for _, src := range sc.ExtensionHost.Sources {
		resolved := tc.expand(src)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(sc.SourcePath), resolved)
		}
		args = append(args, resolved)
	}
	args = append(args, "--validate-only", "--json")

	cwd := resolveScenarioCWD(sc.SourcePath, sc.ExtensionHost.CWD)
	if cwd == "" {
		cwd = defaultCWDFixture()
	}
	{
		snap, err := snapshotCWD(t, cwd)
		if err != nil {
			return Result{Err: fmt.Errorf("snapshot cwd %s: %w", cwd, err)}
		}
		cwd = snap
	}
	timeout := time.Duration(sc.ExtensionHost.TimeoutSeconds) * time.Second
	out, code, ms, err := runCmd(ctx, bin.Path, args, env, timeout, cwd)
	return Result{Output: out, ExitCode: code, RuntimeMs: ms, ReadyOK: err == nil, Err: err}
}
