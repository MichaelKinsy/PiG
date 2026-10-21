package ai

// Ports packages/ai/src/utils/identity.ts (withClientIdentity).

import "context"

// ClientIdentityModels is the request surface of Models that WithClientIdentity wraps: the methods generation and compaction call. *Models, the coding Model Runtime and the durable Harness's models satisfy it.
type ClientIdentityModels interface {
	GetModel(provider, id string) *Model
	StreamSimple(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessageEventStream
	CompleteSimple(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessage
	FetchDeferred(ctx context.Context, model *Model, handle DeferredHandle, options ...DeferredFetchOptions) *AssistantMessage
	CancelDeferred(ctx context.Context, model *Model, handle DeferredHandle, options ...DeferredCancelOptions) error
}

// ModelsLister is the optional listing of the chat models of a Models.
type ModelsLister interface {
	GetModels(provider ...string) []*Model
}

// ModelsCatalog is the optional model catalog of a Models: every model type, classification and image generation.
type ModelsCatalog interface {
	GetModelsOfType(modelType ModelType, provider ...string) []AnyModel
	GetAvailableOfType(ctx context.Context, modelType ModelType, provider ...string) ([]AnyModel, error)
	GetModelOfType(modelType ModelType, provider, id string) AnyModel
	Classify(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ...ModelsClassifierOptions) ClassifierResult
	GenerateImages(ctx context.Context, model *ImageModel, request ImagesContext, options ...ModelsImagesOptions) AssistantImages
}

// WithClientIdentity is models with identity as the default identity of its requests: a request's Identity overrides it field by field, and it overrides the identity models was created with. Everything else is delegated unchanged. The result has the optional ModelsLister and ModelsCatalog surfaces models has, and no others. The wrapper is a typed view: Go has no Proxy, so a Models that is not a ClientIdentityModels is not wrapped.
func WithClientIdentity(models ClientIdentityModels, identity ClientIdentity) ClientIdentityModels {
	core := &identityModels{models: models, identity: &identity}
	lister, hasLister := models.(ModelsLister)
	catalog, hasCatalog := models.(ModelsCatalog)
	switch {
	case hasLister && hasCatalog:
		return &identityListerCatalogModels{identityModels: core, identityLister: identityLister{lister}, identityCatalog: identityCatalog{catalog, core.identity}}
	case hasLister:
		return &identityListerModels{identityModels: core, identityLister: identityLister{lister}}
	case hasCatalog:
		return &identityCatalogModels{identityModels: core, identityCatalog: identityCatalog{catalog, core.identity}}
	}
	return core
}

type identityModels struct {
	models   ClientIdentityModels
	identity *ClientIdentity
}

func withStreamIdentity(identity *ClientIdentity, options []StreamOptions) []StreamOptions {
	var merged StreamOptions
	if len(options) > 0 {
		merged = options[0]
	}
	merged.Identity = MergeClientIdentity(identity, merged.Identity)
	return []StreamOptions{merged}
}

func (m *identityModels) GetModel(provider, id string) *Model { return m.models.GetModel(provider, id) }

func (m *identityModels) StreamSimple(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessageEventStream {
	return m.models.StreamSimple(ctx, model, request, withStreamIdentity(m.identity, options)...)
}

func (m *identityModels) CompleteSimple(ctx context.Context, model *Model, request Context, options ...StreamOptions) *AssistantMessage {
	return m.models.CompleteSimple(ctx, model, request, withStreamIdentity(m.identity, options)...)
}

func (m *identityModels) FetchDeferred(ctx context.Context, model *Model, handle DeferredHandle, options ...DeferredFetchOptions) *AssistantMessage {
	var merged DeferredFetchOptions
	if len(options) > 0 {
		merged = options[0]
	}
	merged.Identity = MergeClientIdentity(m.identity, merged.Identity)
	return m.models.FetchDeferred(ctx, model, handle, merged)
}

func (m *identityModels) CancelDeferred(ctx context.Context, model *Model, handle DeferredHandle, options ...DeferredCancelOptions) error {
	return m.models.CancelDeferred(ctx, model, handle, withStreamIdentity(m.identity, options)...)
}

type identityLister struct{ lister ModelsLister }

func (l identityLister) GetModels(provider ...string) []*Model {
	return l.lister.GetModels(provider...)
}

type identityCatalog struct {
	catalog  ModelsCatalog
	identity *ClientIdentity
}

func (c identityCatalog) GetModelsOfType(modelType ModelType, provider ...string) []AnyModel {
	return c.catalog.GetModelsOfType(modelType, provider...)
}

func (c identityCatalog) GetAvailableOfType(ctx context.Context, modelType ModelType, provider ...string) ([]AnyModel, error) {
	return c.catalog.GetAvailableOfType(ctx, modelType, provider...)
}

func (c identityCatalog) GetModelOfType(modelType ModelType, provider, id string) AnyModel {
	return c.catalog.GetModelOfType(modelType, provider, id)
}

func (c identityCatalog) Classify(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ...ModelsClassifierOptions) ClassifierResult {
	var merged ModelsClassifierOptions
	if len(options) > 0 {
		merged = options[0]
	}
	merged.Identity = MergeClientIdentity(c.identity, merged.Identity)
	return c.catalog.Classify(ctx, model, request, merged)
}

func (c identityCatalog) GenerateImages(ctx context.Context, model *ImageModel, request ImagesContext, options ...ModelsImagesOptions) AssistantImages {
	var merged ModelsImagesOptions
	if len(options) > 0 {
		merged = options[0]
	}
	merged.Identity = MergeClientIdentity(c.identity, merged.Identity)
	return c.catalog.GenerateImages(ctx, model, request, merged)
}

type identityListerModels struct {
	*identityModels
	identityLister
}

type identityCatalogModels struct {
	*identityModels
	identityCatalog
}

type identityListerCatalogModels struct {
	*identityModels
	identityLister
	identityCatalog
}
