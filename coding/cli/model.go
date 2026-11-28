package cli

// Ports packages/coding-agent/src/core/model-resolver.ts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── Model Resolution ─────────────────────────────────────────────────────────

// buildModelFromRef resolves the startup model with resolveStartupModelEntry
// and builds it with coding.BuildModelFromEntry, the constructor /model uses.
func buildModelFromRef(ctx context.Context, providerID, modelID string, services *coding.AgentSessionServices) (*ai.Model, error) {
	registry := services.Registry().ModelRegistry
	// A virtual model is a catalog entry of the model runtime, not of the registry (model-runtime.ts:285-293).
	if model := services.ModelRuntime().GetModel(providerID, modelID); coding.IsVirtualModel(model) {
		return model, nil
	}
	if model := coding.BuildNativeModel(registry, providerID, modelID); model != nil {
		return model, nil
	}
	if registry.GetProvider(providerID) != nil {
		model := services.ModelRuntime().GetModel(providerID, modelID)
		if model == nil {
			return nil, fmt.Errorf("model not found: %s/%s", providerID, modelID)
		}
		return model, nil
	}
	entry := resolveStartupModelEntry(providerID, modelID, registry)

	// Radius runtime keys are in-memory metadata; the shared builder owns stored credential reads and refresh.
	if _, radius := registry.RadiusOAuth(providerID); radius {
		if key, ok := registry.RuntimeAPIKey(providerID); ok && key != "" {
			entry.APIKey = key
		}
	}
	return coding.BuildModelFromEntry(providerID, modelID, entry, services)
}

// resolveStartupModelEntry resolves a model by provider and id: the remote
// catalog's record, else the bundled catalog's, else the registry entry (a
// models.json or registered definition, or the provider's settings alone for
// an id nothing defines). An id no catalog carries that `--model` named is
// not resolved here: startup builds the resolver's copy of one of the
// provider's models (coding.BuildFallbackModel).
func resolveStartupModelEntry(providerID, modelID string, registry *codingagent.ModelRegistry) codingagent.ModelEntry {
	if remote, ok := registry.ResolveRemoteCatalogModel(providerID, modelID); ok {
		return remote
	}
	if generated, ok := ai.LookupModelExact(providerID + "/" + modelID); ok {
		return registry.ResolveGeneratedModel(providerID, modelID, generated)
	}
	entry, _ := registry.Resolve(providerID, modelID)
	return entry
}

// agentDirForModel returns the path passed to NewAuthStorage. We don't have
// flag context inside buildModel, so we re-resolve from the env and default.
// (Honors --agent-dir indirectly via the global resolved at startup.)
var agentDirForModelOverride string

func agentDirForModel() string {
	if agentDirForModelOverride != "" {
		return agentDirForModelOverride
	}
	return codingagent.AgentDir()
}

// formatTokenCount formats a token count as human-readable (e.g., 200000 → "200K").
// Mirrors upstream list-models.ts:formatTokenCount.
func formatTokenCount(count int) string {
	if count >= 1_000_000 {
		m := float64(count) / 1_000_000
		if m == float64(int(m)) { //portlint:allow numbers count is an int and >= 1e6 (or 1e3), so count/1e6 is finite and at most 9.3e12: int() is exact, and m==float64(int(m)) is JS m%1===0 (node differential over 39062 counts incl. 2^53)
			return fmt.Sprintf("%dM", int(m)) //portlint:allow numbers count is an int and >= 1e6 (or 1e3), so count/1e6 is finite and at most 9.3e12: int() is exact, and m==float64(int(m)) is JS m%1===0 (node differential over 39062 counts incl. 2^53)
		}
		return jsstring.ToFixed(m, 1) + "M"
	}
	if count >= 1_000 {
		k := float64(count) / 1_000
		if k == float64(int(k)) { //portlint:allow numbers count is an int and >= 1e6 (or 1e3), so count/1e6 is finite and at most 9.3e12: int() is exact, and m==float64(int(m)) is JS m%1===0 (node differential over 39062 counts incl. 2^53)
			return fmt.Sprintf("%dK", int(k)) //portlint:allow numbers count is an int and >= 1e6 (or 1e3), so count/1e6 is finite and at most 9.3e12: int() is exact, and m==float64(int(m)) is JS m%1===0 (node differential over 39062 counts incl. 2^53)
		}
		return jsstring.ToFixed(k, 1) + "K"
	}
	return fmt.Sprintf("%d", count)
}

// listModelsTimeout is main.ts:951's AbortSignal.timeout(15_000).
const listModelsTimeout = 15 * time.Second // upstream: coding-agent/src/main.ts:main

// listModels is upstream listModels (cli/list-models.ts:29-115) under the bound of main.ts:951. It warns of the runtime's error (:34-37), awaits a
// fresh availability pass (:39, model-runtime.ts:490-506) and prints the table of the registry, which reads the availability that pass published. A
// pass that fails, or that the bound or ctx ends, returns its error and no table line is printed. The caller owns process exit and extension-host
// teardown.
func listModels(ctx context.Context, runtime *coding.ModelRuntime, registry *codingagent.ModelRegistry, search string) error {
	ctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()
	if _, err := runtime.GetAvailable(ctx); err != nil {
		return err
	}
	printModelTable(registry, search)
	return nil
}

// uncaughtListModelsError is the line Node heads its report with for the error of listModels, which Pi's main leaves uncaught: the bound that
// fired is the DOMException of AbortSignal.timeout.
func uncaughtListModelsError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "DOMException [TimeoutError]: The operation was aborted due to timeout"
	}
	return err.Error()
}

// printModelTable prints the models the given registry (already populated with any
// extension-contributed providers) reports as available: exactly modelRuntime.getAvailable()
// (cli/list-models.ts:39-114). A built-in model the runtime's own catalog no longer carries,
// such as one an OpenRouter personalized catalog dropped, is not listed.
func printModelTable(registry *codingagent.ModelRegistry, search string) {
	for _, line := range modelListLines(registry.GetAvailable(), search) {
		fmt.Println(line)
	}
}

// modelListLines is what upstream listModels prints to stdout for the available models: the no-models message, the no-match line, or the
// table sorted by provider then id with each column padded to its widest cell in UTF-16 units (String.prototype.padEnd).
func modelListLines(entries []codingagent.ModelEntry, search string) []string {
	if len(entries) == 0 {
		return []string{codingagent.FormatNoModelsAvailableMessage()}
	}
	// upstream: fuzzyFilter(models, searchPattern, (m) => `${m.provider} ${m.id}`).
	if search != "" {
		entries = tui.FuzzyFilter(entries, search, func(e codingagent.ModelEntry) string {
			return e.ProviderID + " " + e.ModelID
		})
	}
	if len(entries) == 0 {
		return []string{`No models matching "` + search + `"`}
	}
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b codingagent.ModelEntry) int {
		if c := localecompare.Compare(a.ProviderID, b.ProviderID); c != 0 {
			return c
		}
		return localecompare.Compare(a.ModelID, b.ModelID)
	})
	type row [6]string
	rows := []row{{"provider", "model", "context", "max-out", "thinking", "images"}}
	for _, e := range entries {
		thinking, images := "no", "no"
		if e.Reasoning {
			thinking = "yes"
		}
		if slices.Contains(e.Input, "image") {
			images = "yes"
		}
		rows = append(rows, row{e.ProviderID, e.ModelID, formatTokenCount(e.ContextWindow), formatTokenCount(e.MaxTokens), thinking, images})
	}
	var widths [6]int
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = max(widths[i], jsstring.Length(cell))
		}
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		cells := make([]string, len(r))
		for j, cell := range r {
			cells[j] = cell + strings.Repeat(" ", widths[j]-jsstring.Length(cell))
		}
		lines[i] = strings.Join(cells, "  ")
	}
	return lines
}
