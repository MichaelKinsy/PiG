package cli

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi resolves an RPC dialog's promise on the stdin line that answers it (rpc-mode.ts createDialogPromise), so the dialogs a chunk
// answers continue in the order of its lines. The host records that order by running a dialog call's settle function
// (extension.WithDialogSettle) when HandleResponse reads the answer, before the goroutine that waits in the dialog can run.
func TestRPCUIResponsesSettleTheirDialogsInStdinOrder(t *testing.T) {
	var mu sync.Mutex
	var requests []rpcUIRequest
	opened := make(chan struct{}, 2)
	ui := newRPCUIContext(func(request any) {
		mu.Lock()
		requests = append(requests, request.(rpcUIRequest))
		mu.Unlock()
		opened <- struct{}{}
	})
	var settled []string
	var received sync.WaitGroup
	for _, title := range []string{"First?", "Second?"} {
		ctx := extension.WithDialogSettle(context.Background(), func() {
			mu.Lock()
			settled = append(settled, title)
			mu.Unlock()
		})
		received.Go(func() { _, _ = ui.Select(ctx, title, []string{"a"}, extension.ExtensionUIDialogOptions{}) })
		<-opened
	}
	mu.Lock()
	ids := map[string]string{}
	for _, request := range requests {
		ids[*request.Title] = request.ID
	}
	mu.Unlock()
	// The answers arrive as the lines of one chunk: Second? first.
	for _, title := range []string{"Second?", "First?"} {
		line, _ := json.Marshal(map[string]any{"type": "extension_ui_response", "id": ids[title], "value": "a"})
		if !ui.HandleResponse(line) {
			t.Fatalf("the response to %s was not taken", title)
		}
	}
	received.Wait()
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"Second?", "First?"}; !slices.Equal(settled, want) {
		t.Fatalf("dialogs settled in order %v, want the order of the answers %v", settled, want)
	}
}
