package ai

import (
	"context"
	"errors"
	"slices"
	"sync"
)

// RuntimeCredentials is a credential store overlay for non-persistent runtime
// API keys (--api-key). An override reads as an api_key credential and is
// never written to the base store. Mirrors upstream
// packages/coding-agent/src/core/runtime-credentials.ts RuntimeCredentials.
type RuntimeCredentials struct {
	store CredentialStore

	mu        sync.RWMutex
	overrides map[string]string
	// order keeps override insertion order, as a JavaScript Map iterates:
	// replacing a key keeps its position, and removing it drops it.
	order []string

	// pending holds a channel per Modify that has not returned, as Pi's pendingModifications holds a promise per modify (b2cb62923).
	pendingMu sync.Mutex
	pending   map[chan struct{}]struct{}
}

// NewRuntimeCredentials overlays runtime API keys on store. A nil store holds
// no persistent credentials.
func NewRuntimeCredentials(store CredentialStore) *RuntimeCredentials {
	return &RuntimeCredentials{store: store, overrides: map[string]string{}}
}

// SetRuntimeAPIKey installs a non-persistent API key for providerID.
func (c *RuntimeCredentials) SetRuntimeAPIKey(providerID, apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.overrides[providerID]; !exists {
		c.order = append(c.order, providerID)
	}
	c.overrides[providerID] = apiKey
}

// RemoveRuntimeAPIKey drops providerID's runtime key, revealing the base
// store's credential.
func (c *RuntimeCredentials) RemoveRuntimeAPIKey(providerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.overrides[providerID]; exists {
		delete(c.overrides, providerID)
		c.order = slices.DeleteFunc(c.order, func(id string) bool { return id == providerID })
	}
}

// HasRuntimeAPIKey reports whether providerID has a runtime key.
func (c *RuntimeCredentials) HasRuntimeAPIKey(providerID string) bool {
	_, ok := c.RuntimeAPIKey(providerID)
	return ok
}

// RuntimeAPIKey returns providerID's runtime key.
func (c *RuntimeCredentials) RuntimeAPIKey(providerID string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key, ok := c.overrides[providerID]
	return key, ok
}

// Read implements CredentialStore: a runtime key reads as an api_key credential ahead of the base store. Cancellation returns the caller's cause.
func (c *RuntimeCredentials) Read(ctx context.Context, providerID string) (*Credential, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if key, ok := c.RuntimeAPIKey(providerID); ok && key != "" {
		return &Credential{Type: CredentialAPIKey, Key: key}, nil
	}
	if c.store == nil {
		return nil, nil
	}
	return c.store.Read(ctx, providerID)
}

// List implements CredentialStore: base entries, with every runtime key listed as an api_key entry. It checks cancellation after the base list completes and preserves the caller's cause.
func (c *RuntimeCredentials) List(ctx context.Context) ([]CredentialInfo, error) {
	entries := map[string]CredentialInfo{}
	var order []string
	if c.store != nil {
		base, err := c.store.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, entry := range base {
			if _, seen := entries[entry.ProviderID]; !seen {
				order = append(order, entry.ProviderID)
			}
			entries[entry.ProviderID] = entry
		}
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	for _, providerID := range c.order {
		if _, seen := entries[providerID]; !seen {
			order = append(order, providerID)
		}
		entries[providerID] = CredentialInfo{ProviderID: providerID, Type: CredentialAPIKey}
	}
	c.mu.RUnlock()
	infos := make([]CredentialInfo, 0, len(order))
	for _, providerID := range order {
		infos = append(infos, entries[providerID])
	}
	return infos, nil
}

// Modify implements CredentialStore by delegating to the base store; the
// runtime key is unaffected. Until it returns, with a result or an error, the
// modification counts as pending for [RuntimeCredentials.Settled].
func (c *RuntimeCredentials) Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error) {
	if c.store == nil {
		return nil, errors.New("runtime credentials: no base credential store")
	}
	// Every modification is tracked so process exit can wait for an OAuth refresh to be saved.
	done := make(chan struct{})
	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = map[chan struct{}]struct{}{}
	}
	c.pending[done] = struct{}{}
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, done)
		c.pendingMu.Unlock()
		close(done)
	}()
	return c.store.Modify(ctx, providerID, fn)
}

// Settled returns when every modification started before the call has returned, written or failed. An OAuth refresh may already have spent the single-use refresh token, so exiting before its result is written would leave only the spent token in auth.json. A modification that starts later is not waited for. It fails only with ctx's cancellation cause; a modification's own error is its caller's, as Pi's settled() swallows it.
// upstream: packages/coding-agent/src/core/runtime-credentials.ts:settled (b2cb62923)
func (c *RuntimeCredentials) Settled(ctx context.Context) error {
	c.pendingMu.Lock()
	waiting := make([]chan struct{}, 0, len(c.pending))
	for done := range c.pending {
		waiting = append(waiting, done)
	}
	c.pendingMu.Unlock()
	for _, done := range waiting {
		select {
		case <-done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return nil
}

// Delete removes providerID's credential from the base store, then its
// runtime key. Failed or cancelled deletion leaves the runtime key intact and preserves the cancellation cause.
func (c *RuntimeCredentials) Delete(ctx context.Context, providerID string) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if c.store != nil {
		if err := c.store.Delete(ctx, providerID); err != nil {
			return err
		}
	}
	c.RemoveRuntimeAPIKey(providerID)
	return nil
}
