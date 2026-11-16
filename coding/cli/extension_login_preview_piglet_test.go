//go:build !pig_strip_extension_sdk_go

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The extension is a Go program, so a build that strips the Go extension SDK cannot build it: the Piglet reports the strip message.
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
	runner.SetUIContext(ui, extension.ModeTUI)
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	if _, err := runner.Emit(ctx, extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
		t.Fatal(err)
	}
	if !ui.accepted || ui.definition.Name() != "Preview" {
		t.Fatalf("Piglet session_start active login accepted=%t name=%q", ui.accepted, ui.definition.Name())
	}
}
