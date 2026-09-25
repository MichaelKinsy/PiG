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

// Read implements CredentialStore: a runtime key reads as an api_key
// credential ahead of the base store.
func (c *RuntimeCredentials) Read(ctx context.Context, providerID string) (*Credential, error) {
	if err := ctx.Err(); err != nil {
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

// List implements CredentialStore: base entries, with every runtime key
// listed as an api_key entry.
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
	if err := ctx.Err(); err != nil {
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
// runtime key is unaffected.
func (c *RuntimeCredentials) Modify(ctx context.Context, providerID string, fn func(current *Credential) (*Credential, error)) (*Credential, error) {
	if c.store == nil {
		return nil, errors.New("runtime credentials: no base credential store")
	}
	return c.store.Modify(ctx, providerID, fn)
}

// credentialDeleter is a base store that can delete a provider's credential.
type credentialDeleter interface {
	Delete(providerID string) error
}

// Delete removes providerID's credential from the base store, then its
// runtime key.
func (c *RuntimeCredentials) Delete(ctx context.Context, providerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.store != nil {
		deleter, ok := c.store.(credentialDeleter)
		if !ok {
			return errors.New("runtime credentials: base credential store cannot delete")
		}
		if err := deleter.Delete(providerID); err != nil {
			return err
		}
	}
	c.RemoveRuntimeAPIKey(providerID)
	return nil
}
