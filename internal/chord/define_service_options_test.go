package chord

import "testing"

// upstream: packages/chord/src/api.ts:73-85 defineService(id, options): the required `{ local: true }` overload freezes `{ id, local }`, an empty id throws "Service ID must not be empty", and an id beginning with "$chord." throws the reserved-namespace error.
func TestDefineServiceWithOptionsRequiresValidIDsAndKeepsLocal(t *testing.T) {
	local := DefineServiceWithOptions[string]("test.local", ServiceOptions{Local: true})
	remote := DefineServiceWithOptions[string]("test.remote", ServiceOptions{})
	if !local.local || local.id != "test.local" || remote.local || remote.id != "test.remote" {
		t.Fatalf("local = %+v, remote = %+v", local, remote)
	}
	if got := DefineService[string]("test.variadic", ServiceOptions{Local: true}); !got.local {
		t.Fatalf("DefineService dropped its options: %+v", got)
	}
	for id, want := range map[string]string{"": "Service ID must not be empty", "$chord.x": "Service IDs beginning with $chord. are reserved"} {
		func() {
			defer func() {
				if got := recover(); got != want {
					t.Fatalf("DefineServiceWithOptions(%q) panicked with %v, want %q", id, got, want)
				}
			}()
			DefineServiceWithOptions[string](id, ServiceOptions{})
		}()
	}
}
