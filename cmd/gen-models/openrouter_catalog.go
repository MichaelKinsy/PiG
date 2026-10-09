package main

// Ports packages/ai/scripts/openrouter-catalog.ts.

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/modelgen"
)

type openRouterPricing struct {
	Prompt          string                      `json:"prompt"`
	Completion      string                      `json:"completion"`
	InputCacheRead  string                      `json:"input_cache_read"`
	InputCacheWrite string                      `json:"input_cache_write"`
	Overrides       []openRouterPricingOverride `json:"overrides"`
}

type openRouterModelListItem struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	SupportedParameters []string `json:"supported_parameters"`
	Architecture        *struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	Pricing     *openRouterPricing `json:"pricing"`
	TopProvider *struct {
		ContextLength       int `json:"context_length"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	ContextLength int                                   `json:"context_length"`
	Reasoning     *modelgen.OpenRouterReasoningMetadata `json:"reasoning"`
}

// openRouterPricingOverride is a conditional price: min_prompt_tokens selects prompt-length pricing, the utc_* fields time-of-day or weekday pricing.
// The utc_* members keep their raw JSON because only their presence matters (openrouter-catalog.ts cost compares them with undefined).
type openRouterPricingOverride struct {
	MinPromptTokens *float64        `json:"min_prompt_tokens"`
	UTCStart        json.RawMessage `json:"utc_start"`
	UTCEnd          json.RawMessage `json:"utc_end"`
	UTCDays         json.RawMessage `json:"utc_days"`
	Prompt          string          `json:"prompt"`
	Completion      string          `json:"completion"`
	InputCacheRead  string          `json:"input_cache_read"`
	InputCacheWrite string          `json:"input_cache_write"`
}

type openRouterImageModel struct {
	Type     string   `json:"type"`
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	API      string   `json:"api"`
	Provider string   `json:"provider"`
	BaseURL  string   `json:"baseUrl"`
	Input    []string `json:"input"`
	Output   []string `json:"output"`
	Cost     jsonCost `json:"cost"`
}

type openRouterClassifierModel struct {
	Type          string   `json:"type"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	API           string   `json:"api"`
	Provider      string   `json:"provider"`
	BaseURL       string   `json:"baseUrl"`
	Input         []string `json:"input"`
	Cost          jsonCost `json:"cost"`
	ContextWindow int      `json:"contextWindow"`
}

type openRouterCatalog struct {
	Chat        []generatedCatalogModel
	Images      []openRouterImageModel
	Classifiers []openRouterClassifierModel
}

const (
	openRouterBaseURL          = "https://openrouter.ai/api/v1"
	openRouterAnthropicBaseURL = "https://openrouter.ai/api"
)

var (
	openRouterAnthropicID = regexp.MustCompile(`^anthropic/`)
	// parseFloat reads the longest decimal prefix and ignores the rest.
	openRouterFloatPrefix = regexp.MustCompile(`^[+-]?(?:Infinity|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)
)

const jsWhitespace = " \t\n\r\v\f\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// roundCost is Number(value.toFixed(6)): toFixed rounds the exact binary magnitude to a multiple of 1e-6, ties toward the
// larger magnitude, and reattaches the sign.
func roundCost(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	scaled := new(big.Rat).Mul(new(big.Rat).SetFloat64(math.Abs(value)), big.NewRat(1_000_000, 1))
	scaled.Add(scaled, big.NewRat(1, 2))
	floor := new(big.Int).Div(scaled.Num(), scaled.Denom())
	result, _ := strconv.ParseFloat(floor.String()+"e-6", 64)
	return math.Copysign(result, value)
}

// openRouterCost converts $/token pricing to $/million tokens (openrouter-catalog.ts cost). Prompt-length overrides become request-wide tiers whose
// missing rates keep the base price; time-of-day overrides are skipped because a cost cannot express them.
func openRouterCost(model openRouterModelListItem) jsonCost {
	// perMillion is openrouter-catalog.ts perMillion: an empty value takes the fallback.
	perMillion := func(value string, fallback float64) float64 {
		if value == "" {
			return fallback
		}
		prefix := openRouterFloatPrefix.FindString(strings.TrimLeft(value, jsWhitespace))
		if prefix == "" {
			return math.NaN()
		}
		parsed, _ := strconv.ParseFloat(prefix, 64)
		return roundCost(parsed * 1_000_000)
	}
	if model.Pricing == nil {
		return jsonCost{}
	}
	pricing := model.Pricing
	base := jsonCost{Input: perMillion(pricing.Prompt, 0), Output: perMillion(pricing.Completion, 0), CacheRead: perMillion(pricing.InputCacheRead, 0), CacheWrite: perMillion(pricing.InputCacheWrite, 0)}
	for _, override := range pricing.Overrides {
		if override.MinPromptTokens == nil || override.UTCStart != nil || override.UTCEnd != nil || override.UTCDays != nil {
			continue
		}
		base.Tiers = append(base.Tiers, jsonCostTier{
			InputTokensAbove: int(*override.MinPromptTokens),
			Input:            perMillion(override.Prompt, base.Input),
			Output:           perMillion(override.Completion, base.Output),
			CacheRead:        perMillion(override.InputCacheRead, base.CacheRead),
			CacheWrite:       perMillion(override.InputCacheWrite, base.CacheWrite),
		})
	}
	return base
}

// openRouterModalities keeps the distinct text and image entries in order.
func openRouterModalities(values []string) []string {
	out := []string{}
	for _, value := range values {
		if (value == "text" || value == "image") && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func openRouterContextWindow(model openRouterModelListItem) int {
	if model.TopProvider != nil && model.TopProvider.ContextLength != 0 {
		return model.TopProvider.ContextLength
	}
	if model.ContextLength != 0 {
		return model.ContextLength
	}
	return 4096
}

// buildOpenRouterCatalog builds the OpenRouter catalog from the default listing and the output_modalities=image and
// output_modalities=decisions listings. The default listing omits image-only and decision models, so those come from the
// other listings. An id that appears in several results gets a separate entry per operation.
func buildOpenRouterCatalog(listed, imageListed, decisionListed []openRouterModelListItem) openRouterCatalog {
	catalog := openRouterCatalog{Chat: []generatedCatalogModel{}, Images: []openRouterImageModel{}, Classifiers: []openRouterClassifierModel{}}
	for _, model := range listed {
		if !slices.Contains(model.SupportedParameters, "tools") {
			continue
		}
		input := []string{"text"}
		if model.Architecture != nil && strings.Contains(model.Architecture.Modality, "image") {
			input = append(input, "image")
		}
		anthropicMessages := openRouterAnthropicID.MatchString(model.ID) && !strings.HasSuffix(model.ID, ":batch")
		api, baseURL := "openai-completions", openRouterBaseURL
		if anthropicMessages {
			api, baseURL = "anthropic-messages", openRouterAnthropicBaseURL
		}
		maxTokens := 4096
		if model.TopProvider != nil && model.TopProvider.MaxCompletionTokens != 0 {
			maxTokens = model.TopProvider.MaxCompletionTokens
		}
		catalog.Chat = append(catalog.Chat, generatedCatalogModel{Type: "chat", ID: model.ID, Name: model.Name, API: api, Provider: "openrouter", BaseURL: baseURL,
			Reasoning: slices.Contains(model.SupportedParameters, "reasoning"), ThinkingLevelMap: modelgen.GetOpenRouterThinkingLevelMap(model.Reasoning), Input: input,
			Cost: openRouterCost(model), ContextWindow: openRouterContextWindow(model), MaxTokens: maxTokens})
	}
	for _, model := range imageListed {
		if slices.ContainsFunc(catalog.Images, func(entry openRouterImageModel) bool { return entry.ID == model.ID }) || model.Architecture == nil {
			continue
		}
		output := openRouterModalities(model.Architecture.OutputModalities)
		if !slices.Contains(output, "image") {
			continue
		}
		input := openRouterModalities(model.Architecture.InputModalities)
		if len(input) == 0 {
			input = []string{"text"}
		}
		catalog.Images = append(catalog.Images, openRouterImageModel{Type: "image", ID: model.ID, Name: model.Name, API: "openrouter-images", Provider: "openrouter", BaseURL: openRouterBaseURL, Input: input, Output: output, Cost: openRouterCost(model)})
	}
	// Decision models such as TypeSafe's Jev are served through OpenRouter's TypeSafe-compatible System One endpoint.
	for _, model := range decisionListed {
		if slices.ContainsFunc(catalog.Classifiers, func(entry openRouterClassifierModel) bool { return entry.ID == model.ID }) || model.Architecture == nil || !slices.Contains(model.Architecture.OutputModalities, "decisions") {
			continue
		}
		input := openRouterModalities(model.Architecture.InputModalities)
		if len(input) == 0 {
			input = []string{"text"}
		}
		catalog.Classifiers = append(catalog.Classifiers, openRouterClassifierModel{Type: "classifier", ID: model.ID, Name: model.Name, API: "typesafe-system-one", Provider: "openrouter", BaseURL: openRouterBaseURL, Input: input, Cost: openRouterCost(model), ContextWindow: openRouterContextWindow(model)})
	}
	return catalog
}
