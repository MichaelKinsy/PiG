// Command rpc-idle-shutdown-go is a Go-SDK extension whose session_start handler starts a goroutine that, while the session is idle, logs "before", calls ctx.Shutdown() and logs "after". Its session_shutdown handler logs too. RPC mode must return from ctx.Shutdown() at once, so "after" comes before "session_shutdown" (Pi 1.5.0 and Pi main: ctx.shutdown() returns and the check runs on the next tick).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func logLine(line string) {
	f, err := os.OpenFile(os.Getenv("SP_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

func main() {
	ext := sdk.New("rpc-idle-shutdown-go")
	// returned closes once ctx.Shutdown() returned and "after" was logged. The session_shutdown handler waits for it before it logs, because the SDK answers the call on one goroutine and runs the handler on another; a host that blocks the call until the handler ran never closes it, and the 5 s bound ends the wait (a hang detector, not a delay).
	returned := make(chan struct{})
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		go func() {
			time.Sleep(300 * time.Millisecond)
			logLine("before")
			ctx.Shutdown()
			logLine("after")
			close(returned)
		}()
		return nil, nil
	})
	ext.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		select {
		case <-returned:
		case <-time.After(5 * time.Second):
		}
		logLine("session_shutdown")
		return nil, nil
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rpc-idle-shutdown-go: %v\n", err)
		os.Exit(1)
	}
}
