package extensionconformance

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi hands refreshModels the object of models.ts:554-562, {credential, stored, publish, allowNetwork, force, signal, baseUrl}: every key is there, in that order, and a member Pi has no value for is undefined. Node is the one SDK whose extension can see
// that (Object.keys, `=== undefined`); the Go, Rust, Python and Lisp SDKs decode the context into a typed record. The Node runtime builds the context for both forms of the callback, the config's refreshModels ("dyn") and the provider object's ("pixels").
func TestRefreshModelsContextMembersNode(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacementOf(t, []string{"node"}, func(t *testing.T, r *modelTypesRig) {
		config := dynamicProviderConfig(t, r)
		r.mu.Lock()
		object, ok := r.natives["pixels"]
		r.mu.Unlock()
		if !ok || object.RefreshModels == nil {
			t.Fatalf("the host registered no pixels provider object with refreshModels: %v", r.natives)
		}
		refresh := func(t *testing.T, form string, context ai.RefreshModelsContext) map[string]any {
			t.Helper()
			context.Signal = t.Context()
			context.Publish = func(ai.ModelsPublication) (bool, error) { return true, nil }
			var err error
			if form == "config" {
				_, err = config.RefreshModels(context)
			} else {
				err = object.RefreshModels(context)
			}
			if err != nil {
				t.Fatal(err)
			}
			report, _ := r.tool("refresh_context_report")[form].(map[string]any)
			if report == nil {
				t.Fatalf("the extension's %s refreshModels recorded no context", form)
			}
			return report
		}
		order := []any{"credential", "stored", "publish", "allowNetwork", "force", "signal", "baseUrl"}
		force := true
		for _, form := range []string{"config", "object"} {
			t.Run(form, func(t *testing.T) {
				full := refresh(t, form, ai.RefreshModelsContext{
					Credential: &ai.Credential{Type: ai.CredentialOAuth, Access: "tok"}, Stored: &ai.ModelsStoreEntry{CheckedAt: new(float64(77))},
					AllowNetwork: true, Force: &force, BaseURL: "https://dyn.test/v1",
				})
				if !reflect.DeepEqual(full["keys"], order) || !reflect.DeepEqual(full["undefinedMembers"], []any{}) || full["allowNetwork"] != true {
					t.Fatalf("a full context reached the %s refreshModels as %v, want keys %v and no undefined member", form, full, order)
				}
				// The members the host has no value for are undefined, not null and not missing: `ctx.credential === undefined` holds and the key is still listed.
				empty := refresh(t, form, ai.RefreshModelsContext{})
				if !reflect.DeepEqual(empty["keys"], order) || !reflect.DeepEqual(empty["undefinedMembers"], []any{"credential", "stored", "force", "baseUrl"}) || empty["allowNetwork"] != false {
					t.Fatalf("an empty context reached the %s refreshModels as %v, want keys %v with credential, stored, force and baseUrl undefined", form, empty, order)
				}
			})
		}
	})
}
