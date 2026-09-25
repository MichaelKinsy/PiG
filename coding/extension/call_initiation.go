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

// DialogInitiationReporter is implemented by a UIContext whose Select,
// Confirm, Input, and Editor call CallInitiated once the dialog is installed.
// Other UIContexts are treated as initiated when the host dispatches the call.
type DialogInitiationReporter interface {
	ReportsDialogInitiation() bool
}
