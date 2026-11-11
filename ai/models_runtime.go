package ai

// Ports packages/ai/src/models.ts.

import (
	"context"
	"fmt"
	"sync"
)

// RefreshModelsAuthMode mirrors upstream Provider.refreshModelsAuth ("credential" | "configured"); the zero value is the default, "credential".
type RefreshModelsAuthMode string

const (
	RefreshModelsAuthCredential RefreshModelsAuthMode = "credential"
	RefreshModelsAuthConfigured RefreshModelsAuthMode = "configured"
)

// ModelsProvider is the provider-owned runtime unit from models.ts; Provider remains the existing bound-backend streaming contract.
type ModelsProvider struct {
	ID            string
	Name          string
	BaseURL       string
	Headers       ProviderHeaders
	Auth          ProviderAuth
	GetModels     func() ([]*Model, error)
	RefreshModels func(RefreshModelsContext) error
	// RefreshModelsAuth is what the network phase of RefreshModels needs. The default, RefreshModelsAuthCredential, receives the effective
	// credential, so an expired OAuth token is refreshed first. RefreshModelsAuthConfigured is for catalogs served by a source other than the
	// provider, such as pi.dev: the phase still runs only when the provider is configured, but OAuth is never refreshed for it and
	// RefreshModelsContext.Credential is nil.
	RefreshModelsAuth RefreshModelsAuthMode
	FilterModels      func([]*Model, *Credential) []*Model
	// GetAllModels lists models of every type. Providers with only chat models may leave it nil.
	GetAllModels func() ([]AnyModel, error)
	// FilterAllModels is the credential-specific availability policy across every model type.
	FilterAllModels func([]AnyModel, *Credential) []AnyModel
	// GenerateImages is present when the provider supports image models.
	GenerateImages func(context.Context, *ImageModel, ImagesContext, ImagesOptions) (AssistantImages, error)
	// Classify is present when the provider supports classifier models.
	Classify       func(context.Context, *ClassifierModel, ClassifierContext, ClassifierOptions) (ClassifierResult, error)
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
	// BaseURL is the base URL of the registered provider, including configuration overrides that wrappers apply outside the provider receiving this context (models.ts RefreshModelsContext.baseUrl).
	BaseURL string
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
	identity           *ClientIdentity
	modelAuth          func(context.Context, *Model, AuthResolutionOverrides) (*AuthResult, error)
	refreshGenerations map[string]uint64
	refreshControllers map[string]*modelsRefreshSignal
	publicationChains  map[string]chan struct{}
	// operations joins every operation the collection runs, including a caller's login and a stream's auth callbacks; owned is the subset the collection runs for its own queries (refresh, auth resolution and check, availability) and for the credential writes of any auth resolution, which Drain joins.
	operations sync.WaitGroup
	owned      sync.WaitGroup
	// draining is set, under mu, by the Drain that stops the collection admitting owned work. It never clears: a drained collection belongs to a registry that is shutting down.
	draining bool
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
	return &Models{providers: map[string]*ModelsProvider{}, credentials: opts.Credentials, modelsStore: opts.ModelsStore, authContext: authContext, identity: opts.Identity, modelAuth: modelAuth, refreshGenerations: map[string]uint64{}, refreshControllers: map[string]*modelsRefreshSignal{}, publicationChains: map[string]chan struct{}{}}
}

type modelsOperationResult[T any] struct {
	value T
	err   error
}

// errModelsDraining is the failure of work the collection refuses to start because Drain has begun.
var errModelsDraining = fmt.Errorf("the model collection is shutting down: %w", context.Canceled)

// goOwned runs work the collection performs for its own queries. Drain waits for it. It reports false without running the work once Drain has begun: the
// admission is decided under the lock that Drain takes to begin, so no owned work is added after Drain starts waiting, and Drain joins exactly the work admitted before it.
func (m *Models) goOwned(run func()) bool {
	m.mu.Lock()
	if m.draining {
		m.mu.Unlock()
		return false
	}
	m.owned.Add(1)
	m.mu.Unlock()
	m.operations.Go(func() {
		defer m.owned.Done()
		run()
	})
	return true
}

// goOperation runs work Close joins but Drain does not: a caller's login and the callbacks of a stream's auth, which end with their callers' contexts.
func (m *Models) goOperation(run func()) bool {
	m.operations.Go(run)
	return true
}

func awaitModelsOperation[T any](ctx context.Context, spawn func(func()) bool, run func() (T, error)) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, context.Cause(ctx)
	}
	result := make(chan modelsOperationResult[T], 1)
	if !spawn(func() { value, err := run(); result <- modelsOperationResult[T]{value, err} }) {
		return zero, errModelsDraining
	}
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
