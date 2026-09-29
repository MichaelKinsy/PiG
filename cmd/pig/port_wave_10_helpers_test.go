package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/ai"
)

type packageCommandPathsFixture struct {
	root       string
	agentDir   string
	projectDir string
	packageDir string
}

// upstream: packages/coding-agent/test/package-command-paths.test.ts:136-188
func newPackageCommandPathsFixture(t *testing.T) packageCommandPathsFixture {
	t.Helper()
	root := t.TempDir()
	f := packageCommandPathsFixture{
		root: root, agentDir: filepath.Join(root, "agent"),
		projectDir: filepath.Join(root, "project"), packageDir: filepath.Join(root, "local-package"),
	}
	for _, dir := range []string{f.agentDir, f.projectDir, f.packageDir} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	for key, value := range map[string]string{
		"HOME": filepath.Join(root, "home"), "USERPROFILE": filepath.Join(root, "home"),
		"PIG_HOME":             filepath.Join(root, "pig-home"),
		"PIG_CODING_AGENT_DIR": f.agentDir, "PI_CODING_AGENT_DIR": f.agentDir,
		"PIG_CODING_AGENT_SESSION_DIR": filepath.Join(root, "pig-sessions"),
		"PI_CODING_AGENT_SESSION_DIR":  filepath.Join(root, "pi-sessions"),
		"XDG_CONFIG_HOME":              filepath.Join(root, "config"), "XDG_DATA_HOME": filepath.Join(root, "data"),
		"XDG_STATE_HOME": filepath.Join(root, "state"), "XDG_CACHE_HOME": filepath.Join(root, "cache"),
	} {
		require.NoError(t, os.MkdirAll(value, 0o755))
		t.Setenv(key, value)
	}
	// D2 permits Pi's exact .pi fixture paths through explicit shared-directory opt-in.
	t.Setenv("PIG_USE_PI_DIRS", "1")
	for _, key := range []string{"PIG_OFFLINE", "PI_OFFLINE", "PI_SESSION_FILE", "PIG_SESSION_FILE", "PI_PACKAGE_DIR", "PIG_PACKAGE_DIR"} {
		t.Setenv(key, "")
	}
	t.Chdir(f.projectDir)
	stdin, err := os.CreateTemp(root, "stdin-")
	require.NoError(t, err)
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		if err := stdin.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func writePackageCommandSettings(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	writeStartupFixtureFile(t, path, string(data))
}

func readPackageCommandPackages(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var settings struct {
		Packages []string `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(data, &settings))
	return settings.Packages
}

// Unlike canonicalTestPath, upstream realpathSync rejects a missing path rather than returning a cleaned spelling.
func packageCommandRealpath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}

// runPackageCommand is main.go's production dispatch for the upstream main/handlePackageCommand calls.
func capturePackageCommand(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand(args) })
	require.NotEqual(t, -1, code, "package command %q was not handled", args)
	return stdout, stderr, code
}

// Resolve the executable behind a tool-manager shim before replacing HOME, as upstream retains process.execPath.
func nodeExecutableForPackageCommand(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	output, err := exec.CommandContext(t.Context(), node, "-p", "process.execPath").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(output))
}

type packageRefreshSpy struct {
	refresh func(context.Context, ai.ModelsRefreshOptions) ai.ModelsRefreshResult
}

func (spy packageRefreshSpy) Refresh(ctx context.Context, options ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return spy.refresh(ctx, options[0])
}

type initialCatalogStore struct {
	*ai.InMemoryModelsStore
	read func(context.Context, string) (*ai.ModelsStoreEntry, error)
}

func (store initialCatalogStore) Read(ctx context.Context, provider string) (*ai.ModelsStoreEntry, error) {
	return store.read(ctx, provider)
}

type packageUpdateTransport func(*http.Request) (*http.Response, error)

func (transport packageUpdateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}
