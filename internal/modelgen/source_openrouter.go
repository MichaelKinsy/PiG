package modelgen

// Ports packages/ai/catalog/sources/openrouter.ts and the openRouterCost of packages/ai/src/providers/openrouter-api.ts.

import (
	"regexp"
	"slices"
	"strings"
	"sync"
)

const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

// OpenRouterReasoningMetadata is OpenRouter's reasoning metadata of a model.
type OpenRouterReasoningMetadata struct {
	Mandatory        bool      `json:"mandatory,omitempty"`
	DefaultEnabled   bool      `json:"default_enabled,omitempty"`
	SupportedEfforts []*string `json:"supported_efforts,omitempty"`
	DefaultEffort    *string   `json:"default_effort,omitempty"`
}

// GetOpenRouterThinkingLevelMap converts OpenRouter's reasoning metadata into Pi model capabilities; nil when it says nothing.
func GetOpenRouterThinkingLevelMap(reasoning *OpenRouterReasoningMetadata) *Obj {
	if reasoning == nil {
		return nil
	}
	if len(reasoning.SupportedEfforts) == 0 {
		if reasoning.Mandatory {
			return O("off", nil)
		}
		return nil
	}
	// OpenRouter's supported_efforts uses the same effort values as models.dev reasoning_options, so both sources share the same Pi
	// thinking-level conversion.
	levels := GetEffortThinkingLevelMap([]ModelsDevReasoningOption{{Type: "effort", Values: reasoning.SupportedEfforts}})
	if levels == nil {
		if reasoning.Mandatory {
			return O("off", nil)
		}
		return nil
	}
	if reasoning.Mandatory {
		return Spread(levels, O("off", nil))
	}
	return Spread(levels, O("off", "none"))
}

func openRouterReasoning(value any) *OpenRouterReasoningMetadata {
	object, ok := value.(*Obj)
	if !ok {
		return nil
	}
	metadata := &OpenRouterReasoningMetadata{Mandatory: object.member("mandatory") == true}
	for _, effort := range object.List("supported_efforts") {
		if text, ok := effort.(string); ok {
			metadata.SupportedEfforts = append(metadata.SupportedEfforts, &text)
		} else {
			metadata.SupportedEfforts = append(metadata.SupportedEfforts, nil)
		}
	}
	return metadata
}

// OpenRouterCatalog is the tool-capable chat, image and decision models of OpenRouter.
type OpenRouterCatalog struct {
	Chat, Images, Classifiers []*Obj
}

func modalities(values *Obj, key string) []any {
	out := []any{}
	for _, value := range values.StringList(key) {
		if (value == "text" || value == "image") && !slices.Contains(toStrings(out), value) {
			out = append(out, value)
		}
	}
	return out
}

func toStrings(values []any) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i], _ = value.(string)
	}
	return out
}

// perMillionPrice converts one $/token price to $/1M tokens. 0 means free. Missing, invalid and negative prices use the
// fallback: OpenRouter reports -1 for router models such as `openrouter/auto`, whose price depends on the model they pick.
func perMillionPrice(entry *Obj, key string, fallback float64) float64 {
	text := ""
	if value, present := entry.Get(key); present {
		text = stringify(value)
	}
	perToken := ParseFloat(text)
	if perToken >= 0 {
		return RoundCost(perToken * 1_000_000)
	}
	return fallback
}

// OpenRouterCost converts OpenRouter's $/token prices to pi's $/1M tokens. Missing, invalid and negative base prices are 0.
// Prompt-length overrides become request-wide tiers; a rate a tier does not list keeps the base price. Time-of-day overrides are
// skipped because ModelCost cannot express them.
func OpenRouterCost(pricing *Obj) *Obj {
	base := [4]float64{
		perMillionPrice(pricing, "prompt", 0),
		perMillionPrice(pricing, "completion", 0),
		perMillionPrice(pricing, "input_cache_read", 0),
		perMillionPrice(pricing, "input_cache_write", 0),
	}
	cost := O("input", base[0], "output", base[1], "cacheRead", base[2], "cacheWrite", base[3])
	var tiers []any
	for _, item := range pricing.List("overrides") {
		override, ok := item.(*Obj)
		if !ok || !override.Has("min_prompt_tokens") || override.Has("utc_start") || override.Has("utc_end") || override.Has("utc_days") {
			continue
		}
		minPrompt, _ := override.Get("min_prompt_tokens")
		tiers = append(tiers, O(
			"inputTokensAbove", minPrompt,
			"input", perMillionPrice(override, "prompt", base[0]),
			"output", perMillionPrice(override, "completion", base[1]),
			"cacheRead", perMillionPrice(override, "input_cache_read", base[2]),
			"cacheWrite", perMillionPrice(override, "input_cache_write", base[3]),
		))
	}
	if len(tiers) > 0 {
		cost.Set("tiers", tiers)
	}
	return cost
}

var openRouterAnthropicID = regexp.MustCompile(`^~?anthropic/`)

func orFallback(value any, fallback float64) float64 {
	if truthy(value) {
		if number, ok := value.(float64); ok {
			return number
		}
	}
	return fallback
}

func contextLength(model *Obj) float64 {
	if length := orFallback(objectOf(model.member("top_provider")).member("context_length"), 0); length != 0 {
		return length
	}
	return orFallback(model.member("context_length"), 4096)
}

func setDefined(object *Obj, key string, value any) {
	if value != nil {
		object.Set(key, value)
	}
}

// BuildOpenRouterCatalog builds the OpenRouter catalog from the default listing and the `output_modalities=image` and
// `output_modalities=decisions` listings. The default listing omits image-only and decision models, so those come from the other
// listings. An upstream model may appear in several results; it then gets separate entries per operation.
func BuildOpenRouterCatalog(listed, imageListed, decisionListed []*Obj) OpenRouterCatalog {
	catalog := OpenRouterCatalog{Chat: []*Obj{}, Images: []*Obj{}, Classifiers: []*Obj{}}

	for _, model := range listed {
		// Only include models that support tools
		if !model.Includes("supported_parameters", "tools") {
			continue
		}
		input := Strings("text")
		if modality, _ := objectOf(model.member("architecture")).member("modality").(string); strings.Contains(modality, "image") {
			input = Strings("text", "image")
		}
		id := model.Str("id")
		thinkingLevelMap := GetOpenRouterThinkingLevelMap(openRouterReasoning(model.member("reasoning")))
		// Includes the `~anthropic/claude-*-latest` aliases, which OpenRouter's Messages endpoint accepts.
		useAnthropicMessages := openRouterAnthropicID.MatchString(id) && !strings.HasSuffix(id, ":batch")
		api, baseURL := "openai-completions", "https://openrouter.ai/api/v1"
		if useAnthropicMessages {
			api, baseURL = "anthropic-messages", "https://openrouter.ai/api"
		}
		chat := O("type", "chat", "id", id)
		setDefined(chat, "name", model.member("name"))
		chat.Set("api", api).Set("baseUrl", baseURL).Set("provider", "openrouter").Set("reasoning", model.Includes("supported_parameters", "reasoning"))
		if thinkingLevelMap != nil {
			chat.Set("thinkingLevelMap", thinkingLevelMap)
		}
		chat.Set("input", input).Set("cost", OpenRouterCost(objectOf(model.member("pricing")))).Set("contextWindow", contextLength(model)).
			Set("maxTokens", orFallback(objectOf(model.member("top_provider")).member("max_completion_tokens"), 4096))
		catalog.Chat = append(catalog.Chat, chat)
	}

	for _, model := range imageListed {
		if slices.ContainsFunc(catalog.Images, func(entry *Obj) bool { return entry.Str("id") == model.Str("id") }) {
			continue
		}
		architecture := objectOf(model.member("architecture"))
		output := modalities(architecture, "output_modalities")
		if !slices.Contains(toStrings(output), "image") {
			continue
		}
		input := modalities(architecture, "input_modalities")
		if len(input) == 0 {
			input = Strings("text")
		}
		image := O("type", "image", "id", model.Str("id"))
		setDefined(image, "name", model.member("name"))
		image.Set("api", "openrouter-images").Set("provider", "openrouter").Set("baseUrl", "https://openrouter.ai/api/v1").
			Set("input", input).Set("output", output).Set("cost", OpenRouterCost(objectOf(model.member("pricing"))))
		catalog.Images = append(catalog.Images, image)
	}

	// Decision models such as TypeSafe's Jev are served through OpenRouter's TypeSafe-compatible System One endpoint.
	for _, model := range decisionListed {
		if slices.ContainsFunc(catalog.Classifiers, func(entry *Obj) bool { return entry.Str("id") == model.Str("id") }) {
			continue
		}
		architecture := objectOf(model.member("architecture"))
		if !architecture.Includes("output_modalities", "decisions") {
			continue
		}
		input := modalities(architecture, "input_modalities")
		if len(input) == 0 {
			input = Strings("text")
		}
		classifier := O("type", "classifier", "id", model.Str("id"))
		setDefined(classifier, "name", model.member("name"))
		classifier.Set("api", "typesafe-system-one").Set("provider", "openrouter").Set("baseUrl", "https://openrouter.ai/api/v1").
			Set("input", input).Set("cost", OpenRouterCost(objectOf(model.member("pricing")))).Set("contextWindow", contextLength(model)).
			// OpenRouter answers TypeSafe `assets` with 200 but drops the images.
			Set("compat", O("imageInput", "state-image-urls", "sendImageOptInHeader", false))
		catalog.Classifiers = append(catalog.Classifiers, classifier)
	}
	return catalog
}

func fetchOpenRouterList(query string) ([]*Obj, error) {
	data, err := fetchJSON(openRouterModelsURL+query, "OpenRouter API")
	if err != nil {
		return nil, err
	}
	var items []*Obj
	for _, item := range listOf(objectOf(data).member("data")) {
		if object, ok := item.(*Obj); ok {
			items = append(items, object)
		}
	}
	return items, nil
}

// FetchOpenRouterCatalog fetches the tool-capable chat, image, and decision listings.
func FetchOpenRouterCatalog() (OpenRouterCatalog, error) {
	queries := []string{"", "?output_modalities=image", "?output_modalities=decisions"}
	lists := make([][]*Obj, len(queries))
	errs := make([]error, len(queries))
	var wait sync.WaitGroup
	for i, query := range queries {
		wait.Go(func() { lists[i], errs[i] = fetchOpenRouterList(query) })
	}
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return OpenRouterCatalog{}, err
		}
	}
	return BuildOpenRouterCatalog(lists[0], lists[1], lists[2]), nil
}
