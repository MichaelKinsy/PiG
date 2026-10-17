package ai

import (
	"slices"
	"strings"
	"testing"
)

// session-resources.ts: registerSessionResourceCleanup returns an unregister function, and cleanupSessionResources runs every registered hook with the session id.
func TestCleanupSessionResourcesRunsEveryRegisteredHookUntilItIsUnregistered(t *testing.T) {
	var got []string
	unregisterFirst := RegisterSessionResourceCleanup(func(id string) { got = append(got, "first:"+id) })
	unregisterSecond := RegisterSessionResourceCleanup(func(id string) { got = append(got, "second:"+id) })
	t.Cleanup(unregisterFirst)
	t.Cleanup(unregisterSecond)

	if err := CleanupSessionResources("s1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"first:s1", "second:s1"}; !slices.Equal(got, want) {
		t.Fatalf("hooks = %v, want %v in registration order", got, want)
	}

	got = nil
	unregisterFirst()
	if err := CleanupSessionResources("s2"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"second:s2"}; !slices.Equal(got, want) {
		t.Fatalf("after unregistering the first hook: %v, want %v", got, want)
	}
	unregisterFirst() // a second unregister is a no-op
}

// session-resources.ts:19-25: a throwing hook does not stop the later ones, and the failures are reported together after every hook ran (AggregateError "Failed to cleanup session resources").
func TestCleanupSessionResourcesRunsLaterHooksAfterAPanicAndReportsIt(t *testing.T) {
	var ran []string
	for _, hook := range []SessionResourceCleanup{
		func(string) { ran = append(ran, "a") },
		func(string) { panic("boom") },
		func(string) { ran = append(ran, "c") },
	} {
		t.Cleanup(RegisterSessionResourceCleanup(hook))
	}

	err := CleanupSessionResources("s")
	if want := []string{"a", "c"}; !slices.Equal(ran, want) {
		t.Fatalf("hooks that ran = %v, want %v", ran, want)
	}
	// Pi throws an AggregateError whose message is fixed; the thrown value is a member, not part of the message.
	members, ok := err.(interface{ Unwrap() []error })
	if err == nil || err.Error() != "Failed to cleanup session resources" || !ok || len(members.Unwrap()) != 1 || !strings.Contains(members.Unwrap()[0].Error(), "boom") {
		t.Fatalf("error = %v, want the AggregateError carrying the panic as its one member", err)
	}
}
