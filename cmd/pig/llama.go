package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// catalogRefreshTimeout mirrors the 15 s abort upstream attaches to its
// background model catalog refreshes.
// upstream: coding-agent/src/extensions/llama/index.ts:AbortSignal.timeout
const catalogRefreshTimeout = 15 * time.Second

// llamaBuiltinName is the name of the built-in llama.cpp extension, the first entry of builtInExtensions.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:8.
const llamaBuiltinName = "llama.cpp"

// llamaExtension is the extension-set member of the built-in llama.cpp extension: the /llama command it registers. Upstream's
// factory also registers the provider and runs the command in-process; PiG's llama host (startBuiltInLlama) supplies the
// provider, and each mode runs /llama with its own command context, so the command has no handler here. Listing it as a
// registered command keeps /llama in load order among the other extension commands, in the popup and in get_commands.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/llama/index.ts (registerCommand "llama").
func llamaExtension() (extension.Extension, error) {
	return extension.Extension{
		Commands:     map[string]extension.RegisteredCommand{llama.CommandName: {Name: llama.CommandName, Description: llama.CommandDescription}},
		CommandOrder: []string{llama.CommandName},
	}, nil
}

// startBuiltInLlama registers Pi's built-in llama.cpp provider
// (extensions/index.ts builtInExtensions) and restores its stored catalog
// through the cache-only refresh every mode runs at startup
// (agent-session-services.ts `modelRuntime.refresh({ allowNetwork: false })`).
func startBuiltInLlama(ctx context.Context, services *coding.Services) *llama.Host {
	store := ai.NewFileModelsStore(filepath.Join(services.AgentDir(), "models-store.json"))
	host := llama.NewHost(services.Registry().ModelRegistry, services.Auth(), store)
	host.Refresh(ctx, false)
	return host
}

// refreshCatalogsInBackground mirrors main.ts's RPC-mode background catalog
// refresh: skipped offline and abandoned after 15 s. The built-in llama.cpp
// extension is the only catalog it refreshes, so it does nothing when that
// extension is disabled (host is nil).
func refreshCatalogsInBackground(ctx context.Context, host *llama.Host) {
	if host == nil || packagemanager.IsOfflineModeEnabled() {
		return
	}
	go func() {
		refreshCtx, cancel := context.WithTimeout(ctx, catalogRefreshTimeout)
		defer cancel()
		host.Refresh(refreshCtx, true)
	}()
}
