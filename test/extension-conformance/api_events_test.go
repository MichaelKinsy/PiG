package extensionconformance

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// eventBusReference is the Go reference for extension.API.Events: the production in-memory bus (extension.CreateEventBus) the host gives a Go extension as pi.events.
type eventBusReference struct {
	extension.API
	bus extension.EventBus
}

func (r eventBusReference) Events() extension.EventBus { return r.bus }

// TestConformance_ExtensionAPIEventsOrder pins Pi's pi.events (packages/coding-agent/src/core/event-bus.ts:12-33, an EventEmitter) for the ordering rules every
// realm shares: listeners run in registration order, an unsubscribe removes only its own listener and is idempotent, and a new subscription goes to the
// end. The scenario runs in every SDK realm (the cross-realm bridge, as TestNativeEventBusRegistrationOrderAcrossRealms) and through the Go reference of
// extension.API.Events, and both produce the same log.
func TestConformance_ExtensionAPIEventsOrder(t *testing.T) {
	t.Parallel()

	var api extension.API = eventBusReference{bus: extension.CreateEventBus()}
	var reference []string
	unsubscribers := map[string]func(){}
	subscribe := func(name string) {
		unsubscribers[name] = api.Events().On("order", func(data any) { reference = append(reference, fmt.Sprintf("%s|order|%v", name, data)) })
	}
	names := []string{"n1", "g", "n2", "g2"}
	for _, name := range names {
		subscribe(name)
	}
	api.Events().Emit("order", 1)
	first := slices.Clone(reference)
	reference = nil
	unsubscribers["g"]()
	unsubscribers["g"]()
	api.Events().Emit("order", 2)
	second := slices.Clone(reference)
	reference = nil
	subscribe("g")
	api.Events().Emit("order", 3)
	third := slices.Clone(reference)
	wantFirst := []string{"n1|order|1", "g|order|1", "n2|order|1", "g2|order|1"}
	wantSecond := []string{"n1|order|2", "n2|order|2", "g2|order|2"}
	wantThird := []string{"n1|order|3", "n2|order|3", "g2|order|3", "g|order|3"}
	if !slices.Equal(first, wantFirst) || !slices.Equal(second, wantSecond) || !slices.Equal(third, wantThird) {
		t.Fatalf("the Go reference logged %v, %v, %v", first, second, third)
	}

	t.Run("realms", func(t *testing.T) {
		eachBusRealm(t, true, func(t *testing.T, language, isolation string) {
			rig := newBusRig(t,
				busSpec("node", isolation, "n1"),
				busSpec(language, isolation, "g"),
				busSpec("node", isolation, "n2"),
				busSpec(language, "isolated", "g2"),
			)
			for _, name := range names {
				rig.must(name, "sub", "order record")
			}
			rig.must("n1", "emit", "order json 1")
			rig.expect(wantFirst...)
			rig.reset()
			rig.must("g", "unsub", "0")
			rig.must("g", "unsub", "0")
			rig.must("n1", "emit", "order json 2")
			rig.expect(wantSecond...)
			rig.reset()
			rig.must("g", "sub", "order record")
			rig.must("n1", "emit", "order json 3")
			rig.expect(wantThird...)
		})
	})
}
