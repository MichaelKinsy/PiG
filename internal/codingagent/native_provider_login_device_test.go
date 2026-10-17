package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// /login passes the installation's device ID to a provider object's OAuth login: Pi's loginProvider gives Models.login { getDeviceId: () => settingsManager.getOrCreateDeviceId() } (interactive-mode.ts:6331), which hands it to oauth.login(interaction, options) (models.ts:776, auth/types.ts:231).
func TestNativeProviderOAuthLoginFromSlashLoginGetsTheSettingsDeviceID(t *testing.T) {
	m := newNativeLoginMode(t, "node")
	dir := t.TempDir()
	entry := filepath.Join(dir, "device-login.mjs")
	source := `export default pi => pi.registerProvider({id:"device-proxy", name:"Device Proxy",
 auth:{oauth:{name:"Device Proxy SSO", login: async (interaction, options) => ({type:"oauth", access:options.getDeviceId(), refresh:"r", expires:4102444800000}), refresh: async c => c, toAuth: async c => ({apiKey:c.access})}},
 getModels: () => [], stream(){ throw new Error("unused"); }, streamSimple(){ throw new Error("unused"); }});`
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetNativeProviderCallback(m.opts.ModelRegistry.RegisterNativeProvider)
	if _, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{{Name: "device-login", Source: entry, Enabled: true}}); len(errs) != 0 {
		t.Fatalf("load: %v", errs)
	}
	awaitLogin(t, dispatchLogin(t, m, "device-proxy"))
	credential, ok := storedNativeCredential(t, m, "device-proxy")
	want := m.opts.SettingsManager.GetOrCreateDeviceID()
	if !ok || want == "" || credential.Access != want {
		t.Fatalf("stored credential = %+v (present %v), want access = the settings device ID %q", credential, ok, want)
	}
}
