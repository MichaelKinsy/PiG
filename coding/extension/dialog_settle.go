package extension

import "context"

type dialogSettleKey struct{}

// WithDialogSettle returns ctx carrying settle, which a UIContext's Select, Confirm, Input or Editor runs when the user's answer
// reaches it, before it hands the answer to the goroutine that waits in the dialog.
//
// Pi resolves a dialog's promise on the stdin line that answers it, so two dialogs answered back to back continue in the order
// the answers arrived. Go wakes the two waiting goroutines in no order. The host records the order where the answers arrive
// (settle) and keeps it where the extension's results are sent.
func WithDialogSettle(ctx context.Context, settle func()) context.Context {
	return context.WithValue(ctx, dialogSettleKey{}, settle)
}

// DialogSettleFromContext returns the settle function the host attached to the dialog call ctx belongs to, or nil.
func DialogSettleFromContext(ctx context.Context) func() {
	settle, _ := ctx.Value(dialogSettleKey{}).(func())
	return settle
}
