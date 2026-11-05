package cli

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// fusedStageResolver serves the names it holds the way a Piglet Binary's generated registration does.
type fusedStageResolver map[string]func(net.Conn) error

func (r fusedStageResolver) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	serve, ok := r[cfg.Name]
	return serve, ok
}

func installFusedStageResolver(t *testing.T, names ...string) {
	t.Helper()
	resolver := fusedStageResolver{}
	for _, name := range names {
		resolver[name] = sdk.New(name).RunWithConn
	}
	subprocess.SetFusedResolver(resolver)
	t.Cleanup(func() { subprocess.SetFusedResolver(nil) })
}

// A Piglet Binary whose extensions are all fused runs them in this process from their compiled factories. They read no staged SDK, so the load
// must not ask for the SDKs to be staged: staging writes about 300 files and cost the first start on a clean config root 45 ms before its first prompt.
func TestFusedLoadStagesNoSDK(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	installFusedStageResolver(t, "fused-stage")

	// The hook is the production one: it stages the SDKs into the config root when the load needs them.
	var asked []bool
	stage := func(needsStagedSDK bool) {
		asked = append(asked, needsStagedSDK)
		if needsStagedSDK {
			if err := stageExtensionSDKsFor(t.Context(), codingagent.ConfigRoot(), time.Minute); err != nil {
				t.Errorf("stage SDKs: %v", err)
			}
		}
	}
	loaded, host, _, errs := loadFinalSubprocessExtensions(t.Context(), t.TempDir(), extension.ModePrint, nil, []subprocess.ExtConfig{{Name: "fused-stage", Enabled: true}}, nil, nil, nil, stage)
	if host != nil {
		t.Cleanup(func() { host.Shutdown("quit") })
	}
	if len(errs) != 0 || len(loaded) != 1 || loaded[0].Name != "fused-stage" {
		t.Fatalf("fused load = %v, errors %v; want fused-stage with no error", loaded, errs)
	}
	if len(asked) != 1 || asked[0] {
		t.Fatalf("a load of only fused extensions asked for staged SDKs = %v, want one call that needs none", asked)
	}
	if staged, err := filepath.Glob(filepath.Join(os.Getenv("PIG_HOME"), "state", "pigsdk", "sdk-*")); err != nil || len(staged) != 0 {
		t.Fatalf("a load of only fused extensions staged the SDKs: %v (glob error %v)", staged, err)
	}
}

// What builds or resolves from source still needs the staged SDKs, whatever is fused beside it: a configured extension the Binary does not
// carry, an embedded cell, and a configuration the pre-trust pass already loaded.
func TestLoadNeedsStagedSDK(t *testing.T) {
	installFusedStageResolver(t, "fused-a", "fused-b")
	fusedA := subprocess.ExtConfig{Name: "fused-a", Enabled: true}
	fusedB := subprocess.ExtConfig{Name: "fused-b", Enabled: true}
	source := subprocess.UnresolvedExtConfig("/x/extension.ts", nil)
	tests := []struct {
		name      string
		configs   []subprocess.ExtConfig
		embedded  []subprocess.EmbeddedCell
		preloaded *startupExtensionSet
		want      bool
	}{
		{name: "nothing"},
		{name: "fused extensions", configs: []subprocess.ExtConfig{fusedA, fusedB}},
		{name: "source extension", configs: []subprocess.ExtConfig{source}, want: true},
		{name: "source extension among fused", configs: []subprocess.ExtConfig{fusedA, source, fusedB}, want: true},
		{name: "embedded cell", configs: []subprocess.ExtConfig{fusedA}, embedded: []subprocess.EmbeddedCell{{Language: "go"}}, want: true},
		{name: "pre-trust set had a source extension", configs: []subprocess.ExtConfig{fusedA}, preloaded: &startupExtensionSet{configs: []subprocess.ExtConfig{source}}, want: true},
		{name: "pre-trust set had only fused", configs: []subprocess.ExtConfig{fusedA}, preloaded: &startupExtensionSet{configs: []subprocess.ExtConfig{fusedB}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := loadNeedsStagedSDK(tc.configs, tc.embedded, tc.preloaded); got != tc.want {
				t.Fatalf("loadNeedsStagedSDK = %t, want %t", got, tc.want)
			}
		})
	}
}

// Stock pig installs no resolver, so every extension it loads needs the SDKs.
func TestLoadNeedsStagedSDKWithoutFusedResolver(t *testing.T) {
	if !loadNeedsStagedSDK([]subprocess.ExtConfig{{Name: "fused-a", Enabled: true}}, nil, nil) {
		t.Fatal("a stock pig load of a configured extension did not ask for the staged SDKs")
	}
}
