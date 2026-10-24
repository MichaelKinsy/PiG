package coding

import (
	"os"
	"strings"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's DefaultResourceLoader requires agentDir (resource-loader.ts:274, 373) and its package manager resolves it to an absolute path
// (package-manager.ts:823). A Go loader built without one must reject the reload that would install a configured Package, and must
// not create npm/{.gitignore,package.json} in the process directory. Since Pi 6dfb54678 a Package that cannot be installed is skipped and
// reported as a warning instead of rejecting the reload, so the not-absolute error is that warning.
func TestResourceLoaderWithoutAnAgentDirWritesNothingIntoTheProcessDirectory(t *testing.T) {
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	dir := t.TempDir()
	t.Chdir(dir)
	settings := NewInMemorySettingsManager(Settings{})
	if err := settings.SetPackages([]icodingagent.PackageSource{{Source: "npm:not-installed-pkg"}}); err != nil {
		t.Fatal(err)
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{SettingsManager: settings, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true})
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload without an agent directory = %v, want the failure reported as a warning", err)
	}
	if warnings := loader.GetExtensions().Warnings; len(warnings) != 1 || warnings[0].Path != "npm:not-installed-pkg" || !strings.Contains(warnings[0].Warning, "not absolute") {
		t.Fatalf("warnings = %+v, want one that names the not-absolute error", warnings)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		t.Fatalf("Reload wrote %d entries into the process directory, first %q", len(entries), entries[0].Name())
	}
}
