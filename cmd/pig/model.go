package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── Model Resolution ─────────────────────────────────────────────────────────

// buildModelFromRef resolves the startup model with resolveStartupModelEntry
// and builds it with coding.BuildModelFromEntry, the constructor /model uses.
func buildModelFromRef(ctx context.Context, providerID, modelID string, services *coding.Services) (*ai.Model, error) {
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

// resolveStartupModelEntry resolves a model as upstream resolveCliModel does:
// the exact catalog entry, else a models.json definition, else the provider's
// default model under the requested id (buildFallbackModel), else the
// registry entry.
func resolveStartupModelEntry(providerID, modelID string, registry *codingagent.ModelRegistry) codingagent.ModelEntry {
	spec := providerID + "/" + modelID
	if generated, ok := ai.LookupModelExact(spec); ok {
		return registry.ResolveGeneratedModel(providerID, modelID, generated)
	}
	entry, entryOK := registry.Resolve(providerID, modelID)
	if entryOK && registry.HasModelDefinition(providerID, modelID) {
		return entry
	}
	if base, ok := providerFallbackModel(providerID); ok {
		fallback := registry.ResolveGeneratedModel(providerID, modelID, base)
		fallback.DisplayName = modelID
		return fallback
	}
	return entry
}

// printModelDiagnostic writes a model-resolution warning to stderr in the
// same shape as upstream reportDiagnostics: a yellow "Warning: <msg>" line.
// Color is applied only when stderr is a terminal, mirroring chalk's
// auto-detection so piped/redirected output stays plain.
func printModelDiagnostic(msg string) {
	text := "Warning: " + msg
	if term.IsTerminal(int(os.Stderr.Fd())) {
		text = "\x1b[33m" + text + "\x1b[39m"
	}
	fmt.Fprintln(os.Stderr, text)
}

// providerFallbackModel returns the catalog model whose capabilities are
// borrowed when a requested model is unknown under providerID: the
// provider's default model when catalogued, else its first catalogued model.
// Mirrors upstream buildFallbackModel's base selection. Returns false when
// the provider has no catalogued models (then the model stays a bare custom
// id with tool-use-only caps and no warning).
func providerFallbackModel(providerID string) (*ai.GeneratedModel, bool) {
	if defID, ok := codingagent.DefaultModelPerProvider()[providerID]; ok {
		if m, ok := ai.LookupModelExact(providerID + "/" + defID); ok {
			return m, true
		}
	}
	models := ai.ListModels(providerID)
	if len(models) > 0 {
		return &models[0], true
	}
	return nil, false
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
		if m == float64(int(m)) {
			return fmt.Sprintf("%dM", int(m))
		}
		return fmt.Sprintf("%.1fM", m)
	}
	if count >= 1_000 {
		k := float64(count) / 1_000
		if k == float64(int(k)) {
			return fmt.Sprintf("%dK", int(k))
		}
		return fmt.Sprintf("%.1fK", k)
	}
	return fmt.Sprintf("%d", count)
}

// printModelList enumerates the models available in the given registry (already
// populated with any extension-contributed providers) plus built-in models with
// configured auth, then prints the catalog. Mirrors upstream listModels, which
// runs after the extension-populated modelRuntime is built. The caller owns
// process exit and extension-host teardown.
func printModelList(registry *codingagent.ModelRegistry, agentDir, search string) {
	// Load error handling mirrors upstream.
	if loadErr := registry.LoadError(); loadErr != "" {
		fmt.Fprintf(os.Stderr, "Warning: errors loading models.json:\n%s\n", loadErr)
	}

	// Auth-filtered models: mirrors upstream modelRegistry.getAvailable().
	entries := registry.GetAvailable()

	// Also include built-in models with configured auth (env keys, auth.json).
	authed := codingagent.AuthenticatedProviders(agentDir)
	for _, m := range ai.ListModels("") {
		if !authed[m.Provider] {
			continue
		}
		// Skip duplicates already covered by registry entries.
		dup := false
		for _, e := range entries {
			if e.ProviderID == m.Provider && e.ModelID == m.ID {
				dup = true
				break
			}
		}
		if !dup {
			entries = append(entries, codingagent.ModelEntry{
				ProviderID:    m.Provider,
				ModelID:       m.ID,
				DisplayName:   m.DisplayName,
				Reasoning:     m.Reasoning,
				Input:         m.Capabilities,
				ContextWindow: m.ContextWindow,
				MaxTokens:     m.MaxOutputTokens,
			})
		}
	}

	if len(entries) == 0 {
		fmt.Println(codingagent.FormatNoModelsAvailableMessage())
		return
	}

	// Apply fuzzy filter if search pattern provided.
	// Mirrors upstream fuzzyFilter(models, searchPattern, (m) => `${m.provider} ${m.id}`).
	if search != "" {
		entries = tui.FuzzyFilter(entries, search, func(e codingagent.ModelEntry) string {
			return e.ProviderID + " " + e.ModelID
		})
	}

	if len(entries) == 0 {
		fmt.Printf("No models matching %q\n", search)
		return
	}

	// Sort by provider, then by model ID. Mirrors upstream.
	slices.SortFunc(entries, func(a, b codingagent.ModelEntry) int {
		if c := strings.Compare(a.ProviderID, b.ProviderID); c != 0 {
			return c
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})

	// Build rows.
	type row struct {
		provider, model, context, maxOut, thinking, images string
	}
	rows := make([]row, len(entries))
	for i, e := range entries {
		thinking := "no"
		if e.Reasoning {
			thinking = "yes"
		}
		images := "no"
		if slices.Contains(e.Input, "image") {
			images = "yes"
		}
		rows[i] = row{
			provider: e.ProviderID,
			model:    e.ModelID,
			context:  formatTokenCount(e.ContextWindow),
			maxOut:   formatTokenCount(e.MaxTokens),
			thinking: thinking,
			images:   images,
		}
	}

	// Calculate column widths.
	headers := row{"provider", "model", "context", "max-out", "thinking", "images"}
	widths := [6]int{
		len(headers.provider), len(headers.model), len(headers.context),
		len(headers.maxOut), len(headers.thinking), len(headers.images),
	}
	for _, r := range rows {
		widths[0] = max(widths[0], len(r.provider))
		widths[1] = max(widths[1], len(r.model))
		widths[2] = max(widths[2], len(r.context))
		widths[3] = max(widths[3], len(r.maxOut))
		widths[4] = max(widths[4], len(r.thinking))
		widths[5] = max(widths[5], len(r.images))
	}

	// Print header.
	fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
		widths[0], headers.provider,
		widths[1], headers.model,
		widths[2], headers.context,
		widths[3], headers.maxOut,
		widths[4], headers.thinking,
		widths[5], headers.images,
	)

	// Print rows.
	for _, r := range rows {
		fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s\n",
			widths[0], r.provider,
			widths[1], r.model,
			widths[2], r.context,
			widths[3], r.maxOut,
			widths[4], r.thinking,
			widths[5], r.images,
		)
	}
}
