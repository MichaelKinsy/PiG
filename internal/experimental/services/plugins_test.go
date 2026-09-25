package services

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// services/plugins.ts defines these exact tokens and awaited method signatures.
func TestPluginServiceContracts(t *testing.T) {
	for _, test := range []struct {
		name     string
		id       string
		local    bool
		contract reflect.Type
		methods  map[string]reflect.Type
	}{
		{"presentation", PresentationPluginsDefinition.Id(), PresentationPluginsDefinition.Local(), reflect.TypeFor[PresentationPlugins](), map[string]reflect.Type{
			"PrepareSession": reflect.TypeFor[func(context.Context, PrepareSessionPluginsRequest) (pico3.JsonValue, error)](),
			"Reload":         reflect.TypeFor[func(context.Context) (pico3.JsonValue, error)](),
		}},
		{"session", SessionPluginsDefinition.Id(), SessionPluginsDefinition.Local(), reflect.TypeFor[SessionPlugins](), map[string]reflect.Type{
			"Reload": reflect.TypeFor[func(context.Context) error](),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if want := "pi." + test.name + "-plugins"; test.id != want || test.local {
				t.Fatalf("token = %q, local=%v; want %q, false", test.id, test.local, want)
			}
			if test.contract.NumMethod() != len(test.methods) {
				t.Fatalf("methods = %d, want %d", test.contract.NumMethod(), len(test.methods))
			}
			for name, want := range test.methods {
				method, ok := test.contract.MethodByName(name)
				if !ok || method.Type != want {
					t.Errorf("%s = %v, want %v", name, method.Type, want)
				}
			}
		})
	}
}

// A null selection means default packages; [] is an explicit empty selection.
func TestPrepareSessionPluginsRequestJSON(t *testing.T) {
	for _, test := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"default", nil, `{"sessionId":"session-1","packagePaths":null}`},
		{"empty", []string{}, `{"sessionId":"session-1","packagePaths":[]}`},
		{"ordered", []string{"z", "a", "z"}, `{"sessionId":"session-1","packagePaths":["z","a","z"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := PrepareSessionPluginsRequest{SessionId: "session-1", PackagePaths: test.paths}
			data, err := json.Marshal(request)
			if err != nil || string(data) != test.want {
				t.Fatalf("marshal = %s, %v; want %s", data, err, test.want)
			}
			var decoded PrepareSessionPluginsRequest
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, request) {
				t.Fatalf("round trip = %#v, want %#v", decoded, request)
			}
		})
	}
}
