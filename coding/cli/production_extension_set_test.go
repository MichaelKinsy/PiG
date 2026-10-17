package cli

import (
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// productionExtensionSet is the extension set the production startup build (cliRuntimeBuilder.buildResources, the path cmd/pig runs) loads: the
// final extensions in load order and the diagnostics its load errors and warnings become.
type productionExtensionSet struct {
	Extensions  []extension.Extension
	Diagnostics []codingagent.AgentSessionRuntimeDiagnostic
	build       *cliBuild
}

func (r *productionExtensionSet) paths() []string {
	var paths []string
	for _, ext := range r.Extensions {
		paths = append(paths, ext.Path)
	}
	return paths
}

// loadProductionExtensionSet runs the production startup build for a faux-model --print run with args in a fresh project and agent directory and
// returns its extension set. inline are MainOptions.ExtensionFactories.
func loadProductionExtensionSet(t *testing.T, args []string, inline ...extension.InlineExtension) *productionExtensionSet {
	t.Helper()
	root := t.TempDir()
	cwd, agentDir := root+"/project", root+"/agent"
	return loadProductionExtensionSetIn(t, cwd, agentDir, args, inline...)
}

// loadProductionExtensionSetIn is loadProductionExtensionSet over caller-prepared directories.
func loadProductionExtensionSetIn(t *testing.T, cwd, agentDir string, args []string, inline ...extension.InlineExtension) *productionExtensionSet {
	t.Helper()
	result, err := tryProductionExtensionSetIn(t, cwd, agentDir, args, inline...)
	if err != nil {
		t.Fatalf("startup build failed: %v", err)
	}
	return result
}

// tryProductionExtensionSetIn is loadProductionExtensionSetIn returning the startup build's error instead of failing the test.
func tryProductionExtensionSetIn(t *testing.T, cwd, agentDir string, args []string, inline ...extension.InlineExtension) (*productionExtensionSet, error) {
	t.Helper()
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_HOME", agentDir)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_TEST_FAUX", "1")
	flags := parseArgs(append([]string{"--model", "test-faux/faux-1", "--print"}, args...))
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	builder := &cliRuntimeBuilder{
		mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
		extensionFactories: inline,
		settingsManager:    settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(settings),
		startupUIOptions: startupUIOptions(flags, false, cwd, agentDir, settings),
		trustStore:       codingagent.NewProjectTrustStore(agentDir), trustByCWD: map[string]bool{},
		stageExtensionSDKs: func() error { return nil },
	}
	build, err := builder.buildResources(t.Context(), cliBuildInput{CWD: cwd})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		if build.Host != nil {
			build.Host.Shutdown("test")
		}
		build.Services.Close()
	})
	return &productionExtensionSet{Extensions: build.Extensions, Diagnostics: build.ExtensionDiagnostics, build: build}, nil
}

// builtinPaths are the `builtin:<name>` paths of the CLI's own built-in extensions in load order, without the named ones.
func builtinPaths(except ...string) []string {
	var paths []string
	for _, name := range cliBuiltinExtensionNames() {
		if !slices.Contains(except, name) {
			paths = append(paths, "builtin:"+name)
		}
	}
	return paths
}
