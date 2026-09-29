package ai

// Ports packages/ai/src/api/lazy.ts.

import (
	"context"
	"fmt"
	"maps"
	"time"
)

func (m *Models) requireProvider(model *Model) (*ModelsProvider, error) {
	providerID := modelProviderID(model)
	provider := m.GetProvider(providerID)
	if provider == nil {
		return nil, NewModelsError(ModelsErrorProvider, "Unknown provider: "+providerID, nil)
	}
	return provider, nil
}

func (m *Models) applyAuth(ctx context.Context, model *Model, options StreamOptions) (*Model, StreamOptions, error) {
	if _, err := m.requireProvider(model); err != nil {
		return nil, StreamOptions{}, err
	}
	overrides := AuthResolutionOverrides{Env: options.Env}
	if options.APIKey != "" {
		overrides.APIKey = new(options.APIKey)
	}
	resolution, err := m.GetModelAuth(ctx, model, overrides)
	if err != nil {
		return nil, StreamOptions{}, err
	}
	if resolution == nil {
		return nil, StreamOptions{}, NewModelsError(ModelsErrorAuth, "Provider is not configured: "+modelProviderID(model), nil)
	}
	if options.APIKey == "" {
		options.APIKey = resolution.Auth.APIKey
	}
	options.Headers = MergeProviderHeaders(resolution.Auth.Headers, options.Headers)
	if options.TransformHeaders != nil {
		headers := options.Headers
		if headers == nil {
			headers = ProviderHeaders{}
		}
		options.Headers, err = options.TransformHeaders(ctx, headers)
		if err != nil {
			return nil, StreamOptions{}, err
		}
		options.TransformHeaders = nil
	}
	if resolution.Env != nil || options.Env != nil {
		env := make(ProviderEnv, len(resolution.Env)+len(options.Env))
		maps.Copy(env, resolution.Env)
		maps.Copy(env, options.Env)
		options.Env = env
	}
	requestModel := model
	if resolution.Auth.BaseURL != "" {
		requestModel = new(*model)
		requestModel.ProviderMeta.BaseURL = resolution.Auth.BaseURL
	}
	return requestModel, options, nil
}

// lazyStream returns immediately and forwards an owned setup/stream operation. It drains terminal events even after the caller cancels iteration.
func (m *Models) lazyStream(ctx context.Context, model *Model, setup func() (*AssistantMessageEventStream, error)) *AssistantMessageEventStream {
	return startLazyStream(ctx, model, func(context.Context) (*AssistantMessageEventStream, error) {
		inner, err := setup()
		if err == nil && inner == nil {
			err = fmt.Errorf("provider returned no stream")
		}
		return inner, err
	}, m.operations.Go)
}

func pushModelsSetupError(stream *AssistantMessageEventStream, model *Model, err error) {
	message := &AssistantMessage{API: model.ProviderMeta.API, Provider: modelProviderID(model), Model: model.ID, Content: []AssistantContentBlock{}, StopReason: StopReasonError, ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli()}
	_ = stream.Push(ErrorEvent{Reason: StopReasonError, Error: message})
}

func (m *Models) Stream(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessageEventStream {
	return m.stream(ctx, model, request, false, options...)
}

func (m *Models) StreamSimple(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessageEventStream {
	return m.stream(ctx, model, request, true, options...)
}

func (m *Models) stream(ctx context.Context, model *Model, request Context, simple bool, options ...StreamOptions) *AssistantMessageEventStream {
	ctx, _ = withContinuationExecutor(ctx)
	transcript := NormalizeContext(request)
	var opts StreamOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return m.lazyStream(ctx, model, func() (*AssistantMessageEventStream, error) {
		if transcript.err != nil {
			return nil, transcript.err
		}
		provider, err := m.requireProvider(model)
		if err != nil {
			return nil, err
		}
		prepared, params, err := m.applyAuth(ctx, model, opts)
		if err != nil {
			return nil, err
		}
		stream := provider.Stream
		if simple {
			stream = provider.StreamSimple
		}
		if stream == nil {
			return nil, NewModelsError(ModelsErrorStream, "Provider "+provider.ID+" has no stream implementation", nil)
		}
		return stream(ctx, prepared, transcript, params)
	})
}

func (m *Models) Complete(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessage {
	return m.Stream(ctx, model, request, options...).Result()
}

func (m *Models) CompleteSimple(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessage {
	return m.StreamSimple(ctx, model, request, options...).Result()
}

func (m *Models) StreamDeferred(ctx context.Context, model *Model, handle DeferredHandle, options ...DeferredFetchOptions) *AssistantMessageEventStream {
	ctx, _ = withContinuationExecutor(ctx)
	var opts DeferredFetchOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return m.lazyStream(ctx, model, func() (*AssistantMessageEventStream, error) {
		provider, err := m.requireProvider(model)
		if err != nil {
			return nil, err
		}
		if provider.FetchDeferred == nil {
			return nil, NewModelsError(ModelsErrorProvider, "Provider "+provider.ID+" does not support deferred responses", nil)
		}
		prepared, params, err := m.applyAuth(ctx, model, opts.StreamOptions)
		if err != nil {
			return nil, err
		}
		opts.StreamOptions = params
		return provider.FetchDeferred(ctx, prepared, handle, opts)
	})
}

func (m *Models) FetchDeferred(ctx context.Context, model *Model, handle DeferredHandle, options ...DeferredFetchOptions) *AssistantMessage {
	return m.StreamDeferred(ctx, model, handle, options...).Result()
}

func (m *Models) CancelDeferred(ctx context.Context, model *Model, handle DeferredHandle, options ...DeferredCancelOptions) error {
	provider, err := m.requireProvider(model)
	if err != nil {
		return err
	}
	if provider.CancelDeferred == nil {
		return NewModelsError(ModelsErrorProvider, "Provider "+provider.ID+" does not support deferred responses", nil)
	}
	var opts StreamOptions
	if len(options) > 0 {
		opts = options[0]
	}
	prepared, params, err := m.applyAuth(ctx, model, opts)
	if err != nil {
		return err
	}
	return provider.CancelDeferred(ctx, prepared, handle, params)
}
