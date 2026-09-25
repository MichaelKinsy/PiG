package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── Model Resolution ─────────────────────────────────────────────────────────

// defaultModelPerProviderOrder mirrors upstream model-resolver.ts
// defaultModelPerProvider in declaration order: findInitialModel walks the
// providers in this order.
var defaultModelPerProviderOrder = []struct{ provider, modelID string }{
	{"amazon-bedrock", "us.anthropic.claude-opus-4-6-v1"},
	{"ant-ling", "Ring-2.6-1T"},
	{"anthropic", "claude-opus-4-8"},
	{"openai", "gpt-5.5"},
	{"azure-openai-responses", "gpt-5.4"},
	{"openai-codex", "gpt-5.5"},
	{"radius", "balanced"},
	{"nvidia", "nvidia/nemotron-3-super-120b-a12b"},
	{"deepseek", "deepseek-v4-pro"},
	{"google", "gemini-3.1-pro-preview"},
	{"google-vertex", "gemini-3.1-pro-preview"},
	{"github-copilot", "gpt-5.4"},
	{"openrouter", "moonshotai/kimi-k2.6"},
	{"vercel-ai-gateway", "zai/glm-5.1"},
	{"xai", "grok-4.7"},
	{"groq", "openai/gpt-oss-120b"},
	{"cerebras", "gpt-oss-120b"},
	{"zai", "glm-5.3"},
	{"zai-coding-cn", "glm-5.3"},
	{"mistral", "devstral-medium-latest"},
	{"minimax", "MiniMax-M2.7"},
	{"minimax-cn", "MiniMax-M2.7"},
	{"moonshotai", "kimi-k2.6"},
	{"moonshotai-cn", "kimi-k2.6"},
	{"huggingface", "moonshotai/Kimi-K2.6"},
	{"fireworks", "accounts/fireworks/models/kimi-k2p6"},
	{"together", "moonshotai/Kimi-K2.6"},
	{"baseten", "zai-org/GLM-5.2"},
	{"opencode", "kimi-k2.6"},
	{"opencode-go", "kimi-k2.6"},
	{"kimi-coding", "kimi-for-coding"},
	{"meta", "muse-spark-1.3"},
	{"cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6"},
	{"cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6"},
	{"qwen-token-plan", "qwen3.7-max"},
	{"qwen-token-plan-cn", "qwen3.7-max"},
	{"qwen-token-plan-individual", "qwen3.8-max"},
	{"xiaomi", "mimo-v2.5-pro"},
	{"xiaomi-token-plan-cn", "mimo-v2.5-pro"},
	{"xiaomi-token-plan-ams", "mimo-v2.5-pro"},
	{"xiaomi-token-plan-sgp", "mimo-v2.5-pro"},
}

// defaultModelPerProvider returns the default model id of each known provider.
func defaultModelPerProvider() map[string]string {
	defaults := make(map[string]string, len(defaultModelPerProviderOrder))
	for _, entry := range defaultModelPerProviderOrder {
		defaults[entry.provider] = entry.modelID
	}
	return defaults
}

// buildModelFromRef constructs the Model for one provider and model id. The
// warning reports an id unknown under a known provider.
func buildModelFromRef(ctx context.Context, providerID, modelID string, registry *codingagent.ModelRegistry) (*ai.Model, string, string, error) {
	spec := providerID + "/" + modelID
	entry, entryOK := registry.Resolve(providerID, modelID)
	apiKey := entry.APIKey
	baseURL := entry.BaseURL
	apiKind := ai.API(entry.API)
	if apiKind == "" {
		if generated, ok := ai.LookupModel(spec); ok {
			apiKind = generated.API
		} else if generated, ok := ai.LookupModel(modelID); ok {
			apiKind = generated.API
		}
	}

	// Auto-detect compat if not explicitly set in models.json.
	// Mirrors upstream getCompat() merge: explicit compat overrides detected compat.
	compat := entry.Compat
	if compat == nil {
		compat = ai.DetectCompat(providerID, baseURL)
	}

	var provider ai.Provider
	// A runtime (--api-key) or stored auth.json credential owns the provider
	// ahead of the configured and environment keys, as in upstream
	// resolveProviderAuth; its read or refresh failure surfaces without an
	// env fallback. Radius and Copilot resolve their own stored credentials
	// after a runtime key.
	if _, radius := registry.RadiusOAuth(providerID); radius {
		if key, ok := registry.RuntimeAPIKey(providerID); ok && key != "" {
			apiKey = key
		}
	} else if providerID != "test-faux" && providerID != "github-copilot" {
		stored, ok, err := storedRequestAPIKey(ctx, registry, filepath.Join(agentDirForModel(), "auth.json"), providerID)
		if err != nil {
			return nil, "", "", fmt.Errorf("%s: %w", providerID, err)
		}
		if ok {
			apiKey = stored
		}
	}
	switch providerID {
	case "test-faux":
		if os.Getenv("PIG_TEST_FAUX") != "1" {
			return nil, "", "", fmt.Errorf("test-faux provider is test-only; set PIG_TEST_FAUX=1 to enable it")
		}
		provider = &ai.TestFauxProvider{Scenario: os.Getenv("PIG_TEST_FAUX_SCENARIO")}
	case "github-copilot":
		auth, err := ai.NewAuthStorage(filepath.Join(agentDirForModel(), "auth.json"))
		if err != nil {
			return nil, "", "", fmt.Errorf("github-copilot: %w", err)
		}
		prov, err := ai.NewCopilotProvider(ai.CopilotProviderConfig{
			Auth:      auth,
			Model:     modelID,
			API:       apiKind,
			Reasoning: entry.Reasoning,
			// entry.APIKey carries the env-resolved key (COPILOT_GITHUB_TOKEN/
			// GH_TOKEN/GITHUB_TOKEN) for github-copilot; pass it so the provider
			// can fall back to it when auth.json has no OAuth credential.
			EnvToken: apiKey,
			RuntimeToken: func() (string, bool) {
				return registry.RuntimeAPIKey("github-copilot")
			},
		})
		if err != nil {
			return nil, "", "", err
		}
		provider = prov
	case "openai":
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKind == ai.APIOpenAIResponses {
			provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{
				BaseURL:     baseURL,
				APIKey:      apiKey,
				Model:       modelID,
				ProviderID:  "openai",
				Compat:      aiCloneCompat(compat),
				IsReasoning: entry.Reasoning,
				Env:         entry.Env,
			})
		} else {
			provider = ai.NewOpenAIProvider(ai.OpenAIConfig{
				BaseURL:    baseURL,
				APIKey:     apiKey,
				Model:      modelID,
				ProviderID: "openai",
				Compat:     compat,
				Env:        entry.Env,
			})
		}
	case "openrouter":
		if apiKey == "" {
			apiKey = os.Getenv("OPENROUTER_API_KEY")
		}
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{
			BaseURL:    "https://openrouter.ai/api/v1",
			APIKey:     apiKey,
			Model:      modelID,
			ProviderID: "openrouter",
			Compat:     compat,
			Env:        entry.Env,
			ExtraHeaders: map[string]string{
				"HTTP-Referer": "https://github.com/MichaelKinsy/PiG",
				"X-Title":      "pig",
			},
		})
	case "together":
		if apiKey == "" {
			apiKey = os.Getenv("TOGETHER_API_KEY")
		}
		if baseURL == "" {
			baseURL = "https://api.together.ai/v1"
		}
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{
			BaseURL:    baseURL,
			APIKey:     apiKey,
			Model:      modelID,
			ProviderID: "together",
			Compat:     compat,
			Env:        entry.Env,
		})
	case "groq":
		if apiKey == "" {
			apiKey = os.Getenv("GROQ_API_KEY")
		}
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{
			BaseURL:    "https://api.groq.com/openai/v1",
			APIKey:     apiKey,
			Model:      modelID,
			ProviderID: "groq",
			Compat:     compat,
			Env:        entry.Env,
		})
	case "anthropic":
		// ANTHROPIC_AUTH_TOKEN outranks the key variables; the provider sends
		// it as an Authorization bearer header when no key is set.
		if apiKey == "" && os.Getenv(ai.AnthropicAuthTokenEnv) == "" {
			apiKey = os.Getenv(ai.AnthropicAPIKeyEnv)
		}
		provider = ai.NewAnthropicProvider(ai.AnthropicConfig{
			BaseURL:      baseURL,
			APIKey:       apiKey,
			Model:        modelID,
			ProviderID:   "anthropic",
			ExtraHeaders: entry.Headers,
			Compat:       (*ai.AnthropicMessagesCompat)(aiCloneCompat(compat)),
			Env:          entry.Env,
		})
	case "ollama":
		ollamaURL := baseURL
		if ollamaURL == "" {
			ollamaURL = os.Getenv("OLLAMA_HOST")
		}
		if ollamaURL == "" {
			ollamaURL = "http://localhost:11434/v1"
		}
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{
			BaseURL:    ollamaURL,
			Model:      modelID,
			ProviderID: "ollama",
			Compat:     compat,
			Env:        entry.Env,
		})
	case "azure-openai-responses":
		if apiKey == "" {
			apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
		}
		provider = ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{
			Compat:     aiCloneCompat(compat),
			BaseURL:    baseURL,
			APIKey:     apiKey,
			Model:      modelID,
			ProviderID: providerID,
			Env:        entry.Env,
		})
	case "openai-codex":
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		provider = ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{
			Compat:  aiCloneCompat(compat),
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   modelID,
		})
	case "google":
		// Pi's google provider reads only GEMINI_API_KEY (providers/google.ts).
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
		}
		provider = ai.NewGoogleProvider(ai.GoogleConfig{
			APIKey:     apiKey,
			Model:      modelID,
			ProviderID: providerID,
			BaseURL:    baseURL,
		})
	case "google-vertex":
		// Pi's google-vertex provider reads only GOOGLE_CLOUD_API_KEY and
		// otherwise uses ADC (providers/google-vertex.ts).
		if apiKey == "" {
			apiKey = os.Getenv("GOOGLE_CLOUD_API_KEY")
		}
		provider = ai.NewGoogleVertexProvider(ai.GoogleVertexConfig{
			APIKey:     apiKey,
			Model:      modelID,
			BaseURL:    baseURL,
			ProviderID: providerID,
			Headers:    entry.Headers,
		})
	case "mistral":
		if apiKey == "" {
			apiKey = os.Getenv("MISTRAL_API_KEY")
		}
		provider = ai.NewMistralProvider(ai.MistralConfig{
			APIKey:     apiKey,
			Model:      modelID,
			ProviderID: providerID,
		})
	default:
		if apiKind == ai.APIPiMessages {
			provider = ai.NewPiMessagesProvider(ai.PiMessagesConfig{BaseURL: baseURL, APIKey: apiKey, Model: modelID, ProviderID: providerID, ExtraHeaders: entry.Headers})
			break
		}
		// Treat as OpenAI-compatible with env key
		envKey := strings.ToUpper(providerID) + "_API_KEY"
		if apiKey == "" {
			apiKey = os.Getenv(envKey)
		}
		// Build extra headers from models.json config.
		// If authHeader=true, include API key in Authorization header.
		headers := entry.Headers
		if entry.AuthHeader && apiKey != "" {
			if headers == nil {
				headers = make(map[string]string)
			}
			headers["Authorization"] = "Bearer " + apiKey
		}
		provider = ai.NewOpenAIProvider(ai.OpenAIConfig{
			BaseURL:      baseURL,
			APIKey:       apiKey,
			Model:        modelID,
			ProviderID:   providerID,
			ExtraHeaders: headers,
			Compat:       compat,
			Env:          entry.Env,
			Insecure:     entry.Insecure,
		})
	}

	// Resolve catalog metadata (context window, cost, capabilities).
	// Mirrors upstream resolveCliModel + buildFallbackModel: prefer an exact
	// provider/model catalog entry, then a user-defined models.json model,
	// then a provider-scoped fallback (the provider's default model caps with
	// the requested id) that emits a warning. A model unknown under its
	// provider must NOT silently borrow another provider's same-named caps.
	caps := ai.ModelCapabilities{SupportsToolUse: true}
	displayName := modelID
	var modelWarning string
	switch providerID {
	case "test-faux":
		displayName = "Test Faux"
		apiKind = ai.API("test-faux")
		baseURL = "http://localhost:0"
		caps.ContextWindow = ai.TestFauxContextWindow
		caps.MaxOutputTokens = ai.TestFauxMaxTokens
		caps.SupportsImages = true
	default:
		if m, ok := ai.LookupModelExact(spec); ok {
			caps = m.ToCapabilities()
			if m.DisplayName != "" {
				displayName = m.DisplayName
			}
		} else if entryOK && registry.HasModelDefinition(providerID, modelID) {
			caps = entryCapabilities(entry)
			if entry.DisplayName != "" {
				displayName = entry.DisplayName
			}
		} else if base, ok := providerFallbackModel(providerID); ok {
			caps = base.ToCapabilities()
			displayName = modelID
			modelWarning = fmt.Sprintf("Model %q not found for provider %q. Using custom model id.", modelID, providerID)
		} else if entryOK {
			caps = entryCapabilities(entry)
			if entry.DisplayName != "" {
				displayName = entry.DisplayName
			}
		}
	}

	// Resolve ThinkingLevelMap: prefer the registry entry (user overrides),
	// fall back to the generated catalog. Without this, models like gpt-5-mini
	// whose catalog maps "off" → null get an empty map from the registry entry,
	// making GetSupportedThinkingLevels return the full level set including
	// "off" when the model explicitly disables it.
	tlm := cloneThinkingLevelMap(entry.ThinkingLevelMap)
	if len(tlm) == 0 {
		if m, ok := ai.LookupModelExact(spec); ok {
			tlm = cloneThinkingLevelMap(m.ThinkingLevelMap)
		} else if base, ok := providerFallbackModel(providerID); ok {
			tlm = cloneThinkingLevelMap(base.ThinkingLevelMap)
		}
	}

	return &ai.Model{
		ID:               modelID,
		DisplayName:      displayName,
		Provider:         provider,
		Capabilities:     caps,
		ThinkingLevelMap: tlm,
		ProviderMeta: ai.ProviderMetadata{
			ProviderID: providerID,
			API:        apiKind,
			BaseURL:    baseURL,
			Headers:    aiCloneHeaders(entry.Headers),
			Compat:     aiCloneCompat(compat),
			Reasoning:  entry.Reasoning,
		},
	}, "", modelWarning, nil
}

// storedRequestAPIKey resolves the request key a runtime key (--api-key) or
// a stored auth.json credential supplies for providerID. ok is false when
// nothing is stored; an unopenable auth store counts as nothing stored.
func storedRequestAPIKey(ctx context.Context, registry *codingagent.ModelRegistry, authPath, providerID string) (key string, ok bool, err error) {
	if key, ok := registry.RuntimeAPIKey(providerID); ok {
		return key, true, nil
	}
	auth, err := ai.NewAuthStorage(authPath)
	if err != nil {
		return "", false, nil
	}
	if _, stored, err := auth.GetRaw(providerID); err != nil || !stored {
		return "", false, err
	}
	key, _, err = ai.ResolveStoredAPIKeyFromStorageContext(ctx, auth, providerID)
	return key, true, err
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

// entryCapabilities lifts a registry ModelEntry (a models.json-defined model
// or provider config) into runtime capabilities.
func entryCapabilities(entry codingagent.ModelEntry) ai.ModelCapabilities {
	caps := ai.ModelCapabilities{
		SupportsToolUse:     true,
		ContextWindow:       entry.ContextWindow,
		MaxOutputTokens:     entry.MaxTokens,
		InputCostPer1M:      entry.InputCost,
		OutputCostPer1M:     entry.OutputCost,
		CacheReadCostPer1M:  entry.CacheReadCost,
		CacheWriteCostPer1M: entry.CacheWriteCost,
	}
	if entry.Reasoning {
		caps.MaxThinking = ai.ThinkingHigh
		for _, level := range []ai.ThinkingLevel{ai.ThinkingXHigh, ai.ThinkingMax} {
			if mapped, ok := entry.ThinkingLevelMap[level]; ok && mapped != nil {
				caps.MaxThinking = level
			}
		}
	}
	if slices.Contains(entry.Input, "image") {
		caps.SupportsImages = true
	}
	return caps
}

// providerFallbackModel returns the catalog model whose capabilities are
// borrowed when a requested model is unknown under providerID: the
// provider's default model when catalogued, else its first catalogued model.
// Mirrors upstream buildFallbackModel's base selection. Returns false when
// the provider has no catalogued models (then the model stays a bare custom
// id with tool-use-only caps and no warning).
func providerFallbackModel(providerID string) (*ai.GeneratedModel, bool) {
	if defID, ok := defaultModelPerProvider()[providerID]; ok {
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

func cloneThinkingLevelMap(in ai.ThinkingLevelMap) ai.ThinkingLevelMap {
	if len(in) == 0 {
		return nil
	}
	out := make(ai.ThinkingLevelMap, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		value := *v
		out[k] = &value
	}
	return out
}

func aiCloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func aiCloneCompat(in *ai.OpenAICompat) *ai.ModelCompat {
	if in == nil {
		return nil
	}
	out := ai.ModelCompat(*in)
	if in.OpenRouterRouting != nil {
		out.OpenRouterRouting = make(map[string]any, len(in.OpenRouterRouting))
		maps.Copy(out.OpenRouterRouting, in.OpenRouterRouting)
	}
	if in.VercelGatewayRouting != nil {
		out.VercelGatewayRouting = make(map[string]any, len(in.VercelGatewayRouting))
		maps.Copy(out.VercelGatewayRouting, in.VercelGatewayRouting)
	}
	return &out
}

// agentDirForModel returns the path passed to NewAuthStorage. We don't have
// flag context inside buildModel, so we re-resolve from the env and default.
// (Honors --agent-dir indirectly via the global resolved at startup.)
var agentDirForModelOverride string

func agentDirForModel() string {
	if agentDirForModelOverride != "" {
		return agentDirForModelOverride
	}
	if envDir := os.Getenv("PIG_CODING_AGENT_DIR"); envDir != "" {
		return envDir
	}
	return codingagent.DefaultAgentDir()
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
		fmt.Println("No models available. Set API keys in environment variables.")
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
