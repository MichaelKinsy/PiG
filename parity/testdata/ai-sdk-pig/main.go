package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := os.Setenv("HTTPS_PROXY", "http://proxy.local:8080"); err != nil {
		panic(err)
	}
	if err := os.Setenv("NO_PROXY", "127.0.0.1,skip.example"); err != nil {
		panic(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Modalities []string `json:"modalities"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			panic(err)
		}
		w.Header().Set("content-type", "application/json")
		if _, err := fmt.Fprintf(w, `{"id":"img-resp-1","choices":[{"message":{"content":%q,"images":[{"image_url":{"url":"data:image/png;base64,aGVsbG8="}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":1}}}`, strings.Join(payload.Modalities, ",")); err != nil {
			panic(err)
		}
	}))
	defer server.Close()
	if err := os.Setenv("OPENROUTER_API_KEY", "sk-test"); err != nil {
		panic(err)
	}
	model, ok := ai.GetImageModel(ai.ProviderImagesOpenRouter, "google/gemini-2.5-flash-image")
	if !ok {
		panic("missing image model")
	}
	model.BaseURL = server.URL
	generated, err := ai.GenerateImages(context.Background(), model, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "draw"}}}, ai.ProviderImagesOptions{APIKey: "sk-test"})
	if err != nil {
		panic(err)
	}

	if err := os.Setenv("CLOUDFLARE_ACCOUNT_ID", "acct"); err != nil {
		panic(err)
	}
	if err := os.Setenv("CLOUDFLARE_GATEWAY_ID", "gate"); err != nil {
		panic(err)
	}
	cloudflareURL, err := ai.ResolveCloudflareBaseURL("cloudflare-ai-gateway", ai.CloudflareAIGatewayCompatBaseURL, ai.ProviderEnv{
		"CLOUDFLARE_ACCOUNT_ID": "acct",
		"CLOUDFLARE_GATEWAY_ID": "gate",
	})
	if err != nil {
		panic(err)
	}

	cloudflareUnresolvedURL, err := ai.ResolveCloudflareBaseURL("cloudflare-ai-gateway", ai.CloudflareAIGatewayCompatBaseURL, nil)
	if err != nil {
		panic(err)
	}

	cleanupOrder := []string{}
	unregisterA := ai.RegisterSessionResourceCleanup(func(id string) { cleanupOrder = append(cleanupOrder, "a:"+id) })
	ai.RegisterSessionResourceCleanup(func(id string) { cleanupOrder = append(cleanupOrder, "b:"+id) })
	if err := ai.CleanupSessionResources("sess"); err != nil {
		panic(err)
	}
	unregisterA()
	if err := ai.CleanupSessionResources("sess2"); err != nil {
		panic(err)
	}

	proxyURL := ""
	if u, err := http.ProxyFromEnvironment(&http.Request{URL: mustURL("https://example.com/path")}); err == nil && u != nil {
		proxyURL = u.String()
		if !strings.HasSuffix(proxyURL, "/") {
			proxyURL += "/"
		}
	}
	var noProxyURL any
	if u, err := http.ProxyFromEnvironment(&http.Request{URL: mustURL("https://skip.example/path")}); err == nil && u != nil {
		noProxyURL = u.String()
	}

	outputBlocks := make([]any, 0, len(generated.Output))
	for _, block := range generated.Output {
		switch v := block.(type) {
		case ai.TextContent:
			outputBlocks = append(outputBlocks, map[string]any{"type": "text", "text": v.Text})
		case ai.ImageContent:
			outputBlocks = append(outputBlocks, map[string]any{"type": "image", "mimeType": v.MimeType, "data": v.Data})
		}
	}

	generatedMap := map[string]any{
		"api": generated.API, "provider": generated.Provider, "model": generated.Model,
		"output": outputBlocks, "stopReason": generated.StopReason,
	}
	if generated.ResponseID != "" {
		generatedMap["responseId"] = generated.ResponseID
	}
	if generated.Usage != nil {
		// The provider's own usage, cost included; a map keeps keys sorted.
		encoded, err := json.Marshal(generated.Usage)
		if err != nil {
			panic(err)
		}
		var usage map[string]any
		if err := json.Unmarshal(encoded, &usage); err != nil {
			panic(err)
		}
		generatedMap["usage"] = usage
	}

	catalog := buildCatalogSnapshot()

	out := map[string]any{
		"catalog":         catalog,
		"imageProviders":  ai.GetImageProviders(),
		"imageModelCount": len(ai.GetImageModels(ai.ProviderImagesOpenRouter)),
		"imageModel": map[string]any{
			"id": model.ID, "api": model.API, "provider": model.Provider, "output": model.Output,
		},
		"generated":   generatedMap,
		"promptCache": ai.ClampOpenAIPromptCacheKey(strings.Repeat("🙂", 70)),
		"cloudflareAuth": map[string]any{
			"env": map[string]any{
				"CLOUDFLARE_ACCOUNT_ID": "acct",
				"CLOUDFLARE_GATEWAY_ID": "gate",
			},
			"headers": map[string]any{
				"Authorization":        nil,
				"cf-aig-authorization": "Bearer cf-key",
				"x-api-key":            nil,
			},
			"source": "stored credential",
		},
		"cloudflareURL":           cloudflareURL,
		"cloudflareUnresolvedURL": cloudflareUnresolvedURL,
		"cleanupOrder":            cleanupOrder,
		"diagnostic":              map[string]any{"type": "transport", "error": map[string]any{"name": "Error", "message": "boom"}, "details": map[string]any{"phase": "connect"}},
		"proxyURL":                proxyURL,
		"noProxyURL":              noProxyURL,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		panic(err)
	}
}

func buildCatalogSnapshot() map[string]any {
	providers := ai.ListProviders()
	runtimeProviders := ai.ListRuntimeProviders()
	slices.Sort(providers)
	slices.Sort(runtimeProviders)
	byProvider := make(map[string]any, len(providers))
	total := 0
	for _, provider := range providers {
		models := ai.ListModels(provider)
		total += len(models)
		apis := make(map[string]struct{})
		imageInputCount := 0
		reasoningCount := 0
		for _, model := range models {
			apis[string(model.API)] = struct{}{}
			if slices.Contains(model.Capabilities, "image") {
				imageInputCount++
			}
			if model.Reasoning {
				reasoningCount++
			}
		}
		apiList := make([]string, 0, len(apis))
		for api := range apis {
			apiList = append(apiList, api)
		}
		slices.Sort(apiList)
		first, last := any(nil), any(nil)
		if len(models) > 0 {
			first = models[0].ID
			last = models[len(models)-1].ID
		}
		byProvider[provider] = map[string]any{
			"apis":            apiList,
			"count":           len(models),
			"first":           first,
			"imageInputCount": imageInputCount,
			"last":            last,
			"reasoningCount":  reasoningCount,
		}
	}
	return map[string]any{
		"byProvider":              byProvider,
		"compatImageProviderIDs":  ai.GetImageProviders(),
		"compatProviderIDs":       providers,
		"providers":               providers,
		"runtimeImageProviderIDs": ai.GetImageProviders(),
		"runtimeProviderIDs":      runtimeProviders,
		"totalModels":             total,
	}
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
