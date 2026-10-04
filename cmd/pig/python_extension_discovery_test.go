package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A Python extension directory loads at startup and on /reload from the agent's extensions directory and from a settings entry, as `pig install DIR --validate-only` already accepts it. Discovery used to skip it, so only `pig -e DIR` loaded one.
func TestPythonExtensionDirectoriesLoadFromDiscoveryAndSettings(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "work"), filepath.Join(root, "agent")
	factory := func(name string) string {
		return "import pig_sdk\n\n\ndef new_extension() -> pig_sdk.Extension:\n    return pig_sdk.Extension(\"" + name + "\")\n"
	}
	for path, content := range map[string]string{
		filepath.Join(agentDir, "extensions", "hello-python", "hello_python.py"): factory("hello-python"),
		filepath.Join(root, "py-ext", "py_ext.py"):                               factory("py-ext"),
	} {
		writeStartupFixtureFile(t, path, content)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetExtensionPaths([]string{filepath.Join(root, "py-ext")}); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, config := range collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil) {
		if err := config.ResolveError(); err != nil {
			t.Fatalf("%s: %v", config.Name, err)
		}
		names = append(names, config.Name)
	}
	slices.Sort(names)
	if want := []string{"hello-python", "py-ext"}; !slices.Equal(names, want) {
		t.Fatalf("extensions = %q, want %q", names, want)
	}
}
