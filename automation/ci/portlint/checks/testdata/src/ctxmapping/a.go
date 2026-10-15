package ctxmapping

import (
	"context"
	"net/http"
	"os/exec"
)

func bad(ctx context.Context) {
	_ = context.Background()                       // want `context.Background\(\) inside a function`
	_, _ = http.NewRequest("GET", "http://x", nil) // want `http.NewRequest ignores`
	_ = exec.Command("true")                       // want `exec.Command ignores`
}

func good(ctx context.Context) {
	_, _ = http.NewRequestWithContext(ctx, "GET", "http://x", nil)
	_ = exec.CommandContext(ctx, "true")
}

func noCtx() { _ = context.Background() }
