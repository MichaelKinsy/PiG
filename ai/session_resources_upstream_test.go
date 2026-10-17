package ai

import (
	"errors"
	"slices"
	"testing"
)

// session-resources.ts: registerSessionResourceCleanup adds a hook and returns its remover; cleanupSessionResources calls every hook
// with the session id in registration order, keeps going when a hook throws, and then throws an AggregateError
// ("Failed to cleanup session resources") holding each failure.
func TestSessionResourceCleanupCallsHooksInOrderAndUnregisters(t *testing.T) {
	var calls []string
	unregisterA := RegisterSessionResourceCleanup(func(id string) { calls = append(calls, "a:"+id) })
	unregisterB := RegisterSessionResourceCleanup(func(id string) { calls = append(calls, "b:"+id) })
	defer unregisterB()
	defer unregisterA()

	if err := CleanupSessionResources("s1"); err != nil {
		t.Fatal(err)
	}
	unregisterA()
	unregisterA() // removing twice is a no-op, as Set.delete is
	if err := CleanupSessionResources("s2"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a:s1", "b:s1", "b:s2"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestSessionResourceCleanupRunsEveryHookAndAggregatesFailures(t *testing.T) {
	boom := errors.New("boom")
	var calls []string
	unregister := []func(){
		RegisterSessionResourceCleanup(func(string) { calls = append(calls, "first"); panic(boom) }),
		RegisterSessionResourceCleanup(func(string) { calls = append(calls, "second") }),
		RegisterSessionResourceCleanup(func(string) { calls = append(calls, "third"); panic("text") }),
	}
	defer func() {
		for _, remove := range unregister {
			remove()
		}
	}()

	err := CleanupSessionResources("s")
	if err == nil || err.Error() != "Failed to cleanup session resources" {
		t.Fatalf("err = %v, want the AggregateError message", err)
	}
	if want := []string{"first", "second", "third"}; !slices.Equal(calls, want) {
		t.Fatalf("a failing hook stopped the others: calls = %v, want %v", calls, want)
	}
	members, ok := err.(interface{ Unwrap() []error })
	if !ok || len(members.Unwrap()) != 2 {
		t.Fatalf("aggregate members = %v", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("the thrown error is a member of the aggregate: %v", err)
	}
}
