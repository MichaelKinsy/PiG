// Package deadterm is the Go SDK extension of the dead-terminal integration test (test/integration). It appends its
// process ID at session_start, and the reason at session_shutdown before and after a wait of PIG_DEAD_TERMINAL_WAIT_MS
// that the host has to sit through, to the file that PIG_DEAD_TERMINAL_MARKER names.
package deadterm

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func mark(format string, args ...any) {
	f, err := os.OpenFile(os.Getenv("PIG_DEAD_TERMINAL_MARKER"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, format+"\n", args...)
}

// Extension is the factory.
func Extension() *sdk.Extension {
	ext := sdk.New("deadterm")
	ext.OnEvent("session_start", func(sdk.Context, map[string]any) (any, error) {
		mark("ready pid=%d", os.Getpid())
		return nil, nil
	})
	ext.OnSessionShutdown(func(_ sdk.Context, event map[string]any) (any, error) {
		mark("session_shutdown reason=%v", event["reason"])
		wait, _ := strconv.Atoi(os.Getenv("PIG_DEAD_TERMINAL_WAIT_MS"))
		time.Sleep(time.Duration(wait) * time.Millisecond)
		mark("session_shutdown settled reason=%v", event["reason"])
		return nil, nil
	})
	return ext
}
