package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (resultErr error) {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "registry-metadata-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(dir)) }()
	counter := filepath.Join(dir, "counter")
	if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
		return err
	}
	command := `!sh -c 'count=$(cat "` + counter + `"); echo $((count + 1)) > "` + counter + `"; echo key-value'`
	provider := func(key string) map[string]any {
		return map[string]any{"baseUrl": "https://example.test/v1", "api": "openai-completions", "apiKey": key, "authHeader": true, "models": []any{map[string]any{"id": "test-model"}}}
	}
	data, err := json.Marshal(map[string]any{"providers": map[string]any{"custom-provider": provider(command), "failed-provider": provider("!exit 1"), "broken-one": map[string]any{"api": "openai-completions", "models": []any{map[string]any{"id": "one"}}}, "broken-two": map[string]any{"api": "openai-completions", "models": []any{map[string]any{"id": "two"}}}, "anthropic": map[string]any{"modelOverrides": map[string]any{"claude-fable-5": map[string]any{"compat": map[string]any{"allowedFallbackModels": []any{}}}}}}})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
		return err
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		return err
	}
	registry := services.Registry()
	_ = services.ModelRuntime().GetModels()
	_ = registry.GetAll()
	_ = registry.RuntimeModels()
	_ = registry.GetAvailable()
	status := registry.GetProviderAuthStatus("custom-provider")
	readCount := func() (string, error) { data, err := os.ReadFile(counter); return strings.TrimSpace(string(data)), err }
	count, err := readCount()
	if err != nil {
		return err
	}
	if count != "0" {
		return fmt.Errorf("metadata executed command %s times", count)
	}
	output := map[string]any{"metadataCommands": count, "status": map[string]any{"configured": status.Configured, "source": status.Source}, "compositionErrors": map[string]bool{"one": strings.Contains(registry.GetError(), `Provider "broken-one"`), "two": strings.Contains(registry.GetError(), `Provider "broken-two"`)}}
	key := registry.GetAPIKeyForProvider(ctx, "custom-provider")
	output["key"] = key
	selected := registry.Find("custom-provider", "test-model")
	output["auth"] = registry.GetAPIKeyAndHeaders(ctx, selected)
	count, err = readCount()
	if err != nil {
		return err
	}
	output["requestCommands"] = count
	if err := services.Auth().Set("custom-provider", ai.Credential{Type: ai.CredentialAPIKey, Key: "stored-key"}); err != nil {
		return err
	}
	output["stored"] = registry.GetAPIKeyAndHeaders(ctx, selected)
	count, err = readCount()
	if err != nil {
		return err
	}
	output["storedCommands"] = count
	output["failed"] = registry.GetAPIKeyAndHeaders(ctx, registry.Find("failed-provider", "test-model"))
	model := registry.Find("anthropic", "claude-fable-5")
	output["emptyFallback"] = model.ProviderMeta.Compat.AllowedFallbackModels
	copilot := ai.ListModels("github-copilot")[0].ID
	ids, err := json.Marshal([]string{copilot})
	if err != nil {
		return err
	}
	if err := services.Auth().Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Access: "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;", Refresh: "github-access-token", Expires: time.Now().Add(time.Minute).UnixMilli(), AvailableModelIDs: ids}); err != nil {
		return err
	}
	var available []string
	for _, model := range registry.GetAvailable() {
		if model.ProviderID == "github-copilot" {
			available = append(available, model.ModelID)
		}
	}
	output["copilot"] = available
	return json.NewEncoder(os.Stdout).Encode(output)
}
