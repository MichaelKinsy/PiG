package coding

import (
	"reflect"
	"testing"
)

func TestModelResolverPersistedScopeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name            string
		scoped, persist bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:873
		{"adds a persisted default to an existing scoped model list", true, true},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:893
		{"does not create a scoped model list when all models are available", false, true},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:902
		{"keeps session-only model changes out of scope", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := newTestServices(t)
			sonnet, err := BuildModel("anthropic/claude-sonnet-4-5", services)
			if err != nil {
				t.Fatal(err)
			}
			opus, err := BuildModel("anthropic/claude-opus-4-8", services)
			if err != nil {
				t.Fatal(err)
			}
			services.Registry().SetRuntimeAPIKey("anthropic", "test-key")
			options := SessionOptions{Model: sonnet, NoSession: true, SystemPrompt: "test", SkipBuiltinTools: true}
			var want []string
			if tc.scoped {
				options.ScopedModels = []ScopedModel{{Model: sonnet}}
				want = []string{"anthropic/claude-sonnet-4-5"}
				if err := services.SettingsManager().UpdateGlobal(func(settings *Settings) { settings.EnabledModels = want }); err != nil {
					t.Fatal(err)
				}
			}
			session, err := NewSession(services, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			if err := session.SetModel(opus, ModelMutationOptions{Persist: tc.persist}); err != nil {
				t.Fatal(err)
			}
			if tc.persist {
				settings := services.Settings()
				if settings.DefaultProvider != "anthropic" || settings.DefaultModel != "claude-opus-4-8" {
					t.Fatalf("defaults=%s/%s", settings.DefaultProvider, settings.DefaultModel)
				}
				if tc.scoped {
					want = append(want, "anthropic/claude-opus-4-8")
				}
			}
			var actual []string
			for _, entry := range session.ScopedModels() {
				actual = append(actual, providerID(entry.Model)+"/"+entry.Model.ID)
			}
			if !reflect.DeepEqual(actual, want) {
				t.Errorf("session scope=%v, want %v", actual, want)
			}
			if actual := services.SettingsManager().GetEnabledModels(); !reflect.DeepEqual(actual, want) {
				t.Errorf("settings scope=%v, want %v", actual, want)
			}
		})
	}
}
