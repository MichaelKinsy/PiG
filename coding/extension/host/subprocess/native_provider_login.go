package subprocess

// A provider object's login reverse calls, decoded into the AuthPrompt and AuthEvent unions of packages/ai/src/auth/types.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// apiKeyLogin runs the provider object's own auth.apiKey.login. Its prompt and notify reverse calls are the login's
// AuthInteraction, as Pi's Models.login passes the interaction to apiKey.login (models.ts:762-776).
func (p *nativeProviderProxy) apiKeyLogin(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
	return nativeObjectValue[ai.Credential](p, ctx, "auth.apiKey.login", map[string]any{}, nativeAuthInteraction(ctx, interaction))
}

// oauthLogin runs the provider object's own auth.oauth.login with the login's AuthInteraction.
func (p *nativeProviderProxy) oauthLogin(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
	return nativeObjectValue[ai.Credential](p, ctx, "auth.oauth.login", map[string]any{}, nativeAuthInteraction(ctx, interaction))
}

// nativeLoginCallback is a login reverse call: a prompt the login awaits, or a notification. payload is the prompt or
// event as the extension sent it.
type nativeLoginCallback struct {
	prompt  ai.AuthPrompt
	event   ai.AuthEvent
	payload json.RawMessage
}

// decodeNativeLoginCallback reads a provider object's login reverse call. A method, prompt type or event type outside
// Pi's AuthInteraction is an error.
func decodeNativeLoginCallback(data json.RawMessage) (nativeLoginCallback, error) {
	var request struct {
		Method string `json:"method"`
		Params struct {
			Prompt json.RawMessage `json:"prompt"`
			Event  json.RawMessage `json:"event"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return nativeLoginCallback{}, err
	}
	switch request.Method {
	case "prompt":
		prompt, err := decodeAuthPrompt(request.Params.Prompt)
		return nativeLoginCallback{prompt: prompt, payload: request.Params.Prompt}, err
	case "notify":
		event, err := decodeAuthEvent(request.Params.Event)
		return nativeLoginCallback{event: event, payload: request.Params.Event}, err
	default:
		return nativeLoginCallback{}, fmt.Errorf("unexpected login callback %s", request.Method)
	}
}

// nativeAuthInteraction answers a provider object's login reverse calls with interaction. A prompt's answer is the
// entered or selected string; a notification is acknowledged with null.
func nativeAuthInteraction(ctx context.Context, interaction ai.AuthInteraction) nativeProviderCallback {
	return func(data json.RawMessage) (json.RawMessage, error) {
		callback, err := decodeNativeLoginCallback(data)
		if err != nil {
			return nil, err
		}
		if callback.event != nil {
			if interaction.Notify != nil {
				interaction.Notify(callback.event)
			}
			return json.RawMessage("null"), nil
		}
		if interaction.Prompt == nil {
			return nil, errors.New("login prompts are not available")
		}
		value, err := interaction.Prompt(ctx, callback.prompt)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
}

func decodeAuthVariant[T any](data json.RawMessage) (T, error) {
	var value T
	err := json.Unmarshal(data, &value)
	return value, err
}

// decodeAuthPrompt reads Pi's AuthPrompt union, discriminated by type.
func decodeAuthPrompt(data json.RawMessage) (ai.AuthPrompt, error) {
	var kind struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return nil, err
	}
	switch kind.Type {
	case "text":
		return decodeAuthVariant[ai.AuthTextPrompt](data)
	case "secret":
		return decodeAuthVariant[ai.AuthSecretPrompt](data)
	case "manual_code":
		return decodeAuthVariant[ai.AuthManualCodePrompt](data)
	case "select":
		return decodeAuthVariant[ai.AuthSelectPrompt](data)
	default:
		return nil, fmt.Errorf("unknown login prompt type %q", kind.Type)
	}
}

// decodeAuthEvent reads Pi's AuthEvent union, discriminated by type.
func decodeAuthEvent(data json.RawMessage) (ai.AuthEvent, error) {
	var kind struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return nil, err
	}
	switch kind.Type {
	case "info":
		return decodeAuthVariant[ai.AuthInfoEvent](data)
	case "auth_url":
		return decodeAuthVariant[ai.AuthURLEvent](data)
	case "device_code":
		return decodeAuthVariant[ai.AuthDeviceCodeEvent](data)
	case "progress":
		return decodeAuthVariant[ai.AuthProgressEvent](data)
	default:
		return nil, fmt.Errorf("unknown login event type %q", kind.Type)
	}
}
