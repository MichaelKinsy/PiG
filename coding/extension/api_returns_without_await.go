package extension

// SessionStartRegistrarWithoutAwait is implemented by the API the factory loader gives a factory (it is not a method of API, which mirrors Pi's ExtensionAPI): it registers a
// "session_start" handler as one that returns without awaiting (Extension.AddEventHandlerReturningWithoutAwait), as a Pi handler that is not async does. The host that binds the
// extensions asks the runner whether any handler of the event can pend (Runner.HasHandlersThatMayAwait) before it serves the terminal for one. A built-in factory whose Pi handler
// does not await registers through it where the API implements it; another API (a test double with no runner to ask) registers the handler as OnSessionStart does.
type SessionStartRegistrarWithoutAwait interface {
	OnSessionStartReturningWithoutAwait(handler ExtensionHandlerNoResult[SessionStartEvent]) func()
}

// OnSessionStartReturningWithoutAwait registers handler for "session_start" on api as one that returns without awaiting when api can record that
// (SessionStartRegistrarWithoutAwait), and as api.OnSessionStart does otherwise.
func OnSessionStartReturningWithoutAwait(api API, handler ExtensionHandlerNoResult[SessionStartEvent]) func() {
	if registrar, ok := api.(SessionStartRegistrarWithoutAwait); ok {
		return registrar.OnSessionStartReturningWithoutAwait(handler)
	}
	return api.OnSessionStart(handler)
}
