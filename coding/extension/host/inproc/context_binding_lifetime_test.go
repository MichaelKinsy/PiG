package inproc_test

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestRunnerContextQueriesDuringUIReplacement(t *testing.T) {
	r := inproc.NewRunner(nil, t.TempDir())
	ctx := r.CreateCommandContext()
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 1000 {
				_, _ = ctx.Mode()
				_, _ = ctx.HasUI()
				_, _ = ctx.UI()
			}
		})
	}
	for range 1000 {
		r.SetUIContext(extension.NoopUIContext, extension.ModeRPC)
		r.SetUIContext(nil, extension.ModeJSON)
	}
	readers.Wait()
	if mode, err := ctx.Mode(); err != nil || mode != extension.ModeJSON {
		t.Fatalf("mode=%q error=%v", mode, err)
	}
	if ui, err := ctx.UI(); err != nil || ui != extension.NoopUIContext {
		t.Fatalf("UI=%T error=%v", ui, err)
	}
}

func BenchmarkRunnerContextBinding(b *testing.B) {
	exts := make([]extension.Extension, 64)
	for i := range exts {
		exts[i].InitializeEventHandlers()
		exts[i].AddEventHandler("agent_start", 1, func(args ...any) (any, error) {
			ctx := extension.FromContext(args[1].(context.Context))
			_, _ = ctx.Mode()
			_, _ = ctx.HasUI()
			_, _ = ctx.UI()
			return nil, nil
		})
	}
	r := inproc.NewRunner(exts, ".")
	r.SetUIContext(extension.NoopUIContext, extension.ModeTUI)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.Emit(ctx, extension.AgentStartEvent{Type: "agent_start"}); err != nil {
			b.Fatal(err)
		}
	}
}
