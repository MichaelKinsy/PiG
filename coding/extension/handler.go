package extension

import "context"

// ExtensionHandler is types.ts ExtensionHandler<E, R>: the handler of event E whose result is R, which ExtensionAPI.on takes per event. Go carries the
// ExtensionContext argument in ctx (see [FromContext]) and reports a handler failure as the returned error, where upstream's handler throws or rejects.
// An event whose handler has no result is registered with a function that returns only an error.
type ExtensionHandler[E, R any] = func(ctx context.Context, evt E) (R, error)
