package sdk

import (
	"context"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The members of Pi's AgentSession that an extension reaches over the wire, after the ones in child_session.go. Each one is the host's own AgentSession member (CallChildSessionOp): the value is read from, or the change made on, the session the host owns, so a call returns what the session holds now. A member Pi types `X | undefined` returns a nil pointer, map or an empty string for undefined, as noted on it.
//
// Async contract. A member Pi declares `async` blocks until its Promise settles and returns its value or error (Compact, NavigateTree, Reload, WaitForIdle, ExecuteBash, ExportToHTML, SummarizeForBugReport, SetModel, CycleModel, SendCustomMessage, SendUserMessage); it starts in call order, and a later call (an abort from another goroutine) never waits for it. Cancelling the ctx of Compact, NavigateTree and ExecuteBash runs Pi's abort for the work (AbortCompaction, AbortBranchSummary, AbortBash); the other awaited members have no abort in Pi and ctx ends only the wait before they start. A member Pi declares synchronous is a blocking call that returns the host's error with its value.

// SessionStatsTokens is Pi's SessionStats.tokens.
type SessionStatsTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
}

// SessionStats is Pi's SessionStats, the value of GetSessionStats.
type SessionStats struct {
	SessionFile       string             `json:"sessionFile,omitempty"`
	SessionID         string             `json:"sessionId"`
	UserMessages      int                `json:"userMessages"`
	AssistantMessages int                `json:"assistantMessages"`
	ToolCalls         int                `json:"toolCalls"`
	ToolResults       int                `json:"toolResults"`
	TotalMessages     int                `json:"totalMessages"`
	Tokens            SessionStatsTokens `json:"tokens"`
	Cost              float64            `json:"cost"`
	// ContextUsage is nil when no usable model context window is available.
	ContextUsage *ContextUsage `json:"contextUsage,omitempty"`
}

// CompactionResult is Pi's CompactionResult, the value of Compact.
type CompactionResult struct {
	Summary              string `json:"summary"`
	FirstKeptEntryID     string `json:"firstKeptEntryId"`
	TokensBefore         int    `json:"tokensBefore"`
	EstimatedTokensAfter *int   `json:"estimatedTokensAfter,omitempty"`
	// Usage is the summarization request's usage, nil when there is none.
	Usage   map[string]any `json:"usage,omitempty"`
	Details any            `json:"details,omitempty"`
}

// NavigateTreeResult is the value of AgentSession.navigateTree: `{editorText?, cancelled, aborted?, summaryEntry?}`.
type NavigateTreeResult struct {
	Cancelled  bool   `json:"cancelled"`
	Aborted    bool   `json:"aborted,omitempty"`
	EditorText string `json:"editorText,omitempty"`
	// SummaryEntry is the branch_summary entry the navigation created, nil when none.
	SummaryEntry map[string]any `json:"summaryEntry,omitempty"`
}

// BashResult is Pi's BashResult, the value of ExecuteBash and the argument of RecordBashResult.
type BashResult struct {
	Output string `json:"output"`
	// ExitCode is nil when the command has no exit status, including cancellation.
	ExitCode       *int   `json:"exitCode,omitempty"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// ExecuteBashOptions is the options object of AgentSession.executeBash. The onChunk callback is not carried: the output chunks reach Subscribe listeners as the session's events do.
type ExecuteBashOptions struct {
	// ExcludeFromContext keeps the result out of the LLM context (the `!!cmd` semantics); nil leaves it unset.
	ExcludeFromContext *bool
}

// ModelMutationOptions is Pi's ModelMutationOptions: Persist writes the choice to the global defaults.
type ModelMutationOptions struct {
	Persist bool
}

// ModelCycleResult is Pi's ModelCycleResult: the model a cycle selected, its clamped thinking level and whether it came from the scoped models.
type ModelCycleResult struct {
	Model         map[string]any `json:"model"`
	ThinkingLevel string         `json:"thinkingLevel"`
	IsScoped      bool           `json:"isScoped"`
}

// RoutedSelection is the value of AgentSession.routedModel: under a virtual selection, the physical model and thinking level of the latest successful response.
type RoutedSelection struct {
	Model         map[string]any `json:"model"`
	ThinkingLevel string         `json:"thinkingLevel,omitempty"`
}

// CacheWarmingStatus is Pi's CacheWarmingStatus, the value of CacheWarmingStatus.
type CacheWarmingStatus struct {
	// State is "inactive", "scheduled" or "refreshing".
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// NextWarmAt is the Unix millisecond time of the next decision, or 0.
	NextWarmAt        int64          `json:"nextWarmAt,omitempty"`
	Decision          map[string]any `json:"decision,omitempty"`
	ExtensionOverride bool           `json:"extensionOverride,omitempty"`
}

// ForkMessage is one entry of GetUserMessagesForForking.
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// QueuedMessages is the value of ClearQueue: the messages it removed.
type QueuedMessages struct {
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

const callChildSessionOp = "childSession.op"
const callChildSessionOpAsync = "childSession.opAsync"

// member runs a synchronous AgentSession member and decodes its value into T.
func member[T any](s *AgentSession, name string, args any) (T, error) {
	var value T
	raw, err := s.memberCall(callChildSessionOp, name, args)
	if err != nil {
		return value, err
	}
	return decodeMember(raw, value)
}

// memberAsync runs an awaited AgentSession member, as Pi's Promise: it returns when it settled. abort, when set, is Pi's abort for the work and runs when ctx is cancelled during the call.
func memberAsync[T any](ctx context.Context, s *AgentSession, name string, args any, abort string) (T, error) {
	var value T
	if err := ctx.Err(); err != nil {
		return value, err
	}
	if abort != "" && ctx.Done() != nil {
		stop := context.AfterFunc(ctx, func() {
			if _, err := s.memberCall(callChildSessionOp, abort, nil); err != nil && !s.disposed.Load() {
				diagnosticf("pig: %s of child session %s failed: %v\n", abort, s.id, err)
			}
		})
		defer stop()
	}
	raw, err := s.memberCall(callChildSessionOpAsync, name, args)
	if err != nil {
		return value, err
	}
	return decodeMember(raw, value)
}

func decodeMember[T any](raw json.RawMessage, zero T) (T, error) {
	if len(raw) == 0 {
		return zero, nil
	}
	if err := json.Unmarshal(raw, &zero); err != nil {
		var none T
		return none, err
	}
	return zero, nil
}

func (s *AgentSession) memberCall(method, name string, args any) (json.RawMessage, error) {
	op := struct {
		ID   string          `json:"id"`
		Op   string          `json:"op"`
		Args json.RawMessage `json:"args,omitempty"`
	}{ID: s.id, Op: name}
	if args != nil {
		encoded, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		op.Args = encoded
	}
	return s.call(method, op)
}

// run is a member that has no value.
func (s *AgentSession) run(name string, args any) error {
	_, err := s.memberCall(callChildSessionOp, name, args)
	return err
}

func (s *AgentSession) runAsync(ctx context.Context, name string, args any, abort string) error {
	_, err := memberAsync[json.RawMessage](ctx, s, name, args, abort)
	return err
}

// ── State reads ──────────────────────────────────────────────────────────────

// IsStreaming is `session.isStreaming`.
func (s *AgentSession) IsStreaming() (bool, error) { return member[bool](s, "isStreaming", nil) }

// IsIdle is `session.isIdle`: neither streaming nor compacting.
func (s *AgentSession) IsIdle() (bool, error) { return member[bool](s, "isIdle", nil) }

// IsCompacting is `session.isCompacting`.
func (s *AgentSession) IsCompacting() (bool, error) { return member[bool](s, "isCompacting", nil) }

// IsRetrying is `session.isRetrying`.
func (s *AgentSession) IsRetrying() (bool, error) { return member[bool](s, "isRetrying", nil) }

// IsBashRunning is `session.isBashRunning`.
func (s *AgentSession) IsBashRunning() (bool, error) { return member[bool](s, "isBashRunning", nil) }

// HasPendingBashMessages is `session.hasPendingBashMessages`: bash results wait to be flushed after the agent turn.
func (s *AgentSession) HasPendingBashMessages() (bool, error) {
	return member[bool](s, "hasPendingBashMessages", nil)
}

// RetryAttempt is `session.retryAttempt`.
func (s *AgentSession) RetryAttempt() (int, error) { return member[int](s, "retryAttempt", nil) }

// PendingMessageCount is `session.pendingMessageCount`: the queued steering and follow-up messages.
func (s *AgentSession) PendingMessageCount() (int, error) {
	return member[int](s, "pendingMessageCount", nil)
}

// CurrentSessionID is `session.sessionId`.
func (s *AgentSession) CurrentSessionID() (string, error) { return member[string](s, "sessionId", nil) }

// CurrentSessionFile is `session.sessionFile`; it is empty for an in-memory session, Pi's undefined.
func (s *AgentSession) CurrentSessionFile() (string, error) {
	file, err := member[*string](s, "sessionFile", nil)
	if file == nil {
		return "", err
	}
	return *file, err
}

// SessionName is `session.sessionName`; it is empty when the session has no name, Pi's undefined.
func (s *AgentSession) SessionName() (string, error) {
	name, err := member[*string](s, "sessionName", nil)
	if name == nil {
		return "", err
	}
	return *name, err
}

// CurrentSystemPrompt is `session.systemPrompt`.
func (s *AgentSession) CurrentSystemPrompt() (string, error) { return member[string](s, "systemPrompt", nil) }

// CurrentThinkingLevel is `session.thinkingLevel`.
func (s *AgentSession) CurrentThinkingLevel() (string, error) {
	return member[string](s, "thinkingLevel", nil)
}

// CurrentModel is `session.model`: the session's model object, nil without one.
func (s *AgentSession) CurrentModel() (map[string]any, error) {
	return member[map[string]any](s, "model", nil)
}

// RoutedModel is `session.routedModel`: under a virtual selection, the physical model and thinking level of the latest successful response, nil otherwise.
func (s *AgentSession) RoutedModel() (*RoutedSelection, error) {
	return member[*RoutedSelection](s, "routedModel", nil)
}

// ScopedModels is `session.scopedModels`: the models the session cycles through, empty for all available models.
func (s *AgentSession) ScopedModels() ([]ScopedModel, error) {
	models, err := member[[]ScopedModel](s, "scopedModels", nil)
	if models == nil && err == nil {
		models = []ScopedModel{}
	}
	return models, err
}

// SteeringMode is `session.steeringMode`: "all" or "one-at-a-time".
func (s *AgentSession) SteeringMode() (string, error) { return member[string](s, "steeringMode", nil) }

// FollowUpMode is `session.followUpMode`: "all" or "one-at-a-time".
func (s *AgentSession) FollowUpMode() (string, error) { return member[string](s, "followUpMode", nil) }

// AutoCompactionEnabled is `session.autoCompactionEnabled`.
func (s *AgentSession) AutoCompactionEnabled() (bool, error) {
	return member[bool](s, "autoCompactionEnabled", nil)
}

// AutoRetryEnabled is `session.autoRetryEnabled`.
func (s *AgentSession) AutoRetryEnabled() (bool, error) {
	return member[bool](s, "autoRetryEnabled", nil)
}

// SupportsThinking is `session.supportsThinking()`.
func (s *AgentSession) SupportsThinking() (bool, error) {
	return member[bool](s, "supportsThinking", nil)
}

// GetAvailableThinkingLevels is `session.getAvailableThinkingLevels()`.
func (s *AgentSession) GetAvailableThinkingLevels() ([]string, error) {
	return memberList[string](s, "getAvailableThinkingLevels")
}

// GetLastAssistantText is `session.getLastAssistantText()`; it is nil when the session has none, Pi's undefined.
func (s *AgentSession) GetLastAssistantText() (*string, error) {
	return member[*string](s, "getLastAssistantText", nil)
}

// GetActiveToolNames is `session.getActiveToolNames()`.
func (s *AgentSession) GetActiveToolNames() ([]string, error) {
	return memberList[string](s, "getActiveToolNames")
}

// GetCallableToolNames is `session.getCallableToolNames()`.
func (s *AgentSession) GetCallableToolNames() ([]string, error) {
	return memberList[string](s, "getCallableToolNames")
}

// GetSteeringMessages is `session.getSteeringMessages()`: the steering messages that have not been delivered.
func (s *AgentSession) GetSteeringMessages() ([]string, error) {
	return memberList[string](s, "getSteeringMessages")
}

// GetFollowUpMessages is `session.getFollowUpMessages()`: the follow-up messages that have not been delivered.
func (s *AgentSession) GetFollowUpMessages() ([]string, error) {
	return memberList[string](s, "getFollowUpMessages")
}

// memberList is a member whose value is a list Pi never leaves undefined.
func memberList[T any](s *AgentSession, name string) ([]T, error) {
	list, err := member[[]T](s, name, nil)
	if list == nil && err == nil {
		list = []T{}
	}
	return list, err
}

// GetAllTools is `session.getAllTools()`. A tool's ReachesTools hook is a function of the extension that registered the tool and is nil here.
func (s *AgentSession) GetAllTools() ([]ToolInfo, error) {
	return memberList[ToolInfo](s, "getAllTools")
}

// GetToolDefinition is `session.getToolDefinition(name)`: the definition's data, nil when no tool has the name. The definition's execute function and hooks run in the extension that registered the tool and are not carried.
func (s *AgentSession) GetToolDefinition(name string) (*ToolInfo, error) {
	return member[*ToolInfo](s, "getToolDefinition", map[string]any{"name": name})
}

// GetContextUsage is `session.getContextUsage()`: nil when no usable model context window is available.
func (s *AgentSession) GetContextUsage() (*ContextUsage, error) {
	return member[*ContextUsage](s, "getContextUsage", nil)
}

// GetSessionStats is `session.getSessionStats()`.
func (s *AgentSession) GetSessionStats() (SessionStats, error) {
	return member[SessionStats](s, "getSessionStats", nil)
}

// GetUserMessagesForForking is `session.getUserMessagesForForking()`.
func (s *AgentSession) GetUserMessagesForForking() ([]ForkMessage, error) {
	return memberList[ForkMessage](s, "getUserMessagesForForking")
}

// PromptTemplates is `session.promptTemplates`.
func (s *AgentSession) PromptTemplates() ([]PromptTemplate, error) {
	return memberList[PromptTemplate](s, "promptTemplates")
}

// CacheWarmingStatus is `session.cacheWarmingStatus`: nil when the session has no cache warmer.
func (s *AgentSession) CacheWarmingStatus() (*CacheWarmingStatus, error) {
	return member[*CacheWarmingStatus](s, "cacheWarmingStatus", nil)
}

// HasExtensionHandlers is `session.hasExtensionHandlers(eventType)`: an extension the session loaded handles the event.
func (s *AgentSession) HasExtensionHandlers(eventType string) (bool, error) {
	return member[bool](s, "hasExtensionHandlers", map[string]any{"eventType": eventType})
}

// ── Changes ──────────────────────────────────────────────────────────────────

// SetModel is `session.setModel(model, options)`: model is a Pi Model object whose provider and id name a model of the host's registry. It fails for a model whose provider has no credentials.
func (s *AgentSession) SetModel(ctx context.Context, model map[string]any, options ...ModelMutationOptions) error {
	return s.runAsync(ctx, "setModel", map[string]any{"model": model, "persist": persistOf(options)}, "")
}

// CycleModel is `session.cycleModel(direction, options)`: direction is "forward" (empty) or "backward". It is nil when there is no model to cycle to.
func (s *AgentSession) CycleModel(ctx context.Context, direction string, options ...ModelMutationOptions) (*ModelCycleResult, error) {
	return memberAsync[*ModelCycleResult](ctx, s, "cycleModel", map[string]any{"direction": direction, "persist": persistOf(options)}, "")
}

// SetThinkingLevel is `session.setThinkingLevel(level, options)`; the level is clamped to the model.
func (s *AgentSession) SetThinkingLevel(level string, options ...ModelMutationOptions) error {
	return s.run("setThinkingLevel", map[string]any{"level": level, "persist": persistOf(options)})
}

// CycleThinkingLevel is `session.cycleThinkingLevel(options)`: the new level, empty when the model does not support thinking, Pi's undefined.
func (s *AgentSession) CycleThinkingLevel(options ...ModelMutationOptions) (string, error) {
	level, err := member[*string](s, "cycleThinkingLevel", map[string]any{"persist": persistOf(options)})
	if level == nil {
		return "", err
	}
	return *level, err
}

func persistOf(options []ModelMutationOptions) bool { return len(options) > 0 && options[0].Persist }

// SetScopedModels is `session.setScopedModels(models)`.
func (s *AgentSession) SetScopedModels(models []ScopedModel) error {
	if models == nil {
		models = []ScopedModel{}
	}
	return s.run("setScopedModels", map[string]any{"models": models})
}

// SetActiveToolsByName is `session.setActiveToolsByName(names)`.
func (s *AgentSession) SetActiveToolsByName(names []string) error {
	if names == nil {
		names = []string{}
	}
	return s.run("setActiveToolsByName", map[string]any{"names": names})
}

// SetSteeringMode is `session.setSteeringMode(mode)`: "all" or "one-at-a-time".
func (s *AgentSession) SetSteeringMode(mode string) error {
	return s.run("setSteeringMode", map[string]any{"mode": mode})
}

// SetFollowUpMode is `session.setFollowUpMode(mode)`: "all" or "one-at-a-time".
func (s *AgentSession) SetFollowUpMode(mode string) error {
	return s.run("setFollowUpMode", map[string]any{"mode": mode})
}

// SetAutoCompactionEnabled is `session.setAutoCompactionEnabled(enabled)`.
func (s *AgentSession) SetAutoCompactionEnabled(enabled bool) error {
	return s.run("setAutoCompactionEnabled", map[string]any{"enabled": enabled})
}

// SetAutoRetryEnabled is `session.setAutoRetryEnabled(enabled)`.
func (s *AgentSession) SetAutoRetryEnabled(enabled bool) error {
	return s.run("setAutoRetryEnabled", map[string]any{"enabled": enabled})
}

// SetCacheWarmingMode is `session.setCacheWarmingMode(mode)`: "off", "streaming" or "idle".
func (s *AgentSession) SetCacheWarmingMode(mode string) error {
	return s.run("setCacheWarmingMode", map[string]any{"mode": mode})
}

// SetSessionName is `session.setSessionName(name)`.
func (s *AgentSession) SetSessionName(name string) error {
	return s.run("setSessionName", map[string]any{"name": name})
}

// ClearQueue is `session.clearQueue()`: it removes the queued steering and follow-up messages and returns them.
func (s *AgentSession) ClearQueue() (QueuedMessages, error) {
	queued, err := member[QueuedMessages](s, "clearQueue", nil)
	if queued.Steering == nil {
		queued.Steering = []string{}
	}
	if queued.FollowUp == nil {
		queued.FollowUp = []string{}
	}
	return queued, err
}

// AbortBash is `session.abortBash()`.
func (s *AgentSession) AbortBash() error { return s.run("abortBash", nil) }

// AbortBranchSummary is `session.abortBranchSummary()`.
func (s *AgentSession) AbortBranchSummary() error { return s.run("abortBranchSummary", nil) }

// AbortCompaction is `session.abortCompaction()`.
func (s *AgentSession) AbortCompaction() error { return s.run("abortCompaction", nil) }

// AbortRetry is `session.abortRetry()`.
func (s *AgentSession) AbortRetry() error { return s.run("abortRetry", nil) }

// ── Awaited members ──────────────────────────────────────────────────────────

// WaitForIdle is `session.waitForIdle()`: it returns when the session is neither streaming nor compacting.
func (s *AgentSession) WaitForIdle(ctx context.Context) error {
	return s.runAsync(ctx, "waitForIdle", nil, "")
}

// Reload is `session.reload()`: settings, extensions and resources are loaded again. Pi rejects it while the session runs or compacts.
func (s *AgentSession) Reload(ctx context.Context) error { return s.runAsync(ctx, "reload", nil, "") }

// Compact is `session.compact(customInstructions)`: manual compaction. A session with nothing to compact is an error, as Pi's rejection. Cancelling ctx runs AbortCompaction.
func (s *AgentSession) Compact(ctx context.Context, customInstructions string) (CompactionResult, error) {
	return memberAsync[CompactionResult](ctx, s, "compact", map[string]any{"customInstructions": customInstructions}, "abortCompaction")
}

// NavigateTree is `session.navigateTree(targetId, options)`. Cancelling ctx runs AbortBranchSummary.
func (s *AgentSession) NavigateTree(ctx context.Context, targetID string, options ...NavigateTreeOptions) (NavigateTreeResult, error) {
	args := map[string]any{"targetId": targetID}
	if len(options) > 0 {
		o := options[0]
		args["summarize"], args["customInstructions"], args["replaceInstructions"], args["label"] = o.Summarize, o.CustomInstructions, o.ReplaceInstructions, o.Label
	}
	return memberAsync[NavigateTreeResult](ctx, s, "navigateTree", args, "abortBranchSummary")
}

// ExecuteBash is `session.executeBash(command, onChunk, options)`: the command runs outside the agent loop and the result is recorded for the next turn. Cancelling ctx runs AbortBash.
func (s *AgentSession) ExecuteBash(ctx context.Context, command string, options ...ExecuteBashOptions) (BashResult, error) {
	args := map[string]any{"command": command}
	if len(options) > 0 && options[0].ExcludeFromContext != nil {
		args["excludeFromContext"] = *options[0].ExcludeFromContext
	}
	return memberAsync[BashResult](ctx, s, "executeBash", args, "abortBash")
}

// RecordBashResult is `session.recordBashResult(command, result, options)`.
func (s *AgentSession) RecordBashResult(command string, result BashResult, options ...ExecuteBashOptions) error {
	args := map[string]any{"command": command, "result": result}
	if len(options) > 0 && options[0].ExcludeFromContext != nil {
		args["excludeFromContext"] = *options[0].ExcludeFromContext
	}
	return s.run("recordBashResult", args)
}

// ExportHTMLOptions is the options of ExportToHTML.
type ExportHTMLOptions struct {
	// ThemeName exports with this theme when it is registered.
	ThemeName string
}

// ExportToHTML is `session.exportToHtml(outputPath, options)`: it writes the session as an HTML file and returns its path. An empty outputPath is Pi's default location.
func (s *AgentSession) ExportToHTML(ctx context.Context, outputPath string, options ...ExportHTMLOptions) (string, error) {
	args := map[string]any{"outputPath": outputPath}
	if len(options) > 0 {
		args["themeName"] = options[0].ThemeName
	}
	return memberAsync[string](ctx, s, "exportToHtml", args, "")
}

// ExportToJsonl is `session.exportToJsonl(outputPath)`: it writes the session as JSONL and returns its path.
func (s *AgentSession) ExportToJsonl(outputPath string) (string, error) {
	return member[string](s, "exportToJsonl", map[string]any{"outputPath": outputPath})
}

// SummarizeForBugReport is `session.summarizeForBugReport({hint, signal})`. ctx is the signal for the wait before the request starts; a request in flight is not cancelled.
func (s *AgentSession) SummarizeForBugReport(ctx context.Context, hint string) (string, error) {
	return memberAsync[string](ctx, s, "summarizeForBugReport", map[string]any{"hint": hint}, "")
}

// SendCustomMessage is `session.sendCustomMessage(message, options)`.
func (s *AgentSession) SendCustomMessage(ctx context.Context, message CustomMessage, options SendMessageOptions) error {
	wire := map[string]any{"customType": message.CustomType, "content": message.Content, "display": message.Display}
	if message.Details != nil {
		wire["details"] = message.Details
	}
	opts := map[string]any{}
	if options.TriggerTurn != nil {
		opts["triggerTurn"] = *options.TriggerTurn
	}
	if options.DeliverAs != "" {
		opts["deliverAs"] = options.DeliverAs
	}
	return s.runAsync(ctx, "sendCustomMessage", map[string]any{"message": wire, "options": opts}, "")
}

// SendUserMessage is `session.sendUserMessage(content, options)`: content is a string or an array of text and image content blocks.
func (s *AgentSession) SendUserMessage(ctx context.Context, content any, options SendUserMessageOptions) error {
	return s.runAsync(ctx, "sendUserMessage", map[string]any{"content": content, "options": options}, "")
}
