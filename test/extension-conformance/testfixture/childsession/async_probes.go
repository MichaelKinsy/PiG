package childsession

import (
	"context"
	"encoding/json"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The async contract rows (AGENTS.md "TypeScript async/Promise parity"): a member Pi declares `async` starts in call order and never holds a later call, and cancelling the ctx of a member with an abort in Pi runs that abort. child_abort_probe cancels an ExecuteBash that sleeps; child_order_probe starts one awaited member the test holds in the host and times an AbortBash sent from another goroutine behind it.

func registerAsyncProbes(e *sdk.Extension) {
	probeOptions := func() sdk.CreateAgentSessionOptions {
		return sdk.CreateAgentSessionOptions{
			Model: map[string]any{"provider": "sentinel", "id": "echo"}, InMemory: true,
			Settings:  map[string]any{"compaction": map[string]any{"enabled": false}},
			Resources: sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true},
		}
	}
	// child_abort_probe: ExecuteBash("sleep 25") is cancelled 0.7 s after it started; it must return Cancelled long before the command ends.
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_abort_probe", Label: "child_abort_probe", Description: "child_abort_probe", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			session, err := ctx.CreateAgentSession(probeOptions())
			if err != nil {
				return nil, err
			}
			defer session.Dispose()
			type outcome struct {
				result sdk.BashResult
				err    error
			}
			cancelCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan outcome, 1)
			start := time.Now()
			go func() {
				result, err := session.ExecuteBash(cancelCtx, "sleep 25")
				done <- outcome{result, err}
			}()
			time.Sleep(700 * time.Millisecond)
			cancel()
			select {
			case o := <-done:
				message := ""
				if o.err != nil {
					message = o.err.Error()
				}
				encoded, err := json.Marshal(map[string]any{"returned": true, "cancelled": o.result.Cancelled, "err": message, "seconds": time.Since(start).Seconds()})
				return string(encoded), err
			case <-time.After(12 * time.Second):
				encoded, err := json.Marshal(map[string]any{"returned": false})
				return string(encoded), err
			}
		},
	})
	// child_order_probe: args.first names the awaited member to start; the host holds it, and AbortBash is sent 0.4 s later.
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_order_probe", Label: "child_order_probe", Description: "child_order_probe", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			session, err := ctx.CreateAgentSession(probeOptions())
			if err != nil {
				return nil, err
			}
			defer session.Dispose()
			first, _ := args["first"].(string)
			done := make(chan struct{})
			go func() {
				defer close(done)
				switch first {
				case "cycleModel":
					_, _ = session.CycleModel(context.Background(), "forward")
				case "executeBash":
					_, _ = session.ExecuteBash(context.Background(), "echo x")
				case "setModel":
					_ = session.SetModel(context.Background(), map[string]any{"provider": "sentinel", "id": "echo"})
				}
			}()
			time.Sleep(400 * time.Millisecond)
			start := time.Now()
			if err := session.AbortBash(); err != nil {
				return nil, err
			}
			abortTook := time.Since(start).Seconds()
			<-done
			encoded, err := json.Marshal(map[string]any{"abortTook": abortTook})
			return string(encoded), err
		},
	})
}
