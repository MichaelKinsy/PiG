package models

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/models/resources.ts: p2's resource observer (settings.json, models.json, auth.json).

// ResourceOptions are the options of Resources: the server's overrides (tests: a fixture provider) and where it is.
type ResourceOptions struct {
	p2runtime.ServerResourceOptions
	Directory string
	Fake      bool
}

// fallbackInterval is how often the configuration files are read without a working watcher (an error).
const fallbackInterval = 2 * time.Second

var configurationFiles = []string{"settings.json", "models.json", "models-store.json", "auth.json"}

// Resources is one instance per process, never a server-owned reload coordinator. A watch on the configuration files
// (creation, deletion, in-place writes, atomic renames) triggers loads; nothing polls while it works.
type Resources struct {
	directory   string
	credentials *FileCredentials
	modelStore  *FileModelsStore
	options     ResourceOptions
	current     atomic.Pointer[ai.Models]
	observer    *p2runtime.Observer
	ctx         context.Context
	cancel      context.CancelFunc
	writes      sync.WaitGroup

	mu           sync.Mutex
	settings     p2runtime.Settings
	errors       []contract.ResourceError
	fingerprints map[string]string
	// unconfirmed are the resources that failed once and are read again before their error is reported.
	unconfirmed map[string]bool
	listeners   map[int]func()
	nextID      int
	closed      bool
	revision    int
}

var _ contract.Resources = (*Resources)(nil)

// OpenResources watches the configuration files and loads them; it returns after the first load.
func OpenResources(ctx context.Context, options ResourceOptions) (*Resources, error) {
	resources, err := NewResources(options)
	if err != nil {
		return nil, err
	}
	if err := resources.Start(ctx); err != nil {
		_ = resources.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return resources, nil
}

// NewResources is the observer of options.Directory, not yet started.
func NewResources(options ResourceOptions) (*Resources, error) {
	directory := p2runtime.NormalizePath(options.Directory)
	resources := &Resources{
		directory:    directory,
		credentials:  NewFileCredentials(filepath.Join(directory, "auth.json")),
		modelStore:   NewFileModelsStore(filepath.Join(directory, "models-store.json")),
		options:      options,
		fingerprints: map[string]string{},
		unconfirmed:  map[string]bool{},
		listeners:    map[int]func(){},
	}
	resources.ctx, resources.cancel = context.WithCancel(context.Background())
	current, err := resources.baseModels(resources.credentials)
	if err != nil {
		resources.cancel()
		return nil, err
	}
	resources.current.Store(current)
	files := []p2runtime.ObservedFiles{{
		Env: nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: directory}),
		Targets: func(context.Context) ([]durableenv.WatchTarget, error) {
			targets := make([]durableenv.WatchTarget, len(configurationFiles))
			for i, name := range configurationFiles {
				targets[i] = durableenv.WatchTarget{Path: filepath.Join(directory, name)}
			}
			return targets, nil
		},
	}}
	resources.observer = p2runtime.NewObserver(files, resources.load, fallbackInterval)
	return resources, nil
}

func (resources *Resources) baseModels(credentials ai.CredentialStore) (*ai.Models, error) {
	if resources.options.CreateModels != nil {
		return resources.options.CreateModels(credentials, resources.modelStore)
	}
	return CreateP2Models(resources.options.Fake, ai.CreateModelsOptions{Credentials: credentials, ModelsStore: resources.modelStore}), nil
}

// Start watches the configuration files and loads them; it returns after the first load.
func (resources *Resources) Start(ctx context.Context) error { return resources.observer.Start(ctx) }

// Directory is the data directory holding the configuration files.
func (resources *Resources) Directory() string { return resources.directory }

// Credentials is the credential store (auth.json).
func (resources *Resources) Credentials() ai.CredentialStore { return resources.credentials }

// FileCredentials is the same store, for code in this package.
func (resources *Resources) FileCredentials() *FileCredentials { return resources.credentials }

// ModelStore is the catalog store (models-store.json).
func (resources *Resources) ModelStore() *FileModelsStore { return resources.modelStore }

// Models is the current configuration generation of the models: read it at each use, a reload replaces it.
func (resources *Resources) Models() *ai.Models { return resources.current.Load() }

// Durable is Durable's view of the models.
func (resources *Resources) Durable() durable.Models { return durableModels{resources} }

// durableModels binds each call to an immutable configuration generation at invocation: a stream waiting on auth does
// not switch providers during a reload.
type durableModels struct{ resources *Resources }

var _ durable.Models = durableModels{}

func (models durableModels) GetModel(provider, id string) *ai.Model {
	return models.resources.current.Load().GetModel(provider, id)
}

func (models durableModels) StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessageEventStream {
	return models.resources.current.Load().StreamSimple(ctx, model, request, options...)
}

func (models durableModels) CompleteSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessage {
	return models.resources.current.Load().CompleteSimple(ctx, model, request, options...)
}

func (models durableModels) FetchDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredFetchOptions) *ai.AssistantMessage {
	return models.resources.current.Load().FetchDeferred(ctx, model, handle, options...)
}

func (models durableModels) CancelDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredCancelOptions) error {
	return models.resources.current.Load().CancelDeferred(ctx, model, handle, options...)
}

// Settings is a copy of the current, valid settings.json.
func (resources *Resources) Settings() p2runtime.Settings {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return resources.settingsLocked()
}

func (resources *Resources) settingsLocked() p2runtime.Settings {
	if resources.settings == nil {
		return chordjson.NewObject(0)
	}
	return resources.settings.Clone()
}

// ResourceErrors are the files that failed to load.
func (resources *Resources) ResourceErrors() []contract.ResourceError {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return append([]contract.ResourceError(nil), resources.errors...)
}

// HarnessSettings are the Harness's settings, read at each decision: requests and tool invocations already in progress
// keep the decisions they took; later phases read new values.
func (resources *Resources) HarnessSettings() *harness.HarnessSettings {
	return harnessSettings(resources.Settings())
}

// Subscribe calls listener after each change of the configuration; the result stops it.
func (resources *Resources) Subscribe(listener func()) func() {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	id := resources.nextID
	resources.nextID++
	resources.listeners[id] = listener
	return func() {
		resources.mu.Lock()
		defer resources.mu.Unlock()
		delete(resources.listeners, id)
	}
}

// changedLocked is whether resource's content (fingerprint) is not the one last loaded, or it failed or awaits a second
// read.
func (resources *Resources) changedLocked(resource, fingerprint string) bool {
	if previous, loaded := resources.fingerprints[resource]; loaded && previous == fingerprint && !resources.hasErrorLocked(resource) && !resources.unconfirmed[resource] {
		return false
	}
	resources.fingerprints[resource] = fingerprint
	return true
}

func (resources *Resources) hasErrorLocked(resource string) bool {
	for _, each := range resources.errors {
		if each.Resource == resource {
			return true
		}
	}
	return false
}

func (resources *Resources) deleteErrorLocked(resource string) {
	for i, each := range resources.errors {
		if each.Resource == resource {
			resources.errors = append(resources.errors[:i:i], resources.errors[i+1:]...)
			return
		}
	}
}

// setErrorLocked records message for resource (Map.set: an existing resource keeps its position) and reports whether it
// changed.
func (resources *Resources) setErrorLocked(resource, message string) bool {
	for i, each := range resources.errors {
		if each.Resource == resource {
			if each.Message == message {
				return false
			}
			resources.errors[i].Message = message
			return true
		}
	}
	resources.errors = append(resources.errors, contract.ResourceError{Resource: resource, Message: message})
	return true
}

// Reload loads the configuration files again now; it returns once loaded.
func (resources *Resources) Reload(ctx context.Context) error { return resources.observer.Reload(ctx) }

// relayCredentials serves the stored snapshot while a catalog is restored from its cache (no stored key is resolved, no
// command runs), then reads through.
type relayCredentials struct {
	credentials *FileCredentials
	restoring   atomic.Bool
}

func (relay *relayCredentials) Read(ctx context.Context, providerID string) (*ai.Credential, error) {
	if relay.restoring.Load() {
		return relay.credentials.StoredSnapshot(providerID), nil
	}
	return relay.credentials.Read(ctx, providerID)
}

func (relay *relayCredentials) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	return relay.credentials.List(ctx)
}

func (relay *relayCredentials) Modify(ctx context.Context, providerID string, update func(current *ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	return relay.credentials.Modify(ctx, providerID, update)
}

func (relay *relayCredentials) Delete(ctx context.Context, providerID string) error {
	return relay.credentials.Delete(ctx, providerID)
}

// load reads the configuration files; it returns whether a resource failed for the first time and should be read once
// more before reporting it.
func (resources *Resources) load(ctx context.Context, retrying bool) bool {
	changed, retry := false, false
	attempt := func(resource string, action func() error) {
		err := action()
		resources.mu.Lock()
		defer resources.mu.Unlock()
		if err == nil {
			delete(resources.unconfirmed, resource)
			return
		}
		// An editor's save may have been caught half-way: read once more before reporting it.
		if !retrying && !resources.unconfirmed[resource] {
			resources.unconfirmed[resource] = true
			retry = true
			return
		}
		delete(resources.unconfirmed, resource)
		if resources.setErrorLocked(resource, "Could not load "+resource+" resources; keeping last valid state") {
			changed = true
		}
	}
	attempt("settings", func() error {
		content, err := p2runtime.OptionalText(filepath.Join(resources.directory, "settings.json"))
		if err != nil {
			return err
		}
		text := "{}"
		if content != nil {
			text = *content
		}
		resources.mu.Lock()
		unchanged := !resources.changedLocked("settings", text)
		resources.mu.Unlock()
		if unchanged {
			return nil
		}
		settings, err := p2runtime.ParseSettings(text)
		if err != nil {
			return err
		}
		resources.mu.Lock()
		resources.settings = settings
		resources.deleteErrorLocked("settings")
		resources.mu.Unlock()
		changed = true
		return nil
	})
	attempt("models", func() error {
		path := filepath.Join(resources.directory, "models.json")
		content, err := p2runtime.OptionalText(path)
		if err != nil {
			return err
		}
		text := ""
		if content != nil {
			text = *content
		}
		fingerprint, err := resources.modelStore.Fingerprint()
		if err != nil {
			return err
		}
		resources.mu.Lock()
		unchanged := !resources.changedLocked("models", text+"\n"+fingerprint)
		resources.mu.Unlock()
		if unchanged {
			return nil
		}
		config := LoadModelConfig(path)
		if config.Error() != "" {
			return errors.New("Invalid models configuration")
		}
		relay := &relayCredentials{credentials: resources.credentials}
		relay.restoring.Store(true)
		next, err := resources.baseModels(relay)
		if err != nil {
			return err
		}
		offline := false
		restored := next.Refresh(resources.ctx, ai.ModelsRefreshOptions{AllowNetwork: &offline})
		if len(restored.Errors) > 0 {
			return errors.New("Could not restore cached model catalogs")
		}
		relay.restoring.Store(false)
		// Build and validate completely before publishing.
		if err := ApplyModelConfig(next, config); err != nil {
			return err
		}
		resources.current.Store(next)
		resources.mu.Lock()
		resources.deleteErrorLocked("models")
		resources.mu.Unlock()
		changed = true
		return nil
	})
	attempt("credentials", func() error {
		fingerprint, err := fileFingerprint(filepath.Join(resources.directory, "auth.json"))
		if err != nil {
			return err
		}
		resources.mu.Lock()
		unchanged := !resources.changedLocked("credentials", fingerprint)
		resources.mu.Unlock()
		if unchanged {
			return nil
		}
		if err := resources.credentials.Reload(resources.ctx); err != nil {
			return err
		}
		resources.mu.Lock()
		if revision := resources.credentials.Revision(); revision != "" {
			resources.fingerprints["credentials"] = revision
		}
		resources.deleteErrorLocked("credentials")
		resources.mu.Unlock()
		changed = true
		return nil
	})
	if changed {
		resources.publish()
	}
	return retry
}

// Snapshot is the configuration's state now, without secrets.
func (resources *Resources) Snapshot() contract.ResourceSnapshot {
	resources.mu.Lock()
	snapshot := contract.ResourceSnapshot{
		Version:  resources.revision,
		Settings: resources.settingsLocked(),
		Errors:   append([]contract.ResourceError{}, resources.errors...),
	}
	resources.mu.Unlock()
	for _, info := range resources.credentials.Metadata() {
		snapshot.Credentials = append(snapshot.Credentials, contract.CredentialInfo{ProviderID: info.ProviderID, Type: string(info.Type)})
	}
	if snapshot.Credentials == nil {
		snapshot.Credentials = []contract.CredentialInfo{}
	}
	models := resources.current.Load()
	snapshot.Providers = []contract.ProviderInfo{}
	for _, provider := range models.GetProviders() {
		info := contract.ProviderInfo{ID: provider.ID, Name: provider.Name, AuthMethods: []string{}, LoginMethods: []contract.LoginMethod{}}
		if apiKey := provider.Auth.APIKey; apiKey != nil {
			if apiKey.Login != nil {
				info.LoginMethods = append(info.LoginMethods, contract.LoginMethod{Type: "api_key", Label: apiKey.Name})
			}
		}
		if oauth := provider.Auth.OAuth; oauth != nil {
			label := oauth.LoginLabel
			if label == "" {
				label = oauth.Name
			}
			info.LoginMethods = append(info.LoginMethods, contract.LoginMethod{Type: "oauth", Label: label})
		}
		if provider.Auth.APIKey != nil {
			info.AuthMethods = append(info.AuthMethods, "api_key")
		}
		if provider.Auth.OAuth != nil {
			info.AuthMethods = append(info.AuthMethods, "oauth")
		}
		snapshot.Providers = append(snapshot.Providers, info)
	}
	snapshot.Models = []contract.ModelInfo{}
	for _, model := range models.GetModels() {
		levels := []string{}
		for _, level := range ai.GetSupportedThinkingLevels(model) {
			levels = append(levels, string(level))
		}
		snapshot.Models = append(snapshot.Models, contract.ModelInfo{
			Provider: model.ProviderMeta.ProviderID, ModelID: model.ID, Name: model.DisplayName,
			ThinkingLevels: levels, ContextWindow: float64(model.Capabilities.ContextWindow),
		})
	}
	return snapshot
}

// Publish tells the listeners the models changed (a login, a refresh).
func (resources *Resources) Publish() { resources.publish() }

func (resources *Resources) publish() {
	resources.mu.Lock()
	if resources.closed {
		resources.mu.Unlock()
		return
	}
	resources.revision++
	listeners := make([]func(), 0, len(resources.listeners))
	for _, listener := range resources.listeners {
		listeners = append(listeners, listener)
	}
	resources.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// RefreshModels refreshes providerID's models from the network.
func (resources *Resources) RefreshModels(ctx context.Context, providerID string) error {
	online, force := true, true
	result := resources.current.Load().Refresh(ctx, ai.ModelsRefreshOptions{Providers: []string{providerID}, AllowNetwork: &online, Force: &force})
	resources.publish()
	if result.Aborted {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return context.Canceled
	}
	if len(result.Errors) > 0 {
		return errors.New("Could not refresh provider models")
	}
	return nil
}

// SaveSettings writes settings.json and returns after the reload that follows.
func (resources *Resources) SaveSettings(ctx context.Context, value any) error {
	resources.writes.Add(1)
	defer resources.writes.Done()
	if err := p2runtime.SaveSettings(resources.ctx, filepath.Join(resources.directory, "settings.json"), value); err != nil {
		return err
	}
	return resources.Reload(ctx)
}

// Close stops watching and waits for the writes in progress.
func (resources *Resources) Close(context.Context) error {
	resources.mu.Lock()
	resources.closed = true
	resources.mu.Unlock()
	resources.cancel()
	resources.observer.Close()
	resources.writes.Wait()
	resources.credentials.Close()
	resources.modelStore.Close()
	resources.mu.Lock()
	clear(resources.listeners)
	resources.mu.Unlock()
	return nil
}
