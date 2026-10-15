package extension_test

// pi: packages/coding-agent/src/core/event-bus.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/test/suite/regressions/7193-event-bus-lifecycle.test.ts:15-62 (the bus half) and src/core/event-bus.ts:3-10, 12-33.
// The host owns an EventBusController, the extension sees only the EventBus (`pi.events: EventBus`, so it cannot clear). Both subscribe to "reload:test"; the extension's listener is removed through the unsubscribe function On returned (reload and dispose do that), the host's stays.
// Pi's expectations, with the emit helper's before/after deltas: {extension: 1, host: 1}, then after the extension's unsubscribe {extension: 0, host: 1}, then after the owner's Clear {extension: 0, host: 0}.
// mutation-checked: On registering on the wrong channel, the unsubscribe removing every listener of the channel, and Clear doing nothing each fail it.
func TestEventBusLifecycleAcrossOwnerAndExtensionViewsUpstream(t *testing.T) {
	var controller extension.EventBusController = extension.CreateEventBus()
	extensionCalls, hostCalls := 0, 0
	emit := func(bus extension.EventBus) (extensionDelta, hostDelta int) {
		extensionBefore, hostBefore := extensionCalls, hostCalls
		bus.Emit("reload:test", nil)
		return extensionCalls - extensionBefore, hostCalls - hostBefore
	}

	var extensionView extension.EventBus = controller
	unsubscribe := extensionView.On("reload:test", func(any) { extensionCalls++ })
	controller.On("reload:test", func(any) { hostCalls++ })

	if e, h := emit(controller); e != 1 || h != 1 {
		t.Fatalf("first emit: extension %d, host %d; want 1, 1", e, h)
	}
	unsubscribe()
	if e, h := emit(controller); e != 0 || h != 1 {
		t.Fatalf("after the extension's unsubscribe: extension %d, host %d; want 0, 1", e, h)
	}
	controller.Clear()
	if e, h := emit(controller); e != 0 || h != 0 {
		t.Fatalf("after Clear: extension %d, host %d; want 0, 0", e, h)
	}
}
