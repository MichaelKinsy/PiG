// Ports packages/coding-agent/src/core/model-runtime.ts
// Ports packages/coding-agent/src/core/provider-composer.ts
package codingagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// providerModelInput is the model-bearing part of a registered provider: its name, endpoint, api, models of every type and the implementations of its image and classifier models. Callback and credential fields are added by legacyProviderInput.
func providerModelInput(id string, config providerConfig) ProviderConfigInput {
	input := ProviderConfigInput{Name: config.Name, BaseURL: config.BaseURL, API: ai.API(config.API), Images: config.Images, Classifiers: config.Classifiers}
	if config.Models != nil {
		input.Models = make([]ai.AnyModel, 0, len(config.Models))
		for _, model := range config.Models {
			input.Models = append(input.Models, anyModelFromDefinition(id, config, model))
		}
	}
	return input
}

// legacyProviderInput preserves the effective legacy definition when callers switch between the two Go registration payloads.
func legacyProviderInput(id string, config providerConfig) ProviderConfigInput {
	input := providerModelInput(id, config)
	input.APIKey, input.AuthHeader = config.APIKey, config.AuthHeader
	if config.Headers != nil {
		input.Headers = make(map[string]string, len(config.Headers))
		for name, value := range config.Headers {
			if value != nil {
				input.Headers[name] = *value
			}
		}
	}
	if callback := config.StreamSimple; callback != nil {
		input.StreamSimple = func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return InvokeProviderStreamSimple(ctx, id, callback, model, transcript, options)
		}
	}
	if callbacks := config.oauthCallbacks; callbacks != nil {
		input.OAuth = &ExtensionOAuthConfig{Name: callbacks.Name, IsSubscription: callbacks.IsSubscription}
		if callbacks.Login != nil {
			input.OAuth.Login = func(_ context.Context, interaction ai.OAuthLoginCallbacks) (ai.Credential, error) {
				value, err := callbacks.Login(interaction)
				if err != nil {
					return ai.Credential{}, err
				}
				return legacyOAuthCredential(value)
			}
		}
		if callbacks.RefreshToken != nil {
			input.OAuth.RefreshToken = func(_ context.Context, credential ai.Credential) (ai.Credential, error) {
				value, err := callbacks.RefreshToken(credential)
				if err != nil {
					return ai.Credential{}, err
				}
				return legacyOAuthCredential(value)
			}
		}
		if callbacks.GetAPIKey != nil {
			input.OAuth.GetAPIKey = func(credential ai.Credential) string { return callbacks.GetAPIKey(credential) }
		}
		if callbacks.ModifyModels != nil {
			input.OAuth.ModifyModels = func(models []*ai.Model, credential ai.Credential) []*ai.Model {
				opaque := make([]extension.Model, len(models))
				for i, model := range models {
					opaque[i] = model
				}
				modified := callbacks.ModifyModels(opaque, credential)
				result := make([]*ai.Model, len(modified))
				for i, model := range modified {
					result[i] = model.(*ai.Model)
				}
				return result
			}
		}
	}
	return input
}

func legacyOAuthCredential(value any) (ai.Credential, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ai.Credential{}, err
	}
	var credential ai.Credential
	if err := json.Unmarshal(encoded, &credential); err != nil {
		return ai.Credential{}, err
	}
	credential.Type = ai.CredentialOAuth
	return credential, nil
}

// InvokeProviderStreamSimple is the shared dynamic-to-native callback boundary. The caller resolves request credentials before invoking it.
func InvokeProviderStreamSimple(ctx context.Context, id string, callback extension.ProviderStreamSimple, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (stream *ai.AssistantMessageEventStream, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			stream = nil
			err = fmt.Errorf("provider %q streamSimple: %v", id, recovered)
		}
	}()
	options.Signal = ctx
	value := callback(model, transcript, options)
	stream, ok := value.(*ai.AssistantMessageEventStream)
	if !ok || stream == nil {
		return nil, fmt.Errorf("provider %q streamSimple returned %T, expected an assistant message event stream", id, value)
	}
	return stream, nil
}
