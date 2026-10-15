package harness

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// RegistryReader (packages/durable/src/harness/types.ts:291-298) is what a Harness reads: `snapshot()` is an "immutable view of the whole current registry" and `subscribe(listener)`
// is "called synchronously after every publication", returning an unsubscribe (registry.ts:62-91). Every install, replacement and uninstall of an installed extension
// publishes at once, so a listener already sees the new snapshot; a snapshot taken earlier does not change; uninstalling an extension that is not installed and an install that
// fails publish nothing.
// mutation-checked: notifying before the new state is current, replacing an installed extension in place, notifying after a failed install or an absent uninstall, or ignoring unsubscribe fails it
func TestRegistryReaderSnapshotsAreImmutableAndSubscribersSeeEachPublication(t *testing.T) {
	registry := CreateRegistry()
	var reader RegistryReader = registry
	names := func(snapshot durable.RegistrySnapshot) []string {
		var got []string
		for _, installed := range snapshot.Sections() {
			got = append(got, installed.Extension.Name+":"+installed.Section.Key)
		}
		return got
	}

	var seen [][]string
	var calls int
	unsubscribe := reader.Subscribe(func() {
		calls++
		seen = append(seen, names(reader.Snapshot()))
	})
	var second int
	reader.Subscribe(func() { second++ })

	first := &durable.Extension{Name: "first", Sections: []*durable.PromptSection{Section("a", text("a"))}}
	before := reader.Snapshot()
	if err := registry.Install(first); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || second != 1 || !slices.Equal(seen[0], []string{"first:a"}) {
		t.Fatalf("after install: calls = %d/%d, seen = %v; want one synchronous publication that already shows the new snapshot", calls, second, seen)
	}
	if got := names(before); len(got) != 0 {
		t.Fatalf("a snapshot taken before the install now lists %v, want it unchanged", got)
	}

	held := reader.Snapshot()
	replacement := &durable.Extension{Name: "first", Sections: []*durable.PromptSection{Section("b", text("b"))}}
	if err := registry.Install(replacement); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !slices.Equal(seen[1], []string{"first:b"}) || !slices.Equal(names(held), []string{"first:a"}) {
		t.Fatalf("after replace: calls = %d, seen = %v, held = %v", calls, seen, names(held))
	}

	// A failed install publishes nothing: a task name that a built-in already holds collides.
	colliding := &durable.Extension{Name: "colliding", Tasks: []durable.AnyTask{GenerationTask.Erased()}}
	if err := registry.Install(colliding); err == nil {
		t.Fatal("an extension whose task collides with a built-in task was installed")
	}
	// Uninstalling an extension that is not installed publishes nothing.
	registry.Uninstall(&durable.Extension{Name: "absent"})
	if calls != 2 || second != 2 {
		t.Fatalf("a failed install or an absent uninstall published: calls = %d/%d, want 2", calls, second)
	}

	registry.Uninstall(replacement)
	if calls != 3 || len(seen[2]) != 0 {
		t.Fatalf("after uninstall: calls = %d, seen = %v", calls, seen)
	}

	unsubscribe()
	if err := registry.Install(first); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || second != 4 {
		t.Fatalf("after unsubscribe: calls = %d (want 3), other listener = %d (want 4)", calls, second)
	}
}
