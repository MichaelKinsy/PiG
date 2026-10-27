package subprocess

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// registerGraphNativeProvider is registerNativeProvider for a graph ModelRuntime: the same connection-owned proxy (the calls the runtime makes on the carrier reach the extension that declared the Provider object, as the Session's do), registered in the registry register belongs to. The host keeps no claim on the provider's name beyond the proxy's own lifetime: the Session's registrations, OAuth bridging and ownership transfer are those of the extension's registration with the Session, and this one is the runtime's.
func (h *Host) registerGraphNativeProvider(ctx context.Context, conn *Conn, raw json.RawMessage, register func(context.Context, *extension.NativeProvider) error) (json.RawMessage, func(), error) {
	var declaration NativeProviderDeclaration
	if err := json.Unmarshal(raw, &declaration); err != nil {
		return nil, nil, err
	}
	if declaration.ID == "" || declaration.Key == "" || declaration.Auth == nil {
		return nil, nil, errors.New("native provider requires id, callback key and auth declaration")
	}
	h.mu.Lock()
	var owner *managedExt
	for _, me := range h.exts {
		if me.connection() == conn {
			owner = me
			break
		}
	}
	if owner == nil {
		h.mu.Unlock()
		return nil, nil, errors.New("the extension that declared the Provider object is not connected")
	}
	declaration.Handle = rand.Text()
	p := &nativeProviderProxy{host: h, owner: owner, registered: true, references: map[*Conn]map[string]struct{}{}, conn: conn, declaration: declaration, publications: map[string]nativeProviderCallback{}}
	if h.nativeProviderHandles == nil {
		h.nativeProviderHandles = map[string]*nativeProviderProxy{}
	}
	h.nativeProviderHandles[declaration.Handle] = p
	h.mu.Unlock()
	carrier := p.carrier()
	// The runtime's registration is current while the proxy is registered; the Session's table of registrations is not consulted.
	carrier.IsCurrent = func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return p.registered && h.nativeProviderHandles[declaration.Handle] == p
	}
	var released bool
	release := func() {
		h.mu.Lock()
		if released {
			h.mu.Unlock()
			return
		}
		released = true
		p.registered = false
		dropped := h.collectProviderObjectLocked(p)
		h.mu.Unlock()
		h.releaseProviderCallbacks(dropped)
	}
	if err := register(ctx, carrier); err != nil {
		release()
		return nil, nil, err
	}
	extension.CallInitiated(ctx)
	declared, err := json.Marshal(declaration)
	if err != nil {
		release()
		return nil, nil, err
	}
	return declared, release, nil
}
