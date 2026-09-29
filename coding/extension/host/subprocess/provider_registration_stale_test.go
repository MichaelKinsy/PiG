package subprocess

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// A registration or unregistration that an extension makes after a reload replaced it must not take the live replacement's provider claim (model-runtime.ts:744-766 keeps one effective registration; the replacement is the extension Pi has).
func TestReplacedExtensionRegistrationCallsAreRefused(t *testing.T) {
	for _, method := range []string{"registerProvider", "unregisterProvider"} {
		t.Run(method, func(t *testing.T) {
			rig := newOAuthProxyRig(t)
			live := &managedExt{config: ExtConfig{Name: "live"}, providerNames: []string{"p"}, oauthProviderNames: []string{"p"}}
			rig.host.exts = map[string]*managedExt{"live": live}
			rig.me.shuttingDown.Store(true)
			args, _ := json.Marshal(map[string]any{"name": "p", "config": map[string]any{}})
			if _, err := rig.host.handleProviderRegistrationCall(context.Background(), rig.me, rig.me.connection(), "c1", &CallPayload{Method: method, Args: args}); err == nil {
				t.Fatal("a replaced extension's call was applied")
			}
			if !slices.Contains(live.providerNames, "p") || !slices.Contains(live.oauthProviderNames, "p") {
				t.Fatalf("the live replacement lost its claim: %v %v", live.providerNames, live.oauthProviderNames)
			}
			if slices.Contains(rig.me.providerNames, "p") {
				t.Fatal("the replaced extension took the claim")
			}
		})
	}
}
