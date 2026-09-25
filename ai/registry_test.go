package ai

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/pigversion"
)

// TestImageCatalogPin0871 counts come from @earendil-works/pi-ai 0.87.1
// dist/image-models.generated.js.
func TestImageCatalogPin0871(t *testing.T) {
	if got := len(GeneratedImageModels); got != 55 {
		t.Fatalf("image catalog size = %d, want published pi-ai 0.87.1 image-catalog count 55", got)
	}
	model, ok := GetImageModel("openrouter", "microsoft/mai-image-2.5-pro")
	if !ok {
		t.Fatal("0.83.0 image model microsoft/mai-image-2.5-pro missing")
	}
	if model.Name != "Microsoft AI: MAI-Image-2.5 Pro" || model.Cost.Input != 5 {
		t.Fatalf("image model = %+v", model)
	}
	if _, ok := GetImageModel("openrouter", "meta/muse-image"); !ok {
		t.Fatal("0.86.1 image model meta/muse-image missing")
	}
	added, ok := GetImageModel("openrouter", "inclusionai/ming-image-0.1-design")
	if !ok || added.Name != "inclusionAI: Ming Image 0.1 Design" {
		t.Fatalf("0.87.1 image model inclusionai/ming-image-0.1-design = %+v, %t", added, ok)
	}
}

func TestRegistryHasModels(t *testing.T) {
	const want = 1495 // Count of the exact published @earendil-works/pi-ai 0.87.1 MODELS export.
	if got := len(GeneratedModels); got != want {
		t.Fatalf("catalog size = %d want %d", got, want)
	}
}

// TestCatalogPin0871 binds the generated catalog to the exact published Pi
// 0.87.1 package. The expected values come from @earendil-works/pi-ai 0.87.1
// dist/models.generated.js, not from the Go generator output.
func TestCatalogPin0871(t *testing.T) {
	if v := UpstreamVersionString(); v != "0.87.1" {
		t.Fatalf("pin = %q want 0.87.1", v)
	}
	opus, ok := LookupModelExact("anthropic/claude-opus-5-5")
	if !ok || opus.API != APIAnthropicMessages || opus.ContextWindow != 1000000 {
		t.Fatalf("0.87.1 Claude Opus 5.5 model = %+v, %t", opus, ok)
	}
	want := &ModelInputLimits{
		MaxRequestBytes: 33554432,
		Images: &ModelImageInputLimits{
			MaxPerRequest: 600,
			Resize:        &ModelImageResizeOptions{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 4718592, JPEGQuality: 80},
		},
	}
	if !reflect.DeepEqual(opus.InputLimits, want) {
		t.Fatalf("0.87.1 Claude Opus 5.5 inputLimits = %#v, want %#v", opus.InputLimits, want)
	}
	for _, id := range []string{"xai/grok-4.7", "openai/gpt-6-sol", "openai/gpt-6-luna"} {
		if _, ok := LookupModelExact(id); !ok {
			t.Fatalf("0.87.1 catalog is missing %s", id)
		}
	}
	if _, ok := LookupModelExact("anthropic/anthropic/claude-3.5-haiku"); ok {
		t.Fatal(`retired model "anthropic/claude-3.5-haiku" is still in the 0.87.1 catalog`)
	}
}

func TestGeneratedInputLimitsAreNotAliasedByToModel(t *testing.T) {
	opus, ok := LookupModelExact("anthropic/claude-opus-5-5")
	if !ok || opus.InputLimits == nil || opus.InputLimits.Images == nil || opus.InputLimits.Images.Resize == nil {
		t.Fatalf("Claude Opus 5.5 inputLimits missing: %+v", opus)
	}
	model := opus.ToModel()
	model.InputLimits.MaxRequestBytes = 1
	model.InputLimits.Images.MaxPerRequest = 1
	model.InputLimits.Images.Resize.MaxWidth = 1
	if opus.InputLimits.MaxRequestBytes != 33554432 || opus.InputLimits.Images.MaxPerRequest != 600 || opus.InputLimits.Images.Resize.MaxWidth != 2000 {
		t.Fatalf("ToModel aliased catalog inputLimits: %#v", opus.InputLimits)
	}
}

// Pi's getBuiltinProviders and builtinModels include the published radius
// catalog (RADIUS_MODELS); pi-messages is an implemented runtime API.
func TestRuntimeDiscoveryIncludesRadiusCatalog(t *testing.T) {
	generatedRadius := 0
	for _, model := range GeneratedModels {
		if model.API == APIPiMessages {
			generatedRadius++
		}
	}
	if generatedRadius != 30 {
		t.Fatalf("generated pi-messages models = %d, want published pi-ai 0.87.1 count 30", generatedRadius)
	}
	if providers := ListProviders(); !slices.Contains(providers, "radius") {
		t.Fatalf("ListProviders() omits radius: %v", providers)
	}
	if runtimeProviders := ListRuntimeProviders(); !slices.Contains(runtimeProviders, "radius") {
		t.Fatalf("ListRuntimeProviders() omits radius: %v", runtimeProviders)
	}
	models := ListModels("radius")
	if len(models) != generatedRadius {
		t.Fatalf("ListModels(radius) = %d models, want %d", len(models), generatedRadius)
	}
	for _, model := range models {
		if model.API != APIPiMessages {
			t.Fatalf("radius model %s API = %q", model.ID, model.API)
		}
	}
	balanced, ok := LookupModelExact("radius/balanced")
	if !ok || balanced.Enabled == nil || !*balanced.Enabled || balanced.Lab != "Moonshot AI" || len(balanced.Providers) != 3 {
		t.Fatalf("Radius generated evidence = %+v, %t", balanced, ok)
	}
}

func TestLookupByFullyQualified(t *testing.T) {
	m, ok := LookupModel("openai/gpt-4o")
	if !ok {
		t.Fatal("openai/gpt-4o not found")
	}
	if m.Provider != "openai" {
		t.Fatalf("provider = %q want openai", m.Provider)
	}
	if m.ContextWindow == 0 {
		t.Errorf("gpt-4o context window = 0 (parser regression)")
	}
}

func TestLookupByBareID(t *testing.T) {
	if _, ok := LookupModel("gpt-4o"); !ok {
		t.Fatal("bare 'gpt-4o' must resolve")
	}
}

func TestLookupMiss(t *testing.T) {
	if m, ok := LookupModel("not/a-real-model-xyz"); ok {
		t.Fatalf("expected miss, got %+v", m)
	}
	if _, ok := LookupModel(""); ok {
		t.Fatal("empty spec must miss")
	}
}

// LookupModelExact resolves only the fully-qualified provider/id key and must
// NOT borrow another provider's same-named model the way LookupModel does.
// This is the property model resolution relies on to avoid giving
// github-copilot/gpt-4o openai gpt-4o's capabilities.
func TestLookupModelExact_NoCrossProviderBorrow(t *testing.T) {
	// LookupModel cross-resolves the bare id (openai's gpt-4o)...
	if _, ok := LookupModel("github-copilot/gpt-4o"); !ok {
		t.Fatal("precondition: LookupModel should cross-resolve github-copilot/gpt-4o to openai's")
	}
	// ...but LookupModelExact must not, since copilot has no gpt-4o.
	if _, ok := LookupModelExact("github-copilot/gpt-4o"); ok {
		t.Error("LookupModelExact resolved github-copilot/gpt-4o; copilot has no gpt-4o")
	}
	// A genuine provider/model still resolves.
	if _, ok := LookupModelExact("github-copilot/gpt-5.4"); !ok {
		t.Error("LookupModelExact should resolve github-copilot/gpt-5.4")
	}
	if _, ok := LookupModelExact(""); ok {
		t.Error("empty spec must miss")
	}
}

func TestToCapabilities(t *testing.T) {
	m, ok := LookupModel("openai/gpt-4o")
	if !ok {
		t.Fatal("gpt-4o missing")
	}
	caps := m.ToCapabilities()
	if !caps.SupportsToolUse {
		t.Error("SupportsToolUse should be true")
	}
	if caps.ContextWindow != m.ContextWindow {
		t.Errorf("ContextWindow mismatch %d vs %d", caps.ContextWindow, m.ContextWindow)
	}
	if caps.InputCostPer1M != m.InputCostPerMTokens {
		t.Errorf("InputCost mismatch")
	}
}

func TestGeneratedCatalogThinkingLevelMap(t *testing.T) {
	t.Run("anthropic opus exposes max mapping", func(t *testing.T) {
		m, ok := LookupModel("anthropic/claude-opus-4-6")
		if !ok {
			t.Fatal("anthropic/claude-opus-4-6 missing")
		}
		if m.ThinkingLevelMap == nil {
			t.Fatal("ThinkingLevelMap = nil")
		}
		// 0.81.1 renamed the top Anthropic thinking key from xhigh to max
		// (thinkingLevelMap {max: max}).
		mapped, ok := m.ThinkingLevelMap[ThinkingMax]
		if !ok || mapped == nil || *mapped != "max" {
			t.Fatalf("max mapping = %v, %t; want max", mapped, ok)
		}
		levels := GetSupportedThinkingLevels(m.ToModel())
		if len(levels) == 0 || levels[len(levels)-1] != ThinkingMax {
			t.Fatalf("supported thinking levels = %v, want max enabled", levels)
		}
	})

	t.Run("gpt-5 disables off via explicit nil mapping", func(t *testing.T) {
		m, ok := LookupModel("openai/gpt-5")
		if !ok {
			t.Fatal("openai/gpt-5 missing")
		}
		mapped, ok := m.ThinkingLevelMap[ThinkingOff]
		if !ok || mapped != nil {
			t.Fatalf("off mapping = %v, %t; want explicit nil", mapped, ok)
		}
		levels := GetSupportedThinkingLevels(m.ToModel())
		want := []ThinkingLevel{ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh}
		if len(levels) != len(want) {
			t.Fatalf("supported thinking levels = %v, want %v", levels, want)
		}
		for i := range want {
			if levels[i] != want[i] {
				t.Fatalf("supported thinking levels = %v, want %v", levels, want)
			}
		}
	})
}

func TestGeneratedCatalog0861TranscriptMetadata(t *testing.T) {
	generated, ok := LookupModelExact("anthropic/claude-fable-5")
	if !ok {
		t.Fatal("anthropic/claude-fable-5 missing")
	}
	if generated.PromptCache["short"] != 300 || generated.PromptCache["long"] != 3600 {
		t.Fatalf("prompt cache = %v", generated.PromptCache)
	}
	compat := generated.Compat
	if compat == nil || compat.SupportsMidConvoSystemMessages == nil || !*compat.SupportsMidConvoSystemMessages || compat.SupportsMidConvoToolChanges == nil || !*compat.SupportsMidConvoToolChanges {
		t.Fatalf("mid-conversation compat = %+v", compat)
	}
	if len(compat.AllowedFallbackModels) != 2 || compat.AllowedFallbackModels[0].Model != "claude-opus-4-8" {
		t.Fatalf("allowed fallback models = %+v", compat.AllowedFallbackModels)
	}

	model := generated.ToModel()
	model.PromptCache["short"] = 1
	model.ProviderMeta.Compat.AllowedFallbackModels[0].Model = "changed"
	if generated.PromptCache["short"] != 300 || generated.Compat.AllowedFallbackModels[0].Model != "claude-opus-4-8" {
		t.Fatal("ToModel returned aliases into generated catalog metadata")
	}
}

func TestListModelsFilter(t *testing.T) {
	all := ListModels("")
	openai := ListModels("openai")
	if len(openai) == 0 {
		t.Fatal("no openai models")
	}
	if len(openai) >= len(all) {
		t.Fatalf("openai-only subset (%d) should be smaller than all (%d)", len(openai), len(all))
	}
	for _, m := range openai {
		if m.Provider != "openai" {
			t.Fatalf("filter leaked %q", m.Provider)
		}
	}
}

func TestGeneratedCatalogV0741RemovalsAndRenames(t *testing.T) {
	removed := []string{
		"amazon-bedrock/amazon.nova-premier-v1:0",
		"amazon-bedrock/anthropic.claude-3-5-haiku-20241022-v1:0",
		"amazon-bedrock/anthropic.claude-3-5-sonnet-20240620-v1:0",
		"amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
		"amazon-bedrock/anthropic.claude-3-7-sonnet-20250219-v1:0",
		"amazon-bedrock/anthropic.claude-3-haiku-20240307-v1:0",
		"amazon-bedrock/anthropic.claude-opus-4-20250514-v1:0",
		"amazon-bedrock/anthropic.claude-sonnet-4-20250514-v1:0",
	}
	for _, spec := range removed {
		t.Run(spec, func(t *testing.T) {
			if m, ok := LookupModel(spec); ok {
				t.Fatalf("LookupModel(%q) = %+v, true; want miss", spec, m)
			}
		})
	}

	renamed := []struct {
		spec        string
		wantName    string
		wantContext int
	}{
		{
			spec:        "amazon-bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0",
			wantName:    "Claude Sonnet 4.5",
			wantContext: 200000,
		},
		{
			spec:        "amazon-bedrock/anthropic.claude-sonnet-4-6",
			wantName:    "Claude Sonnet 4.6",
			wantContext: 1000000,
		},
	}
	for _, tt := range renamed {
		t.Run(tt.spec, func(t *testing.T) {
			m, ok := LookupModel(tt.spec)
			if !ok {
				t.Fatalf("LookupModel(%q) miss", tt.spec)
			}
			if m.DisplayName != tt.wantName {
				t.Fatalf("display name = %q want %q", m.DisplayName, tt.wantName)
			}
			if m.ContextWindow != tt.wantContext {
				t.Fatalf("context window = %d want %d", m.ContextWindow, tt.wantContext)
			}
		})
	}
}

func TestGeneratedCatalogUpgradeSpotChecks(t *testing.T) {
	tests := []struct {
		spec                string
		wantProvider        string
		wantName            string
		wantAPI             API
		wantMaxTokens       int
		wantContext         int
		wantInputCost       float64
		wantOutputCost      float64
		wantCacheReadCost   float64
		wantCacheWriteCost  float64
		wantReasoning       bool
		wantCapabilities    []string
		wantBaseURL         string
		wantThinkingEntries map[ThinkingLevel]*string
	}{
		{
			spec:               "amazon-bedrock/au.anthropic.claude-opus-4-6-v1",
			wantProvider:       "amazon-bedrock",
			wantName:           "AU Anthropic Claude Opus 4.6",
			wantAPI:            "bedrock-converse-stream",
			wantMaxTokens:      128000,
			wantContext:        1000000,
			wantInputCost:      5.5,
			wantOutputCost:     27.5,
			wantCacheReadCost:  0.55,
			wantCacheWriteCost: 6.875,
			wantReasoning:      true,
			wantCapabilities:   []string{"text", "image"},
			wantBaseURL:        "https://bedrock-runtime.us-east-1.amazonaws.com",
			wantThinkingEntries: map[ThinkingLevel]*string{
				ThinkingMax: new("max"),
			},
		},
		{
			spec:               "amazon-bedrock/au.anthropic.claude-sonnet-4-6",
			wantProvider:       "amazon-bedrock",
			wantName:           "AU Anthropic Claude Sonnet 4.6",
			wantAPI:            "bedrock-converse-stream",
			wantMaxTokens:      128000,
			wantContext:        1000000,
			wantInputCost:      3.3,
			wantOutputCost:     16.5,
			wantCacheReadCost:  0.33,
			wantCacheWriteCost: 4.125,
			wantReasoning:      true,
			wantCapabilities:   []string{"text", "image"},
			wantBaseURL:        "https://bedrock-runtime.us-east-1.amazonaws.com",
			wantThinkingEntries: map[ThinkingLevel]*string{
				ThinkingMax: new("max"),
			},
		},
		{
			spec:               "azure-openai-responses/gpt-5.5-pro",
			wantProvider:       "azure-openai-responses",
			wantName:           "GPT-5.5 Pro",
			wantAPI:            "azure-openai-responses",
			wantMaxTokens:      128000,
			wantContext:        1050000,
			wantInputCost:      30,
			wantOutputCost:     180,
			wantCacheReadCost:  0,
			wantCacheWriteCost: 0,
			wantReasoning:      true,
			wantCapabilities:   []string{"text", "image"},
			wantBaseURL:        "",
			wantThinkingEntries: map[ThinkingLevel]*string{
				ThinkingLevel("low"):     nil,
				ThinkingLevel("minimal"): nil,
				ThinkingLevel("off"):     nil,
				ThinkingXHigh:            new("xhigh"),
			},
		},
		{
			spec:               "cloudflare-ai-gateway/claude-opus-4.5",
			wantProvider:       "cloudflare-ai-gateway",
			wantName:           "Claude Opus 4.5 (latest)",
			wantAPI:            "anthropic-messages",
			wantMaxTokens:      64000,
			wantContext:        200000,
			wantInputCost:      5,
			wantOutputCost:     25,
			wantCacheReadCost:  0.5,
			wantCacheWriteCost: 6.25,
			wantReasoning:      true,
			wantCapabilities:   []string{"text", "image"},
			wantBaseURL:        "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic",
		},
		{
			spec:               "github-copilot/gpt-5.4-mini",
			wantProvider:       "github-copilot",
			wantName:           "GPT-5.4 mini",
			wantAPI:            "openai-responses",
			wantMaxTokens:      128000,
			wantContext:        400000,
			wantInputCost:      0.75,
			wantOutputCost:     4.5,
			wantCacheReadCost:  0.075,
			wantCacheWriteCost: 0,
			wantReasoning:      true,
			wantCapabilities:   []string{"text", "image"},
			wantBaseURL:        "https://api.individual.githubcopilot.com",
			wantThinkingEntries: map[ThinkingLevel]*string{
				ThinkingOff:     nil,
				ThinkingMinimal: new("low"),
				ThinkingLow:     new("low"),
				ThinkingMedium:  new("medium"),
				ThinkingHigh:    new("high"),
				ThinkingXHigh:   new("xhigh"),
				ThinkingMax:     nil,
			},
		},
		{
			spec:               "openai-codex/gpt-5.3-codex-spark",
			wantProvider:       "openai-codex",
			wantName:           "GPT-5.3 Codex Spark",
			wantAPI:            "openai-codex-responses",
			wantMaxTokens:      128000,
			wantContext:        128000,
			wantInputCost:      1.75,
			wantOutputCost:     14,
			wantCacheReadCost:  0.175,
			wantCacheWriteCost: 0,
			wantReasoning:      true,
			wantCapabilities:   []string{"text"},
			wantBaseURL:        "https://chatgpt.com/backend-api",
			wantThinkingEntries: map[ThinkingLevel]*string{
				ThinkingMinimal: new("low"),
				ThinkingXHigh:   new("xhigh"),
			},
		},
		{
			spec:               "azure-openai-responses/gpt-5.6-luna",
			wantProvider:       "azure-openai-responses",
			wantName:           "GPT-5.6 Luna",
			wantAPI:            "azure-openai-responses",
			wantMaxTokens:      128000,
			wantContext:        1050000,
			wantInputCost:      0.2,
			wantOutputCost:     1.2,
			wantCacheReadCost:  0.02,
			wantCacheWriteCost: 0.25,
			wantReasoning:      true,
			wantCapabilities:   []string{"text", "image"},
			wantBaseURL:        "",
			wantThinkingEntries: map[ThinkingLevel]*string{
				ThinkingOff:   nil,
				ThinkingXHigh: new("xhigh"),
				ThinkingMax:   new("max"),
			},
		},
		{
			spec:               "azure-openai-responses/gpt-realtime-2.1",
			wantProvider:       "azure-openai-responses",
			wantName:           "GPT-Realtime-2.1",
			wantAPI:            "azure-openai-responses",
			wantMaxTokens:      32000,
			wantContext:        128000,
			wantInputCost:      4,
			wantOutputCost:     24,
			wantCacheReadCost:  0.4,
			wantCacheWriteCost: 0,
			wantReasoning:      true,
			wantCapabilities:   []string{"text", "image"},
			wantBaseURL:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			m, ok := LookupModel(tt.spec)
			if !ok {
				t.Fatalf("LookupModel(%q) miss", tt.spec)
			}
			if m.Provider != tt.wantProvider {
				t.Fatalf("provider = %q want %q", m.Provider, tt.wantProvider)
			}
			if m.DisplayName != tt.wantName {
				t.Fatalf("display name = %q want %q", m.DisplayName, tt.wantName)
			}
			if m.API != tt.wantAPI {
				t.Fatalf("api = %q want %q", m.API, tt.wantAPI)
			}
			if m.MaxOutputTokens != tt.wantMaxTokens {
				t.Fatalf("max tokens = %d want %d", m.MaxOutputTokens, tt.wantMaxTokens)
			}
			if m.ContextWindow != tt.wantContext {
				t.Fatalf("context window = %d want %d", m.ContextWindow, tt.wantContext)
			}
			if m.InputCostPerMTokens != tt.wantInputCost {
				t.Fatalf("input cost = %v want %v", m.InputCostPerMTokens, tt.wantInputCost)
			}
			if m.OutputCostPerMTokens != tt.wantOutputCost {
				t.Fatalf("output cost = %v want %v", m.OutputCostPerMTokens, tt.wantOutputCost)
			}
			if m.CacheReadCost != tt.wantCacheReadCost {
				t.Fatalf("cache read = %v want %v", m.CacheReadCost, tt.wantCacheReadCost)
			}
			if m.CacheWriteCost != tt.wantCacheWriteCost {
				t.Fatalf("cache write = %v want %v", m.CacheWriteCost, tt.wantCacheWriteCost)
			}
			if m.Reasoning != tt.wantReasoning {
				t.Fatalf("reasoning = %t want %t", m.Reasoning, tt.wantReasoning)
			}
			if m.BaseURL != tt.wantBaseURL {
				t.Fatalf("base URL = %q want %q", m.BaseURL, tt.wantBaseURL)
			}
			if len(m.Capabilities) != len(tt.wantCapabilities) {
				t.Fatalf("capabilities len = %d want %d (%v)", len(m.Capabilities), len(tt.wantCapabilities), m.Capabilities)
			}
			for i, cap := range tt.wantCapabilities {
				if m.Capabilities[i] != cap {
					t.Fatalf("capabilities[%d] = %q want %q (all=%v)", i, m.Capabilities[i], cap, m.Capabilities)
				}
			}
			if tt.wantThinkingEntries == nil {
				if len(m.ThinkingLevelMap) != 0 {
					t.Fatalf("thinking level map = %v want empty", m.ThinkingLevelMap)
				}
			} else {
				if len(m.ThinkingLevelMap) != len(tt.wantThinkingEntries) {
					t.Fatalf("thinking level map len = %d want %d (%v)", len(m.ThinkingLevelMap), len(tt.wantThinkingEntries), m.ThinkingLevelMap)
				}
				for level, want := range tt.wantThinkingEntries {
					got, ok := m.ThinkingLevelMap[level]
					if !ok {
						t.Fatalf("thinking level %q missing in %v", level, m.ThinkingLevelMap)
					}
					if want == nil {
						if got != nil {
							t.Fatalf("thinking level %q = %v want nil", level, got)
						}
						continue
					}
					if got == nil || *got != *want {
						t.Fatalf("thinking level %q = %v want %q", level, got, *want)
					}
				}
			}
		})
	}
}

// TestCodegenByteIdentical re-runs both model catalog generators against temp
// paths and checks the outputs match the committed files byte-for-byte.
// Uses the same source resolution as the upgrade script: prefer the
// installed pi binary's model file (parity ground truth), fall back to
// the git-tag mirror.
func TestCodegenByteIdentical(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Skip("can't find repo root:", err)
	}
	src := resolveModelSource(repoRoot)
	if src == "" {
		t.Skip("no model source found (upstream mirror not present and pi binary not installed)")
	}
	committed, err := os.ReadFile(filepath.Join(repoRoot, "ai/models_generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "models_generated.go")
	cmd := exec.Command("go", "run", "./cmd/gen-models", "-src", src, "-out", tmp)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gen-models failed: %v\n%s", err, out)
	}
	regenerated, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(normalizeSourceComment(committed), normalizeSourceComment(regenerated)) {
		t.Fatalf("models_generated.go drift\ncommitted=%d bytes regenerated=%d bytes: re-run `go generate ./ai/...`",
			len(committed), len(regenerated))
	}
	imageSource := filepath.Join(filepath.Dir(src), "image-models.generated.js")
	if _, err := os.Stat(imageSource); err != nil {
		t.Fatalf("published image catalog: %v", err)
	}
	committedImages, err := os.ReadFile(filepath.Join(repoRoot, "ai/image_models_generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	tmpImages := filepath.Join(t.TempDir(), "image_models_generated.go")
	cmd = exec.Command("go", "run", "./cmd/gen-image-models", "-src", imageSource, "-out", tmpImages)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen-image-models failed: %v\n%s", err, out)
	}
	regeneratedImages, err := os.ReadFile(tmpImages)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committedImages, regeneratedImages) {
		t.Fatalf("image_models_generated.go drift: re-run `go generate ./ai/...`")
	}
}

// sourceCommentRE matches the generated header's `// Source: models.generated.<ext>`
// line. cmd/gen-models embeds basename(src), which keeps the file extension:
// `go generate` (doc.go) feeds the .upstream mirror's models.generated.ts, while
// this test feeds the installed pi binary's compiled models.generated.js as the
// parity ground truth. The extension is metadata, not model data; the 979-model
// catalog below must still match byte-for-byte. Normalizing only this line keeps
// the comparison a true drift check on model data while tolerating the two
// equivalent upstream sources' differing extensions.
var sourceCommentRE = regexp.MustCompile(`(?m)^// Source: models\.generated\.(?:ts|js)$`)

func normalizeSourceComment(b []byte) []byte {
	return sourceCommentRE.ReplaceAll(b, []byte("// Source: models.generated"))
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// resolveModelSource returns the path to the parity-authoritative model
// file. Prefers the installed pi binary's bundled model file (what
// `make parity` compares against) over the git-tag mirror, since npm
// publishes can include model spec updates after the tag is cut.
func resolveModelSource(repoRoot string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return resolveGitTagSource(repoRoot)
	}
	ver := UpstreamVersionString()
	for _, pkg := range []string{
		"npm-earendil-works-pi-coding-agent",
		"npm-mariozechner-pi-coding-agent",
	} {
		for _, sub := range []string{
			"@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai",
			"@mariozechner/pi-coding-agent/node_modules/@mariozechner/pi-ai",
		} {
			p := filepath.Join(home, ".local", "share", "mise", "installs",
				pkg, ver, "lib", "node_modules", filepath.FromSlash(sub),
				"dist", "models.generated.js")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return resolveGitTagSource(repoRoot)
}

func resolveGitTagSource(repoRoot string) string {
	p := filepath.Join(repoRoot, ".upstream", "current", "packages", "ai", "src", "models.generated.ts")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// UpstreamVersionString returns the shared upstream version without importing
// coding, which would create a cycle.
func UpstreamVersionString() string {
	return pigversion.UpstreamVersion
}
