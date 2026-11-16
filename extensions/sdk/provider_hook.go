package sdk

import "reflect"

// Pi hands every before_provider_request handler the one payload object and sends what the runner returns (runner.ts emitBeforeProviderRequest returns
// currentPayload), so a handler that edits event.payload in place and returns nothing changes the request, even when it throws afterwards. Its
// before_provider_headers handlers mutate event.headers in place and their return value is ignored (runner.ts emitBeforeProviderHeaders). The host cannot
// share the maps, so the reply carries what the handler left of them.

// providerHookReply is the reply to a before_provider_request or before_provider_headers handler. object is the event's payload or headers member as the handler
// received it (a handler that assigns another value to the member of its data changes nothing, as assigning event.payload changes nothing in Pi), result is
// what the handler returned and sent returns the member as the host sent it, decoded again.
//
// A headers reply is always the headers object, an empty one when the event carried none. A payload reply is the handler's result when it returned one (the
// edit of the object it replaced is dropped, as in Pi), else object when it differs from what the host sent, else nothing: the host keeps its own payload.
func providerHookReply(event string, object, result any, sent func() any) any {
	if event == "before_provider_headers" {
		if headers, ok := object.(map[string]any); ok && headers != nil {
			return headers
		}
		return map[string]any{}
	}
	if result != nil {
		return result
	}
	switch object.(type) {
	case map[string]any, []any:
	default:
		return nil
	}
	if reflect.DeepEqual(object, sent()) {
		return nil
	}
	return object
}
