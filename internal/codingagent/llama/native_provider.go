package llama

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// ModelsProvider is the Provider object createLlamaProvider returns to Pi's llama factory, which hands it to registerProvider. It maps this
// package's provider.ts port onto pi-ai's Provider so the model registry owns auth resolution, refresh generations, publication and streaming.
// upstream: packages/coding-agent/src/extensions/llama/provider.ts:166-290 (createLlamaProvider), index.ts:48-49
func (c *LlamaProviderController) ModelsProvider() *ai.ModelsProvider {
	provider := c.Provider
	return &ai.ModelsProvider{
		ID:      provider.ID,
		Name:    provider.Name,
		BaseURL: provider.BaseURL,
		Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{
			Name: provider.APIKey.Name,
			Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
				return provider.APIKey.Login(llamaInteraction(ctx, interaction))
			},
			Check: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
				check, err := provider.APIKey.Check(ctx, AuthContext{Env: input.Ctx.Env}, input.Credential)
				if err != nil || check == nil {
					return nil, err
				}
				return &ai.AuthCheck{Type: ai.CredentialType(check.Type), Source: check.Source}, nil
			},
			Resolve: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
				result, err := provider.APIKey.Resolve(ctx, AuthContext{Env: input.Ctx.Env}, input.Credential)
				if err != nil || result == nil {
					return nil, err
				}
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: result.Auth.APIKey, BaseURL: result.Auth.BaseURL}, Env: result.Env, Source: result.Source}, nil
			},
		}},
		GetModels: func() ([]*ai.Model, error) {
			var models []*ai.Model
			for _, model := range provider.GetModels() {
				models = append(models, model.aiChatModel())
			}
			return models, nil
		},
		GetAllModels: func() ([]ai.AnyModel, error) {
			var models []ai.AnyModel
			for _, model := range provider.GetAllModels() {
				switch model := model.(type) {
				case Model:
					models = append(models, model.aiChatModel())
				case ClassifierModel:
					classifier := model.aiModel()
					models = append(models, &classifier)
				}
			}
			return models, nil
		},
		RefreshModels: func(refresh ai.RefreshModelsContext) error {
			ctx := refresh.Signal
			if ctx == nil {
				ctx = context.Background()
			}
			return provider.RefreshModels(RefreshModelsContext{
				Ctx: ctx, Credential: refresh.Credential, Stored: refresh.Stored, AllowNetwork: refresh.AllowNetwork,
				Publish: func(publication ModelsPublication) (bool, error) {
					return refresh.Publish(ai.ModelsPublication{Persist: publication.Persist, PersistSet: publication.Persist != nil, Update: publication.Update})
				},
			})
		},
		Stream:       ai.StreamOpenAICompletions,
		StreamSimple: ai.StreamSimpleOpenAICompletions,
		Classify: func(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
			if model == nil {
				return ai.ClassifierResult{}, errors.New("llama.cpp classify needs a model")
			}
			return provider.Classify(ctx, ClassifierModel{
				Type: "classifier", ID: model.ID, Name: model.Name, API: string(model.API), Provider: model.Provider, BaseURL: model.BaseURL,
				Input: model.Input, Cost: ModelCost{Input: model.Cost.Input, Output: model.Cost.Output, CacheRead: model.Cost.CacheRead, CacheWrite: model.Cost.CacheWrite},
				ContextWindow: model.ContextWindow,
			}, request, options), nil
		},
	}
}

// llamaInteraction adapts pi-ai's login interaction to the text and secret prompts login asks for.
func llamaInteraction(ctx context.Context, interaction ai.AuthInteraction) AuthInteraction {
	return AuthInteraction{Ctx: ctx, Prompt: func(prompt AuthPrompt) (string, error) {
		if interaction.Prompt == nil {
			return "", errors.New("login needs an interactive prompt")
		}
		if prompt.Type == "secret" {
			return interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: prompt.Message, Placeholder: prompt.Placeholder})
		}
		return interaction.Prompt(ctx, ai.AuthTextPrompt{Message: prompt.Message, Placeholder: prompt.Placeholder})
	}}
}

// aiChatModel is the Model<"openai-completions"> this provider publishes, as pi-ai's model type.
func (m Model) aiChatModel() *ai.Model {
	var compat *ai.ModelCompat
	if data, err := json.Marshal(m.Compat); err == nil {
		var decoded ai.ModelCompat
		if json.Unmarshal(data, &decoded) == nil {
			compat = &decoded
		}
	}
	var thinking ai.ThinkingLevelMap
	if m.ThinkingLevelMap != nil {
		thinking = m.ThinkingLevelMap.levels()
	}
	maxThinking := ai.ThinkingLevel("")
	if m.Reasoning {
		levels := ai.GetSupportedThinkingLevels(&ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh}, ThinkingLevelMap: thinking})
		maxThinking = ai.ThinkingLevel(levels[len(levels)-1])
	}
	return &ai.Model{
		ID: m.ID, DisplayName: m.Name,
		Capabilities: ai.ModelCapabilities{
			MaxThinking: maxThinking, SupportsImages: slices.Contains(m.Input, "image"), SupportsToolUse: true,
			ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxTokens,
			InputCostPer1M: m.Cost.Input, OutputCostPer1M: m.Cost.Output, CacheReadCostPer1M: m.Cost.CacheRead, CacheWriteCostPer1M: m.Cost.CacheWrite,
		},
		Input: slices.Clone(m.Input), ThinkingLevelMap: thinking,
		ProviderMeta: ai.ProviderMetadata{ProviderID: m.Provider, API: ai.API(m.API), BaseURL: m.BaseURL, Compat: compat, Reasoning: m.Reasoning},
	}
}

func (m *ThinkingLevelMap) levels() ai.ThinkingLevelMap {
	return ai.ThinkingLevelMap{
		"off": m.Off, "minimal": m.Minimal, "low": m.Low,
		"medium": m.Medium, "high": m.High, "xhigh": m.XHigh,
	}
}
