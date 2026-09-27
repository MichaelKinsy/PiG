package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
)

// catalogRefreshTimeout mirrors the 15 s abort upstream attaches to its
// background model catalog refreshes.
// upstream: coding-agent/src/extensions/llama/index.ts:AbortSignal.timeout
const catalogRefreshTimeout = 15 * time.Second

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
// refresh: skipped offline and abandoned after 15 s.
func refreshCatalogsInBackground(ctx context.Context, host *llama.Host) {
	if IsOfflineModeEnabled() {
		return
	}
	go func() {
		refreshCtx, cancel := context.WithTimeout(ctx, catalogRefreshTimeout)
		defer cancel()
		host.Refresh(refreshCtx, true)
	}()
}
