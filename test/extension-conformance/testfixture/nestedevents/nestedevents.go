// Package nestedevents is the Go SDK extension of the nested-event conformance row (TestRPCNestedEventsCompleteInEverySDK):
// one command sends a triggering message while a before_agent_start handler is subscribed, and one appends an entry and then
// names the session while a session_info_changed handler is subscribed. Pi runs both handlers inside the call that emits
// their event (agent-session.ts sendCustomMessage under PR #10739, setSessionName), so neither command may wait on the other.
package nestedevents

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// Extension is the row's Go extension. The fused placement links it into the pig binary and the isolated placement runs it as
// a standalone executable; the packed placement builds the same source from a factory module the test writes.
func Extension(name string) *sdk.Extension {
	ext := sdk.New(name)
	ext.OnEvent("before_agent_start", func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus("nested", "busy")
		return nil, nil
	})
	ext.OnEvent("session_info_changed", func(ctx sdk.Context, data map[string]any) (any, error) {
		name, _ := data["name"].(string)
		ctx.Notify("name:"+name, "info")
		return nil, nil
	})
	ext.Command("nested-trigger", "Send a triggering message", func(ctx sdk.Context, _ string) error {
		trigger := true
		return ctx.SendMessage("nested", "Say hello", true, sdk.SendMessageOptions{TriggerTurn: &trigger, DeliverAs: "followUp"})
	})
	ext.Command("nested-name", "Append an entry, then name the session", func(ctx sdk.Context, _ string) error {
		if err := ctx.AppendEntry("nested-entry", map[string]any{"value": "hello"}); err != nil {
			return err
		}
		return ctx.SetSessionName("nested-name")
	})
	ext.Command("nested-wait", "Send a triggering message, then wait for the run", func(ctx sdk.Context, _ string) error {
		trigger := true
		if err := ctx.SendMessage("nested", "Say hello", true, sdk.SendMessageOptions{TriggerTurn: &trigger, DeliverAs: "followUp"}); err != nil {
			return err
		}
		if err := ctx.WaitForIdle(); err != nil {
			return err
		}
		ctx.Notify("idle", "info")
		return nil
	})
	ext.OnEvent("session_start", func(ctx sdk.Context, data map[string]any) (any, error) {
		if reason, _ := data["reason"].(string); reason == "new" {
			return nil, ctx.SendMessage("nested", "started", true, sdk.SendMessageOptions{})
		}
		return nil, nil
	})
	ext.Command("nested-send-wait", "Send a user message, then wait for idle", func(ctx sdk.Context, _ string) error {
		if err := ctx.SendUserMessage("Say hello", ""); err != nil {
			return err
		}
		return ctx.WaitForIdle()
	})
	ext.Command("nested-fresh", "Start a new Session", func(ctx sdk.Context, _ string) error {
		_, err := ctx.NewSession(nil)
		return err
	})
	return ext
}
