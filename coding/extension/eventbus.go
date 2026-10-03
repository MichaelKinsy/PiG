package extension

// EventBus is the extension-to-extension event bus. Mirrors upstream's
// core/event-bus.ts EventBus, exposed on ExtensionAPI as the property
// `events: EventBus`, with upstream's method names.
//
// Go exposes upstream's `events` property through [API.Events].
//
// upstream: event-bus.ts:3-6
type EventBus interface {
	// Emit broadcasts data on channel to every current listener. Synchronous:
	// handlers run before Emit returns. A handler's failure does not reach the
	// emitter or the other listeners; the host reports it as
	// `Event handler error (<channel>):` (event-bus.ts:19-23).
	Emit(channel string, data any)

	// On registers handler for channel and returns an idempotent function that
	// removes it. Handlers are dispatched in registration order.
	On(channel string, handler func(data any)) (unsubscribe func())
}
