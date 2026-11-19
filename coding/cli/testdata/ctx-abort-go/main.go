// Command ctx-abort-go is a Go-SDK extension that calls ctx.Abort() once, from a message_update handler the run awaits. It logs the state of ctx.signal before the call ("abort:<state>"), the state of the signal it held and of ctx.signal after the call ("returned:<held>:<now>"), what a second handler of the same event sees and does ("second:<state>", "second again:<state>"), and each agent_end with the stop reason of its last assistant message.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

var fired, again atomic.Bool

func logLine(line string) {
	f, err := os.OpenFile(os.Getenv("ABORT_PROBE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

func state(signal context.Context) string {
	switch {
	case signal == nil:
		return "none"
	case signal.Err() != nil:
		return "aborted"
	}
	return "live"
}

func main() {
	ext := sdk.New("ctx-abort-go")
	ext.OnEvent("message_update", func(ctx sdk.Context, _ map[string]any) (any, error) {
		if fired.CompareAndSwap(false, true) {
			held := ctx.Signal()
			logLine("abort:" + state(held))
			ctx.Abort()
			logLine("returned:" + state(held) + ":" + state(ctx.Signal()))
		}
		return nil, nil
	})
	ext.OnEvent("message_update", func(ctx sdk.Context, _ map[string]any) (any, error) {
		if fired.Load() && again.CompareAndSwap(false, true) {
			logLine("second:" + state(ctx.Signal()))
			ctx.Abort()
			logLine("second again:" + state(ctx.Signal()))
		}
		return nil, nil
	})
	ext.OnEvent("agent_end", func(_ sdk.Context, event map[string]any) (any, error) {
		reason := ""
		messages, _ := event["messages"].([]any)
		for i := len(messages) - 1; i >= 0; i-- {
			if message, ok := messages[i].(map[string]any); ok && message["role"] == "assistant" {
				reason, _ = message["stopReason"].(string)
				break
			}
		}
		logLine("agent_end:" + reason)
		return nil, nil
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-abort-go: %v\n", err)
		os.Exit(1)
	}
}
