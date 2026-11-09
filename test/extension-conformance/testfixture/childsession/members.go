package childsession

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// registerMembers is the child_members scenario: the AgentSession members an extension reaches beyond prompt/steer/abort (C2Sessions), each read or changed on the host's session and observed back. Every value is one the sentinel session produces and a fallback (zero value, an SDK that never calls the host) does not: a mode set to a non-default, a name only this scenario chose, the sentinel model's echo of a prompt, a bash command's output.
func registerMembers(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "child_members", Label: "child_members", Description: "child_members", Parameters: sdk.Schema{"type": "object"},
		Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
			dir := args["dir"].(string)
			session, err := ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{
				Model: map[string]any{"provider": "sentinel", "id": "echo"}, Tools: []string{"sentinel_tool"}, InMemory: true,
				Settings:  map[string]any{"compaction": map[string]any{"enabled": false}},
				Resources: sdk.ResourceOptions{NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true},
				CustomTools: []sdk.ToolDefinition{{
					Name: "sentinel_tool", Label: "sentinel", Description: "uppercases x", Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}, "required": []string{"x"}},
					Execute: func(_ sdk.Context, params map[string]any) (any, error) {
						return strings.ToUpper(params["x"].(string)) + "!", nil
					},
				}},
			})
			if err != nil {
				return nil, err
			}
			defer session.Dispose()
			out := map[string]any{}
			var failure error
			ok := func(value any, err error) any {
				if err != nil && failure == nil {
					failure = err
				}
				return value
			}
			errText := func(err error) string {
				if err == nil {
					return ""
				}
				return err.Error()
			}
			set := func(key string, value any, err error) { out[key] = ok(value, err) }

			// Reads of a fresh session.
			id, err := session.SessionID()
			set("sessionId", id != "", err)
			file, err := session.SessionFile()
			set("sessionFile", file, err)
			model, err := session.Model()
			set("model", model["id"], err)
			set("isIdle", ok(session.IsIdle()), nil)
			set("isStreaming", ok(session.IsStreaming()), nil)
			set("isCompacting", ok(session.IsCompacting()), nil)
			set("isRetrying", ok(session.IsRetrying()), nil)
			set("retryAttempt", ok(session.RetryAttempt()), nil)
			set("pendingMessageCount", ok(session.PendingMessageCount()), nil)
			set("steeringMode", ok(session.SteeringMode()), nil)
			set("followUpMode", ok(session.FollowUpMode()), nil)
			set("autoCompactionEnabled", ok(session.AutoCompactionEnabled()), nil)
			set("autoRetryEnabled", ok(session.AutoRetryEnabled()), nil)
			set("sessionName", ok(session.SessionName()), nil)
			set("scopedModelsStart", len(ok(session.ScopedModels()).([]sdk.ScopedModel)), nil)
			set("promptTemplates", len(ok(session.PromptTemplates()).([]sdk.PromptTemplate)), nil)
			set("hasHandlers", ok(session.HasExtensionHandlers("agent_start")), nil)
			set("lastTextStart", ok(session.GetLastAssistantText()) == (*string)(nil), nil)
			set("systemPromptLen", len(ok(session.SystemPrompt()).(string)) > 0, nil)

			// Changes read back: each value is one no default holds.
			ok(nil, session.SetSteeringMode("all"))
			ok(nil, session.SetFollowUpMode("all"))
			ok(nil, session.SetAutoCompactionEnabled(true))
			ok(nil, session.SetAutoRetryEnabled(false))
			ok(nil, session.SetSessionName("named-by-members"))
			ok(nil, session.SetScopedModels([]sdk.ScopedModel{{Model: map[string]any{"provider": "sentinel", "id": "echo"}, ThinkingLevel: "medium"}}))
			set("steeringModeSet", ok(session.SteeringMode()), nil)
			set("followUpModeSet", ok(session.FollowUpMode()), nil)
			set("autoCompactionSet", ok(session.AutoCompactionEnabled()), nil)
			set("autoRetrySet", ok(session.AutoRetryEnabled()), nil)
			set("sessionNameSet", ok(session.SessionName()), nil)
			scoped := ok(session.ScopedModels()).([]sdk.ScopedModel)
			if len(scoped) == 1 {
				set("scopedModelsSet", fmt.Sprintf("%v:%s", scoped[0].Model["id"], scoped[0].ThinkingLevel), nil)
			}
			cycled, err := session.CycleModel(context.Background(), "forward")
			set("cycleOnlyModel", cycled == nil, err)
			ok(nil, session.SetActiveToolsByName([]string{}))
			set("activeToolsEmpty", ok(session.GetActiveToolNames()), nil)
			ok(nil, session.SetActiveToolsByName([]string{"sentinel_tool"}))
			set("activeTools", ok(session.GetActiveToolNames()), nil)
			tools := []string{}
			for _, tool := range ok(session.GetAllTools()).([]sdk.ToolInfo) {
				tools = append(tools, tool.Name)
			}
			out["allTools"] = tools
			definition, err := session.GetToolDefinition("sentinel_tool")
			if definition != nil {
				set("toolDefinition", definition.Description, err)
			}
			missing, err := session.GetToolDefinition("no_such_tool")
			set("toolDefinitionMissing", missing == nil, err)
			out["setModelRefused"] = errText(session.SetModel(context.Background(), map[string]any{"provider": "nokey", "id": "locked"}))
			ok(nil, session.SetModel(context.Background(), map[string]any{"provider": "sentinel", "id": "echo"}))
			ok(nil, session.SetThinkingLevel("off"))
			set("thinkingLevel", ok(session.ThinkingLevel()), nil)
			set("availableThinkingLevels", ok(session.GetAvailableThinkingLevels()), nil)
			ok(nil, session.SetCacheWarmingMode("off"))

			// A run, observed through the members.
			if err := session.Prompt(context.Background(), "hello"); err != nil {
				return nil, err
			}
			if text := ok(session.GetLastAssistantText()).(*string); text != nil {
				out["lastText"] = *text
			}
			stats := ok(session.GetSessionStats()).(sdk.SessionStats)
			out["stats"] = map[string]any{"user": stats.UserMessages, "assistant": stats.AssistantMessages, "total": stats.TotalMessages, "idMatches": stats.SessionID == id}
			forks := ok(session.GetUserMessagesForForking()).([]sdk.ForkMessage)
			if len(forks) == 1 {
				out["fork"] = forks[0].Text
			}

			// Awaited members: a user message that starts a run, the wait for it, a custom message, bash, compaction, tree navigation, exports, the bug report.
			ok(nil, session.SendUserMessage(context.Background(), "via-send", sdk.SendUserMessageOptions{}))
			ok(nil, session.WaitForIdle(context.Background()))
			if text := ok(session.GetLastAssistantText()).(*string); text != nil {
				out["sentText"] = *text
			}
			ok(nil, session.SendCustomMessage(context.Background(), sdk.CustomMessage{CustomType: "member-note", Content: "noted", Display: true}, sdk.SendMessageOptions{}))
			bash, err := session.ExecuteBash(context.Background(), "echo member-bash")
			set("bash", map[string]any{"output": bash.Output, "exit": bash.ExitCode != nil && *bash.ExitCode == 0}, err)
			ok(nil, session.RecordBashResult("recorded-cmd", sdk.BashResult{Output: "recorded-out", ExitCode: new(int)}))
			set("bashRunning", ok(session.IsBashRunning()), nil)
			messages := ok(session.Messages()).([]map[string]any)
			roles := []string{}
			for _, message := range messages {
				roles = append(roles, message["role"].(string))
			}
			out["roles"] = roles
			_, err = session.Compact(context.Background(), "")
			out["compactRefused"] = errText(err)
			if len(forks) == 1 {
				nav, err := session.NavigateTree(context.Background(), forks[0].EntryID)
				set("navigate", map[string]any{"cancelled": nav.Cancelled, "editorText": nav.EditorText}, err)
			}
			jsonl := filepath.Join(dir, "export.jsonl")
			path, err := session.ExportToJsonl(jsonl)
			set("exportedJsonl", path == jsonl, err)
			if data, err := os.ReadFile(jsonl); err == nil {
				out["jsonlHasSession"] = strings.Contains(string(data), id)
			}
			report, err := session.SummarizeForBugReport(context.Background(), "member-hint")
			set("bugReport", strings.HasPrefix(report, "sentinel-model:echo:"), err)
			cleared := ok(session.ClearQueue()).(sdk.QueuedMessages)
			out["cleared"] = []int{len(cleared.Steering), len(cleared.FollowUp)}
			set("callableTools", ok(session.GetCallableToolNames()), nil)
			set("steeringMessages", len(ok(session.GetSteeringMessages()).([]string)), nil)
			set("followUpMessages", len(ok(session.GetFollowUpMessages()).([]string)), nil)
			set("routedModel", ok(session.RoutedModel()).(*sdk.RoutedSelection) == nil, nil)
			set("pendingBash", ok(session.HasPendingBashMessages()), nil)
			set("supportsThinking", ok(session.SupportsThinking()), nil)
			set("cycledThinking", ok(session.CycleThinkingLevel()), nil)
			if status := ok(session.CacheWarmingStatus()).(*sdk.CacheWarmingStatus); status != nil {
				out["cacheWarming"] = status.State
			}
			_, err = session.ExportToHTML(context.Background(), filepath.Join(dir, "export.html"))
			out["exportHtmlInMemory"] = errText(err)
			ok(nil, session.Reload(context.Background()))
			set("afterReload", ok(session.SessionID()) == id, nil)
			for _, abort := range []func() error{session.AbortBash, session.AbortBranchSummary, session.AbortCompaction, session.AbortRetry} {
				ok(nil, abort())
			}
			if failure != nil {
				return nil, failure
			}
			encoded, err := json.Marshal(out)
			return string(encoded), err
		},
	})
}
