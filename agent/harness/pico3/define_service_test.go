package pico3

import "testing"

// DefineService keeps its pre-options signature, so a typed function value and a call-site conversion still compile. Upstream defineService(id, options) (packages/chord/src/api.ts:70-82) is DefineServiceWithOptions.
func TestDefineServiceSignatureAndOptions(t *testing.T) {
	define := func(f func(string) ServiceDefinition[string]) func(string) ServiceDefinition[string] { return f }(DefineService[string])
	if got := define("test.remote"); got.Id() != "test.remote" || got.Local() {
		t.Fatalf("DefineService = %q local=%t, want a remote token", got.Id(), got.Local())
	}
	if got := DefineServiceWithOptions[string]("test.local", ServiceOptions{Local: true}); got.Id() != "test.local" || !got.Local() {
		t.Fatalf("DefineServiceWithOptions = %q local=%t, want a local token", got.Id(), got.Local())
	}
	if got := DefineServiceWithOptions[string]("test.explicit-remote", ServiceOptions{}); got.Local() {
		t.Fatal("zero ServiceOptions must define a remote service")
	}
	for _, id := range []string{"", "$chord.reserved"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("DefineServiceWithOptions(%q) did not panic", id)
				}
			}()
			DefineServiceWithOptions[string](id, ServiceOptions{Local: true})
		}()
	}
}
