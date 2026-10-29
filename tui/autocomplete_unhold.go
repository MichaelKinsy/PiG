package tui

import "context"

// pig divergence (D213): the host holds the terminal read that follows a request whose provider chain may settle in Pi's microtask
// checkpoint (editor.ts startAutocompleteRequest runs in the Promise jobs after the read; a provider that needs no I/O has opened its
// menu before the next read). A chain that leaves the checkpoint, because it awaits real I/O, calls AutocompleteAwaitsIO on the
// context the editor gave the request, and the host lets the input go on, as Pi's event loop does.
type autocompleteUnholdKey struct{}

// WithAutocompleteUnhold returns ctx carrying the function that releases the host's input hold for one request.
func WithAutocompleteUnhold(ctx context.Context, unhold func()) context.Context {
	return context.WithValue(ctx, autocompleteUnholdKey{}, unhold)
}

// AutocompleteUnhold returns the release function WithAutocompleteUnhold stored in ctx, or nil when the request holds no input.
func AutocompleteUnhold(ctx context.Context) func() {
	unhold, _ := ctx.Value(autocompleteUnholdKey{}).(func())
	return unhold
}

// AutocompleteAwaitsIO reports that the request's provider chain awaits real I/O (a timer, a process, a socket): it will not settle in
// the microtask checkpoint after the read that made the request. It is idempotent and safe on any goroutine.
func AutocompleteAwaitsIO(ctx context.Context) {
	if unhold := AutocompleteUnhold(ctx); unhold != nil {
		unhold()
	}
}
