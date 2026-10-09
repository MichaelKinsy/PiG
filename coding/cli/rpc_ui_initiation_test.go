package cli

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's RPC dialogs write their extension_ui_request synchronously when the
// extension calls ctx.ui.select/confirm/input/editor (rpc-mode.ts
// createDialogPromise). The dialog call is initiated only once that request is
// written, so later calls and a runtime drain observe it on stdout.
func TestRPCUIDialogInitiatesAfterRequestIsWritten(t *testing.T) {
	var reporter extension.DialogInitiationReporter = newRPCUIContext(func(any) {})
	if !reporter.ReportsDialogInitiation() {
		t.Fatal("RPC UI context does not report dialog initiation")
	}
	dialogs := map[string]func(*rpcUIContext, context.Context){
		"select": func(u *rpcUIContext, ctx context.Context) {
			_, _ = u.Select(ctx, "t", []string{"a"}, extension.ExtensionUIDialogOptions{})
		},
		"confirm": func(u *rpcUIContext, ctx context.Context) {
			_, _ = u.Confirm(ctx, "t", "m", extension.ExtensionUIDialogOptions{})
		},
		"input": func(u *rpcUIContext, ctx context.Context) {
			_, _ = u.Input(ctx, "t", "p", extension.ExtensionUIDialogOptions{})
		},
		"editor": func(u *rpcUIContext, ctx context.Context) { _, _ = u.Editor(ctx, "t", "p") },
	}
	for name, open := range dialogs {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var order []string
			record := func(step string) {
				mu.Lock()
				order = append(order, step)
				mu.Unlock()
			}
			written := make(chan struct{})
			ui := newRPCUIContext(func(any) {
				record("request written")
				close(written)
			})
			ctx := extension.WithCallInitiation(context.Background(), func() { record("initiated") })
			done := make(chan struct{})
			go func() {
				defer close(done)
				open(ui, ctx)
			}()
			<-written
			ui.Close()
			<-done
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"request written", "initiated"}; !slices.Equal(order, want) {
				t.Fatalf("order = %v, want %v", order, want)
			}
		})
	}
}
