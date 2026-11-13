package extensionconformance

import (
	"context"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

// modelSpec is the `provider/id` name of a model object, or its id alone when it names no provider.
func modelSpec(model *ai.Model) string {
	if provider := model.ProviderMeta.ProviderID; provider != "" {
		return provider + "/" + model.ID
	}
	return model.ID
}

// specModel is the resolveModel action of a test host: the model object a name stands for carries the name's provider and id and nothing else.
func specModel(_ context.Context, spec string) (*ai.Model, error) {
	provider, id, ok := strings.Cut(spec, "/")
	if !ok {
		provider, id = "", spec
	}
	return &ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider}}, nil
}

// setModelBySpec installs a setModel action that receives the `provider/id` name of the model object an extension set, and the resolveModel action an
// SDK's string form asks first.
func setModelBySpec(bridge interface{ SetHostAction(string, any) }, set func(context.Context, string) (bool, error)) {
	bridge.SetHostAction("setModel", func(ctx context.Context, model *ai.Model) (bool, error) { return set(ctx, modelSpec(model)) })
	bridge.SetHostAction("resolveModel", specModel)
}
