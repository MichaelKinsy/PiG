package mcpext

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/handleryield"
)

// Pi's session_shutdown handler awaits Promise.all over the background actions and the connections it closes
// (index.ts:1249-1255): its event loop turns there only when one of them is pending. A signal's disposal holds the run
// until a handler yields, so the handler reports the yield exactly then.
func TestSessionShutdownYieldsOnlyWhileWorkIsPending(t *testing.T) {
	yields := func(e *Extension) int {
		n := 0
		e.sessionShutdown(handleryield.With(context.Background(), func() { n++ }))
		return n
	}
	idle := New(nil, Options{LoadConfig: func(context.Context) LoadedMcpConfig { return LoadedMcpConfig{} }, LogPath: t.TempDir() + "/mcp.log"})
	if got := yields(idle); got != 0 {
		t.Fatalf("idle shutdown yields = %d, want 0", got)
	}
	busy := New(nil, Options{LoadConfig: func(context.Context) LoadedMcpConfig { return LoadedMcpConfig{} }, LogPath: t.TempDir() + "/mcp.log"})
	release := make(chan struct{})
	busy.background.Go(func() { <-release })
	n := 0
	// The pending action ends only once the handler yielded, so a handler that does not yield never returns.
	busy.sessionShutdown(handleryield.With(context.Background(), func() {
		n++
		close(release)
	}))
	if n != 1 {
		t.Fatalf("shutdown with a background action yields = %d, want 1", n)
	}
}
