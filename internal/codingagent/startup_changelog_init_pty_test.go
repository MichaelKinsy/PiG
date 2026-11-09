//go:build linux && !pig_strip_changelog

package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/source"
)

const initChangelogChildEnv = "PIG_TEST_INIT_CHANGELOG_CHILD"

// interactive-mode.ts:976-981: init() prepares the changelog data right after registering the signal handlers, before the model scope notice, the UI and the
// extensions, so a first start or an update records the running version and each package's version before anything else in init() runs. The first startup mark is
// the earliest point of the real Init the test can observe; the notices themselves are shown later (interactive_changelog.go showStartupChangelog).
func TestInitRecordsTheChangelogVersionsBeforeAnythingStarts(t *testing.T) {
	runInitPTYChild(t, "TestInitRecordsTheChangelogVersionsBeforeAnythingStartsChild", initChangelogChildEnv)
}

func TestInitRecordsTheChangelogVersionsBeforeAnythingStartsChild(t *testing.T) {
	report := os.Getenv(initChangelogChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitRecordsTheChangelogVersionsBeforeAnythingStarts")
	}
	// The production startup this child runs selects a theme and replaces the registry; TestMain fails a binary that leaves them.
	restoreStartupTheme(t)
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	t.Setenv("PI_OFFLINE", "1")
	cwd, agentDir, packageRoot := t.TempDir(), t.TempDir(), t.TempDir()
	for name, content := range map[string]string{"CHANGELOG.md": extensionChangelogFixture, "package.json": `{"name":"pi-demo-extension","version":"1.0.0"}`} {
		if err := os.WriteFile(filepath.Join(packageRoot, name), []byte(content), 0o644); err != nil {
			childFatalf(t, "%v", err)
		}
	}
	info := PiSourceInfo{Path: filepath.Join(packageRoot, "a.ts"), Source: "npm:pi-demo-extension", Scope: "user", Origin: "package", BaseDir: packageRoot, ChangelogPath: filepath.Join(packageRoot, "CHANGELOG.md")}
	sm := NewSettingsManager(cwd, agentDir)
	m := NewInteractiveMode(nil, InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, NoThemes: true, SettingsManager: sm, AppVersion: "9.9.9",
		ExtensionRunner: inproc.NewRunner([]extension.Extension{{Name: "demo", Path: info.Path, ResolvedPath: info.Path, SourceInfo: info}}, cwd),
	})
	var marks []string
	var versionAtFirstMark, packageAtFirstMark string
	m.opts.StartupMark = func(label string) {
		if marks = append(marks, label); len(marks) == 1 {
			versionAtFirstMark = sm.GetLastChangelogVersion()
			packageAtFirstMark, _ = sm.GetExtensionChangelogVersion(source.GetSourcePackageKey(info.Source, info.Scope))
		}
	}
	if err := m.Init(t.Context()); err == nil {
		childFatalf(t, "Init without a SessionHandle succeeded")
	}
	if len(marks) == 0 || versionAtFirstMark != "9.9.9" {
		childFatalf(t, "at the first startup mark %v the recorded changelog version is %q, want the running version 9.9.9: init() records it before the UI starts", marks, versionAtFirstMark)
	}
	if packageAtFirstMark != "1.0.0" {
		childFatalf(t, "at the first startup mark the recorded version of the extension package is %q, want 1.0.0: init() records it before the UI starts", packageAtFirstMark)
	}
	if err := m.Stop(); err != nil {
		childFatalf(t, "%v", err)
	}
}
