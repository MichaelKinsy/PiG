package sdk

import (
	"encoding/json"
	"testing"
)

// Pi keeps one effective registration per provider and merges a later registration's defined values over it, whichever extension makes it (model-runtime.ts:921-940). After another extension's partial re-registration, the host's merged registration still calls this extension's streamSimple, operations and OAuth closures, and a retained Provider object keeps its callbacks until provider_release. provider_superseded therefore drops none of them (TestLateProviderSupersededByAnotherExtensionKeepsOperationsAcrossSDKs runs the cross-process path).
func TestProviderSupersededKeepsTheCallbacksTheHostStillCalls(t *testing.T) {
	ext := New("superseded-probe")
	ext.providerMu.Lock()
	ext.providerStreams = map[string]ProviderStreamSimpleFunc{"gone": func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
		return nil, nil
	}}
	ext.providerOperations = map[string]providerOperations{"gone": {}}
	ext.oauthProviders = map[string]*OAuthProvider{"gone": {}}
	ext.nativeProviders = map[string]*Provider{"key-gone": {ID: "gone"}}
	ext.providerMu.Unlock()

	args, _ := json.Marshal(map[string]string{"name": "gone"})
	ext.handleNotify(envelope{Type: msgNotify, Notify: &notifyMsg{Method: "provider_superseded", Args: args}})

	ext.providerMu.Lock()
	defer ext.providerMu.Unlock()
	if ext.providerStreams["gone"] == nil {
		t.Error("the superseded author's streamSimple was dropped while the merged registration still calls it")
	}
	if _, ok := ext.providerOperations["gone"]; !ok {
		t.Error("the superseded author's image and classifier implementations were dropped while the merged registration still calls them")
	}
	if ext.oauthProviders["gone"] == nil {
		t.Error("the superseded author's OAuth closures were dropped while the host's OAuth login still calls them")
	}
	if ext.nativeProviders["key-gone"] == nil {
		t.Error("a Provider object's callbacks were dropped before provider_release")
	}
}
