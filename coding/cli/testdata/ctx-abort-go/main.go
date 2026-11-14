// Command ctx-abort-go is a Go-SDK extension that calls ctx.Abort() once, from a message_update handler the run awaits, and logs "abort" before the call, "returned" after it and each agent_end with the stop reason of its last assistant message.
package main

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

var fired atomic.Bool

func logLine(line string) {
	f, err := os.OpenFile(os.Getenv("ABORT_PROBE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

func main() {
	ext := sdk.New("ctx-abort-go")
	ext.OnEvent("message_update", func(ctx sdk.Context, _ map[string]any) (any, error) {
		if fired.CompareAndSwap(false, true) {
			logLine("abort")
			ctx.Abort()
			logLine("returned")
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
