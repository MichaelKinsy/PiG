package codingagent

// Ports packages/coding-agent/src/core/model-runtime.ts.

import (
	"context"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// CredentialSynchronizationOperation identifies the credential change that committed before synchronization failed.
type CredentialSynchronizationOperation string

const (
	CredentialSynchronizationLogin               CredentialSynchronizationOperation = "login"
	CredentialSynchronizationLogout              CredentialSynchronizationOperation = "logout"
	CredentialSynchronizationSetRuntimeAPIKey    CredentialSynchronizationOperation = "setRuntimeApiKey"
	CredentialSynchronizationRemoveRuntimeAPIKey CredentialSynchronizationOperation = "removeRuntimeApiKey"
)

// CredentialSynchronizationError reports a committed credential change whose local model/auth snapshot could not be synchronized.
type CredentialSynchronizationError struct {
	ProviderID string
	Operation  CredentialSynchronizationOperation
	Credential *ai.Credential
	Cause      error
}

func (e *CredentialSynchronizationError) Error() string {
	return fmt.Sprintf("Credential %s committed for %s, but local synchronization failed", e.Operation, e.ProviderID)
}

func (e *CredentialSynchronizationError) Unwrap() error { return e.Cause }

// beginCredentialOperation serializes same-provider operations through local synchronization. A queued cancellation removes only that waiter; an active caller owns its operation until settlement.
func (r *ModelRegistry) beginCredentialOperation(ctx context.Context, id string) (func(), error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	r.nativeMu.Lock()
	if r.credentialOperations == nil {
		r.credentialOperations = make(map[string][]chan struct{})
	}
	ready := make(chan struct{})
	queue := r.credentialOperations[id]
	r.credentialOperations[id] = append(queue, ready)
	if len(queue) == 0 {
		close(ready)
	}
	r.nativeMu.Unlock()
	release := func() {
		r.nativeMu.Lock()
		defer r.nativeMu.Unlock()
		queue := r.credentialOperations[id]
		index := slices.Index(queue, ready)
		queue = slices.Delete(queue, index, index+1)
		if len(queue) == 0 {
			delete(r.credentialOperations, id)
		} else {
			r.credentialOperations[id] = queue
			if index == 0 {
				close(queue[0])
			}
		}
	}
	select {
	case <-ready:
	case <-ctx.Done():
	}
	if err := context.Cause(ctx); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (r *ModelRegistry) synchronizeCredentialState(ctx context.Context, id string, operation CredentialSynchronizationOperation, credential *ai.Credential) error {
	err := context.Cause(ctx)
	if err == nil {
		r.nativeMu.Lock()
		base, input := r.nativeBase[id], r.nativeInputs[id]
		r.nativeMu.Unlock()
		if base != nil {
			var composed *ai.ModelsProvider
			composed, err = r.composeNativeProvider(base, input)
			if err == nil {
				r.NativeModels().SetProvider(composed)
			}
		}
	}
	if err == nil {
		result := r.RefreshNativeProviders(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{id}})
		err = result.Errors[id]
		if result.Aborted {
			err = context.Cause(ctx)
		}
	}
	if err != nil {
		return &CredentialSynchronizationError{ProviderID: id, Operation: operation, Credential: credential, Cause: err}
	}
	return nil
}
