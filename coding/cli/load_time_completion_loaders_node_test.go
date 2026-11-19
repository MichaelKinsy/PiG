//go:build !pig_strip_node_extensions

package cli

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestExtensionValidationAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	reports, err := validateExtensionRuntimes(module)
	if err != nil || len(reports) != 1 || !reports[0].Valid {
		t.Fatalf("validation = %+v, %v, want the module valid", reports, err)
	}
	requireLoadTimeAnswers(t, answers)
}

func TestAuthContributionInspectionAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	registry := &authContributionRegistry{cwd: t.TempDir(), ctx: ctx, targets: map[string]authContribution{}, owners: map[string]string{}, configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}}
	defer registry.close()
	if err := registry.inspectConfigs([]subprocess.ExtConfig{{Name: "load-time", Source: module, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if len(registry.diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want the module inspected", registry.diagnostics)
	}
	requireLoadTimeAnswers(t, answers)
}

func TestAuthContributionLoadByNameAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	registry := &authContributionRegistry{cwd: t.TempDir(), ctx: ctx, targets: map[string]authContribution{}, configs: map[string]subprocess.ExtConfig{"load-time": {Name: "load-time", Source: module, Enabled: true}}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}}
	defer registry.close()
	if err := registry.loadExtension("load-time"); err != nil {
		t.Fatal(err)
	}
	requireLoadTimeAnswers(t, answers)
}

func TestExtensionLoginPreviewAnswersLoadTimeCompletions(t *testing.T) {
	module, answers := loadTimeCompletionModule(t)
	t.Setenv("PIG_HOME", t.TempDir())
	// The preview has nothing to show for a module that registers no login; the load, and the answers the module recorded while it loaded, are what this checks.
	_ = runExtensionLoginPreview([]string{module})
	requireLoadTimeAnswers(t, answers)
}
