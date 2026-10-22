package childsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// hangTool is the custom tool of the lifecycle rows: it announces that it started and then waits for the cancellation of its context, which is what aborting the session or closing the connection does to a running tool. marker, when set, names a file it writes once cancelled.
func hangTool(started chan<- struct{}, sawAbort *atomic.Bool, marker string) sdk.ToolDefinition {
	var once sync.Once
	return sdk.ToolDefinition{
		Name: "sentinel_tool", Label: "sentinel", Description: "waits for its abort", Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []string{"x"}},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			sawAbort.Store(true)
			if marker != "" {
				_ = os.WriteFile(marker, []byte("cancelled"), 0o600)
			}
			return nil, errors.New("aborted")
		},
	}
}

func hangSession(ctx sdk.Context, custom sdk.ToolDefinition) (*sdk.AgentSession, error) {
	return ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{
		Model: map[string]any{"provider": "sentinel", "id": "echo"}, Tools: []string{"sentinel_tool"}, InMemory: true,
		Settings:    map[string]any{"compaction": map[string]any{"enabled": false}},
		Resources:   sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true},
		CustomTools: []sdk.ToolDefinition{custom},
	})
}

func registerLifecycle(e *sdk.Extension) {
	// child_lifecycle is the scenario of the Node row (runtime_node_child_session_test.go, TestNodeCreateAgentSessionLifecycleMatchesPi): a prompt waits in a tool, a second prompt without a streaming behavior is refused, abort ends the run and the tool sees the cancellation.
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_lifecycle", Label: "child_lifecycle", Description: "child_lifecycle", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			started := make(chan struct{})
			var sawAbort atomic.Bool
			session, err := hangSession(ctx, hangTool(started, &sawAbort, ""))
			if err != nil {
				return nil, err
			}
			defer session.Dispose()
			var mu sync.Mutex
			var events []string
			session.Subscribe(func(event map[string]any) {
				name := event["type"].(string)
				if name == "agent_settled" {
					name += fmt.Sprintf(":aborted=%v", event["aborted"])
				}
				mu.Lock()
				events = append(events, name)
				mu.Unlock()
			})
			running := make(chan error, 1)
			go func() { running <- session.Prompt(context.Background(), "tool:hang") }()
			<-started
			out := map[string]any{"concurrentPrompt": "resolved"}
			if err := session.Prompt(context.Background(), "again"); err != nil {
				out["concurrentPrompt"] = err.Error()
			}
			if err := session.Abort(); err != nil {
				return nil, err
			}
			if err := <-running; err != nil {
				return nil, err
			}
			state, err := session.State()
			if err != nil {
				return nil, err
			}
			messages, err := session.Messages()
			if err != nil {
				return nil, err
			}
			mu.Lock()
			tail := events[max(0, len(events)-4):]
			mu.Unlock()
			session.Dispose()
			out["afterDispose"] = "resolved"
			if err := session.Prompt(context.Background(), "after dispose"); err != nil {
				out["afterDispose"] = err.Error()
			}
			out["toolSawAbort"], out["idleAfterAbort"], out["tail"], out["lastRole"] = sawAbort.Load(), !state.IsStreaming, tail, messages[len(messages)-1]["role"]
			encoded, err := json.Marshal(out)
			return string(encoded), err
		},
	})
	// child_hold starts a prompt that waits in a tool and returns, leaving the session to the connection: the host must end it when the connection closes. The tool writes args.marker when its context is cancelled.
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_hold", Label: "child_hold", Description: "child_hold", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			started := make(chan struct{})
			var sawAbort atomic.Bool
			marker, _ := args["marker"].(string)
			session, err := hangSession(ctx, hangTool(started, &sawAbort, marker))
			if err != nil {
				return nil, err
			}
			go func() { _ = session.Prompt(context.Background(), "tool:hang") }()
			<-started
			return `{"held":true}`, nil
		},
	})
	// child_errors creates sessions whose model cannot be used (a provider without credentials, no model at all) and reports what creation and prompt say.
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_errors", Label: "child_errors", Description: "child_errors", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			norm := func(message string) any {
				if message == "" {
					return nil
				}
				lines := strings.Split(message, "\n")
				docs := []string{}
				for _, line := range lines {
					if line = strings.TrimSpace(line); strings.HasSuffix(line, ".md") {
						docs = append(docs, line[strings.LastIndexAny(line, `/\`)+1:])
					}
				}
				return map[string]any{"first": lines[0], "docs": docs}
			}
			out := map[string]any{}
			for _, attempt := range []struct {
				name  string
				model any
			}{{"no-key", map[string]any{"provider": "nokey", "id": "locked"}}, {"no-model", nil}} {
				session, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{
					Model: attempt.model, InMemory: true, Settings: map[string]any{"compaction": map[string]any{"enabled": false}},
					Resources: sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true},
				})
				if err != nil {
					return nil, fmt.Errorf("%s: %w", attempt.name, err)
				}
				promptError := ""
				if err := session.Prompt(context.Background(), "hi"); err != nil {
					promptError = err.Error()
				}
				state, err := session.State()
				session.Dispose()
				if err != nil {
					return nil, err
				}
				modelID := any(nil)
				if state.Model != nil {
					modelID = state.Model["id"]
				}
				out[attempt.name] = map[string]any{"fallback": norm(session.ModelFallbackMessage), "model": modelID, "promptError": norm(promptError)}
			}
			encoded, err := json.Marshal(out)
			return string(encoded), err
		},
	})
	// child_failures reports what the calls that cannot succeed say.
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_failures", Label: "child_failures", Description: "child_failures", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			out := map[string]any{}
			message := func(err error) any {
				if err == nil {
					return "resolved"
				}
				return err.Error()
			}
			quiet := sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true}
			_, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{Model: map[string]any{"provider": "nope", "id": "missing"}, InMemory: true, Resources: quiet})
			out["unknownModel"] = message(err)
			_, err = ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{Model: map[string]any{"provider": "sentinel", "id": "echo"}, InMemory: true, Resources: quiet, CustomTools: []sdk.ToolDefinition{{Name: "no_schema", Execute: func(sdk.Context, map[string]any) (any, error) { return "", nil }}}})
			out["customToolWithoutSchema"] = message(err)
			session, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{Model: map[string]any{"provider": "sentinel", "id": "echo"}, InMemory: true, Resources: quiet})
			if err != nil {
				return nil, err
			}
			out["steerIdle"] = message(session.Steer("nothing is running"))
			session.Dispose()
			session.Dispose()
			out["promptAfterDispose"] = message(session.Prompt(context.Background(), "hello"))
			out["stateAfterDispose"] = func() any { _, err := session.State(); return message(err) }()
			encoded, err := json.Marshal(out)
			return string(encoded), err
		},
	})
}
