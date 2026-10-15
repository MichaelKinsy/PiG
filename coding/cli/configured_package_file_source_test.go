package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// upstream: packages/coding-agent/src/core/package-manager.ts:1368-1372 resolveLocalExtensionSource: a local package source that is a file is one enabled extension (baseDir is the file's directory). The settings entry packages:["/x/ext.ts"] therefore loads that file as an extension at startup.
func TestSingleFileLocalPackageSourceLoadsAsAnExtension(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "work"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "single", "ext.mjs")
	writeStartupFixtureFile(t, file, "export default function (pi) {}\n")
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: file}}); err != nil {
		t.Fatal(err)
	}
	if err := validateConfiguredPackagesForStartup(cwd, sm, allScopesLoadExtensions); err != nil {
		t.Fatalf("startup validation rejects a single-file package source: %v", err)
	}
	configs := collectPackageExtensionConfigs(cwd, sm, nil)
	if len(configs) != 1 || configs[0].Name != "ext" || configs[0].SourceInfo.Path != file || configs[0].SourceInfo.BaseDir != filepath.Dir(file) || configs[0].SourceInfo.Origin != "package" {
		t.Fatalf("package extension configs = %+v, want one extension \"ext\" from %s with base dir %s", configs, file, filepath.Dir(file))
	}
	if err := startupPackageValidationError(cwd, sm); err != nil {
		t.Fatalf("startup path rejects the extension: %v", err)
	}
}

// The same source reaches `pig auth`'s extension inventory; it must list the file as an extension, not fail the whole inventory as an invalid Package.
func TestSingleFileLocalPackageSourceIsInTheAuthExtensionInventory(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "work"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "single", "ext.mjs")
	writeStartupFixtureFile(t, file, "export default function (pi) {}\n")
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: file}}); err != nil {
		t.Fatal(err)
	}
	configs, diagnostics, err := authExtensionConfigs(cwd, agentDir, sm)
	if err != nil {
		t.Fatalf("auth inventory rejects a single-file package source: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "ext" || len(diagnostics) != 0 {
		t.Fatalf("configs = %+v, diagnostics = %+v, want one extension \"ext\"", configs, diagnostics)
	}
}
