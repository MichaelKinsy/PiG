//go:build !pig_strip_llama_cpp

package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/tui"
)

// An extension's registered native provider with an API-key auth method (the built-in llama.cpp) is offered by /login, at its
// name-ordered position, and only while it is registered.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts getLoginProviderOptions
func TestLoginListsARegisteredNativeAPIKeyProviderInNameOrder(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	registry := NewModelRegistry(t.TempDir())
	m.opts.ModelRegistry = registry
	names := func() []string {
		var out []string
		for _, provider := range m.oauthProviderList("login-api-key") {
			out = append(out, provider.Name)
		}
		return out
	}
	if slices.Contains(names(), "llama.cpp") {
		t.Fatal("llama.cpp is offered before it is registered")
	}
	if err := registry.RegisterNativeModelsProvider(llama.CreateLlamaProvider().ModelsProvider()); err != nil {
		t.Fatal(err)
	}
	listed := m.oauthProviderList("login-api-key")
	index := slices.IndexFunc(listed, func(p tui.OAuthProvider) bool { return p.ID == llama.LlamaProviderID })
	if index < 0 || listed[index].Name != "llama.cpp" || listed[index].AuthType != "api_key" {
		t.Fatalf("login list = %+v, want llama.cpp as an api_key provider", listed)
	}
	if index > 0 && loginNameCollator.CompareString(listed[index-1].Name, "llama.cpp") > 0 || index+1 < len(listed) && loginNameCollator.CompareString(listed[index+1].Name, "llama.cpp") < 0 {
		t.Fatalf("llama.cpp is out of name order among %v", names())
	}
}
