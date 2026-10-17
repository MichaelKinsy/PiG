package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// models decodes the provider object's model records. A record without a provider id belongs to this provider, as the registration's own records do; the id
// follows the record's own members, which keep the order the extension wrote them in (models.ts:440-471 returns the provider object's model objects as they are).
func (p *nativeProviderProxy) decodeModels(raw []json.RawMessage) ([]ai.AnyModel, error) {
	records := make([]json.RawMessage, len(raw))
	for i, record := range raw {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(record, &fields); err != nil {
			return nil, err
		}
		if fields == nil {
			return nil, fmt.Errorf("decode provider model: %s is not an object", record)
		}
		records[i] = record
		if _, ok := fields["provider"]; ok {
			continue
		}
		id, err := json.Marshal(p.declaration.ID)
		if err != nil {
			return nil, err
		}
		member := append([]byte(`"provider":`), id...)
		if len(fields) > 0 {
			member = append([]byte(","), member...)
		}
		body := bytes.TrimRight(record, " \t\r\n")
		records[i] = slices.Concat(body[:len(body)-1], member, []byte("}"))
	}
	models, err := ai.DecodeStoredModels(records)
	if err != nil {
		return nil, err
	}
	return ai.DecodeModelsCatalog(models, p.declaration.ID)
}

func (p *nativeProviderProxy) getModels(ctx context.Context) ([]*ai.Model, error) {
	models, err := p.listModels(ctx, "getModels")
	return chatModels(models), err
}

func (p *nativeProviderProxy) allModels(ctx context.Context) ([]ai.AnyModel, error) {
	return p.listModels(ctx, "getAllModels")
}

func (p *nativeProviderProxy) listModels(ctx context.Context, method string) ([]ai.AnyModel, error) {
	raw, err := nativeObjectValue[[]json.RawMessage](p, ctx, method, map[string]any{}, nil)
	if err != nil {
		return nil, err
	}
	return p.decodeModels(raw)
}

func chatModels(models []ai.AnyModel) []*ai.Model {
	chat := []*ai.Model{}
	for _, model := range models {
		if typed, ok := model.(*ai.Model); ok {
			chat = append(chat, typed)
		}
	}
	return chat
}

func (p *nativeProviderProxy) filterModels(ctx context.Context, models []*ai.Model, credential *ai.Credential) ([]*ai.Model, error) {
	all := make([]ai.AnyModel, len(models))
	for i, model := range models {
		all[i] = model
	}
	filtered, err := p.filterWith(ctx, "filterModels", all, credential)
	return chatModels(filtered), err
}

func (p *nativeProviderProxy) filterAllModels(ctx context.Context, models []ai.AnyModel, credential *ai.Credential) ([]ai.AnyModel, error) {
	return p.filterWith(ctx, "filterAllModels", models, credential)
}

func (p *nativeProviderProxy) filterWith(ctx context.Context, method string, models []ai.AnyModel, credential *ai.Credential) ([]ai.AnyModel, error) {
	if !slices.Contains(p.declaration.Methods, method) {
		return models, nil
	}
	encoded, err := ai.EncodeModelsCatalog(models)
	if err != nil {
		return nil, err
	}
	result, err := nativeObjectValue[struct {
		Models []json.RawMessage `json:"models"`
	}](p, ctx, method, map[string]any{"models": encoded, "credential": credential}, nil)
	if err != nil {
		return nil, err
	}
	return p.decodeModels(result.Models)
}

// refreshModels is Pi's Provider.refreshModels(context): the provider object restores its stored catalog and refreshes it, publishing persistence and synchronous state changes through the context's publish.
// upstream: packages/ai/src/models.ts:188 (Provider.refreshModels), RefreshModelsContext.publish
func (p *nativeProviderProxy) refreshModels(refresh ai.RefreshModelsContext) error {
	if !slices.Contains(p.declaration.Methods, "refreshModels") {
		return nil
	}
	_, err := p.objectCall(refresh.Signal, "refreshModels", refreshModelsParams(refresh), func(raw json.RawMessage) (json.RawMessage, error) {
		var callback struct {
			Method string `json:"method"`
			Params struct {
				Publication struct {
					Persist json.RawMessage `json:"persist"`
				} `json:"publication"`
				Token string `json:"token"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &callback); err != nil {
			return nil, err
		}
		if callback.Method != "publish" {
			return nil, fmt.Errorf("unexpected refresh callback %s", callback.Method)
		}
		var publication ai.ModelsPublication
		if persist := callback.Params.Publication.Persist; len(persist) > 0 {
			publication.PersistSet = true
			if string(persist) != "null" {
				var entry ai.ModelsStoreEntry
				if err := json.Unmarshal(persist, &entry); err != nil {
					return nil, err
				}
				publication.Persist = &entry
			}
		}
		var updateErr error
		if token := callback.Params.Token; token != "" {
			publication.Update = func() {
				_, updateErr = p.objectCall(refresh.Signal, "update", map[string]any{"token": token}, nil)
			}
		}
		accepted, err := refresh.Publish(publication)
		if err != nil {
			return nil, err
		}
		if updateErr != nil {
			return nil, updateErr
		}
		return json.Marshal(accepted)
	})
	return err
}

// refreshModelsParams is the refreshModels call's argument: the context minus its callbacks. baseUrl is absent when the registered provider has none, as the
// optional RefreshModelsContext.baseUrl is undefined then (models.ts RefreshModelsContext, pi-mono ee409a85).
func refreshModelsParams(refresh ai.RefreshModelsContext) map[string]any {
	params := map[string]any{"credential": refresh.Credential, "stored": refresh.Stored, "allowNetwork": refresh.AllowNetwork, "force": refresh.Force}
	if refresh.BaseURL != "" {
		params["baseUrl"] = refresh.BaseURL
	}
	return params
}
