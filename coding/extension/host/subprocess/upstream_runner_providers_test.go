package subprocess

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi loader.ts:202-217 retains registrations before bindCore supplies a registry. The Go Host must not discard a factory registration merely because its registry callback binds after load.
func TestHostProviderRegistrationBeforeBinding(t *testing.T) {
	nodeCellRequireNode(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			root := t.TempDir()
			entry := filepath.Join(root, "queued.ts")
			write(t, entry, `export default function(pi) {
  pi.registerProvider("queued-provider", {baseUrl:"https://provider.test/v1",api:"openai-completions",apiKey:"provider-test-key"});
}`)
			host := NewHost(root)
			t.Cleanup(func() { host.Shutdown("test done") })
			loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: "queued", Source: entry, Enabled: true, Isolation: isolation}})
			if len(failures) != 0 || len(loaded) != 1 {
				t.Fatalf("loaded=%v failures=%v", loaded, failures)
			}
			var names []string
			host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
				if config.BaseURL != "https://provider.test/v1" || config.API != "openai-completions" || config.APIKey != "provider-test-key" {
					t.Fatalf("config changed before bind: %+v", config)
				}
				names = append(names, name)
				return nil
			}, nil)
			if !reflect.DeepEqual(names, []string{"queued-provider"}) {
				t.Fatalf("bound registrations=%v; want queued-provider", names)
			}
		})
	}
}
