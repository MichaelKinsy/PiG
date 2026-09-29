package subprocess

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi ModelRuntime.registerProvider validates before replacing a native registration (model-runtime.ts:753-766). Decoder and registry errors must both leave its callable owner intact.
func TestRejectedProviderRegistrationKeepsNativeOwner(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "registry error", true: "decode error"}[malformed], func(t *testing.T) {
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			// The unstarted Conn records frames only; it owns no socket or workers to close.
			owner := withConn(&managedExt{providerNames: []string{"provider"}}, NewConn("owner", nil))
			provider := &nativeProviderProxy{host: host, owner: owner, conn: owner.connection(), registered: true, references: map[*Conn]map[string]struct{}{}, declaration: NativeProviderDeclaration{ID: "provider", Key: "callback", Handle: "handle"}}
			host.nativeProviders = map[*Conn]map[string]*nativeProviderProxy{owner.connection(): {"provider": provider}}
			host.nativeProviderHandles = map[string]*nativeProviderProxy{"handle": provider}
			sentinel := errors.New("invalid incoming model definitions")
			host.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return sentinel }, nil)
			config := `{"name":"provider","config":{"api":"openai-completions"}}`
			if malformed {
				config = `{"name":"provider","config":{"models":"invalid array"}}`
			}
			_, err := host.handleProviderRegistrationCall(t.Context(), owner, owner.connection(), "", &CallPayload{Method: "registerProvider", Args: json.RawMessage(config)})
			if err == nil || (!malformed && !errors.Is(err, sentinel)) {
				t.Fatalf("registration error=%v", err)
			}
			if host.nativeProviders[owner.connection()]["provider"] != provider || !provider.registered || host.nativeProviderHandles["handle"] != provider || !reflect.DeepEqual(owner.providerNames, []string{"provider"}) || len(owner.connection().outCh) != 0 {
				t.Fatal("rejected registration retired or released the existing native Provider")
			}
		})
	}
}
