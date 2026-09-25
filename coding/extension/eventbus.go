package extension

// EventBus is the extension-to-extension pub/sub bus. Mirrors upstream's
// core/event-bus.EventBus, exposed on ExtensionAPI as the property
// `events: EventBus`.
//
// Go exposes upstream's `events` property through [API.Events].
type EventBus interface {
	// Publish broadcasts payload under the given topic to every current
	// subscriber. Synchronous: handlers run before Publish returns. Panics
	// in handlers are recovered and logged by the host.
	Publish(topic string, payload any)

	// Subscribe registers handler for topic and returns a cancel func that
	// unsubscribes. Handlers are dispatched in registration order.
	Subscribe(topic string, handler func(payload any)) (cancel func())
}
