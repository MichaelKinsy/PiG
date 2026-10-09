package main

// Ports packages/ai/scripts/generate-models.ts fetchAiGatewayModels (the Vercel AI Gateway catalog stage).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
)

const (
	aiGatewayModelsURL       = "https://ai-gateway.vercel.sh/v1"
	aiGatewayBaseURL         = "https://ai-gateway.vercel.sh"
	aiGatewayTypesafeBaseURL = "https://ai-gateway.vercel.sh/typesafe/v1"
)

// aiGatewayModel is generate-models.ts AiGatewayModel.
type aiGatewayModel struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Type          string            `json:"type"`
	ContextWindow float64           `json:"context_window"`
	MaxTokens     float64           `json:"max_tokens"`
	Tags          json.RawMessage   `json:"tags"`
	Pricing       *aiGatewayPricing `json:"pricing"`
}

// aiGatewayCatalog is the chat and classifier models of the Vercel AI Gateway listing.
type aiGatewayCatalog struct {
	Chat        []generatedCatalogModel
	Classifiers []openRouterClassifierModel
}

// orDefault is JavaScript's `value || fallback` for a number: zero takes the fallback.
func orDefault(value float64, fallback int) int {
	if value == 0 {
		return fallback
	}
	return int(value)
}

// buildAiGatewayCatalog converts the AI Gateway listing: an evaluation model is a classifier served through the TypeSafe-compatible System One
// endpoint, and any other model becomes a chat model when it carries the tool-use tag.
func buildAiGatewayCatalog(items []aiGatewayModel) aiGatewayCatalog {
	catalog := aiGatewayCatalog{Chat: []generatedCatalogModel{}, Classifiers: []openRouterClassifierModel{}}
	for _, model := range items {
		name := model.Name
		if name == "" {
			name = model.ID
		}
		if model.Type == "evaluation" {
			var pricing aiGatewayPricing
			if model.Pricing != nil {
				pricing = *model.Pricing
			}
			catalog.Classifiers = append(catalog.Classifiers, openRouterClassifierModel{Type: "classifier", ID: model.ID, Name: name, API: "typesafe-system-one", Provider: "vercel-ai-gateway",
				BaseURL: aiGatewayTypesafeBaseURL, Input: []string{"text"}, Cost: jsonCost{Input: aiGatewayPerMillion(pricing.Input), Output: aiGatewayPerMillion(pricing.Output)},
				ContextWindow: orDefault(model.ContextWindow, 4096)})
			continue
		}
		var tags []string
		if json.Unmarshal(model.Tags, &tags) != nil || !slices.Contains(tags, "tool-use") {
			continue
		}
		input := []string{"text"}
		if slices.Contains(tags, "vision") {
			input = append(input, "image")
		}
		allowEmptySignature := true
		catalog.Chat = append(catalog.Chat, generatedCatalogModel{Type: "chat", ID: model.ID, Name: name, API: "anthropic-messages", Provider: "vercel-ai-gateway", BaseURL: aiGatewayBaseURL,
			Reasoning: slices.Contains(tags, "reasoning"), Input: input, Compat: &ModelCompat{AllowEmptySignature: &allowEmptySignature}, Cost: getAiGatewayCost(model.Pricing),
			ContextWindow: orDefault(model.ContextWindow, 4096), MaxTokens: orDefault(model.MaxTokens, 4096)})
	}
	return catalog
}

// fetchAiGatewayModels fetches and converts the listing at baseURL/models. A failure is fatal under strict and otherwise reported on stderr with an
// empty catalog.
func fetchAiGatewayModels(ctx context.Context, baseURL string, strict bool) (aiGatewayCatalog, error) {
	items, err := fetchAiGatewayList(ctx, baseURL)
	if err == nil {
		return buildAiGatewayCatalog(items), nil
	}
	fmt.Fprintln(os.Stderr, "Failed to fetch Vercel AI Gateway models:", err)
	if strict {
		return aiGatewayCatalog{}, err
	}
	return aiGatewayCatalog{Chat: []generatedCatalogModel{}, Classifiers: []openRouterClassifierModel{}}, nil
}

func fetchAiGatewayList(ctx context.Context, baseURL string) ([]aiGatewayModel, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 { // fetch response.ok
		return nil, fmt.Errorf("Vercel AI Gateway API returned %d", response.StatusCode)
	}
	var list struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		return nil, err
	}
	// Array.isArray(data.data) ? data.data : []: anything else is an empty listing, and an item whose members have unexpected types is still listed.
	var raw []json.RawMessage
	if json.Unmarshal(list.Data, &raw) != nil {
		return nil, nil
	}
	items := make([]aiGatewayModel, 0, len(raw))
	for _, entry := range raw {
		// A member of an unexpected type leaves its zero value and the rest of the item decodes, as a JavaScript object keeps its other members.
		var item aiGatewayModel
		_ = json.Unmarshal(entry, &item)
		items = append(items, item)
	}
	return items, nil
}
