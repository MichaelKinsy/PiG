package planmode

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	planStateType   = "plan-mode"
	planContextType = "plan-mode-context"
)

type planModeState struct {
	Enabled             bool       `json:"enabled"`
	Todos               []TodoItem `json:"todos"`
	Executing           bool       `json:"executing"`
	ToolsBeforePlanMode []string   `json:"toolsBeforePlanMode,omitzero"`
}

type persistedPlanModeState struct {
	Enabled             *bool       `json:"enabled"`
	Todos               *[]TodoItem `json:"todos"`
	Executing           *bool       `json:"executing"`
	ToolsBeforePlanMode *[]string   `json:"toolsBeforePlanMode"`
}

type planMode struct {
	mu                  sync.Mutex
	enabled             bool
	executing           bool
	todos               []TodoItem
	toolsBeforePlanMode []string
}

// Extension constructs the opt-in plan-mode example.
func Extension() *sdk.Extension {
	ext := sdk.New("plan-mode")
	mode := &planMode{}

	ext.Flag("plan", sdk.FlagOptions{
		Description: "Start in plan mode (read-only exploration)",
		Type:        sdk.FlagBoolean,
		Default:     false,
	})
	ext.Command("plan", "Toggle plan mode (read-only exploration)", func(ctx sdk.Context, _ string) error {
		return mode.toggle(ctx)
	})
	ext.Command("todos", "Show current plan todo list", func(ctx sdk.Context, _ string) error {
		return mode.showTodos(ctx)
	})
	ext.Shortcut("ctrl+alt+p", "Toggle plan mode", mode.toggle)
	ext.OnEvent("tool_call", mode.onToolCall)
	ext.OnEvent("context", mode.onContext)
	ext.OnEvent("before_agent_start", mode.onBeforeAgentStart)
	ext.OnEvent("turn_end", mode.onTurnEnd)
	ext.OnEvent("agent_end", mode.onAgentEnd)
	ext.OnSessionStart(mode.onSessionStart)

	return ext
}

func (p *planMode) toggle(ctx sdk.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.enabled = !p.enabled
	p.executing = false
	p.todos = nil
	if p.enabled {
		p.enablePlanModeTools(ctx)
		ctx.Notify("Plan mode enabled. Built-in write tools disabled.", "info")
	} else {
		p.restoreNormalModeTools(ctx)
		ctx.Notify("Plan mode disabled. Full access restored.", "info")
	}
	if err := p.updateStatus(ctx); err != nil {
		return err
	}
	return p.persistState(ctx)
}

func (p *planMode) showTodos(ctx sdk.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.todos) == 0 {
		ctx.Notify("No todos. Create a plan first with /plan", "info")
		return nil
	}
	var list strings.Builder
	for i, item := range p.todos {
		if i > 0 {
			list.WriteByte('\n')
		}
		marker := "○"
		if item.Completed {
			marker = "✓"
		}
		fmt.Fprintf(&list, "%d. %s %s", i+1, marker, item.Text)
	}
	ctx.Notify("Plan Progress:\n"+list.String(), "info")
	return nil
}

func (p *planMode) onToolCall(_ sdk.Context, data map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.enabled || data["toolName"] != "bash" {
		return nil, nil
	}
	input, _ := data["input"].(map[string]any)
	command, _ := input["command"].(string)
	if IsSafeCommand(command) {
		return nil, nil
	}
	return map[string]any{
		"block":  true,
		"reason": fmt.Sprintf("Plan mode: command blocked (not allowlisted). Use /plan to disable plan mode first.\nCommand: %s", command),
	}, nil
}

func (p *planMode) onContext(_ sdk.Context, data map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.enabled {
		return nil, nil
	}
	messages, _ := data["messages"].([]any)
	filtered := make([]any, 0, len(messages))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok || keepContextMessage(message) {
			filtered = append(filtered, raw)
		}
	}
	return map[string]any{"messages": filtered}, nil
}

func keepContextMessage(message map[string]any) bool {
	if message["customType"] == planContextType {
		return false
	}
	if message["role"] != "user" {
		return true
	}
	switch content := message["content"].(type) {
	case string:
		return !strings.Contains(content, "[PLAN MODE ACTIVE]")
	case []any:
		for _, raw := range content {
			block, _ := raw.(map[string]any)
			if block["type"] == "text" {
				text, _ := block["text"].(string)
				if strings.Contains(text, "[PLAN MODE ACTIVE]") {
					return false
				}
			}
		}
	}
	return true
}

func (p *planMode) onBeforeAgentStart(_ sdk.Context, _ map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.enabled {
		return map[string]any{"message": map[string]any{
			"customType": planContextType,
			"content": `[PLAN MODE ACTIVE]
You are in plan mode - a read-only exploration mode for safe code analysis.

Restrictions:
- Built-in edit and write tools are disabled
- Other currently active tools remain available
- Bash is restricted to an allowlist of read-only commands

Ask clarifying questions using the questionnaire tool.
Use brave-search skill via bash for web research.

Create a detailed numbered plan under a "Plan:" header:

Plan:
1. First step description
2. Second step description
...

Do NOT attempt to make changes - just describe what you would do.`,
			"display": false,
		}}, nil
	}
	if !p.executing || len(p.todos) == 0 {
		return nil, nil
	}
	remaining := make([]string, 0, len(p.todos))
	for _, item := range p.todos {
		if !item.Completed {
			remaining = append(remaining, fmt.Sprintf("%d. %s", item.Step, item.Text))
		}
	}
	return map[string]any{"message": map[string]any{
		"customType": "plan-execution-context",
		"content": fmt.Sprintf(`[EXECUTING PLAN - Full tool access enabled]

Remaining steps:
%s

Execute each step in order.
After completing a step, include a [DONE:n] tag in your response.`, strings.Join(remaining, "\n")),
		"display": false,
	}}, nil
}

func (p *planMode) onTurnEnd(ctx sdk.Context, data map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.executing || len(p.todos) == 0 {
		return nil, nil
	}
	message, _ := data["message"].(map[string]any)
	text, ok := assistantText(message)
	if !ok {
		return nil, nil
	}
	if MarkCompletedSteps(text, p.todos) > 0 {
		if err := p.updateStatus(ctx); err != nil {
			return nil, err
		}
	}
	return nil, p.persistState(ctx)
}

func (p *planMode) onAgentEnd(ctx sdk.Context, data map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.executing && len(p.todos) > 0 {
		if !allTodosCompleted(p.todos) {
			return nil, nil
		}
		completed := make([]string, 0, len(p.todos))
		for _, item := range p.todos {
			completed = append(completed, "~~"+item.Text+"~~")
		}
		if err := ctx.SendMessage("plan-complete", "**Plan Complete!** ✓\n\n"+strings.Join(completed, "\n"), true, sdk.SendMessageOptions{}); err != nil {
			return nil, err
		}
		p.executing = false
		p.todos = nil
		if err := p.updateStatus(ctx); err != nil {
			return nil, err
		}
		return nil, p.persistState(ctx)
	}

	if !p.enabled || !hasDialogUI(ctx.Mode()) {
		return nil, nil
	}
	if text, ok := lastAssistantText(data["messages"]); ok {
		if extracted := ExtractTodoItems(text); len(extracted) > 0 {
			p.todos = extracted
		}
	}
	if len(p.todos) == 0 {
		return nil, nil
	}
	if err := p.persistState(ctx); err != nil {
		return nil, err
	}

	planMessage := p.planTodoListMessage()
	choice, ok, err := ctx.Select("Plan mode - what next?", []string{
		"Execute the plan (track progress)",
		"Stay in plan mode",
		"Refine the plan",
	})
	if err != nil || !ok {
		return nil, err
	}
	if strings.HasPrefix(choice, "Execute") {
		first := p.todos[0]
		p.enabled = false
		p.executing = true
		p.restoreNormalModeTools(ctx)
		if err := p.updateStatus(ctx); err != nil {
			return nil, err
		}
		if err := p.persistState(ctx); err != nil {
			return nil, err
		}
		if err := ctx.SendMessage("plan-todo-list", planMessage, true, sdk.SendMessageOptions{DeliverAs: "followUp"}); err != nil {
			return nil, err
		}
		remaining := make([]string, 0, len(p.todos))
		for _, item := range p.todos {
			remaining = append(remaining, fmt.Sprintf("%d. %s", item.Step, item.Text))
		}
		executionMessage := fmt.Sprintf(`Execute the plan.

Remaining steps:
%s

Start with: %s
After completing a step, include a [DONE:n] tag in your response.`, strings.Join(remaining, "\n"), first.Text)
		triggerTurn := true
		return nil, ctx.SendMessage("plan-mode-execute", executionMessage, true, sdk.SendMessageOptions{
			TriggerTurn: &triggerTurn,
			DeliverAs:   "followUp",
		})
	}
	if choice == "Refine the plan" {
		refinement, accepted, err := ctx.Editor("Refine the plan:", "")
		refinement = jsTrim(refinement)
		if err != nil || !accepted || refinement == "" {
			return nil, err
		}
		if err := ctx.SendMessage("plan-todo-list", planMessage, true, sdk.SendMessageOptions{DeliverAs: "followUp"}); err != nil {
			return nil, err
		}
		return nil, ctx.SendUserMessage(refinement, "followUp")
	}
	return nil, nil
}

func (p *planMode) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if plan, ok := ctx.GetFlag("plan").(bool); ok && plan {
		p.enabled = true
	}
	entries := ctx.GetEntries()
	lastState := -1
	var restored persistedPlanModeState
	for i, raw := range entries {
		var entry struct {
			Type       string          `json:"type"`
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &entry) == nil && entry.Type == "custom" && entry.CustomType == planStateType {
			lastState = i
			restored = persistedPlanModeState{}
			_ = json.Unmarshal(entry.Data, &restored)
		}
	}
	if lastState >= 0 {
		if restored.Enabled != nil {
			p.enabled = *restored.Enabled
		}
		if restored.Todos != nil {
			p.todos = slices.Clone(*restored.Todos)
		}
		if restored.Executing != nil {
			p.executing = *restored.Executing
		}
		if restored.ToolsBeforePlanMode != nil {
			p.toolsBeforePlanMode = slices.Clone(*restored.ToolsBeforePlanMode)
		}
	}
	if lastState >= 0 && p.executing && len(p.todos) > 0 {
		p.rebuildCompletionState(entries)
	}
	if p.enabled {
		p.enablePlanModeTools(ctx)
	}
	return nil, p.updateStatus(ctx)
}

func (p *planMode) rebuildCompletionState(entries []json.RawMessage) {
	executeIndex := -1
	for i, raw := range slices.Backward(entries) {
		var entry struct {
			CustomType string `json:"customType"`
		}
		if json.Unmarshal(raw, &entry) == nil && entry.CustomType == "plan-mode-execute" {
			executeIndex = i
			break
		}
	}
	var messages []string
	for _, raw := range entries[executeIndex+1:] {
		var entry struct {
			Type    string         `json:"type"`
			Message map[string]any `json:"message"`
		}
		if json.Unmarshal(raw, &entry) != nil || entry.Type != "message" {
			continue
		}
		if text, ok := assistantText(entry.Message); ok {
			messages = append(messages, text)
		}
	}
	MarkCompletedSteps(strings.Join(messages, "\n"), p.todos)
}

func (p *planMode) updateStatus(ctx sdk.Context) error {
	switch {
	case p.executing && len(p.todos) > 0:
		completed := 0
		for _, item := range p.todos {
			if item.Completed {
				completed++
			}
		}
		ctx.SetStatus("plan-mode", fmt.Sprintf("📋 %d/%d", completed, len(p.todos)))
	case p.enabled:
		ctx.SetStatus("plan-mode", "⏸ plan")
	default:
		ctx.SetStatus("plan-mode", "")
	}

	if p.executing && len(p.todos) > 0 {
		lines := make([]string, 0, len(p.todos))
		for _, item := range p.todos {
			if item.Completed {
				lines = append(lines, "☑ \x1b[9m"+item.Text+"\x1b[29m")
			} else {
				lines = append(lines, "☐ "+item.Text)
			}
		}
		return ctx.SetWidget("plan-todos", lines)
	}
	return ctx.SetWidget("plan-todos", []string(nil))
}

func (p *planMode) persistState(ctx sdk.Context) error {
	todos := slices.Clone(p.todos)
	if todos == nil {
		todos = []TodoItem{}
	}
	return ctx.AppendEntry(planStateType, planModeState{
		Enabled:             p.enabled,
		Todos:               todos,
		Executing:           p.executing,
		ToolsBeforePlanMode: slices.Clone(p.toolsBeforePlanMode),
	})
}

func (p *planMode) enablePlanModeTools(ctx sdk.Context) {
	if p.toolsBeforePlanMode == nil {
		p.toolsBeforePlanMode = ctx.GetActiveTools()
	}
	ctx.SetActiveTools(getPlanModeTools(p.toolsBeforePlanMode))
}

func (p *planMode) restoreNormalModeTools(ctx sdk.Context) {
	tools := p.toolsBeforePlanMode
	if tools == nil {
		tools = getNormalModeTools(ctx.GetActiveTools())
	}
	ctx.SetActiveTools(tools)
	p.toolsBeforePlanMode = nil
}

func getPlanModeTools(active []string) []string {
	tools := make([]string, 0, len(active)+6)
	for _, name := range active {
		if name != "edit" && name != "write" {
			tools = append(tools, name)
		}
	}
	return uniqueToolNames(append(tools, "read", "bash", "grep", "find", "ls", "questionnaire"))
}

func getNormalModeTools(active []string) []string {
	tools := []string{"read", "bash", "edit", "write"}
	for _, name := range active {
		if !isPlanManagedTool(name) {
			tools = append(tools, name)
		}
	}
	return uniqueToolNames(tools)
}

func isPlanManagedTool(name string) bool {
	switch name {
	case "read", "bash", "grep", "find", "ls", "questionnaire", "edit", "write":
		return true
	default:
		return false
	}
}

func uniqueToolNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	return unique
}

func (p *planMode) planTodoListMessage() string {
	lines := make([]string, 0, len(p.todos))
	for i, item := range p.todos {
		lines = append(lines, fmt.Sprintf("%d. ☐ %s", i+1, item.Text))
	}
	return fmt.Sprintf("**Plan Steps (%d):**\n\n%s", len(p.todos), strings.Join(lines, "\n"))
}

func lastAssistantText(raw any) (string, bool) {
	messages, _ := raw.([]any)
	for _, raw := range slices.Backward(messages) {
		message, _ := raw.(map[string]any)
		if text, ok := assistantText(message); ok {
			return text, true
		}
	}
	return "", false
}

func assistantText(message map[string]any) (string, bool) {
	if message["role"] != "assistant" {
		return "", false
	}
	content, ok := message["content"].([]any)
	if !ok {
		return "", false
	}
	var texts []string
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] == "text" {
			text, _ := block["text"].(string)
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n"), true
}

func allTodosCompleted(items []TodoItem) bool {
	for _, item := range items {
		if !item.Completed {
			return false
		}
	}
	return true
}

func hasDialogUI(mode string) bool {
	return mode == "tui" || mode == "rpc"
}
