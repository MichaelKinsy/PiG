// Package extensiontest provides test doubles for the [extension.API]
// surface. The primary export is [Fake], a no-op [extension.API]
// implementation that records every call. Use it in unit tests for code
// that consumes the API surface without standing up a real host.
//
// Example:
//
//	api := extensiontest.NewFake()
//	myExtension(api)
//	if len(api.RegisterToolCalls) != 1 {
//	    t.Fatalf("expected one tool registered, got %d", len(api.RegisterToolCalls))
//	}
package extensiontest

import (
	"context"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Fake is a recording, no-op implementation of [extension.API]. Each method
// appends to a per-method call slice; tests assert on those slices.
//
// Fake is NOT goroutine-safe. Tests that drive a fake from multiple
// goroutines must wrap access with their own synchronisation.
type Fake struct {
	// ── Event subscriptions ──
	OnProjectTrustHandlers          []func(ctx context.Context, evt extension.ProjectTrustEvent) (extension.ProjectTrustEventResult, error)
	OnResourcesDiscoverHandlers     []func(ctx context.Context, evt extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error)
	OnSessionStartHandlers          []func(ctx context.Context, evt extension.SessionStartEvent) error
	OnSessionInfoChangedHandlers    []func(ctx context.Context, evt extension.SessionInfoChangedEvent) error
	OnSessionBeforeSwitchHandlers   []func(ctx context.Context, evt extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error)
	OnSessionBeforeForkHandlers     []func(ctx context.Context, evt extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error)
	OnSessionBeforeCompactHandlers  []func(ctx context.Context, evt extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error)
	OnSessionCompactHandlers        []func(ctx context.Context, evt extension.SessionCompactEvent) error
	OnSessionCompactFailedHandlers  []func(ctx context.Context, evt extension.SessionCompactFailedEvent) error
	OnSessionShutdownHandlers       []func(ctx context.Context, evt extension.SessionShutdownEvent) error
	OnSessionBeforeTreeHandlers     []func(ctx context.Context, evt extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error)
	OnSessionTreeHandlers           []func(ctx context.Context, evt extension.SessionTreeEvent) error
	OnContextHandlers               []func(ctx context.Context, evt extension.ContextEvent) (extension.ContextEventResult, error)
	OnContextWithSystemHandlers     []func(ctx context.Context, evt extension.ContextWithSystemEvent) (extension.ContextEventResult, error)
	OnBeforeProviderRequestHandlers []func(ctx context.Context, evt extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error)
	OnAfterProviderResponseHandlers []func(ctx context.Context, evt extension.AfterProviderResponseEvent) error
	OnBeforeProviderHeadersHandlers []func(ctx context.Context, evt extension.BeforeProviderHeadersEvent) error
	OnBeforeAgentStartHandlers      []func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error)
	OnAgentStartHandlers            []func(ctx context.Context, evt extension.AgentStartEvent) error
	OnAgentEndHandlers              []func(ctx context.Context, evt extension.AgentEndEvent) error
	OnAgentBeforeSettleHandlers     []func(ctx context.Context, evt *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error)
	OnAgentSettledHandlers          []func(ctx context.Context, evt extension.AgentSettledEvent) error
	OnUiPromptStartHandlers         []func(ctx context.Context, evt extension.UIPromptStartEvent) error
	OnUiPromptEndHandlers           []func(ctx context.Context, evt extension.UIPromptEndEvent) error
	OnTurnStartHandlers             []func(ctx context.Context, evt extension.TurnStartEvent) error
	OnTurnEndHandlers               []func(ctx context.Context, evt extension.TurnEndEvent) error
	OnMessageStartHandlers          []func(ctx context.Context, evt extension.MessageStartEvent) error
	OnMessageUpdateHandlers         []func(ctx context.Context, evt extension.MessageUpdateEvent) error
	OnMessageEndHandlers            []func(ctx context.Context, evt extension.MessageEndEvent) (extension.MessageEndEventResult, error)
	OnToolExecutionStartHandlers    []func(ctx context.Context, evt extension.ToolExecutionStartEvent) error
	OnToolExecutionUpdateHandlers   []func(ctx context.Context, evt extension.ToolExecutionUpdateEvent) error
	OnToolExecutionEndHandlers      []func(ctx context.Context, evt extension.ToolExecutionEndEvent) error
	OnModelSelectHandlers           []func(ctx context.Context, evt extension.ModelSelectEvent) error
	OnThinkingLevelSelectHandlers   []func(ctx context.Context, evt extension.ThinkingLevelSelectEvent) error
	OnToolCallHandlers              []func(ctx context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error)
	OnToolResultHandlers            []func(ctx context.Context, evt extension.ToolResultEvent) (extension.ToolResultEventResult, error)
	OnUserBashHandlers              []func(ctx context.Context, evt extension.UserBashEvent) (extension.UserBashEventResult, error)
	OnInputHandlers                 []func(ctx context.Context, evt extension.InputEvent) (extension.InputEventResult, error)

	// ── Registrations ──
	RegisterToolCalls            []extension.ToolDefinition
	RegisterCommandCalls         []RegisterCommandCall
	RegisterShortcutCalls        []RegisterShortcutCall
	RegisterFlagCalls            []RegisterFlagCall
	RegisterMessageRendererCalls []RegisterMessageRendererCall
	RegisterEntryRendererCalls   []RegisterEntryRendererCall
	RegisterMarkdownTransformers []extension.MarkdownTransformer
	RegisterProviderCalls        []RegisterProviderCall
	UnregisterProviderCalls      []string

	// ── Actions ──
	SendMessageCalls     []SendMessageCall
	SendUserMessageCalls []SendUserMessageCall
	AppendEntryCalls     []AppendEntryCall

	// ── Session metadata ──
	SessionName         string
	SetLabelCalls       []SetLabelCall
	ExecCalls           []ExecCall
	ActiveTools         []string
	AllTools            []extension.ToolInfo
	Commands            []extension.SlashCommandInfo
	SetActiveToolsCalls [][]string

	// ── Model + thinking ──
	CurrentModel          extension.Model
	SetModelCalls         []extension.Model
	SetModelReturn        bool
	ThinkingLevel         extension.ThinkingLevel
	SetThinkingLevelCalls []extension.ThinkingLevel

	// ── Flags ──
	Flags map[string]any

	// ── Event bus ──
	Bus extension.EventBus
}

// NewFake returns a fresh Fake with a [Bus] backed by [NewMemBus].
func NewFake() *Fake {
	return &Fake{
		Flags: map[string]any{},
		Bus:   NewMemBus(),
	}
}

// ─── Recording call structs ─────────────────────────────────────────────

type RegisterCommandCall struct {
	Name    string
	Options extension.CommandOptions
}

type RegisterShortcutCall struct {
	Shortcut extension.KeyID
	Options  extension.ShortcutOptions
}

type RegisterFlagCall struct {
	Name    string
	Options extension.FlagOptions
}

type RegisterMessageRendererCall struct {
	CustomType string
	Renderer   extension.MessageRenderer
}

type RegisterEntryRendererCall struct {
	CustomType string
	Renderer   extension.EntryRenderer
}

type RegisterProviderCall struct {
	Name   string
	Config extension.ProviderConfig
}

type SendMessageCall struct {
	Message extension.SendMessagePayload
	Options *extension.SendMessageOptions
}

type SendUserMessageCall struct {
	Content any
	Options *extension.SendUserMessageOptions
}

type AppendEntryCall struct {
	CustomType string
	Data       any
}

type SetLabelCall struct {
	EntryID string
	Label   string
}

type ExecCall struct {
	Command string
	Args    []string
	Options *extension.ExecOptions
}

// ─── Event subscription methods ────────────────────────────────────────

func (f *Fake) OnResourcesDiscover(h func(ctx context.Context, evt extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error)) {
	f.OnResourcesDiscoverHandlers = append(f.OnResourcesDiscoverHandlers, h)
}
func (f *Fake) OnSessionStart(h func(ctx context.Context, evt extension.SessionStartEvent) error) {
	f.OnSessionStartHandlers = append(f.OnSessionStartHandlers, h)
}
func (f *Fake) OnSessionInfoChanged(h func(ctx context.Context, evt extension.SessionInfoChangedEvent) error) {
	f.OnSessionInfoChangedHandlers = append(f.OnSessionInfoChangedHandlers, h)
}
func (f *Fake) OnSessionBeforeSwitch(h func(ctx context.Context, evt extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error)) {
	f.OnSessionBeforeSwitchHandlers = append(f.OnSessionBeforeSwitchHandlers, h)
}
func (f *Fake) OnSessionBeforeFork(h func(ctx context.Context, evt extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error)) {
	f.OnSessionBeforeForkHandlers = append(f.OnSessionBeforeForkHandlers, h)
}
func (f *Fake) OnSessionBeforeCompact(h func(ctx context.Context, evt extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error)) {
	f.OnSessionBeforeCompactHandlers = append(f.OnSessionBeforeCompactHandlers, h)
}
func (f *Fake) OnProjectTrust(h func(ctx context.Context, evt extension.ProjectTrustEvent) (extension.ProjectTrustEventResult, error)) {
	f.OnProjectTrustHandlers = append(f.OnProjectTrustHandlers, h)
}
func (f *Fake) OnSessionCompact(h func(ctx context.Context, evt extension.SessionCompactEvent) error) {
	f.OnSessionCompactHandlers = append(f.OnSessionCompactHandlers, h)
}
func (f *Fake) OnSessionCompactFailed(h func(ctx context.Context, evt extension.SessionCompactFailedEvent) error) {
	f.OnSessionCompactFailedHandlers = append(f.OnSessionCompactFailedHandlers, h)
}
func (f *Fake) OnSessionShutdown(h func(ctx context.Context, evt extension.SessionShutdownEvent) error) {
	f.OnSessionShutdownHandlers = append(f.OnSessionShutdownHandlers, h)
}
func (f *Fake) OnSessionBeforeTree(h func(ctx context.Context, evt extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error)) {
	f.OnSessionBeforeTreeHandlers = append(f.OnSessionBeforeTreeHandlers, h)
}
func (f *Fake) OnSessionTree(h func(ctx context.Context, evt extension.SessionTreeEvent) error) {
	f.OnSessionTreeHandlers = append(f.OnSessionTreeHandlers, h)
}
func (f *Fake) OnContext(h func(ctx context.Context, evt extension.ContextEvent) (extension.ContextEventResult, error)) {
	f.OnContextHandlers = append(f.OnContextHandlers, h)
}
func (f *Fake) OnContextWithSystem(h func(ctx context.Context, evt extension.ContextWithSystemEvent) (extension.ContextEventResult, error)) {
	f.OnContextWithSystemHandlers = append(f.OnContextWithSystemHandlers, h)
}
func (f *Fake) OnBeforeProviderRequest(h func(ctx context.Context, evt extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error)) {
	f.OnBeforeProviderRequestHandlers = append(f.OnBeforeProviderRequestHandlers, h)
}
func (f *Fake) OnAfterProviderResponse(h func(ctx context.Context, evt extension.AfterProviderResponseEvent) error) {
	f.OnAfterProviderResponseHandlers = append(f.OnAfterProviderResponseHandlers, h)
}
func (f *Fake) OnBeforeProviderHeaders(h func(ctx context.Context, evt extension.BeforeProviderHeadersEvent) error) {
	f.OnBeforeProviderHeadersHandlers = append(f.OnBeforeProviderHeadersHandlers, h)
}
func (f *Fake) OnBeforeAgentStart(h func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error)) {
	f.OnBeforeAgentStartHandlers = append(f.OnBeforeAgentStartHandlers, h)
}
func (f *Fake) OnAgentStart(h func(ctx context.Context, evt extension.AgentStartEvent) error) {
	f.OnAgentStartHandlers = append(f.OnAgentStartHandlers, h)
}
func (f *Fake) OnAgentEnd(h func(ctx context.Context, evt extension.AgentEndEvent) error) {
	f.OnAgentEndHandlers = append(f.OnAgentEndHandlers, h)
}
func (f *Fake) OnAgentBeforeSettle(h func(ctx context.Context, evt *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error)) {
	f.OnAgentBeforeSettleHandlers = append(f.OnAgentBeforeSettleHandlers, h)
}

func (f *Fake) OnAgentSettled(h func(ctx context.Context, evt extension.AgentSettledEvent) error) {
	f.OnAgentSettledHandlers = append(f.OnAgentSettledHandlers, h)
}
func (f *Fake) OnUiPromptStart(h func(ctx context.Context, evt extension.UIPromptStartEvent) error) {
	f.OnUiPromptStartHandlers = append(f.OnUiPromptStartHandlers, h)
}
func (f *Fake) OnUiPromptEnd(h func(ctx context.Context, evt extension.UIPromptEndEvent) error) {
	f.OnUiPromptEndHandlers = append(f.OnUiPromptEndHandlers, h)
}
func (f *Fake) OnTurnStart(h func(ctx context.Context, evt extension.TurnStartEvent) error) {
	f.OnTurnStartHandlers = append(f.OnTurnStartHandlers, h)
}
func (f *Fake) OnTurnEnd(h func(ctx context.Context, evt extension.TurnEndEvent) error) {
	f.OnTurnEndHandlers = append(f.OnTurnEndHandlers, h)
}
func (f *Fake) OnMessageStart(h func(ctx context.Context, evt extension.MessageStartEvent) error) {
	f.OnMessageStartHandlers = append(f.OnMessageStartHandlers, h)
}
func (f *Fake) OnMessageUpdate(h func(ctx context.Context, evt extension.MessageUpdateEvent) error) {
	f.OnMessageUpdateHandlers = append(f.OnMessageUpdateHandlers, h)
}
func (f *Fake) OnMessageEnd(h func(ctx context.Context, evt extension.MessageEndEvent) (extension.MessageEndEventResult, error)) {
	f.OnMessageEndHandlers = append(f.OnMessageEndHandlers, h)
}
func (f *Fake) OnToolExecutionStart(h func(ctx context.Context, evt extension.ToolExecutionStartEvent) error) {
	f.OnToolExecutionStartHandlers = append(f.OnToolExecutionStartHandlers, h)
}
func (f *Fake) OnToolExecutionUpdate(h func(ctx context.Context, evt extension.ToolExecutionUpdateEvent) error) {
	f.OnToolExecutionUpdateHandlers = append(f.OnToolExecutionUpdateHandlers, h)
}
func (f *Fake) OnToolExecutionEnd(h func(ctx context.Context, evt extension.ToolExecutionEndEvent) error) {
	f.OnToolExecutionEndHandlers = append(f.OnToolExecutionEndHandlers, h)
}
func (f *Fake) OnModelSelect(h func(ctx context.Context, evt extension.ModelSelectEvent) error) {
	f.OnModelSelectHandlers = append(f.OnModelSelectHandlers, h)
}
func (f *Fake) OnThinkingLevelSelect(h func(ctx context.Context, evt extension.ThinkingLevelSelectEvent) error) {
	f.OnThinkingLevelSelectHandlers = append(f.OnThinkingLevelSelectHandlers, h)
}
func (f *Fake) OnToolCall(h func(ctx context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error)) {
	f.OnToolCallHandlers = append(f.OnToolCallHandlers, h)
}
func (f *Fake) OnToolResult(h func(ctx context.Context, evt extension.ToolResultEvent) (extension.ToolResultEventResult, error)) {
	f.OnToolResultHandlers = append(f.OnToolResultHandlers, h)
}
func (f *Fake) OnUserBash(h func(ctx context.Context, evt extension.UserBashEvent) (extension.UserBashEventResult, error)) {
	f.OnUserBashHandlers = append(f.OnUserBashHandlers, h)
}
func (f *Fake) OnInput(h func(ctx context.Context, evt extension.InputEvent) (extension.InputEventResult, error)) {
	f.OnInputHandlers = append(f.OnInputHandlers, h)
}

// ─── Registrations ─────────────────────────────────────────────────────

func (f *Fake) RegisterTool(tool extension.ToolDefinition) {
	f.RegisterToolCalls = append(f.RegisterToolCalls, tool)
}
func (f *Fake) RegisterCommand(name string, options extension.CommandOptions) {
	f.RegisterCommandCalls = append(f.RegisterCommandCalls, RegisterCommandCall{Name: name, Options: options})
}
func (f *Fake) RegisterShortcut(shortcut extension.KeyID, options extension.ShortcutOptions) {
	f.RegisterShortcutCalls = append(f.RegisterShortcutCalls, RegisterShortcutCall{Shortcut: shortcut, Options: options})
}
func (f *Fake) RegisterFlag(name string, options extension.FlagOptions) {
	f.RegisterFlagCalls = append(f.RegisterFlagCalls, RegisterFlagCall{Name: name, Options: options})
}
func (f *Fake) GetFlag(name string) any {
	return f.Flags[name]
}
func (f *Fake) RegisterMessageRenderer(customType string, renderer extension.MessageRenderer) {
	f.RegisterMessageRendererCalls = append(f.RegisterMessageRendererCalls, RegisterMessageRendererCall{CustomType: customType, Renderer: renderer})
}
func (f *Fake) RegisterEntryRenderer(customType string, renderer extension.EntryRenderer) {
	f.RegisterEntryRendererCalls = append(f.RegisterEntryRendererCalls, RegisterEntryRendererCall{CustomType: customType, Renderer: renderer})
}

func (f *Fake) RegisterMarkdownTransformer(transformer extension.MarkdownTransformer) {
	f.RegisterMarkdownTransformers = append(f.RegisterMarkdownTransformers, transformer)
}
func (f *Fake) RegisterProvider(name string, config extension.ProviderConfig) {
	f.RegisterProviderCalls = append(f.RegisterProviderCalls, RegisterProviderCall{Name: name, Config: config})
}
func (f *Fake) UnregisterProvider(name string) {
	f.UnregisterProviderCalls = append(f.UnregisterProviderCalls, name)
}

// ─── Actions ───────────────────────────────────────────────────────────

func (f *Fake) SendMessage(message extension.SendMessagePayload, options *extension.SendMessageOptions) {
	f.SendMessageCalls = append(f.SendMessageCalls, SendMessageCall{Message: message, Options: options})
}
func (f *Fake) SendUserMessage(content any, options *extension.SendUserMessageOptions) {
	f.SendUserMessageCalls = append(f.SendUserMessageCalls, SendUserMessageCall{Content: content, Options: options})
}
func (f *Fake) AppendEntry(customType string, data any) {
	f.AppendEntryCalls = append(f.AppendEntryCalls, AppendEntryCall{CustomType: customType, Data: data})
}

// ─── Session metadata ──────────────────────────────────────────────────

func (f *Fake) SetSessionName(name string) {
	f.SessionName = name
}
func (f *Fake) GetSessionName() string {
	return f.SessionName
}
func (f *Fake) SetLabel(entryID string, label string) {
	f.SetLabelCalls = append(f.SetLabelCalls, SetLabelCall{EntryID: entryID, Label: label})
}
func (f *Fake) Exec(command string, args []string, options *extension.ExecOptions) (extension.ExecResult, error) {
	f.ExecCalls = append(f.ExecCalls, ExecCall{Command: command, Args: args, Options: options})
	return extension.ExecResult{}, nil
}
func (f *Fake) GetActiveTools() []string {
	return f.ActiveTools
}
func (f *Fake) GetAllTools() []extension.ToolInfo {
	return f.AllTools
}
func (f *Fake) SetActiveTools(toolNames []string) {
	f.SetActiveToolsCalls = append(f.SetActiveToolsCalls, toolNames)
	f.ActiveTools = toolNames
}
func (f *Fake) GetCommands() []extension.SlashCommandInfo {
	return f.Commands
}

// ─── Model + thinking ──────────────────────────────────────────────────

func (f *Fake) SetModel(model extension.Model) (bool, error) {
	f.SetModelCalls = append(f.SetModelCalls, model)
	f.CurrentModel = model
	return f.SetModelReturn, nil
}
func (f *Fake) GetThinkingLevel() extension.ThinkingLevel {
	return f.ThinkingLevel
}
func (f *Fake) SetThinkingLevel(level extension.ThinkingLevel) {
	f.SetThinkingLevelCalls = append(f.SetThinkingLevelCalls, level)
	f.ThinkingLevel = level
}

// ─── Event bus ─────────────────────────────────────────────────────────

func (f *Fake) Events() extension.EventBus {
	return f.Bus
}

// Compile-time assertion that *Fake implements extension.API.
var _ extension.API = (*Fake)(nil)
