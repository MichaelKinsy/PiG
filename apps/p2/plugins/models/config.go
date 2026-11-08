package models

import (
	"os"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/models/config.ts: shared by the server and worker facets, p2's resource observer (settings.json,
// models.json, auth.json).

// ModelSummary is what the model picker needs: the models of the signed-in providers, the default, and resource errors.
type ModelSummary struct {
	Models       []contract.ModelInfo
	DefaultModel *contract.ModelChoice
	Errors       []string
}

// JSON is the summary as state shows it.
func (summary ModelSummary) JSON() *delta.JsonObject {
	models := make([]any, len(summary.Models))
	for i, model := range summary.Models {
		models[i] = model.JSON()
	}
	var defaultModel any
	if summary.DefaultModel != nil {
		defaultModel = summary.DefaultModel.JSON()
	}
	errors := make([]any, len(summary.Errors))
	for i, message := range summary.Errors {
		errors[i] = message
	}
	return delta.JsonObjectOf("models", models, "defaultModel", defaultModel, "errors", errors)
}

// Summarize is the models of signed-in providers (and the fake provider in fake mode), the default, and resource errors.
// It fails for a default model half set in the environment (P2_PROVIDER without P2_MODEL, or the reverse).
func Summarize(resources contract.Resources, fake bool) (ModelSummary, error) {
	return summarizeSnapshot(resources.Snapshot(), fake)
}

func summarizeSnapshot(snapshot contract.ResourceSnapshot, fake bool) (ModelSummary, error) {
	signedIn := map[string]bool{}
	for _, credential := range snapshot.Credentials {
		signedIn[credential.ProviderID] = true
	}
	if fake {
		signedIn["faux"] = true
	}
	models := []contract.ModelInfo{}
	for _, model := range snapshot.Models {
		if signedIn[model.Provider] {
			models = append(models, model)
		}
	}
	// settings.json's default, then the environment's (Pi Mobile's order), then the first signed-in model.
	var configured *contract.ModelChoice
	if stored, ok := snapshot.Settings.Value("defaultModel").(*chordjson.Object); ok {
		provider, _ := stored.Value("provider").(string)
		modelID, _ := stored.Value("modelId").(string)
		configured = &contract.ModelChoice{Provider: provider, ModelID: modelID}
	} else {
		fromEnv, err := p2runtime.EnvDefaultModel(os.LookupEnv)
		if err != nil {
			return ModelSummary{}, err
		}
		if fromEnv != nil {
			configured = &contract.ModelChoice{Provider: fromEnv.Provider, ModelID: fromEnv.ModelID}
		}
	}
	wanted, ok := snapshot.Settings.Value("thinkingLevel").(string)
	if !ok {
		wanted = "high"
	}
	// The settings' level, clamped to the model's levels; a default model that is not listed keeps the settings' level
	// (as Pi Mobile's create falls back to it) rather than becoming "off".
	level := func(thinkingLevels []string) string {
		if len(thinkingLevels) == 0 || slices.Contains(thinkingLevels, wanted) {
			return wanted
		}
		return thinkingLevels[len(thinkingLevels)-1]
	}
	var fallback *contract.ModelInfo
	if configured != nil {
		for i := range snapshot.Models {
			if snapshot.Models[i].Provider == configured.Provider && snapshot.Models[i].ModelID == configured.ModelID {
				fallback = &snapshot.Models[i]
				break
			}
		}
	} else if len(models) > 0 {
		fallback = &models[0]
	}
	var defaultModel *contract.ModelChoice
	switch {
	case configured != nil:
		var levels []string
		if fallback != nil {
			levels = fallback.ThinkingLevels
		}
		defaultModel = &contract.ModelChoice{Provider: configured.Provider, ModelID: configured.ModelID, ThinkingLevel: level(levels)}
	case fallback != nil:
		defaultModel = &contract.ModelChoice{Provider: fallback.Provider, ModelID: fallback.ModelID, ThinkingLevel: level(fallback.ThinkingLevels)}
	}
	errors := make([]string, len(snapshot.Errors))
	for i, each := range snapshot.Errors {
		errors[i] = each.Message
	}
	return ModelSummary{Models: models, DefaultModel: defaultModel, Errors: errors}, nil
}

// ResourceState is the configuration's state now: what `ModelConfig` publishes after each change.
type ResourceState struct {
	Summary  ModelSummary
	Snapshot contract.ResourceSnapshot
}

// StateOf is the configuration's state now.
func StateOf(resources contract.Resources, fake bool) (ResourceState, error) {
	snapshot := resources.Snapshot()
	summary, err := summarizeSnapshot(snapshot, fake)
	if err != nil {
		return ResourceState{}, err
	}
	return ResourceState{Summary: summary, Snapshot: snapshot}, nil
}

// number is a settings integer.
func number(object *chordjson.Object, key string) *int {
	value, ok := object.Value(key).(float64)
	if !ok {
		return nil
	}
	integer := int(value)
	return &integer
}

func boolean(object *chordjson.Object, key string) *bool {
	value, ok := object.Value(key).(bool)
	if !ok {
		return nil
	}
	return &value
}

func section(settings p2runtime.Settings, key string) *chordjson.Object {
	object, _ := settings.Value(key).(*chordjson.Object)
	return object
}

// harnessSettings are the Harness's settings as settings say (stream, retry, compaction, execution and queue modes).
func harnessSettings(settings p2runtime.Settings) *harness.HarnessSettings {
	result := &harness.HarnessSettings{}
	if stream := section(settings, "stream"); stream != nil {
		options := &durable.ConversationStreamOptions{
			TimeoutMs: number(stream, "timeoutMs"), MaxRetries: number(stream, "maxRetries"), MaxRetryDelayMs: number(stream, "maxRetryDelayMs"),
		}
		if transport, ok := stream.Value("transport").(string); ok {
			options.Transport = ai.Transport(transport)
		}
		if retention, ok := stream.Value("cacheRetention").(string); ok {
			options.CacheRetention = ai.CacheRetention(retention)
		}
		result.Stream = options
	}
	if retry := section(settings, "retry"); retry != nil {
		result.Retry = &harness.RetryPolicyPatch{
			Enabled: boolean(retry, "enabled"), MaxRetries: number(retry, "maxRetries"),
			BaseDelayMs: number(retry, "baseDelayMs"), MaxAgentDelayMs: number(retry, "maxAgentDelayMs"),
		}
	}
	if compaction := section(settings, "compaction"); compaction != nil {
		result.Compaction = &harness.CompactionPolicyPatch{
			Enabled: boolean(compaction, "enabled"), ReserveTokens: number(compaction, "reserveTokens"),
			KeepRecentTokens: number(compaction, "keepRecentTokens"), BackgroundTokens: number(compaction, "backgroundTokens"),
		}
	}
	if mode, ok := settings.Value("toolExecution").(string); ok {
		result.ToolExecution = durable.ToolExecutionMode(mode)
	}
	if mode, ok := settings.Value("steeringMode").(string); ok {
		result.SteeringMode = durable.QueueMode(mode)
	}
	if mode, ok := settings.Value("followUpMode").(string); ok {
		result.FollowUpMode = durable.QueueMode(mode)
	}
	return result
}
