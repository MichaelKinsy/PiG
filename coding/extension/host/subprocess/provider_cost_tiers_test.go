//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestNodePostBindProviderRegistrationRetainsCostTiers(t *testing.T) {
	// Original model/config and tier expectation: extensions-runner.test.ts:50-79,1173-1195.
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	source := filepath.Join(dir, "tier-owner.mjs")
	script := `export default function(pi){
 const config={baseUrl:"https://provider.test/v1",apiKey:"provider-test-key",api:"openai-completions",models:[{id:"instant-model",name:"Instant Model",reasoning:false,input:["text"],cost:{input:1,output:2,cacheRead:0.1,cacheWrite:1.25,tiers:[{inputTokensAbove:272000,input:2,output:3,cacheRead:0.2,cacheWrite:2.5}]},contextWindow:128000,maxTokens:4096}]};
 pi.registerCommand("register-tier",{handler:()=>pi.registerProvider("instant-provider",config)});
 pi.registerCommand("unregister-tier",{handler:()=>pi.unregisterProvider("instant-provider")});
}`
	if err := os.WriteFile(source, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("done") })
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	owner, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "tier-owner", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Commands["register-tier"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("instant-provider", "instant-model")
	if model == nil {
		t.Fatal("post-bind registration missing")
	}
	want := []ai.CostTier{{InputTokensAbove: 272000, InputCostPer1M: 2, OutputCostPer1M: 3, CacheReadCostPer1M: 0.2, CacheWriteCostPer1M: 2.5}}
	if !reflect.DeepEqual(model.Capabilities.CostTiers, want) {
		t.Fatalf("cost tiers=%+v want %+v", model.Capabilities.CostTiers, want)
	}
	if err := owner.Commands["unregister-tier"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if services.ModelRuntime().GetModel("instant-provider", "instant-model") != nil {
		t.Fatal("unregister retained model")
	}
}
