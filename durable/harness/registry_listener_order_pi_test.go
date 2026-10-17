package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// pi: packages/durable/src/harness/registry.ts:62-70,89-92 (RegistryReader.subscribe, types.ts:292-297): listeners are a Set, so publication calls them in
// subscription order, over a copy taken when the publication starts (`for (const listener of [...this.#listeners])`): a listener that subscribes
// another during a publication does not see that one called in the same round, and a listener that unsubscribes a later one still lets it run in
// that round. Uninstalling an extension that is not installed returns before publishing (registry.ts:82-83), so no listener runs. The expected calls
// follow from those lines (Set insertion order, the spread copy); Pi's durable runtime is not installable here to run them.
// mutation-checked: prepending a new listener, iterating the live list, and publishing for an absent uninstall each fail it.
func TestRegistryListenersRunInSubscriptionOrderOverACopyTakenWhenPublicationStarts(t *testing.T) {
	registry := CreateRegistry()
	var calls []string
	var stopSecond func()
	var late bool
	registry.Subscribe(func() {
		calls = append(calls, "first")
		stopSecond()
		if !late {
			late = true
			registry.Subscribe(func() { calls = append(calls, "late") })
		}
	})
	stopSecond = registry.Subscribe(func() { calls = append(calls, "second") })
	registry.Subscribe(func() { calls = append(calls, "third") })

	mustInstall(t, registry, new(durable.Extension{Name: "a"}))
	expectStrings(t, calls, []string{"first", "second", "third"})

	calls = nil
	mustInstall(t, registry, new(durable.Extension{Name: "b"}))
	expectStrings(t, calls, []string{"first", "third", "late"})

	calls = nil
	registry.Uninstall(new(durable.Extension{Name: "missing"}))
	expectStrings(t, calls, nil)
}
