package ai

// Ports packages/ai/src/models.ts.

import (
	"context"
	"sync"
)

// ModelsProvider is the provider-owned runtime unit from models.ts; Provider remains the existing bound-backend streaming contract.
type ModelsProvider struct {
	ID             string
	Name           string
	BaseURL        string
	Headers        ProviderHeaders
	Auth           ProviderAuth
	GetModels      func() ([]*Model, error)
	RefreshModels  func(RefreshModelsContext) error
	FilterModels   func([]*Model, *Credential) []*Model
	Stream         ModelsStreamFunction
	StreamSimple   ModelsStreamFunction
	FetchDeferred  func(context.Context, *Model, DeferredHandle, DeferredFetchOptions) (*AssistantMessageEventStream, error)
	CancelDeferred func(context.Context, *Model, DeferredHandle, DeferredCancelOptions) error
}

type ModelsStreamFunction func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error)

type DeferredFetchOptions struct {
	StreamOptions
	Wait *float64
}

type DeferredCancelOptions = StreamOptions

type ModelsPublication struct {
	Persist *ModelsStoreEntry
	// PersistSet with a nil Persist is an explicit null publication, which deletes storage.
	PersistSet bool
	Update     func()
}

type RefreshModelsContext struct {
	Credential   *Credential
	Stored       *ModelsStoreEntry
	Publish      func(ModelsPublication) (bool, error)
	AllowNetwork bool
	Force        *bool
	Signal       context.Context
}

type ModelsRefreshOptions struct {
	AllowNetwork *bool
	Providers    []string
	Force        *bool
}

type ModelsRefreshResult struct {
	Aborted    bool
	Errors     map[string]error
	ErrorOrder []string
}

// Models owns provider selection, request auth and refresh publication. Callers own operation contexts; callbacks must settle before their owners are discarded.
type Models struct {
	mu                 sync.RWMutex
	providers          map[string]*ModelsProvider
	order              []string
	credentials        CredentialStore
	modelsStore        ModelsStore
	authContext        AuthContext
	modelAuth          func(context.Context, *Model, AuthResolutionOverrides) (*AuthResult, error)
	refreshGenerations map[string]uint64
	refreshControllers map[string]*modelsRefreshSignal
	publicationChains  map[string]chan struct{}
	operations         sync.WaitGroup
}

type MutableModels = Models

func CreateModels(options ...CreateModelsOptions) *Models {
	return createModels(nil, options...)
}

// CreateModelsWithModelAuth supplies model-level authentication for GetModelAuth and every request. The callback must resolve provider auth with GetAuth, not GetModelAuth, to avoid recursion.
func CreateModelsWithModelAuth(modelAuth func(ctx context.Context, model *Model, overrides AuthResolutionOverrides) (*AuthResult, error), options ...CreateModelsOptions) *Models {
	return createModels(modelAuth, options...)
}

func createModels(modelAuth func(context.Context, *Model, AuthResolutionOverrides) (*AuthResult, error), options ...CreateModelsOptions) *Models {
	var opts CreateModelsOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Credentials == nil {
		opts.Credentials = NewInMemoryCredentialStore()
	}
	if opts.ModelsStore == nil {
		opts.ModelsStore = NewInMemoryModelsStore()
	}
	authContext := DefaultProviderAuthContext()
	if opts.AuthContext != nil {
		authContext = *opts.AuthContext
	}
	return &Models{providers: map[string]*ModelsProvider{}, credentials: opts.Credentials, modelsStore: opts.ModelsStore, authContext: authContext, modelAuth: modelAuth, refreshGenerations: map[string]uint64{}, refreshControllers: map[string]*modelsRefreshSignal{}, publicationChains: map[string]chan struct{}{}}
}

type modelsOperationResult[T any] struct {
	value T
	err   error
}

func awaitModelsOperation[T any](ctx context.Context, owner *sync.WaitGroup, run func() (T, error)) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, context.Cause(ctx)
	}
	result := make(chan modelsOperationResult[T], 1)
	owner.Go(func() { value, err := run(); result <- modelsOperationResult[T]{value, err} })
	select {
	case outcome := <-result:
		if ctx.Err() != nil {
			return zero, context.Cause(ctx)
		}
		return outcome.value, outcome.err
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	}
}
