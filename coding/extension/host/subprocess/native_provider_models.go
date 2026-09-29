package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func (p *nativeProviderProxy) filterModels(ctx context.Context, models []extension.ProviderModelConfig, credential *ai.Credential) ([]extension.ProviderModelConfig, error) {
	if !slices.Contains(p.declaration.Methods, "filterModels") {
		return models, nil
	}
	result, err := nativeObjectValue[struct {
		Models []extension.ProviderModelConfig `json:"models"`
	}](p, ctx, "filterModels", map[string]any{"models": models, "credential": credential}, nil)
	return result.Models, err
}

func (p *nativeProviderProxy) refreshModels(ctx context.Context, credential *ai.Credential, stored *ai.ModelsStoreEntry, network bool, force *bool, publish func(extension.NativeProviderPublication) error) ([]extension.ProviderModelConfig, error) {
	if slices.Contains(p.declaration.Methods, "refreshModels") {
		_, err := p.objectCall(ctx, "refreshModels", map[string]any{"credential": credential, "stored": stored, "allowNetwork": network, "force": force}, func(raw json.RawMessage) (json.RawMessage, error) {
			var callback struct {
				Method string `json:"method"`
				Params struct {
					Publication extension.NativeProviderPublication `json:"publication"`
					Token       string                              `json:"token"`
				} `json:"params"`
			}
			if err := json.Unmarshal(raw, &callback); err != nil {
				return nil, err
			}
			if callback.Method != "publish" {
				return nil, fmt.Errorf("unexpected refresh callback %s", callback.Method)
			}
			if err := publish(callback.Params.Publication); err != nil {
				return nil, err
			}
			if callback.Params.Token != "" {
				if _, err := p.objectCall(ctx, "update", map[string]any{"token": callback.Params.Token}, nil); err != nil {
					return nil, err
				}
			}
			models, err := nativeObjectValue[[]extension.ProviderModelConfig](p, ctx, "getModels", map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			if err := publish(extension.NativeProviderPublication{Models: &models}); err != nil {
				return nil, err
			}
			return json.RawMessage("true"), nil
		})
		if err != nil {
			return nil, err
		}
	}
	return nativeObjectValue[[]extension.ProviderModelConfig](p, ctx, "getModels", map[string]any{}, nil)
}
