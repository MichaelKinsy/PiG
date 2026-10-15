package ai

import (
	"reflect"
	"slices"
	"testing"
)

// Pi's OAuthLoginCallbacks (packages/ai/src/compat/extension-oauth-types.ts:35-43) are onAuth, onDeviceCode, onPrompt, onProgress,
// onManualCodeInput, onSelect and signal (the Context forms carry it). Information a flow shows beside its prompts is an AuthInfoEvent
// the flow's interaction notifies (notifyAuthDialog, interactive-mode.ts:6308), not a login callback; a callback nothing calls and no
// wire method reaches only hides the missing route. The Go-only members are the flow's own manual code prompt, the device ID and the agent name.
func TestOAuthLoginCallbacksAreTheCallbacksOfPi(t *testing.T) {
	allowed := []string{
		"OnAuth", "OnDeviceCode", "OnPrompt", "OnPromptContext", "OnProgress", "OnManualCodeInput", "OnManualCodeInputContext",
		"OnSelect", "OnSelectContext", "OnManualCodePromptContext", "GetDeviceID", "AgentName",
	}
	typ := reflect.TypeFor[OAuthLoginCallbacks]()
	for field := range typ.Fields() {
		if !slices.Contains(allowed, field.Name) {
			t.Errorf("OAuthLoginCallbacks.%s is not a callback of Pi's OAuthLoginCallbacks", field.Name)
		}
	}
	for _, name := range allowed {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("OAuthLoginCallbacks has no %s", name)
		}
	}
}
