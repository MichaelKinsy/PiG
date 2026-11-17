package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// resource-loader.ts reload keeps the install failures and package warnings of the load in getExtensions().warnings, and showLoadedResources lists them under
// [Extension issues]. Each /reload replaces them with the warnings of its own load.
func TestReloadListsTheInstallWarningsOfItsLoadAsExtensionIssues(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	warnings := []ExtensionWarning{{Path: "npm:fail-pkg", Warning: "failed to install (npm install failed with code 1:\nnpm ERR! boom). Run \"pig remove npm:fail-pkg\" to remove it from settings.json."}}
	m := reloadTestMode(InteractiveModeOptions{CWD: t.TempDir()})
	m.opts.ReloadResourceProvider = func() ReloadResourceSnapshot { return ReloadResourceSnapshot{ExtensionWarnings: warnings} }
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	want := extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: warnings[0].Warning, Path: warnings[0].Path}
	if !slices.Contains(m.extensionDiagnostics(), want) {
		t.Fatalf("extension diagnostics = %+v, want %+v", m.extensionDiagnostics(), want)
	}

	m.opts.ReloadResourceProvider = func() ReloadResourceSnapshot { return ReloadResourceSnapshot{} }
	if err := m.buildSlashContext(t.Context()).Reload(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(m.extensionDiagnostics(), want) {
		t.Fatalf("the warning of the earlier load is still listed: %+v", m.extensionDiagnostics())
	}
}

// The startup load lists its install warnings under [Extension issues] too (interactive-mode.ts showLoadedResources reads them from the resource loader).
func TestStartupListsTheInstallWarningsOfItsLoadAsExtensionIssues(t *testing.T) {
	warnings := []ExtensionWarning{{Path: "git:github.com/probe/missing", Warning: "failed to install (git clone failed with code 128:\nfatal: boom)."}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), ExtensionWarnings: warnings})
	want := extension.ResourceDiagnostic{Type: extension.DiagnosticWarning, Message: warnings[0].Warning, Path: warnings[0].Path}
	if !slices.Contains(m.extensionDiagnostics(), want) {
		t.Fatalf("extension diagnostics = %+v, want %+v", m.extensionDiagnostics(), want)
	}
}
