package subprocess

import (
	"encoding/json"
	"testing"
)

func TestProviderObjectReferenceLifetime(t *testing.T) {
	h := NewHost(t.TempDir())
	owner := withConn(&managedExt{providerNames: []string{"provider"}}, NewConn("owner", nil))
	reader := withConn(&managedExt{}, NewConn("reader", nil))
	provider := &nativeProviderProxy{host: h, owner: owner, conn: owner.connection(), registered: true, references: map[*Conn]map[string]struct{}{}, declaration: NativeProviderDeclaration{ID: "provider", Key: "callback", Handle: "handle"}}
	h.nativeProviders = map[*Conn]map[string]*nativeProviderProxy{owner.connection(): {"provider": provider}}
	h.nativeProviderHandles = map[string]*nativeProviderProxy{"handle": provider}
	reference := func(method, token string) {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"handle": "handle", "token": token})
		if _, err := h.handleProviderReference(reader.connection(), &CallPayload{Method: method, Args: args}); err != nil {
			t.Fatal(err)
		}
	}
	reference("provider.retain", "first")
	reference("provider.retain", "second")
	h.mu.Lock()
	released, superseded := h.retireNativeProviderLocked(reader, reader.connection(), "provider")
	h.mu.Unlock()
	if len(released) != 0 || h.nativeProviderHandles["handle"] != provider {
		t.Fatal("unregister destroyed a captured Provider")
	}
	// The retired owner runs in another process than the caller, so its local registration is superseded (Pi keeps one effective registration per provider).
	if len(superseded) != 1 || superseded[0] != owner.connection() {
		t.Fatalf("superseded = %v, want the retired owner's connection", superseded)
	}
	if len(owner.providerNames) != 0 {
		t.Fatal("retired owner can still unregister a replacement")
	}
	reference("provider.release", "first")
	if h.nativeProviderHandles["handle"] != provider {
		t.Fatal("one release destroyed another live reference")
	}
	provider.calls++
	reference("provider.release", "second")
	if h.nativeProviderHandles["handle"] != provider {
		t.Fatal("release destroyed an active invocation")
	}
	h.mu.Lock()
	provider.calls--
	released = h.collectProviderObjectLocked(provider)
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	if len(h.nativeProviderHandles) != 0 {
		t.Fatal("unreferenced retired Provider was retained")
	}
	var message Envelope
	if err := json.Unmarshal((<-owner.connection().outCh).payloadBytes(), &message); err != nil {
		t.Fatal(err)
	}
	if message.Notify == nil || message.Notify.Method != "provider_release" || string(message.Notify.Args) != `{"key":"callback"}` {
		t.Fatalf("callback release: %+v", message)
	}
}
