package coding

// Ports packages/coding-agent/src/core/model-runtime.ts:67-82,173-226.

import (
	"context"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// CreateModelRuntimeOptions selects file paths, catalog storage, and initial refresh behavior.
// Signal is the context passed to CreateModelRuntime. ModelsPath uses two pointers to preserve omitted, null, and string: nil selects the default; a pointer to nil disables models.json.
type CreateModelRuntimeOptions struct {
	// Credentials replaces the auth.json store at AuthPath. When set, no auth.json is opened.
	Credentials           ai.CredentialStore
	AuthPath              string
	ModelsPath            **string
	ModelsStore           ai.ModelsStore
	ModelsStorePath       string
	AllowModelNetwork     bool
	ModelRefreshTimeoutMs *int
	RefreshOnCreate       *bool
}

// CreateModelRuntime creates a model runtime without a Session. The caller owns ModelRuntime.Close. Initial refresh restores cached catalogs without network unless explicitly enabled, and shares the caller's cancellation with every refresh operation.
func CreateModelRuntime(ctx context.Context, options CreateModelRuntimeOptions) (*ModelRuntime, error) {
	agentDir := icodingagent.AgentDir()
	var auth *ai.AuthStorage
	credentials := options.Credentials
	if credentials == nil {
		authPath := options.AuthPath
		if authPath == "" {
			authPath = filepath.Join(agentDir, "auth.json")
		}
		var err error
		auth, err = ai.NewAuthStorage(authPath)
		if err != nil {
			return nil, err
		}
		credentials = auth
	}
	modelsPath := filepath.Join(agentDir, "models.json")
	if options.ModelsPath != nil {
		modelsPath = ""
		if *options.ModelsPath != nil {
			modelsPath = **options.ModelsPath
		}
	}
	store := options.ModelsStore
	if store == nil {
		if modelsPath == "" {
			store = ai.NewInMemoryModelsStore()
		} else {
			storePath := options.ModelsStorePath
			if storePath == "" {
				storePath = filepath.Join(filepath.Dir(modelsPath), "models-store.json")
			}
			store = ai.NewFileModelsStore(storePath)
		}
	}
	registry := icodingagent.NewModelRegistryWithModelsPath(modelsPath)
	registry.SetCredentialStore(credentials)
	registry.SetModelsStore(store)
	services, err := newConfiguredServices("", agentDir, auth, credentials, nil, registry)
	if err != nil {
		return nil, err
	}
	runtime := services.ModelRuntime()
	allowNetwork := runtime.modelNetworkEnabled && options.AllowModelNetwork
	if options.RefreshOnCreate == nil || *options.RefreshOnCreate {
		refreshContext := ctx
		if allowNetwork && options.ModelRefreshTimeoutMs != nil {
			var cancel context.CancelFunc
			refreshContext, cancel = context.WithTimeout(ctx, time.Duration(*options.ModelRefreshTimeoutMs)*time.Millisecond)
			defer cancel()
		}
		// Upstream create awaits refresh but returns the runtime even when refresh reports cancellation or provider errors.
		runtime.Refresh(refreshContext, ai.ModelsRefreshOptions{AllowNetwork: new(allowNetwork)})
	}
	return runtime, nil
}
