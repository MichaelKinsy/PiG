package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

func previewDefinition() extension.LoginDefinition {
	rows := func(w, h int) []string {
		r := make([]string, h)
		for i := range r {
			r[i] = strings.Repeat(".", w)
		}
		return r
	}
	d := extension.LoginDefinition{Brand: rows(41, 5), Hero: rows(32, 14), Mascot: rows(16, 14), Palette: map[string]string{"X": "#112233"}, Name: "Preview", Description: "Production renderer", Tagline: "No model and no session"}
	d.Brand[0] = "X" + d.Brand[0][1:]
	return d
}

// useLoginPreviewTestBudget bounds preview-login by the test wait budget: each
// test builds the fixture extension into a fresh PIG_HOME, which can exceed
// the 30 s product bound on a loaded machine.
func useLoginPreviewTestBudget(t *testing.T) {
	t.Helper()
	old := extensionLoginPreviewTimeout
	extensionLoginPreviewTimeout = testbudget.Wait(t)
	t.Cleanup(func() { extensionLoginPreviewTimeout = old })
}

func TestExtensionLoginPreviewRendersProductionOutputWithoutSessionOrSettingsMutation(t *testing.T) {
	home := t.TempDir()
	useLoginPreviewTestBudget(t)
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_LOGIN_PREVIEW_FIXTURE", "valid")
	t.Setenv("PIG_LOGO_GLYPHFREE", "0")
	t.Setenv("COLORTERM", "truecolor")
	old := extensionLoginPreviewWidth
	extensionLoginPreviewWidth = func() int { return 80 }
	t.Cleanup(func() { extensionLoginPreviewWidth = old })
	if err := pigsdk.EnsureSynced(codingagent.ConfigRoot()); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(codingagent.ConfigRoot(), "settings.json")
	settings := []byte("{\"theme\":\"dark\"}\n")
	if err := os.WriteFile(settingsPath, settings, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeSessions := sessionFiles(t, home)

	out, stderr, code := captureStdoutStderr(t, func() int {
		return runExtensionCommand([]string{"extension", "preview-login", "testdata/login-preview"})
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	d, err := extension.ValidateLoginDefinition(previewDefinition())
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(codingagent.RenderLoginHeader(d, 80, codingagent.LoginHeaderOptions{TrueColor: tui.SupportsTrueColor()}), "\n") + "\n"
	if out != want {
		t.Fatalf("output is not production-equivalent\ngot=%q\nwant=%q", out, want)
	}
	afterSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterSettings, settings) {
		t.Fatalf("settings changed: got %q, want %q", afterSettings, settings)
	}
	if afterSessions := sessionFiles(t, home); afterSessions != beforeSessions {
		t.Fatalf("session files changed: %q -> %q", beforeSessions, afterSessions)
	}
}

func TestExtensionLoginPreviewMatchesGlyphFreeProductionOutput(t *testing.T) {
	useLoginPreviewTestBudget(t)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_LOGIN_PREVIEW_FIXTURE", "valid")
	t.Setenv("PIG_LOGO_GLYPHFREE", "1")
	old := extensionLoginPreviewWidth
	extensionLoginPreviewWidth = func() int { return 130 }
	t.Cleanup(func() { extensionLoginPreviewWidth = old })

	out, stderr, code := captureStdoutStderr(t, func() int {
		return runExtensionCommand([]string{"extension", "preview-login", "testdata/login-preview"})
	})
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	d, err := extension.ValidateLoginDefinition(previewDefinition())
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(codingagent.RenderLoginHeader(d, 130, codingagent.LoginHeaderOptions{TrueColor: tui.SupportsTrueColor(), GlyphFree: true}), "\n") + "\n"
	if out != want {
		t.Fatal("glyph-free preview output differs from the production renderer")
	}
}

func TestExtensionLoginPreviewRejectsInvalidNoLoginHandlerAndArguments(t *testing.T) {
	cases := []struct {
		name, mode, want string
		args             []string
	}{
		{"invalid", "invalid", "brand", []string{"extension", "preview-login", "testdata/login-preview"}},
		{"none", "none", "did not set a login", []string{"extension", "preview-login", "testdata/login-preview"}},
		{"handler", "handler-error", "fixture session start failed", []string{"extension", "preview-login", "testdata/login-preview"}},
		{"zero", "valid", "exactly one extension source", []string{"extension", "preview-login"}},
		{"multiple", "valid", "exactly one extension source", []string{"extension", "preview-login", "a", "b"}},
		{"option", "valid", "unknown option --wat", []string{"extension", "preview-login", "--wat"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useLoginPreviewTestBudget(t)
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("PIG_LOGIN_PREVIEW_FIXTURE", tc.mode)
			_, stderr, code := captureStdoutStderr(t, func() int { return runExtensionCommand(tc.args) })
			if code != 1 || !strings.Contains(strings.ToLower(stderr), strings.ToLower(tc.want)) {
				t.Fatalf("code=%d stderr=%q want=%q", code, stderr, tc.want)
			}
		})
	}
}

func sessionFiles(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(paths, "\n")
}

func TestGeneratedLoginExtensionIsIdentifiedThroughPigletResolution(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_LOGIN_PREVIEW_FIXTURE", "valid")
	if err := pigsdk.EnsureSynced(codingagent.ConfigRoot()); err != nil {
		t.Fatal(err)
	}
	pigletDir := t.TempDir()
	extensionPath := filepath.Join(pigletDir, "login-preview")
	if err := os.MkdirAll(extensionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	mainSource, err := os.ReadFile(filepath.Join("testdata", "login-preview", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionPath, "main.go"), mainSource, 0o600); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/login-preview\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	if err := os.WriteFile(filepath.Join(extensionPath, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(pigletDir, "generated-login.piglet.yaml")
	source := "name: generated-login\nextensions:\n  - name: login-preview\n    origins:\n      - local:./login-preview\n"
	if err := os.WriteFile(pigletPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	configs := resolvePigletExtConfigs(parsed)
	if len(configs) != 1 || configs[0].Name != "login-preview" || filepath.Base(configs[0].Source) != "login-preview" {
		t.Fatalf("resolved configs = %#v", configs)
	}

	ctx := testbudget.Context(t)
	host := subprocess.NewHost(pigletDir)
	defer host.Shutdown("test complete")
	ui := newLoginPreviewUI()
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	host.SetUIBridge(bridge)
	host.SetMode(string(extension.ModeTUI))
	host.SetWidthFunc(func() int { return 80 })
	loaded, loadErrors := host.LoadAll(ctx, configs)
	if len(loadErrors) != 0 || len(loaded) != 1 {
		t.Fatalf("Piglet extension load errors=%v count=%d", loadErrors, len(loaded))
	}
	runner := inproc.NewRunner(loaded, pigletDir)
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{UI: ui, Mode: extension.ModeTUI}, nil)
	if _, err := runner.Emit(ctx, extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
		t.Fatal(err)
	}
	if !ui.accepted || ui.definition.Name() != "Preview" {
		t.Fatalf("Piglet session_start active login accepted=%t name=%q", ui.accepted, ui.definition.Name())
	}
}

func TestExtensionCommandHelpIncludesLoginPreview(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runExtensionCommand([]string{"extension", "--help"}) })
	if code != 0 || stderr != "" || !strings.Contains(stdout, "pig extension preview-login <path>") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
