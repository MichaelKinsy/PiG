package extension

import "context"

// Upstream extensions call the host in process: a Promise-returning API such
// as ctx.ui.select applies its synchronous part (installing the dialog) before
// it returns the pending Promise, so the extension's next call applies after
// it. A host that runs such a call off its ordered dispatcher learns that the
// synchronous part is done through the initiation mark on the call's context.

type callInitiationKey struct{}

// WithCallInitiation returns ctx carrying mark, which the handler of a
// Promise-shaped call runs once its synchronous part has been applied.
func WithCallInitiation(ctx context.Context, mark func()) context.Context {
	return context.WithValue(ctx, callInitiationKey{}, mark)
}

// CallInitiated reports that the call carried by ctx has applied its
// synchronous part. It is a no-op without a mark and safe to call repeatedly
// when the mark is idempotent.
func CallInitiated(ctx context.Context) {
	if ctx == nil {
		return
	}
	if mark, ok := ctx.Value(callInitiationKey{}).(func()); ok && mark != nil {
		mark()
	}
}

type callerSettlementKey struct{}

// WithCallerSettlement returns ctx carrying after, through which the handler of
// a call leaves work for the call's transport to run once the code that awaits
// the call has gone on from its result to its next await or its end.
func WithCallerSettlement(ctx context.Context, after func(fn func())) context.Context {
	return context.WithValue(ctx, callerSettlementKey{}, after)
}

// AfterCallerSettles runs fn once the code that awaits the call carried by ctx
// has resumed from the call's result and then awaited again or ended. Pi runs
// that continuation in the microtasks of the call's settlement, before its
// event loop turns. Without a transport that follows the caller, fn runs at once.
func AfterCallerSettles(ctx context.Context, fn func()) {
	if ctx != nil {
		if after, ok := ctx.Value(callerSettlementKey{}).(func(func())); ok && after != nil {
			after(fn)
			return
		}
	}
	fn()
}

// DialogInitiationReporter is implemented by a UIContext whose Select,
// Confirm, Input, and Editor call CallInitiated once the dialog is installed.
// Other UIContexts are treated as initiated when the host dispatches the call.
type DialogInitiationReporter interface {
	ReportsDialogInitiation() bool
}
