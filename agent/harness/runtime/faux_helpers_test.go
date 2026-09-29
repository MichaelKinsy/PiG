package runtime

import "github.com/MichaelKinsy/PiG/ai"

type laneFaux struct {
	models             *ai.Models
	model              *ai.Model
	setResponses       func([]ai.FauxResponseStep)
	callCount          func() int
	deferredFetchCount func() int
	cancelledDeferred  func() []ai.DeferredHandle
}

func newLaneFaux(pendingFetches ...int) laneFaux {
	options := laneFauxOptions{}
	if len(pendingFetches) > 0 {
		options.PendingFetches = pendingFetches[0]
	}
	return newLaneFauxWithOptions(options)
}

type laneFauxOptions struct{ PendingFetches, MinTokenSize, MaxTokenSize int }

// newLaneFauxWithOptions uses the production model-aware faux queue and deferred lifecycle.
func newLaneFauxWithOptions(options laneFauxOptions) laneFaux {
	provider := ai.NewFauxProvider(ai.FauxConfig{
		MinTokenSize: options.MinTokenSize,
		MaxTokenSize: options.MaxTokenSize,
		Deferred:     &ai.FauxDeferredConfig{PendingFetches: float64(options.PendingFetches)},
	})
	models := ai.CreateModels()
	models.SetProvider(provider.Provider())
	return laneFaux{models: models, model: provider.GetModel(), setResponses: provider.SetResponses,
		callCount: provider.CallCount, deferredFetchCount: provider.DeferredFetchCount, cancelledDeferred: provider.CancelledDeferred}
}

func laneFauxResponse(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})
}
