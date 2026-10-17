package ai

// Ports packages/ai/src/providers/openrouter.ts personalizeOpenRouterCatalog (pi-mono commit ee409a85, #10672).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

const (
	openRouterBaseURL = "https://openrouter.ai/api/v1"
	// upstream: packages/ai/src/providers/openrouter.ts:OPENROUTER_USER_MODELS_TIMEOUT_MS (15_000)
	openRouterUserModelsTimeout     = 15 * time.Second
	openRouterUserModelsInvalidBody = "Invalid OpenRouter model list response"
)

// PersonalizeOpenRouterCatalog restricts an OpenRouter catalog to the chat models the key may use, with the key's limits and prices from
// `GET /models/user`. Image and classifier models are returned unchanged. A missing limit keeps the catalog value. The request uses the provider's configured
// base URL (refresh.BaseURL), so regional endpoints such as `https://us.openrouter.ai/api/v1` apply their region's filtering. When the endpoint rejects the key
// (401/403) or does not exist (404), for example with a proxy configured in models.json, the catalog is returned unchanged.
func PersonalizeOpenRouterCatalog(catalog []AnyModel, refresh RefreshModelsContext) ([]AnyModel, error) {
	key := ""
	if refresh.Credential != nil {
		key = refresh.Credential.Key
		if refresh.Credential.Type == CredentialOAuth {
			key = refresh.Credential.Access
		}
	}
	if key == "" {
		return catalog, nil
	}
	parent := refresh.Signal
	if parent == nil {
		parent = context.Background() //portlint:allow ctxbackground a refresh without a Signal is never aborted: Pi's RefreshModelsContext always carries a signal
	}
	ctx, cancel := context.WithTimeout(parent, openRouterUserModelsTimeout)
	defer cancel()
	base := refresh.BaseURL
	if base == "" {
		base = openRouterBaseURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/models/user", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("accept", "application/json")
	request.Header.Set("authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return catalog, nil
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("OpenRouter model list request failed: %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	// response.json() rejects with JSON.parse's SyntaxError.
	if !json.Valid(body) {
		if err := jsonparse.Validate(body); err != nil {
			return nil, err
		}
		return nil, errors.New("Unexpected end of JSON input")
	}
	var parsed struct {
		Data json.RawMessage `json:"data"`
	}
	var entries []json.RawMessage
	if json.Unmarshal(body, &parsed) != nil {
		return nil, errors.New(openRouterUserModelsInvalidBody)
	}
	if json.Unmarshal(parsed.Data, &entries) != nil || entries == nil {
		return nil, errors.New(openRouterUserModelsInvalidBody)
	}
	allowed := make(map[string]OpenRouterModel, len(entries))
	for _, raw := range entries {
		var entry OpenRouterModel
		var probe struct {
			ID *string `json:"id"`
		}
		if json.Unmarshal(raw, &probe) != nil || probe.ID == nil {
			continue
		}
		if json.Unmarshal(raw, &entry) != nil {
			entry = OpenRouterModel{ID: *probe.ID}
		}
		allowed[*probe.ID] = entry
	}
	personalized := make([]AnyModel, 0, len(catalog))
	for _, model := range catalog {
		chat, isChat := model.(*Model)
		if !isChat {
			personalized = append(personalized, model)
			continue
		}
		entry, listed := allowed[chat.ID]
		if !listed {
			continue
		}
		updated := *chat
		var top *OpenRouterTopProvider
		if entry.TopProvider != nil {
			top = entry.TopProvider
		} else {
			top = &OpenRouterTopProvider{}
		}
		// `||`: OpenRouter reports a missing limit as absent, null or 0, like the generator's fallback.
		//portlint:allow numbers OpenRouter reports whole token counts, and a model's limits are ints here as in every catalog the generator writes
		updated.Capabilities.ContextWindow = int(orFirstLimit(float64(chat.Capabilities.ContextWindow), top.ContextLength, entry.ContextLength))
		//portlint:allow numbers OpenRouter reports whole token counts, and a model's limits are ints here as in every catalog the generator writes
		updated.Capabilities.MaxOutputTokens = int(orFirstLimit(float64(chat.Capabilities.MaxOutputTokens), top.MaxCompletionTokens))
		if entry.Pricing != nil {
			cost := OpenRouterCost(entry.Pricing)
			updated.Capabilities.InputCostPer1M, updated.Capabilities.OutputCostPer1M = cost.Input, cost.Output
			updated.Capabilities.CacheReadCostPer1M, updated.Capabilities.CacheWriteCostPer1M = cost.CacheRead, cost.CacheWrite
			updated.Capabilities.CostTiers = cost.Tiers
		}
		personalized = append(personalized, &updated)
	}
	return personalized, nil
}
