package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrUnsupportedSubprocessUI is returned for UI methods whose upstream API
// requires passing live callbacks or component factories across the extension
// boundary. The subprocess SDK cannot serialize those closures today.
var ErrUnsupportedSubprocessUI = errors.New("unsupported in subprocess SDK")

// WorkingIndicatorOptions mirrors upstream's loose shape. The host treats this
// as an opaque JSON object.
type WorkingIndicatorOptions = map[string]any

// WidgetOptions is an opaque type mirroring upstream's ExtensionWidgetOptions.
type WidgetOptions = map[string]any

// TerminalInputResult is a raw-input handler's verdict on one chunk, mirroring
// upstream's `{ consume?: boolean; data?: string }`.
type TerminalInputResult struct {
	// Consume suppresses normal handling of the chunk, so the editor and
	// keybindings never see it.
	Consume bool
	// Data, when non-nil, replaces the chunk for later handlers and for normal
	// handling. An empty replacement drops the chunk.
	Data *string
}

// WidthChangeHandler receives the new terminal width after a resize.
//
// Header, footer, and widget lines are sent to the host as static strings, so
// unlike upstream's component factories they are not re-rendered when the
// terminal resizes. An extension that owns any of them re-pushes from here.
type WidthChangeHandler func(ctx Context, width int)

// TerminalInputHandler mirrors upstream's TerminalInputHandler. Like upstream's
// synchronous listener, the input waits for the verdict, so a handler should
// return promptly.
type TerminalInputHandler func(data string) TerminalInputResult

// AutocompleteProviderFactory is an opaque type; subprocess extensions
// cannot serialize the closure, but the method is exposed for parity.
type AutocompleteProviderFactory = any

// RemoteComponent is the serializable subprocess form of an upstream custom
// TUI component. The SDK renders it locally, sends only terminal lines to the
// host, and routes input back only while the host overlay owns focus.
type RemoteComponent interface {
	Render(width int) []string
	HandleInput(data string) (RemoteComponentResult, error)
}

// RemoteComponentResult closes a focused component when Done is true. Value
// must be JSON-serializable and becomes the result returned by [Context.Custom].
type RemoteComponentResult struct {
	Done  bool
	Value any
}

// RemoteComponentInvalidator is implemented by a component that changes without
// terminal input. The SDK installs a bounded render callback while the overlay
// is active and clears it before disposal. Implementations must replace the
// callback and treat nil as detach.
type RemoteComponentInvalidator interface {
	SetInvalidate(func())
}

// RemoteComponentDisposer is implemented by components that own resources.
// Dispose runs once when the overlay closes, the extension disconnects, or the
// host tears it down during reload.
type RemoteComponentDisposer interface {
	Dispose()
}

// RemoteOverlayOptions is the serializable subset of upstream overlay options.
type RemoteOverlayOptions struct {
	Title          string  `json:"title,omitempty"`
	WidthFraction  float64 `json:"widthFraction,omitempty"`
	HeightFraction float64 `json:"heightFraction,omitempty"`
	// Overlay opens the component as a floating viewport overlay instead of
	// replacing the inline editor slot. Mirrors upstream ui.custom() overlay.
	Overlay bool `json:"overlay,omitempty"`
}

// ThemeMeta mirrors upstream getAllThemes() return elements.
type ThemeMeta struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// Theme is the SDK-side opaque type for a theme payload returned by GetTheme.
type Theme = any

func callResultError(result *callResultMsg, err error) error {
	if err != nil {
		return err
	}
	if result != nil && result.Error != nil {
		if result.Error.Code != "" {
			return fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
		}
		return fmt.Errorf("%s", result.Error.Message)
	}
	return nil
}

// Context provides the extension handler with access to host UI methods,
// session state, and message injection. It is passed to every tool, command,
// and event handler.
type Context struct {
	ext        *Extension
	toolCallID string // set for tool handlers
	requestID  string
	ctx        context.Context
}

// Done returns a channel that closes when the host cancels this request.
func (c Context) Done() <-chan struct{} {
	if c.ctx == nil {
		return nil
	}
	return c.ctx.Done()
}

// Err returns the cancellation reason for this request, if any.
func (c Context) Err() error {
	if c.ctx == nil {
		return nil
	}
	return c.ctx.Err()
}

func (c Context) callHost(method string, args any) (*callResultMsg, error) {
	if method != "ui.select" && method != "ui.confirm" && method != "ui.input" && method != "ui.editor" && method != "ui.custom" {
		c.reportRequestState("blocked", "host_call")
	}
	result, err := c.ext.conn.callFor(c.requestID, method, args)
	c.reportRequestState("progress", "")
	return result, err
}

func (c Context) reportRequestState(state, reason string) {
	if c.ext == nil || c.ext.conn == nil || c.requestID == "" {
		return
	}
	_ = c.ext.conn.requestState(c.requestID, state, reason)
}

// ── Notifications & Status ───────────────────────────────────────────────────

// Notify shows a notification to the user.
// Level is one of "info", "warning", "error".
func (c Context) Notify(message, level string) {
	_, _ = c.callHost("ui.notify", map[string]string{
		"message": message,
		"level":   level,
	})
}

// SetStatus sets a status text in the footer/status bar.
// Empty text clears the entry for this key.
func (c Context) SetStatus(key, text string) {
	_, _ = c.callHost("ui.setStatus", map[string]string{
		"key":  key,
		"text": text,
	})
}

// SetWorkingMessage sets the message shown during LLM streaming.
func (c Context) SetWorkingMessage(message string) {
	_, _ = c.callHost("ui.setWorkingMessage", map[string]string{
		"message": message,
	})
}

// SetWorkingVisible toggles whether the working indicator is shown.
func (c Context) SetWorkingVisible(visible bool) {
	_, _ = c.callHost("ui.setWorkingVisible", map[string]bool{
		"visible": visible,
	})
}

// SetWorkingIndicator configures the interactive working indicator.
func (c Context) SetWorkingIndicator(options WorkingIndicatorOptions) error {
	result, err := c.callHost("ui.setWorkingIndicator", options)
	return callResultError(result, err)
}

// SetHiddenThinkingLabel sets the label shown for hidden thinking blocks.
func (c Context) SetHiddenThinkingLabel(label string) error {
	result, err := c.callHost("ui.setHiddenThinkingLabel", map[string]string{
		"label": label,
	})
	return callResultError(result, err)
}

// SetTitle sets the terminal window/tab title.
func (c Context) SetTitle(title string) {
	_, _ = c.callHost("ui.setTitle", map[string]string{
		"title": title,
	})
}

// ── User Interaction (blocking) ──────────────────────────────────────────────

// Select shows a selector and returns the user's choice.
// Returns ("", false, nil) if cancelled and a non-nil error when the host UI call fails.
func (c Context) Select(title string, options []string) (string, bool, error) {
	c.reportRequestState("blocked", "user")
	result, err := c.callHost("ui.select", map[string]any{
		"title":   title,
		"options": options,
	})
	if err := callResultError(result, err); err != nil {
		return "", false, err
	}
	var resp struct {
		Selected string `json:"selected"`
		Ok       bool   `json:"ok"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Selected, resp.Ok, nil
}

// Confirm shows a yes/no confirmation dialog and surfaces host UI failures.
func (c Context) Confirm(title, message string) (bool, error) {
	c.reportRequestState("blocked", "user")
	result, err := c.callHost("ui.confirm", map[string]any{
		"title":   title,
		"message": message,
	})
	if err := callResultError(result, err); err != nil {
		return false, err
	}
	var resp struct {
		Confirmed bool `json:"confirmed"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Confirmed, nil
}

// Input shows a text input dialog.
// Returns ("", false, nil) if cancelled and a non-nil error when the host UI call fails.
func (c Context) Input(title, placeholder string) (string, bool, error) {
	c.reportRequestState("blocked", "user")
	result, err := c.callHost("ui.input", map[string]any{
		"title":       title,
		"placeholder": placeholder,
	})
	if err := callResultError(result, err); err != nil {
		return "", false, err
	}
	var resp struct {
		Text string `json:"text"`
		Ok   bool   `json:"ok"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Text, resp.Ok, nil
}

// Editor opens a multi-line editor.
// Returns ("", false, nil) if cancelled and a non-nil error when the host UI call fails.
func (c Context) Editor(title, prefill string) (string, bool, error) {
	c.reportRequestState("blocked", "user")
	result, err := c.callHost("ui.editor", map[string]any{
		"title":   title,
		"prefill": prefill,
	})
	if err := callResultError(result, err); err != nil {
		return "", false, err
	}
	var resp struct {
		Text string `json:"text"`
		Ok   bool   `json:"ok"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Text, resp.Ok, nil
}

// ── Message Injection ────────────────────────────────────────────────────────

// SendMessageOptions configures how the message is delivered.
//
// Both fields are optional, as upstream's are: a nil TriggerTurn or empty
// DeliverAs is sent as unset, and the host applies upstream's default for the
// session's state (while a turn streams, an unset triggerTurn steers).
type SendMessageOptions struct {
	TriggerTurn *bool  // nil: host default; true starts or joins a turn; false never does
	DeliverAs   string // "steer", "followUp", or "nextTurn"; empty: host default
}

// SendMessage injects a custom message into the conversation.
func (c Context) SendMessage(customType, content string, display bool, opts SendMessageOptions) error {
	options := map[string]any{}
	if opts.TriggerTurn != nil {
		options["triggerTurn"] = *opts.TriggerTurn
	}
	if opts.DeliverAs != "" {
		options["deliverAs"] = opts.DeliverAs
	}
	result, err := c.callHost("sendMessage", map[string]any{
		"message": map[string]any{
			"customType": customType,
			"content":    content,
			"display":    display,
		},
		"options": options,
	})
	if err != nil {
		return err
	}
	if result != nil && result.Error != nil {
		return fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
	}
	return nil
}

// SendUserMessage injects a user message containing a string or text/image content blocks.
func (c Context) SendUserMessage(content any, deliverAs string) error {
	result, err := c.callHost("sendUserMessage", map[string]any{
		"content": content,
		"options": map[string]any{"deliverAs": deliverAs},
	})
	if err != nil {
		return err
	}
	if result != nil && result.Error != nil {
		return fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
	}
	return nil
}

// AppendEntry appends a custom entry to the session for persistence.
func (c Context) AppendEntry(customType string, data any) error {
	result, err := c.callHost("appendEntry", map[string]any{
		"customType": customType,
		"data":       data,
	})
	if err != nil {
		return err
	}
	if result != nil && result.Error != nil {
		return fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
	}
	return nil
}

// ── Editor Access ────────────────────────────────────────────────────────────

// GetEditorText returns the current text in the input editor.
func (c Context) GetEditorText() string {
	result, err := c.callHost("ui.getEditorText", nil)
	if err != nil || result == nil {
		return ""
	}
	var resp struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Text
}

// SetEditorText sets the text in the input editor.
func (c Context) SetEditorText(text string) {
	_, _ = c.callHost("ui.setEditorText", map[string]string{"text": text})
}

// PasteToEditor pastes text into the editor.
func (c Context) PasteToEditor(text string) {
	_, _ = c.callHost("ui.pasteToEditor", map[string]string{"text": text})
}

// ── Session State ────────────────────────────────────────────────────────────

// GetSessionName returns the current session name.
func (c Context) GetSessionName() string {
	result, err := c.callHost("getSessionName", nil)
	if err != nil || result == nil {
		return ""
	}
	var resp struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Name
}

// SetSessionName sets the session name.
func (c Context) SetSessionName(name string) error {
	result, err := c.callHost("setSessionName", map[string]string{"name": name})
	if err != nil {
		return err
	}
	if result != nil && result.Error != nil {
		return fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
	}
	return nil
}

// SetLabel sets or clears an entry label. The host's failure is returned, as
// upstream's setLabel throws when the session cannot record the label.
func (c Context) SetLabel(entryID, label string) error {
	result, err := c.callHost("setLabel", map[string]string{"entryId": entryID, "label": label})
	if err != nil {
		return err
	}
	if result != nil && result.Error != nil {
		return errors.New(result.Error.Message)
	}
	return nil
}

// GetFlag returns the value of a registered CLI flag.
func (c Context) GetFlag(name string) any {
	result, err := c.callHost("getFlag", map[string]string{"name": name})
	if err != nil || result == nil {
		return c.ext.flagDefaults[name]
	}
	var resp struct {
		Value any `json:"value"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	if resp.Value == nil {
		if def, ok := c.ext.flagDefaults[name]; ok {
			return def
		}
	}
	return resp.Value
}

// GetThinkingLevel returns the current thinking level.
func (c Context) GetThinkingLevel() string {
	result, err := c.callHost("getThinkingLevel", nil)
	if err != nil || result == nil {
		return ""
	}
	var resp struct {
		Level string `json:"level"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Level
}

// SetThinkingLevel sets the thinking level.
func (c Context) SetThinkingLevel(level string) {
	_, _ = c.callHost("setThinkingLevel", map[string]string{"level": level})
}

// SetModel changes the active model.
func (c Context) SetModel(model string) (bool, error) {
	result, err := c.callHost("setModel", map[string]string{"model": model})
	if err != nil {
		return false, err
	}
	if result == nil {
		return false, nil
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	if resp.Error != "" {
		return false, fmt.Errorf("%s", resp.Error)
	}
	return resp.Success, nil
}

// ── Tool State ───────────────────────────────────────────────────────────────

// ToolInfo describes a registered tool (returned by GetAllTools).
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
}

// GetActiveTools returns the currently active tool names.
func (c Context) GetActiveTools() []string {
	result, err := c.callHost("getActiveTools", nil)
	if err != nil || result == nil {
		return nil
	}
	var resp struct {
		Tools []string `json:"tools"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Tools
}

// GetAllTools returns all registered tools with their metadata.
func (c Context) GetAllTools() []ToolInfo {
	result, err := c.callHost("getAllTools", nil)
	if err != nil || result == nil {
		return nil
	}
	var resp struct {
		Tools []ToolInfo `json:"tools"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Tools
}

// SetActiveTools sets the active tool list.
func (c Context) SetActiveTools(tools []string) {
	_, _ = c.callHost("setActiveTools", map[string]any{"tools": tools})
}

// RefreshTools reloads tool definitions from the host.
func (c Context) RefreshTools() {
	_, _ = c.callHost("refreshTools", nil)
}

// ── Commands ─────────────────────────────────────────────────────────────────

// CommandInfo describes a registered slash command (returned by GetCommands).
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
}

// GetCommands returns all registered slash commands.
func (c Context) GetCommands() []CommandInfo {
	result, err := c.callHost("getCommands", nil)
	if err != nil || result == nil {
		return nil
	}
	var resp struct {
		Commands []CommandInfo `json:"commands"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Commands
}

// ── Context Usage ────────────────────────────────────────────────────────────

// ContextUsage reports context window utilization from the provider.
type ContextUsage struct {
	Tokens        int     `json:"tokens"`
	ContextWindow int     `json:"contextWindow"`
	Percent       float64 `json:"percent"`
}

// GetContextUsage returns context window usage from the last provider response.
// Returns nil if no usage data is available yet.
func (c Context) GetContextUsage() *ContextUsage {
	result, err := c.callHost("getContextUsage", nil)
	if err != nil || result == nil {
		return nil
	}
	var usage ContextUsage
	if err := json.Unmarshal(result.Result, &usage); err != nil {
		return nil
	}
	if usage.Tokens == 0 && usage.ContextWindow == 0 {
		return nil
	}
	return &usage
}

// ── System Prompt ────────────────────────────────────────────────────────────

// GetSystemPrompt returns the current system prompt text.
// Returns empty string if no system prompt is active.
func (c Context) GetSystemPrompt() string {
	result, err := c.callHost("getSystemPrompt", nil)
	if err != nil || result == nil {
		return ""
	}
	var resp struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Prompt
}

// SystemPromptOptions holds the base inputs pi uses to build the system
// prompt. Same shape as before_agent_start event.systemPromptOptions.
// May include full context-file contents; treat as sensitive.
type SystemPromptOptions struct {
	CustomPrompt       string                    `json:"customPrompt,omitempty"`
	SelectedTools      []string                  `json:"selectedTools,omitempty"`
	ToolSnippets       map[string]string         `json:"toolSnippets,omitempty"`
	PromptGuidelines   []string                  `json:"promptGuidelines,omitempty"`
	AppendSystemPrompt string                    `json:"appendSystemPrompt,omitempty"`
	Cwd                string                    `json:"cwd"`
	ContextFiles       []SystemPromptContextFile `json:"contextFiles,omitempty"`
	Skills             []SystemPromptSkill       `json:"skills,omitempty"`
}

// SystemPromptContextFile is a pre-loaded context file (AGENTS.md, etc.).
type SystemPromptContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SystemPromptSkill is skill metadata surfaced in the prompt.
type SystemPromptSkill struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	FilePath               string `json:"filePath"`
	BaseDir                string `json:"baseDir,omitempty"`
	DisableModelInvocation bool   `json:"disableModelInvocation,omitempty"`
}

// GetSystemPromptOptions returns the base inputs pi currently uses to
// build the system prompt (custom prompt, active tools, tool snippets,
// prompt guidelines, appended text, cwd, context files, skills). It
// reports current base inputs only, not per-turn before_agent_start
// changes. Available in command handlers.
func (c Context) GetSystemPromptOptions() SystemPromptOptions {
	var opts SystemPromptOptions
	result, err := c.callHost("getSystemPromptOptions", nil)
	if err != nil || result == nil {
		return opts
	}
	_ = json.Unmarshal(result.Result, &opts)
	return opts
}

// ── Model Info ───────────────────────────────────────────────────────────────

// ModelInfo contains structured metadata about the active model.
type ModelInfo struct {
	InputLimits         map[string]any `json:"inputLimits,omitempty"`
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Provider            string         `json:"provider"`
	ContextWindow       int            `json:"contextWindow"`
	MaxOutputTokens     int            `json:"maxOutputTokens"`
	Reasoning           bool           `json:"reasoning"`
	InputCostPer1M      float64        `json:"inputCostPer1M"`
	OutputCostPer1M     float64        `json:"outputCostPer1M"`
	CacheReadCostPer1M  float64        `json:"cacheReadCostPer1M"`
	CacheWriteCostPer1M float64        `json:"cacheWriteCostPer1M"`
}

func (m *ModelInfo) UnmarshalJSON(data []byte) error {
	var raw struct {
		InputLimits         map[string]any  `json:"inputLimits"`
		ID                  string          `json:"id"`
		ModelID             string          `json:"modelId"`
		Name                string          `json:"name"`
		DisplayName         string          `json:"displayName"`
		Provider            json.RawMessage `json:"provider"`
		ContextWindow       int             `json:"contextWindow"`
		MaxOutputTokens     int             `json:"maxOutputTokens"`
		MaxTokens           int             `json:"maxTokens"`
		Reasoning           bool            `json:"reasoning"`
		InputCostPer1M      float64         `json:"inputCostPer1M"`
		OutputCostPer1M     float64         `json:"outputCostPer1M"`
		CacheReadCostPer1M  float64         `json:"cacheReadCostPer1M"`
		CacheWriteCostPer1M float64         `json:"cacheWriteCostPer1M"`
		Cost                *struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cacheRead"`
			CacheWrite float64 `json:"cacheWrite"`
		} `json:"cost"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.InputLimits = raw.InputLimits
	m.ID = raw.ID
	if m.ID == "" {
		m.ID = raw.ModelID
	}
	m.Name = raw.Name
	if m.Name == "" {
		m.Name = raw.DisplayName
	}
	m.Provider = decodeModelProvider(raw.Provider)
	m.ContextWindow = raw.ContextWindow
	m.MaxOutputTokens = raw.MaxOutputTokens
	if m.MaxOutputTokens == 0 {
		m.MaxOutputTokens = raw.MaxTokens
	}
	m.Reasoning = raw.Reasoning
	m.InputCostPer1M = raw.InputCostPer1M
	m.OutputCostPer1M = raw.OutputCostPer1M
	m.CacheReadCostPer1M = raw.CacheReadCostPer1M
	m.CacheWriteCostPer1M = raw.CacheWriteCostPer1M
	if raw.Cost != nil {
		if m.InputCostPer1M == 0 {
			m.InputCostPer1M = raw.Cost.Input
		}
		if m.OutputCostPer1M == 0 {
			m.OutputCostPer1M = raw.Cost.Output
		}
		if m.CacheReadCostPer1M == 0 {
			m.CacheReadCostPer1M = raw.Cost.CacheRead
		}
		if m.CacheWriteCostPer1M == 0 {
			m.CacheWriteCostPer1M = raw.Cost.CacheWrite
		}
	}
	return nil
}

func decodeModelProvider(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var provider string
	if err := json.Unmarshal(raw, &provider); err == nil {
		return provider
	}
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.ID
	}
	return ""
}

// GetModelInfo returns structured metadata about the active model.
// Returns nil if no model is set.
func (c Context) GetModelInfo() *ModelInfo {
	result, err := c.callHost("getModelInfo", nil)
	if err != nil || result == nil {
		return nil
	}
	var info ModelInfo
	if err := json.Unmarshal(result.Result, &info); err != nil {
		return nil
	}
	if info.ID == "" {
		return nil
	}
	return &info
}

// ── Session Branch ───────────────────────────────────────────────────────────

// BranchEntry represents a single entry in the session conversation history.
// The host sends entries in upstream's nested format:
//
//	{"type":"message","message":{"role":"assistant","content":[...],"usage":{...}}}
//
// UnmarshalJSON flattens the nested "message" object into the top-level struct
// so extension code can access entry.Role, entry.Usage, etc. directly.
type BranchEntry struct {
	ID               string         `json:"id,omitempty"`
	Type             string         `json:"type"`
	Role             string         `json:"role,omitempty"`
	Content          string         `json:"content,omitempty"`
	FirstKeptEntryID string         `json:"firstKeptEntryId,omitempty"`
	Thinking         string         `json:"thinking,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	ModelID          string         `json:"model,omitempty"`
	ToolName         string         `json:"toolName,omitempty"`
	ToolCallID       string         `json:"toolCallId,omitempty"`
	IsError          bool           `json:"isError,omitempty"`
	Usage            *UsageInfo     `json:"usage,omitempty"`
	ToolCalls        []ToolCallInfo `json:"toolCalls,omitempty"`
}

// UnmarshalJSON handles the nested session entry format from the host.
// Entries arrive as {"type":"message","message":{...}} where the message
// object contains role, content, usage, etc. This method flattens the
// nested fields into the top-level BranchEntry.
func (b *BranchEntry) UnmarshalJSON(data []byte) error {
	// First pass: get the entry type and check for a nested "message" field.
	var raw struct {
		ID      string          `json:"id,omitempty"`
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message,omitempty"`
		// Non-message entry fields (compaction, branch_summary, custom_message).
		Summary          string `json:"summary,omitempty"`
		Content          string `json:"content,omitempty"`
		FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	b.ID = raw.ID
	b.Type = raw.Type
	b.FirstKeptEntryID = raw.FirstKeptEntryID

	if len(raw.Message) > 0 && raw.Message[0] == '{' {
		// Nested message entry: flatten its fields into BranchEntry.
		var msg struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			Provider   string          `json:"provider,omitempty"`
			Model      string          `json:"model,omitempty"`
			ToolName   string          `json:"toolName,omitempty"`
			ToolCallID string          `json:"toolCallId,omitempty"`
			IsError    bool            `json:"isError,omitempty"`
			Usage      *UsageInfo      `json:"usage,omitempty"`
		}
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			return nil // graceful: leave fields empty on decode failure
		}
		b.Role = msg.Role
		b.Provider = msg.Provider
		b.ModelID = msg.Model
		b.ToolName = msg.ToolName
		b.ToolCallID = msg.ToolCallID
		b.IsError = msg.IsError
		b.Usage = msg.Usage

		// Content can be a string or an array of content blocks.
		if len(msg.Content) > 0 {
			switch msg.Content[0] {
			case '"':
				// Plain string content.
				_ = json.Unmarshal(msg.Content, &b.Content)
			case '[':
				// Array of content blocks: extract text, thinking, and tool calls.
				var blocks []struct {
					Type      string `json:"type"`
					Text      string `json:"text,omitempty"`
					Thinking  string `json:"thinking,omitempty"`
					Name      string `json:"name,omitempty"`
					ID        string `json:"id,omitempty"`
					Arguments string `json:"arguments,omitempty"`
					Input     any    `json:"input,omitempty"`
				}
				if json.Unmarshal(msg.Content, &blocks) == nil {
					var textParts []string
					for _, block := range blocks {
						switch block.Type {
						case "text":
							textParts = append(textParts, block.Text)
						case "thinking":
							b.Thinking += block.Thinking
						case "tool_use", "toolCall":
							args := block.Arguments
							if args == "" && block.Input != nil {
								if a, err := json.Marshal(block.Input); err == nil {
									args = string(a)
								}
							}
							b.ToolCalls = append(b.ToolCalls, ToolCallInfo{
								Name: block.Name,
								ID:   block.ID,
								Args: args,
							})
						case "tool_result":
							// tool results in user messages; extract text
							textParts = append(textParts, block.Text)
						}
					}
					b.Content = strings.Join(textParts, "")
				}
			}
		}
	} else {
		// Non-message entries (compaction, branch_summary, custom_message).
		b.Content = raw.Content
		if b.Content == "" {
			b.Content = raw.Summary
		}
	}
	return nil
}

// UsageInfo contains token usage data for an assistant message.
type UsageInfo struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheWrite  int `json:"cacheWrite"`
	TotalTokens int `json:"totalTokens"`
}

// ToolCallInfo describes a tool invocation in an assistant message.
type ToolCallInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Args string `json:"args"`
}

// GetBranch returns the conversation branch as []BranchEntry.
// The slice and its entry values are copies.
// Nested fields are shared with the decoded mirror and must be treated as read-only.
// GetBranch returns nil without a session.
func (c Context) GetBranch() []BranchEntry {
	c.ext.ensureSessionLog()
	entries := c.ext.session.getBranchEntries()
	if len(entries) == 0 {
		return nil
	}
	branch := make([]BranchEntry, len(entries))
	for i, entry := range entries {
		if entry != nil {
			branch[i] = *entry
		}
	}
	return branch
}

// ── Session state ───────────────────────────────────────────────────────────

// GetEntries returns all session entries.
func (c Context) GetEntries() []json.RawMessage {
	c.ext.ensureSessionLog()
	return c.ext.session.getEntries()
}

// GetSessionID returns the current session ID.
func (c Context) GetSessionID() string {
	result, err := c.callHost("getSessionID", nil)
	if err != nil || result == nil {
		return ""
	}
	var resp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.ID
}

// GetSessionFile returns the path to the current session file.
func (c Context) GetSessionFile() string {
	c.ext.mu.RLock()
	cached := c.ext.sessionFile
	c.ext.mu.RUnlock()
	if cached != "" {
		return cached
	}
	result, err := c.callHost("getSessionFile", nil)
	if err != nil || result == nil {
		return ""
	}
	var resp struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Path
}

// GetLeafID returns the current leaf entry ID in the session tree.
func (c Context) GetLeafID() string {
	result, err := c.callHost("getLeafID", nil)
	if err != nil || result == nil {
		return ""
	}
	var resp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.ID
}

// ── Provider ─────────────────────────────────────────────────────────────────

// GetModelAuth returns auth credentials for a specific provider+model.
func (c Context) GetModelAuth(providerID, modelID string) json.RawMessage {
	result, err := c.callHost("getModelAuth", map[string]string{
		"provider": providerID,
		"modelId":  modelID,
	})
	if err != nil || result == nil {
		return nil
	}
	return result.Result
}

// ModelEventStream is the SDK-side pull stream for host model operations.
type ModelEventStream struct {
	mu       sync.Mutex
	delivery sync.Mutex
	queue    []map[string]any
	changed  chan struct{}
	done     chan struct{}
	terminal bool
	result   map[string]any
}

func newModelEventStream() *ModelEventStream {
	return &ModelEventStream{changed: make(chan struct{}), done: make(chan struct{})}
}

func (s *ModelEventStream) push(event map[string]any) {
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, event)
	if eventType, _ := event["type"].(string); eventType == "done" || eventType == "error" {
		s.terminal = true
		if eventType == "done" {
			s.result, _ = event["message"].(map[string]any)
		} else {
			s.result, _ = event["error"].(map[string]any)
		}
		close(s.done)
	}
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// Events returns model events in host order until terminal or cancellation.
func (s *ModelEventStream) Events(ctx context.Context) <-chan map[string]any {
	out := make(chan map[string]any)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			s.delivery.Lock()
			s.mu.Lock()
			if len(s.queue) > 0 {
				event := s.queue[0]
				s.mu.Unlock()
				select {
				case out <- event:
					s.mu.Lock()
					s.queue[0] = nil
					s.queue = s.queue[1:]
					if len(s.queue) == 0 {
						s.queue = nil
					}
					s.mu.Unlock()
					s.delivery.Unlock()
				case <-ctx.Done():
					s.delivery.Unlock()
					return
				}
				continue
			}
			if s.terminal {
				s.mu.Unlock()
				s.delivery.Unlock()
				return
			}
			changed := s.changed
			s.mu.Unlock()
			s.delivery.Unlock()
			select {
			case <-changed:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// Result waits for and returns the decoded terminal assistant message.
func (s *ModelEventStream) Result() map[string]any {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func modelStreamErrorEvent(err error, model map[string]any) map[string]any {
	provider, _ := model["provider"].(string)
	if provider == "" {
		if value, ok := model["provider"].(map[string]any); ok {
			provider, _ = value["id"].(string)
		}
	}
	modelID, _ := model["modelId"].(string)
	if modelID == "" {
		modelID, _ = model["id"].(string)
	}
	api, _ := model["api"].(string)
	return map[string]any{
		"type": "error", "reason": "error",
		"error": map[string]any{
			"role": "assistant", "content": []any{}, "api": api,
			"provider": provider, "model": modelID,
			"usage": map[string]any{
				"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
				"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
			},
			"stopReason": "error", "errorMessage": err.Error(), "timestamp": time.Now().UnixMilli(),
		},
	}
}

// ModelRegistry exposes Session model discovery, request authentication, and
// model operations through the host-owned runtime.
type ModelRegistry struct{ context Context }

// ModelRegistry returns the current Session model registry facade.
func (c Context) ModelRegistry() ModelRegistry { return ModelRegistry{context: c} }

// Find resolves a model by exact provider and model ID through the host registry.
func (r ModelRegistry) Find(providerID, modelID string) map[string]any {
	result, err := r.context.callHost("getModel", map[string]string{"provider": providerID, "modelId": modelID})
	if err := callResultError(result, err); err != nil || result == nil || string(result.Result) == "null" {
		return nil
	}
	var model map[string]any
	if err := json.Unmarshal(result.Result, &model); err != nil {
		return nil
	}
	return model
}

// GetApiKeyAndHeaders resolves request authentication through the host.
func (r ModelRegistry) GetApiKeyAndHeaders(model map[string]any) (map[string]any, error) {
	provider, _ := model["provider"].(string)
	if provider == "" {
		if value, ok := model["provider"].(map[string]any); ok {
			provider, _ = value["id"].(string)
		}
	}
	modelID, _ := model["modelId"].(string)
	if modelID == "" {
		modelID, _ = model["id"].(string)
	}
	result, err := r.context.callHost("getModelAuth", map[string]string{"provider": provider, "modelId": modelID})
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	var auth map[string]any
	if err := json.Unmarshal(result.Result, &auth); err != nil {
		return nil, err
	}
	return auth, nil
}

func (r ModelRegistry) Stream(model, request, options map[string]any) *ModelEventStream {
	stream := newModelEventStream()
	streamID := fmt.Sprintf("model-stream-%d", r.context.ext.modelStreamSeq.Add(1))
	r.context.ext.modelStreamsMu.Lock()
	r.context.ext.modelStreams[streamID] = stream
	r.context.ext.modelStreamsMu.Unlock()
	merged := make(map[string]any, len(request)+len(options))
	for key, value := range request {
		merged[key] = value
	}
	for key, value := range options {
		merged[key] = value
	}
	go func() {
		result, err := r.context.callHost("modelStream", map[string]any{"streamId": streamID, "model": model, "request": merged})
		if err := callResultError(result, err); err != nil {
			stream.push(modelStreamErrorEvent(err, model))
		} else {
			// The host sends every model_stream_event before its call result, but
			// the main loop applies notifications after the read loop routes the
			// result here. Unregister only after those notifications are applied.
			ext := r.context.ext
			ext.waitNotifications(ext.conn.notifications.Load(), stream.done)
			stream.push(modelStreamErrorEvent(errors.New("model stream ended without a terminal event"), model))
		}
		r.context.ext.modelStreamsMu.Lock()
		delete(r.context.ext.modelStreams, streamID)
		r.context.ext.modelStreamsMu.Unlock()
	}()
	return stream
}

func (r ModelRegistry) StreamSimple(model, request, options map[string]any) *ModelEventStream {
	return r.Stream(model, request, options)
}

func (r ModelRegistry) Complete(model, request, options map[string]any) map[string]any {
	return r.Stream(model, request, options).Result()
}

// Complete performs an LLM completion using the host's provider infrastructure.
func (c Context) Complete(model, request, auth map[string]any) (json.RawMessage, error) {
	result, err := c.callHost("complete", map[string]any{
		"model":   model,
		"request": request,
		"auth":    auth,
	})
	if err != nil {
		return nil, err
	}
	if result != nil && result.Error != nil {
		return nil, fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
	}
	if result == nil {
		return nil, nil
	}
	return result.Result, nil
}

// ── Shell ─────────────────────────────────────────────────────────────────────

// ExecResult contains the result of a shell command execution.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"code"`
}

// Exec runs a shell command through the host's bash executor.
func (c Context) Exec(command string, args []string) (*ExecResult, error) {
	result, err := c.callHost("exec", map[string]any{
		"command": command,
		"args":    args,
	})
	if err != nil {
		return nil, err
	}
	if result != nil && result.Error != nil {
		return nil, fmt.Errorf("%s: %s", result.Error.Code, result.Error.Message)
	}
	if result == nil {
		return nil, nil
	}
	var exec ExecResult
	_ = json.Unmarshal(result.Result, &exec)
	return &exec, nil
}

// ── Theme ────────────────────────────────────────────────────────────────────

// GetAllThemes returns all available themes with their names and paths.
func (c Context) GetAllThemes() []ThemeMeta {
	result, err := c.callHost("ui.getAllThemes", nil)
	if err != nil || result == nil || result.Error != nil {
		return nil
	}
	var resp struct {
		Themes []ThemeMeta `json:"themes"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Themes
}

// GetTheme loads a theme by name without switching to it.
func (c Context) GetTheme(name string) (Theme, error) {
	result, err := c.callHost("ui.getTheme", map[string]string{"name": name})
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	var resp struct {
		Theme Theme `json:"theme"`
	}
	if result == nil {
		return nil, nil
	}
	if err := json.Unmarshal(result.Result, &resp); err != nil {
		return nil, err
	}
	return resp.Theme, nil
}

// SetTheme switches the current theme by name.
func (c Context) SetTheme(name string) (bool, string) {
	result, err := c.callHost("ui.setTheme", map[string]string{"theme": name})
	if err != nil || result == nil {
		return false, err.Error()
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error,omitempty"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Success, resp.Error
}

// ── Widgets & advanced UI ───────────────────────────────────────────────────

// SetWidget sets or clears a widget. For the common []string case with no
// options it uses widget_push for efficiency; all other shapes go through the
// request/response bridge so nil clears and option-bearing calls work.
func (c Context) SetWidget(key string, content any, options ...WidgetOptions) error {
	if lines, ok := content.([]string); ok && len(options) == 0 {
		return c.ext.conn.pushWidget(key, lines)
	}
	var opts WidgetOptions
	if len(options) > 0 {
		opts = options[0]
	}
	result, err := c.callHost("ui.setWidget", map[string]any{
		"key":     key,
		"content": content,
		"options": opts,
	})
	return callResultError(result, err)
}

// SetFooter replaces the default footer with pre-rendered lines when lines
// is a non-empty []string, or clears a previously set footer when lines is nil.
// Component factories (non-string values) cannot be serialized across the
// subprocess boundary.
func (c Context) SetFooter(lines []string) error {
	if lines == nil {
		result, err := c.callHost("ui.setFooter", map[string]any{"clear": true})
		return callResultError(result, err)
	}
	result, err := c.callHost("ui.setFooter", map[string]any{"lines": lines})
	return callResultError(result, err)
}

// SetLogin replaces the shared header with a login rendered by the host.
// The host validates the definition and remains authoritative for rendering.
func (c Context) SetLogin(definition LoginDefinition) error {
	result, err := c.ext.conn.call("ui.setLogin", definition)
	return callResultError(result, err)
}

// SetHeader replaces the default header with pre-rendered lines when lines
// is a non-empty []string, or clears a previously set header when lines is nil.
// Component factories (non-string values) cannot be serialized across the
// subprocess boundary.
func (c Context) SetHeader(lines []string) error {
	if lines == nil {
		result, err := c.callHost("ui.setHeader", map[string]any{"clear": true})
		return callResultError(result, err)
	}
	result, err := c.callHost("ui.setHeader", map[string]any{"lines": lines})
	return callResultError(result, err)
}

// Custom opens a focused remote component. Pass a [RemoteComponent] as factory
// and [RemoteOverlayOptions] (or an equivalent JSON object) as options. Other
// factory values retain the explicit unsupported error because live host TUI
// component objects cannot cross a subprocess boundary.
func (c Context) Custom(factory any, options any) (any, error) {
	c.reportRequestState("blocked", "user")
	component, ok := factory.(RemoteComponent)
	if !ok || component == nil {
		result, err := c.callHost("ui.custom", map[string]any{})
		if err := callResultError(result, err); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: custom component must implement sdk.RemoteComponent", ErrUnsupportedSubprocessUI)
	}
	return c.runRemoteComponent(component, options)
}

func (c Context) runRemoteComponent(component RemoteComponent, options any) (_ any, returnErr error) {
	key := fmt.Sprintf("custom-%d", c.ext.overlaySeq.Add(1))
	overlay := newRemoteOverlay(component)
	c.ext.overlaysMu.Lock()
	c.ext.overlays[key] = overlay
	c.ext.overlaysMu.Unlock()
	defer func() {
		stopped := overlay.stop()
		c.ext.overlaysMu.Lock()
		delete(c.ext.overlays, key)
		c.ext.overlaysMu.Unlock()
		if !stopped {
			if returnErr == nil {
				returnErr = errors.New("focused component did not stop before the cleanup deadline")
			}
			return
		}
		if disposer, ok := component.(RemoteComponentDisposer); ok {
			// Upstream resolves the custom call before cleanup and ignores a
			// disposer panic. Keep one bad component from killing its extension.
			func() {
				defer func() { _ = recover() }()
				disposer.Dispose()
			}()
		}
	}()

	args := map[string]any{"key": key}
	if options != nil {
		encoded, err := json.Marshal(options)
		if err != nil {
			return nil, fmt.Errorf("encode custom overlay options: %w", err)
		}
		if err := json.Unmarshal(encoded, &args); err != nil {
			return nil, fmt.Errorf("custom overlay options must be an object: %w", err)
		}
		args["key"] = key
	}
	// Arm input and invalidation before the host sees the open call. A fused
	// transport can focus the overlay and return the first key while beginCallFor
	// is still sending the request.
	if err := overlay.start(c.ext.conn, key, c.Width); err != nil {
		return nil, err
	}
	pending, err := c.ext.conn.beginCallFor(c.requestID, "ui.custom", args)
	if err != nil {
		return nil, err
	}
	if err := overlay.render(c.ext.conn, key, c.Width()); err != nil {
		_ = c.ext.conn.notify("ui.custom.close", map[string]any{"key": key})
		_, _ = c.ext.conn.waitCall(pending)
		return nil, err
	}
	result, err := c.ext.conn.waitCall(pending)
	c.reportRequestState("progress", "")
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	if result == nil || len(result.Result) == 0 || string(result.Result) == "null" {
		return nil, nil
	}
	var response struct {
		Result any  `json:"result"`
		Ok     bool `json:"ok"`
	}
	if err := json.Unmarshal(result.Result, &response); err != nil {
		return nil, err
	}
	if !response.Ok {
		return nil, nil
	}
	return response.Result, nil
}

// AddAutocompleteProvider attempts to register an autocomplete provider.
// The subprocess bridge currently reports this as unsupported.
func (c Context) AddAutocompleteProvider(factory AutocompleteProviderFactory) error {
	result, err := c.callHost("ui.addAutocompleteProvider", map[string]any{})
	return callResultError(result, err)
}

// OnTerminalInput subscribes to raw terminal input, receiving every chunk
// before the editor does. The host is told to start forwarding only on the
// first subscription and to stop on the last, so an extension that never
// subscribes costs the input loop nothing.
//
// The returned unsubscribe is idempotent.
func (c Context) OnTerminalInput(handler TerminalInputHandler) (func(), error) {
	if handler == nil {
		return func() {}, fmt.Errorf("OnTerminalInput: handler must not be nil")
	}
	e := c.ext

	e.terminalInputMu.Lock()
	e.terminalInputNextID++
	id := e.terminalInputNextID
	e.terminalInputFuncs = append(e.terminalInputFuncs, terminalInputSub{id: id, handler: handler})
	first := len(e.terminalInputFuncs) == 1
	e.terminalInputMu.Unlock()

	unsubscribe := sync.OnceFunc(func() {
		e.terminalInputMu.Lock()
		e.terminalInputFuncs = slices.DeleteFunc(e.terminalInputFuncs, func(s terminalInputSub) bool {
			return s.id == id
		})
		last := len(e.terminalInputFuncs) == 0
		e.terminalInputMu.Unlock()
		if last {
			_, _ = e.conn.call("ui.offTerminalInput", map[string]any{})
		}
	})

	if !first {
		return unsubscribe, nil
	}
	result, err := e.conn.call("ui.onTerminalInput", map[string]any{})
	if err := callResultError(result, err); err != nil {
		unsubscribe()
		return func() {}, err
	}
	return unsubscribe, nil
}

// SetEditorComponent clears the custom editor when factory is nil.
func (c Context) SetEditorComponent(factory any) error {
	if factory != nil {
		return fmt.Errorf("%w: editor component factories cannot be serialized", ErrUnsupportedSubprocessUI)
	}
	result, err := c.callHost("ui.setEditorComponent", map[string]any{"clear": true})
	return callResultError(result, err)
}

// GetEditorComponent is unsupported for subprocess extensions because the
// host-side editor component factory cannot be serialized back over RPC.
func (c Context) GetEditorComponent() any {
	return nil
}

// ── Tool expansion ───────────────────────────────────────────────────────────

// GetToolsExpanded returns whether tool outputs are expanded.
func (c Context) GetToolsExpanded() bool {
	result, err := c.callHost("ui.getToolsExpanded", nil)
	if err != nil || result == nil {
		return false
	}
	var resp struct {
		Expanded bool `json:"expanded"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Expanded
}

// SetToolsExpanded sets whether tool outputs are expanded.
func (c Context) SetToolsExpanded(expanded bool) {
	_, _ = c.callHost("ui.setToolsExpanded", map[string]any{"expanded": expanded})
}

// ── Local state (cached from ready message) ──────────────────────────────────

// ConfigHome returns the pig config root directory.
// Reads PIG_HOME env var, defaulting to ~/.pig.
func (c Context) ConfigHome() string {
	if h := os.Getenv("PIG_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig")
}

// Cwd returns the working directory (from the ready message).
func (c Context) Cwd() string {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	return c.ext.cwd
}

// Mode returns the run mode pi is operating in: "tui", "rpc", "json", or
// "print" (from the ready message). Guard terminal-only UI on "tui".
// Defaults to "print" when the host did not specify one.
func (c Context) Mode() string {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	if c.ext.mode == "" {
		return "print"
	}
	return c.ext.mode
}

// Width returns the terminal width (from the ready message).
func (c Context) Width() int {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	return c.ext.width
}

// Height returns the current terminal height in rows. Updated by height_change
// notifications from the host. Returns 0 if the host has not reported a height.
func (c Context) Height() int {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	return c.ext.height
}

// Model returns the active provider model ID when the host exposes one, falling
// back to the model name from the ready/state message.
func (c Context) Model() string {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	return c.ext.model
}

// ModelProvider returns the active model's provider ID (e.g. "github-copilot",
// "anthropic"). Returns "" if unknown. Use with Model() to build a
// provider-qualified model string: provider + "/" + model.
func (c Context) ModelProvider() string {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	return c.ext.modelProvider
}

// ModelQualified returns the provider-qualified model string
// ("provider/model"). If the provider is unknown, returns just the model name.
func (c Context) ModelQualified() string {
	c.ext.mu.RLock()
	defer c.ext.mu.RUnlock()
	if c.ext.modelProvider != "" {
		return c.ext.modelProvider + "/" + c.ext.model
	}
	return c.ext.model
}

// ToolCallID returns the current tool call ID (only valid inside tool handlers).
func (c Context) ToolCallID() string {
	return c.toolCallID
}

// OnUpdate streams a partial result of the running tool, as upstream's
// execute(toolCallId, params, signal, onUpdate) callback does. partial is a
// string or a ToolResult (or any value with its wire shape). The host shows
// updates in order, before the tool's final result.
func (c Context) OnUpdate(partial any) error {
	if c.toolCallID == "" || c.requestID == "" {
		return errors.New("OnUpdate is only available while a tool runs")
	}
	if text, ok := partial.(string); ok {
		partial = ToolResult{Content: text}
	}
	return c.ext.conn.notify("tool_update", map[string]any{"request_id": c.requestID, "result": partial})
}

// ═══════════════════════════════════════════════════════════════════════════════
// Agent control
// ═══════════════════════════════════════════════════════════════════════════════

// IsProjectTrusted reports whether the current project is trusted. Untrusted
// projects have project-scoped settings and hooks disabled. Defaults to trusted
// when the host does not answer, matching the upstream runner.
func (c Context) IsProjectTrusted() bool {
	result, err := c.callHost("isProjectTrusted", nil)
	if err != nil || result == nil || result.Error != nil {
		return true
	}
	var resp struct {
		Trusted bool `json:"trusted"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Trusted
}

// IsIdle returns whether the agent is currently idle (not streaming).
func (c Context) IsIdle() bool {
	result, err := c.callHost("isIdle", nil)
	if err != nil || result == nil || result.Error != nil {
		return true // default to idle on error
	}
	var resp struct {
		Idle bool `json:"idle"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Idle
}

// Abort cancels the current agent operation.
func (c Context) Abort() {
	_, _ = c.callHost("abort", nil)
}

// HasPendingMessages returns whether there are queued messages waiting.
func (c Context) HasPendingMessages() bool {
	result, err := c.callHost("hasPendingMessages", nil)
	if err != nil || result == nil || result.Error != nil {
		return false
	}
	var resp struct {
		Pending bool `json:"pending"`
	}
	_ = json.Unmarshal(result.Result, &resp)
	return resp.Pending
}

// Shutdown triggers a graceful agent shutdown and exit.
func (c Context) Shutdown() {
	_, _ = c.callHost("shutdown", nil)
}

// Compact triggers compaction. Options are optional.
func (c Context) Compact(opts map[string]any) {
	_, _ = c.callHost("compact", opts)
}

// ═══════════════════════════════════════════════════════════════════════════════
// Command-only session control
// ═══════════════════════════════════════════════════════════════════════════════

// CancelledResult mirrors upstream's `{ cancelled: boolean }` return type.
type CancelledResult struct {
	Cancelled bool `json:"cancelled"`
}

func decodeCancelledResult(result *callResultMsg) CancelledResult {
	if result == nil || len(result.Result) == 0 {
		return CancelledResult{}
	}
	var r CancelledResult
	_ = json.Unmarshal(result.Result, &r)
	return r
}

// WaitForIdle blocks until the agent finishes streaming. Command-only.
func (c Context) WaitForIdle() error {
	result, err := c.callHost("waitForIdle", nil)
	return callResultError(result, err)
}

// NewSession starts a new session.
func (c Context) NewSession(opts map[string]any) (CancelledResult, error) {
	result, err := c.callHost("newSession", opts)
	if err := callResultError(result, err); err != nil {
		return CancelledResult{}, err
	}
	return decodeCancelledResult(result), nil
}

// Fork creates a new branch from an entry.
func (c Context) Fork(entryID string, opts map[string]any) (CancelledResult, error) {
	args := map[string]any{"entryId": entryID}
	for k, v := range opts {
		args[k] = v
	}
	result, err := c.callHost("fork", args)
	if err := callResultError(result, err); err != nil {
		return CancelledResult{}, err
	}
	return decodeCancelledResult(result), nil
}

// NavigateTree moves to a different point in the session tree.
func (c Context) NavigateTree(targetID string, opts map[string]any) (CancelledResult, error) {
	args := map[string]any{"targetId": targetID}
	for k, v := range opts {
		args[k] = v
	}
	result, err := c.callHost("navigateTree", args)
	if err := callResultError(result, err); err != nil {
		return CancelledResult{}, err
	}
	return decodeCancelledResult(result), nil
}

// SwitchSession switches to a different session file.
func (c Context) SwitchSession(sessionPath string, opts map[string]any) (CancelledResult, error) {
	args := map[string]any{"sessionPath": sessionPath}
	for k, v := range opts {
		args[k] = v
	}
	result, err := c.callHost("switchSession", args)
	if err := callResultError(result, err); err != nil {
		return CancelledResult{}, err
	}
	return decodeCancelledResult(result), nil
}

// Reload reloads extensions, skills, prompts, and themes.
func (c Context) Reload() error {
	result, err := c.callHost("reload", nil)
	return callResultError(result, err)
}

// OnWidthChange subscribes to terminal resizes, receiving the new width after
// Context.Width has been updated.
//
// Upstream Pi installs headers and footers as component factories whose
// render(width) runs every frame, so they follow a resize with no work from the
// extension. A pig extension is a subprocess and sends static lines instead, so
// a footer keeps the width it was built for until something re-pushes it. This
// is that trigger.
//
// Handlers run on the message loop, so they must not block: re-push the lines
// and return. The returned unsubscribe is idempotent.
func (c Context) OnWidthChange(handler WidthChangeHandler) (func(), error) {
	if handler == nil {
		return func() {}, fmt.Errorf("OnWidthChange: handler must not be nil")
	}
	e := c.ext

	e.widthChangeMu.Lock()
	e.widthChangeNextID++
	id := e.widthChangeNextID
	e.widthChangeFuncs = append(e.widthChangeFuncs, widthChangeSub{id: id, handler: handler})
	e.widthChangeMu.Unlock()

	return sync.OnceFunc(func() {
		e.widthChangeMu.Lock()
		e.widthChangeFuncs = slices.DeleteFunc(e.widthChangeFuncs, func(s widthChangeSub) bool {
			return s.id == id
		})
		e.widthChangeMu.Unlock()
	}), nil
}
