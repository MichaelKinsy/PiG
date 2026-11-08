package models

import (
	"context"
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/models/server.ts: the server facet. `ModelConfig` holds the configuration observer and publishes
// its state; `Models` is what an owner sees of it.

func method(fn func(args *delta.JsonObject, ctx context.Context) error) contracts.MethodFunction {
	return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		object, _ := args.(*delta.JsonObject)
		return nil, fn(object, ctx)
	}
}

func text(object *delta.JsonObject, key string) string {
	value, _ := object.Value(key).(string)
	return value
}

// modelInfosOf is the models of a summary or snapshot's `models` member.
func modelInfosOf(value any) []contract.ModelInfo {
	items, _ := value.([]any)
	models := make([]contract.ModelInfo, 0, len(items))
	for _, item := range items {
		record, _ := item.(*delta.JsonObject)
		if record == nil {
			continue
		}
		model := contract.ModelInfo{Provider: text(record, "provider"), ModelID: text(record, "modelId"), Name: text(record, "name")}
		model.ThinkingLevels = []string{}
		levels, _ := record.Value("thinkingLevels").([]any)
		for _, level := range levels {
			if name, ok := level.(string); ok {
				model.ThinkingLevels = append(model.ThinkingLevels, name)
			}
		}
		model.ContextWindow, _ = record.Value("contextWindow").(float64)
		models = append(models, model)
	}
	return models
}

// enabledModelsOf is settings.json's `enabledModels` (empty when there is no scope).
func enabledModelsOf(snapshot any) []any {
	settings, _ := snapshot.(*delta.JsonObject).Value("settings").(*delta.JsonObject)
	patterns, _ := settings.Value("enabledModels").([]any)
	if patterns == nil {
		return []any{}
	}
	return patterns
}

func scopedModelsOf(summary, snapshot any) []any {
	models := modelInfosOf(summary.(*delta.JsonObject).Value("models"))
	var patterns []string
	for _, pattern := range enabledModelsOf(snapshot) {
		if name, ok := pattern.(string); ok {
			patterns = append(patterns, name)
		}
	}
	resolved := ResolveModelScope(patterns, models).Models
	scoped := make([]any, len(resolved))
	for i, model := range resolved {
		item := delta.JsonObjectOf("provider", model.Provider, "modelId", model.ModelID)
		if model.ThinkingLevel != "" {
			item.Set("thinkingLevel", model.ThinkingLevel)
		}
		scoped[i] = item
	}
	return scoped
}

// resourcesNative is the opened resources of a factory, read at each use.
func resourcesNative(native host.Native[any]) *Resources {
	value, err := native.Value()
	if err != nil {
		panic(err)
	}
	return value.(*Resources)
}

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.ServeModelResources(facet); err != nil {
		return err
	}
	if err := facet.Serve(contract.ModelConfig, host.LocalFactory(serveModelConfig), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.Models, host.RemoteFactory(serveModels), host.ServeOptions{})
})

func serveModelConfig(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	natives := p2runtime.ServerOf(self)
	var fake bool
	opened := self.Open(
		func(ctx context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			object, _ := info.(*delta.JsonObject)
			dataDir, _ := object.Value("dataDir").(string)
			fake, _ = object.Value("fake").(bool)
			parts, err := natives()
			if err != nil {
				return nil, err
			}
			return OpenResources(ctx, ResourceOptions{ServerResourceOptions: parts.ResourceOptions, Directory: dataDir, Fake: fake})
		},
		func(value any, _ host.NativeCloseReason, ctx context.Context) error {
			return value.(*Resources).Close(ctx)
		},
	)
	self.(host.StoreSelf).Contribute(contract.ModelResources, &contract.ModelResourcesValue{
		Resources: func() contract.Resources { return resourcesNative(opened) },
	})
	// The configuration files' state as the resources hold it: a save below resolves after its reload, which told this
	// source, so the caller's answer comes with the new state.
	state := p2runtime.FollowNative(self,
		func(changed func()) func() { return resourcesNative(opened).Subscribe(changed) },
		func() (delta.JsonValue, error) {
			// A change published as the object closes finds the native closed: an error, not a panic (the source fails).
			value, err := opened.Value()
			if err != nil {
				return nil, err
			}
			state, err := StateOf(value.(*Resources), fake)
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("summary", state.Summary.JSON(), "snapshot", state.Snapshot.JSON()), nil
		},
	)
	member := func(key string) chord.Watchable[delta.JsonValue] {
		return self.Derive([]any{state}, func(values []any) (delta.JsonValue, error) {
			return values[0].(*delta.JsonObject).Value(key), nil
		})
	}
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"summary": member("summary"), "snapshot": member("snapshot")},
		Methods: map[string]contracts.MethodFunction{
			"setDefault": method(func(args *delta.JsonObject, ctx context.Context) error {
				chordctx.Suspend(ctx)
				resources := resourcesNative(opened)
				provider, modelID := text(args, "provider"), text(args, "modelId")
				if resources.Models().GetModel(provider, modelID) == nil {
					return errors.New("Unknown model")
				}
				settings := resources.Settings()
				settings.Set("defaultModel", delta.JsonObjectOf("provider", provider, "modelId", modelID))
				settings.Set("thinkingLevel", text(args, "thinkingLevel"))
				return resources.SaveSettings(ctx, settings)
			}),
			"setEnabledModels": method(func(args *delta.JsonObject, ctx context.Context) error {
				chordctx.Suspend(ctx)
				resources := resourcesNative(opened)
				settings := resources.Settings()
				settings.Delete("enabledModels")
				var cleaned []any
				patterns, _ := args.Value("patterns").([]any)
				for _, pattern := range patterns {
					if trimmed := strings.TrimSpace(pattern.(string)); trimmed != "" {
						cleaned = append(cleaned, trimmed)
					}
				}
				if len(cleaned) > 0 {
					settings.Set("enabledModels", cleaned)
				}
				return resources.SaveSettings(ctx, settings)
			}),
		},
	}}, nil
}

func serveModels(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.IsOwner(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("models are owner-only")
	}
	config := self.Get(contract.ModelConfig, delta.NewJsonObject(0))
	summary, snapshot := config.Member("summary"), config.Member("snapshot")
	fromSummary := func(key string) chord.Watchable[delta.JsonValue] {
		return self.Derive([]any{summary}, func(values []any) (delta.JsonValue, error) {
			return values[0].(*delta.JsonObject).Value(key), nil
		})
	}
	forward := func(name string) contracts.MethodFunction {
		return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return config.Call(name, args, ctx)
		}
	}
	_ = chordjson.NewObject
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"models":       fromSummary("models"),
			"defaultModel": fromSummary("defaultModel"),
			"errors":       fromSummary("errors"),
			"enabledModels": self.Derive([]any{snapshot}, func(values []any) (delta.JsonValue, error) {
				return enabledModelsOf(values[0]), nil
			}),
			"scopedModels": self.Derive([]any{summary, snapshot}, func(values []any) (delta.JsonValue, error) {
				return scopedModelsOf(values[0], values[1]), nil
			}),
			"resources": host.Expose(snapshot),
		},
		Methods: map[string]contracts.MethodFunction{"setDefault": forward("setDefault"), "setEnabledModels": forward("setEnabledModels")},
	}}, nil
}
