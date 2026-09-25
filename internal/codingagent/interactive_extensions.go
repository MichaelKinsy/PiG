package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) deliverUserMessage(content any, deliverAs extension.DeliverAs) error {
	parsed, err := subprocessUserContent(content)
	if err != nil {
		return fmt.Errorf("sendUserMessage: %w", err)
	}
	var text string
	var images []ai.ImageContent
	switch value := parsed.(type) {
	case ai.UserText:
		text = string(value)
	case ai.UserContentBlocks:
		var parts []string
		for _, block := range value {
			switch block := block.(type) {
			case ai.TextContent:
				parts = append(parts, block.Text)
			case ai.ImageContent:
				images = append(images, block)
			}
		}
		text = strings.Join(parts, "\n")
	}
	if m.agent == nil {
		return fmt.Errorf("agent not available")
	}
	switch deliverAs {
	case "", extension.DeliverAsFollowUp, extension.DeliverAsSteer:
		// validated; dispatched on the main loop below
	default:
		return fmt.Errorf("unsupported deliverAs %q", deliverAs)
	}

	// Host callbacks may run off the main loop. Marshal onto the UI task queue,
	// where TUI/session mutation is safe.
	//
	// turnActive, not isIdle, decides whether a turn exists to receive the
	// message. The run goroutine clears turnActive before it queues the cleanup
	// that restores isIdle, so a task landing in that window reads isIdle false
	// with no goroutine left to drain a steering queue, and the message is lost.
	// Upstream prompt() throws when a run is active and no streaming
	// behavior was given.
	if deliverAs == "" && m.runStreaming() {
		return errors.New("Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")
	}
	return m.postToMain(m.runCtx, func() {
		if m.agent == nil {
			return
		}
		// Upstream sendUserMessage is prompt() with source "extension" and
		// no command dispatch or template expansion. A run that started
		// after the check above receives an unspecified delivery as a
		// follow-up.
		m.promptUserInput(m.runCtx, text, images, deliverAs != extension.DeliverAsSteer, extension.InputSourceExtension, false)
	})
}

// activeBuiltinToolNames returns the names of built-in tools that are
// currently active (i.e., that refreshAgentTools will actually create and
// add to the agent). If ActiveBuiltinTools is nil, all built-in tools are
// active. Otherwise, only those in the set are active.
//
// This is used by GetAllTools/GetActiveTools host callbacks so that piglet
// scoping sees built-in tools alongside extension tools. Without this, the
// piglet extension's applyScoping would build an AllowedTools set from only
// extension tools, causing refreshAgentTools to filter out all built-in tools
// (bash, read, write, edit, grep, find, ls) after a /reload or rescope.
func (m *InteractiveMode) activeBuiltinToolNames(extTools ...extension.RegisteredTool) []string {
	all := tools.BuiltinToolNames()
	overridden := make(map[string]bool, len(extTools))
	for _, tool := range extTools {
		overridden[tool.Definition.Name] = true
	}
	active := make([]string, 0, len(all))
	for _, name := range all {
		if !overridden[name] && tools.BuiltinToolActive(name, m.opts.ActiveBuiltinTools, m.opts.AllowedTools) {
			active = append(active, name)
		}
	}
	return active
}

// wireInprocContextActions populates the inproc runner's ContextActions and
// the extension action callbacks needed by bundled Go extensions. In-process
// extension handlers (piglet, onboard, etc.) can query session state via
// extension.Context methods like GetAllTools(), SetActiveTools(), GetFlagValue(),
// and can inject user turns through CommandContext.SendUserMessage().
//
// This is the in-process equivalent of wireSubprocessHostCallbacks.
// Both must stay in sync: same session state is exposed through two
// different mechanisms:
//   - subprocess extensions: via SetHostAction → wire protocol
//   - in-process extensions: via ContextActions → direct function calls
func (m *InteractiveMode) wireInprocContextActions() {
	if m.newRunner == nil {
		return
	}
	m.newRunner.AddErrorListener(m.queueExtensionError)
	if m.tuiInst != nil && m.layout != nil {
		m.newRunner.SetUIContext(&ExtUIContext{m: m})
	}
	// Subprocess dialogs reach the TUI through the bridge, so the bridge
	// reports their ui_prompt_start/ui_prompt_end through the current runner.
	if m.opts.SubprocessUIBridge != nil {
		m.opts.SubprocessUIBridge.SetUIPromptScope(m.newRunner)
	}

	actions := extension.ContextActions{
		GetAllTools: func() []extension.ToolInfo {
			extTools := m.newRunner.Tools()
			builtinNames := m.activeBuiltinToolNames(extTools...)
			result := make([]extension.ToolInfo, 0, len(extTools)+len(builtinNames))
			for _, t := range extTools {
				source := "builtin"
				if s, ok := t.SourceInfo.(string); ok && s != "" {
					source = s
				}
				result = append(result, extension.ToolInfo{
					Name:        t.Definition.Name,
					Description: t.Definition.Description,
					SourceInfo:  source,
				})
			}
			// Append built-in tools so piglet scoping sees them. Built-in
			// tools live in the agent (not the runner), so without this they
			// would be invisible to GetAllTools → ScopeTools → SetActiveTools,
			// causing them to be filtered out after a rescope.
			for _, name := range builtinNames {
				result = append(result, extension.ToolInfo{
					Name:        name,
					Description: tools.BuiltinToolDescription(name),
					SourceInfo:  "builtin",
				})
			}
			return result
		},
		GetActiveTools: func() []string {
			extTools := m.newRunner.Tools()
			builtinNames := m.activeBuiltinToolNames(extTools...)
			names := make([]string, 0, len(extTools)+len(builtinNames))
			for _, t := range extTools {
				names = append(names, t.Definition.Name)
			}
			names = append(names, builtinNames...)
			return names
		},
		SetActiveTools: func(names []string) {
			m.opts.AllowedTools = make(map[string]struct{}, len(names)+len(m.opts.ActiveBuiltinTools))
			for _, name := range names {
				m.opts.AllowedTools[name] = struct{}{}
			}
			// Preserve active builtin tools so they aren't gated by
			// extension-only scoping (builtins aren't in GetAllTools).
			if m.opts.ActiveBuiltinTools != nil {
				for name := range m.opts.ActiveBuiltinTools {
					m.opts.AllowedTools[name] = struct{}{}
				}
			} else {
				// nil ActiveBuiltinTools = all builtins active
				for _, name := range []string{"read", "bash", "edit", "write", "grep", "find", "ls"} {
					m.opts.AllowedTools[name] = struct{}{}
				}
			}
			for _, err := range m.refreshAgentTools() {
				fmt.Fprintf(os.Stderr, "tool refresh: %v\n", err)
			}
		},
		GetFlagValue: func(name string) any {
			if m.opts.UnknownFlags != nil {
				if value, ok := m.opts.UnknownFlags[name]; ok {
					return value
				}
			}
			return nil
		},
		GetModel: func() extension.Model {
			if m.opts.Model == nil {
				return nil
			}
			return modelToExtModel(m.opts.Model)
		},
		IsIdle:           m.extensionIsIdle,
		IsProjectTrusted: func() bool { return m.projectTrusted() },
		HasPendingMessages: func() bool {
			if m.agent == nil {
				return false
			}
			steering, followUps := m.agent.PendingMessages()
			return len(steering)+len(followUps) > 0
		},
		Abort: func() {
			if m.agent != nil {
				m.abortFn()
			}
		},
		Shutdown: func() {
			m.requestShutdown()
		},
		GetSystemPrompt: func() string {
			return m.currentSystemPrompt()
		},
		GetSystemPromptOptions: m.currentSystemPromptOptions,
		// Interactive mode is always the terminal UI. Mirrors upstream
		// interactive-mode.ts:1511 (`mode: "tui"`).
		Mode: extension.ModeTUI,
	}

	m.newRunner.BindCore(
		extension.ExtensionActions{
			SendUserMessage: func(content any, opts *extension.SendUserMessageOptions) error {
				var deliverAs extension.DeliverAs
				if opts != nil {
					deliverAs = opts.DeliverAs
				}
				return m.deliverUserMessage(content, deliverAs)
			},
		},
		actions,
		nil,
	)
	m.bindExtensionCommandActions()
}

// bindExtensionCommandActions binds the interactive command-context actions
// over the Session's own, for in-process and subprocess extensions.
func (m *InteractiveMode) bindExtensionCommandActions() {
	actions := extension.CommandActions{
		NavigateTree:        m.extensionNavigateTree,
		NavigateTreeContext: m.extensionNavigateTreeContext,
	}
	if m.newRunner != nil {
		m.newRunner.BindCommandActions(actions)
	}
	if m.opts.SubprocessUIBridge != nil {
		m.opts.SubprocessUIBridge.SetHostAction("navigateTree", m.extensionNavigateTreeContext)
	}
}

// extensionNavigateTree is ctx.navigateTree in interactive mode: navigate the
// Session, then redraw the chat, fill an empty editor with the editor text
// and report it (interactive-mode.ts commandContextActions.navigateTree).
func (m *InteractiveMode) extensionNavigateTree(targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
	return m.extensionNavigateTreeContext(context.Background(), targetID, opts)
}

func (m *InteractiveMode) extensionNavigateTreeContext(ctx context.Context, targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
	if m.opts.SessionHandle == nil {
		return extension.CancelledResult{Cancelled: true}, nil
	}
	var summarize bool
	var customInstructions string
	if opts != nil {
		summarize, customInstructions = opts.Summarize, opts.CustomInstructions
	}
	result, err := m.opts.SessionHandle.NavigateTreeHandle(ctx, targetID, summarize, customInstructions)
	if err != nil {
		return extension.CancelledResult{}, err
	}
	if result.Cancelled {
		return extension.CancelledResult{Cancelled: true}, nil
	}
	m.runOnMain(m.runCtx, func() {
		m.rebuildChatFromSession()
		if result.EditorText != "" && strings.TrimSpace(m.editor.Text()) == "" {
			m.editor.SetText(result.EditorText)
		}
		m.showStatus("Navigated to selected point")
		m.tuiInst.Render()
	})
	return extension.CancelledResult{}, nil
}

// wireSubprocessHostCallbacks sets up host-side callbacks so subprocess
// extensions can query session state (active tools, commands, usage, etc.).
// pig-specific: no upstream equivalent: upstream's extension runner has
// direct access to these via closure captures.
// syncWidgets replaces the widgetContainer children with the current set
// of extension widget proxies. Called by the UIBridge whenever a widget is
// set or cleared. PushProxy satisfies tui.Component (structural typing).
func (m *InteractiveMode) syncWidgets(widgets map[string]*subprocess.PushProxy) {
	if m.widgetContainer == nil {
		return
	}
	children := make([]tui.Component, 0, len(widgets))
	for _, proxy := range widgets {
		children = append(children, proxy)
	}
	m.widgetContainer.SetChildren(children...)
	if m.tuiInst != nil {
		m.tuiInst.RequestRender()
	}
}

func (m *InteractiveMode) wireSubprocessHostCallbacks() func() {
	b := m.opts.SubprocessUIBridge
	detachModelRegistry := WireModelOperations(b, ModelOperationBindings{
		CurrentModel: func() *ai.Model { return m.opts.Model }, ModelLookup: m.opts.ModelLookup, ModelCatalog: m.opts.ModelCatalog,
		Registry: m.opts.ModelRegistry, ModelBuilder: m.opts.ModelBuilder, SessionHandle: m.opts.SessionHandle,
		Thinking: ai.ThinkingLevel(m.thinkingLevel), Transport: ai.Transport(m.opts.Settings.Transport),
	})

	b.SetHostAction("getFlag", func(extName, name string) any {
		if m.opts.UnknownFlags != nil {
			if value, ok := m.opts.UnknownFlags[name]; ok {
				return value
			}
		}
		return nil
	})

	b.SetHostAction("getActiveTools", func() []string {
		if m.newRunner == nil {
			return m.activeBuiltinToolNames()
		}
		extTools := m.newRunner.Tools()
		builtinNames := m.activeBuiltinToolNames(extTools...)
		names := make([]string, 0, len(extTools)+len(builtinNames))
		for _, t := range extTools {
			names = append(names, t.Definition.Name)
		}
		names = append(names, builtinNames...)
		return names
	})
	b.SetHostAction("getAllTools", func() []map[string]string {
		if m.newRunner == nil {
			builtinNames := m.activeBuiltinToolNames()
			result := make([]map[string]string, len(builtinNames))
			for i, name := range builtinNames {
				result[i] = map[string]string{
					"name":        name,
					"description": tools.BuiltinToolDescription(name),
					"source":      "builtin",
				}
			}
			return result
		}
		extTools := m.newRunner.Tools()
		builtinNames := m.activeBuiltinToolNames(extTools...)
		result := make([]map[string]string, 0, len(extTools)+len(builtinNames))
		for _, t := range extTools {
			source := "builtin"
			if s, ok := t.SourceInfo.(string); ok && s != "" {
				source = s
			}
			result = append(result, map[string]string{
				"name":        t.Definition.Name,
				"description": t.Definition.Description,
				"source":      source,
			})
		}
		for _, name := range builtinNames {
			result = append(result, map[string]string{
				"name":        name,
				"description": tools.BuiltinToolDescription(name),
				"source":      "builtin",
			})
		}
		return result
	})
	b.SetHostAction("getCommands", func() []map[string]string {
		if m.newRunner == nil {
			return nil
		}
		commands := m.newRunner.Commands()
		result := make([]map[string]string, len(commands))
		for i, c := range commands {
			result[i] = map[string]string{
				"name":        strings.TrimPrefix(c.InvocationName, "/"),
				"description": c.Description,
			}
		}
		return result
	})
	b.SetHostAction("setActiveTools", func(names []string) {
		m.opts.AllowedTools = make(map[string]struct{}, len(names))
		for _, name := range names {
			m.opts.AllowedTools[name] = struct{}{}
		}
		for _, err := range m.refreshAgentTools() {
			fmt.Fprintf(os.Stderr, "tool refresh: %v\n", err)
		}
	})
	b.SetHostAction("refreshTools", func() {
		for _, err := range m.refreshAgentTools() {
			fmt.Fprintf(os.Stderr, "tool refresh: %v\n", err)
		}
	})
	b.SetHostAction("getThinkingLevel", func() string {
		return string(m.agent.ThinkingLevel())
	})
	b.SetHostAction("setThinkingLevel", func(level string) {
		if m.opts.SessionHandle != nil {
			if err := m.opts.SessionHandle.SetThinkingLevel(ai.ThinkingLevel(level)); err != nil {
				m.runOnMain(m.runCtx, func() { m.showError(err.Error()) })
				return
			}
		} else {
			m.agent.SetThinkingLevel(ai.ClampThinkingLevel(m.agent.Model(), ai.ThinkingLevel(level)))
		}
		m.runOnMain(m.runCtx, m.refreshThinkingLevel)
	})
	b.SetHostAction("isIdle", m.extensionIsIdle)
	b.SetHostAction("isProjectTrusted", func() bool { return m.projectTrusted() })
	if m.statusLine != nil {
		b.SetHostAction("getGitBranch", func() string { return m.statusLine.GitBranch() })
		b.SetHostAction("getExtensionStatuses", func() map[string]string { return m.statusLine.GetExtensionStatuses() })
		b.SetHostAction("getAvailableProviderCount", func() int { return m.statusLine.ProviderCount() })
	}
	// Upstream abort() cancels the retry delay, compaction, branch summary
	// and run, then awaits waitForIdle.
	b.SetHostAction("abort", func() {
		if err := m.postToMain(m.runCtx, func() {
			if m.opts.SessionHandle != nil {
				m.opts.SessionHandle.AbortRetry()
				m.opts.SessionHandle.AbortCompaction()
				m.opts.SessionHandle.AbortBranchSummary()
			}
			m.abortRun(m.runCtx)
		}); err != nil {
			return
		}
		_ = m.waitForIdle(m.runCtx)
	})
	b.SetHostAction("hasPendingMessages", func() bool {
		if m.agent == nil {
			return false
		}
		steering, followUps := m.agent.PendingMessages()
		return len(steering)+len(followUps) > 0
	})
	b.SetHostAction("shutdown", func() {
		m.requestShutdown()
	})
	b.SetHostAction("waitForIdle", func(ctx context.Context) error {
		extension.CallInitiated(ctx)
		return m.waitForIdle(m.runCtx)
	})
	b.SetHostAction("reload", func(ctx context.Context) error {
		extension.CallInitiated(ctx)
		sc := m.buildSlashContext(context.Background())
		if sc.Reload == nil {
			return fmt.Errorf("reload not available")
		}
		sc.Reload()
		return nil
	})
	b.SetHostAction("compact", func(ctx context.Context, opts *extension.CompactOptions) {
		extension.CallInitiated(ctx)
		if m.opts.SessionHandle == nil {
			return
		}
		type compactor interface {
			Compact(ctx context.Context, customInstructions string) error
		}
		c, ok := m.opts.SessionHandle.(compactor)
		if !ok {
			return
		}
		customInstructions := ""
		if opts != nil {
			customInstructions = opts.CustomInstructions
		}
		go func() {
			if err := c.Compact(m.abortCtx, customInstructions); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "extension compact: %v\n", err)
			}
		}()
	})
	b.SetHostAction("setModel", func(ctx context.Context, spec string) (bool, error) {
		extension.CallInitiated(ctx)
		if m.opts.ModelBuilder == nil {
			return false, fmt.Errorf("model switching not configured")
		}
		newModel, err := m.opts.ModelBuilder(spec)
		if err != nil {
			return false, err
		}
		if m.opts.SessionHandle != nil {
			if err := m.opts.SessionHandle.SetModel(newModel); err != nil {
				return false, err
			}
		} else if m.agent != nil {
			m.agent.SetModel(newModel)
		}
		prevModel := m.opts.Model
		m.opts.Model = newModel
		if m.statusLine != nil {
			m.statusLine.SetModel(newModel)
		}
		// Host actions run off the owner loop, which owns the editor.
		m.runOnMain(m.runCtx, m.refreshThinkingLevel)
		emitModelSelect(m.newRunner, modelToExtModel(newModel), modelToExtModel(prevModel), extension.ModelSelectSourceUser)
		return true, nil
	})
	b.SetHostAction("getSessionName", func() string {
		if m.currentSession() != nil {
			return m.currentSession().GetSessionName()
		}
		return ""
	})
	b.SetHostAction("getSessionID", func() string {
		if m.currentSession() != nil {
			return m.currentSession().ID()
		}
		return ""
	})
	b.SetHostAction("getSessionFile", func() string {
		if m.currentSession() != nil {
			return m.currentSession().Path()
		}
		return ""
	})
	b.SetHostAction("getLeafID", func() string {
		if m.currentSession() == nil {
			return ""
		}
		if leaf := m.currentSession().LeafID(); leaf != nil {
			return *leaf
		}
		return ""
	})

	// Context usage mirrors upstream AgentSession.getContextUsage: provider
	// usage before the latest compaction is stale. Until an assistant responds
	// after that compaction, return an unknown token count so extensions can
	// fall back to a compaction-aware estimate.
	b.SetHostAction("getContextUsage", func() *extension.ContextUsage {
		ctxWin := 0
		if m.opts.Model != nil {
			ctxWin = m.opts.Model.Capabilities.ContextWindow
		}
		if ctxWin <= 0 {
			return nil
		}
		if m.currentSession() != nil {
			leaf := m.currentSession().LeafID()
			var branch []SessionEntry
			if leaf == nil {
				branch = m.currentSession().Entries()
			} else {
				branch = m.currentSession().Branch(*leaf)
			}
			if latest := latestCompactionIndex(branch); latest >= 0 && !hasAssistantUsageAfter(branch, latest) {
				return &extension.ContextUsage{ContextWindow: ctxWin}
			}
		}
		if m.agent == nil {
			return nil
		}
		msgs := m.agent.Messages()
		// Walk backwards to find last assistant with usage.
		for _, msg := range slices.Backward(msgs) {
			if a := msg.Assistant; a != nil && a.Usage != nil {
				totalTokens := a.Usage.Input + a.Usage.Output +
					a.Usage.CacheRead + a.Usage.CacheWrite
				var pct int
				if ctxWin > 0 {
					pct = int(float64(totalTokens) / float64(ctxWin) * 100)
				}
				return &extension.ContextUsage{
					Tokens:        &totalTokens,
					ContextWindow: ctxWin,
					Percent:       &pct,
				}
			}
		}
		return nil
	})

	// System prompt.
	b.SetHostAction("getSystemPrompt", func() string {
		return m.currentSystemPrompt()
	})
	b.SetHostAction("getSystemPromptOptions", func() extension.BuildSystemPromptOptions {
		return m.currentSystemPromptOptions()
	})

	// Session branch (conversation history).
	b.SetHostAction("getBranch", func() []json.RawMessage {
		if m.currentSession() == nil {
			return nil
		}
		leaf := m.currentSession().LeafID()
		if leaf == nil {
			return sessionEntriesRaw(m.currentSession().Entries())
		}
		return sessionEntriesRaw(m.currentSession().Branch(*leaf))
	})
	// getEntriesPage backs bounded per-extension state pushes. Session entries
	// are append-only, so a cursor beyond the current length means the session
	// changed and the runtime is resent from zero.
	b.SetHostAction("getEntriesPage", func(cursor, maxBytes int) ([]json.RawMessage, int, bool, string) {
		if m.currentSession() == nil {
			return nil, 0, false, ""
		}
		entries := m.currentSession().Entries()
		if cursor < 0 || cursor > len(entries) {
			cursor = 0
		}
		page, next := sessionEntriesRawPage(entries, cursor, maxBytes)
		leafID := ""
		if leaf := m.currentSession().LeafID(); leaf != nil {
			leafID = *leaf
		}
		return page, next, next < len(entries), leafID
	})
	b.SetHostAction("getEntries", func() []json.RawMessage {
		if m.currentSession() == nil {
			return nil
		}
		return sessionEntriesRaw(m.currentSession().Entries())
	})
	b.SetHostAction("exec", func(ctx context.Context, command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
		return extension.ExecCommand(ctx, m.opts.CWD, command, args, opts)
	})
	b.SetHostAction("setLabel", func(entryID, label string) error {
		if m.currentSession() == nil {
			return fmt.Errorf("session not available")
		}
		// An empty label clears it (upstream label undefined).
		var value *string
		if label != "" {
			value = &label
		}
		return m.currentSession().AppendLabelChange(entryID, value)
	})
	b.SetHostAction("setSessionName", func(name string) error {
		if m.currentSession() == nil {
			return fmt.Errorf("session not available")
		}
		id, err := generateEntryID()
		if err != nil {
			return err
		}
		entry := SessionInfoEntry{
			SessionEntryBase: SessionEntryBase{
				Type:      "session_info",
				ID:        id,
				ParentID:  m.currentSession().LeafID(),
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			},
			Name: name,
		}
		if err := m.currentSession().AppendEntry(entry); err != nil {
			return err
		}
		emitSessionInfoChanged(m.newRunner, m.currentSession().GetSessionName())
		if m.statusLine != nil {
			m.statusLine.SetName(name)
		}
		tui.SetTerminalTitle(tui.BuildTerminalTitle(name, m.opts.CWD))
		return nil
	})
	b.SetHostAction("sendMessage", func(msg extension.CustomMessageRef, opts subprocess.SendMessageOptions) error {
		if m.currentSession() == nil {
			return fmt.Errorf("session not available")
		}
		if strings.TrimSpace(msg.CustomType) == "" {
			return fmt.Errorf("customType is required")
		}
		display := true
		if v, ok := msg.Display.(bool); ok {
			display = v
		}
		id, err := generateEntryID()
		if err != nil {
			return err
		}
		entry := CustomMessageEntry{
			SessionEntryBase: SessionEntryBase{
				Type:      "custom_message",
				ID:        id,
				ParentID:  m.currentSession().LeafID(),
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			},
			CustomType: msg.CustomType,
			Content:    msg.Content,
			Display:    display,
			Details:    msg.Details,
		}
		// Mirrors upstream sendCustomMessage (agent-session.ts:1453-1469), which
		// branches on whether a turn is in flight, not on whether an agent
		// exists:
		//
		//	deliverAs "nextTurn" → defer to the next turn
		//	else if streaming    → followUp / steer on the running turn
		//	else if triggerTurn  → start a turn from this message
		//	else                 → persist and render, start nothing
		//
		// Conflating "an agent exists" with "a turn is running" stranded a
		// triggerTurn message whenever the agent was idle: it went to a queue
		// that only drains inside a running turn, and the persistence branch was
		// skipped because it counted as queued, so the message was invisible in
		// the UI and absent from the session until some later turn happened to
		// drain it.
		//
		// The entry is written here only on the paths that do not hand the
		// message to the agent. When the agent takes it, the agent persists it
		// via OnMessagePersist at the point of delivery, so it cannot land
		// between an assistant's tool_use and its tool_result and invalidate
		// session replay.
		route := routeCustomMessage(m.agent != nil, m.agent != nil && m.runStreaming(), opts.TriggerTurn != nil && *opts.TriggerTurn, opts.DeliverAs)
		startTurn := route == customMessageStartTurn
		queued := route == customMessageQueue
		if !queued && !startTurn {
			if err := m.currentSession().AppendEntry(entry); err != nil {
				return err
			}
		}
		if display {
			m.runOnMain(m.runCtx, func() {
				m.appendCustomMessage(entry)
				m.tuiInst.Render()
			})
		}
		if !queued && !startTurn {
			return nil
		}
		agentMsg := agent.AgentMessage{Custom: map[string]any{
			"role":       agent.RoleCustom,
			"customType": msg.CustomType,
			"content":    msg.Content,
			"display":    display,
			"timestamp":  time.Now().UnixMilli(),
		}}
		if msg.Details != nil {
			agentMsg.Custom["details"] = msg.Details
		}
		deliverAs := opts.DeliverAs
		if deliverAs == "" {
			deliverAs = "followUp"
		}
		switch deliverAs {
		case "followUp", "nextTurn", "steer":
		default:
			return fmt.Errorf("unsupported deliverAs %q", opts.DeliverAs)
		}
		startCustomTurn := func() error {
			// Starting another run from agent_settled waits until every settled
			// handler returns, matching upstream's deferred settled actions.
			start := func() {
				// Starting a turn mutates TUI and session state, so it runs on
				// the owner loop. It re-checks there: a turn may have begun
				// since.
				_ = m.postToMain(m.runCtx, func() {
					if m.agent == nil {
						return
					}
					if m.enqueueIfTurnActive(func() { m.agent.FollowUp(agentMsg) }) {
						m.updatePendingMessagesDisplay()
						return
					}
					m.runCustomMessageTurn(m.runCtx, agentMsg)
				})
			}
			if !m.deferSettledAction(start) {
				start()
			}
			return nil
		}
		if startTurn {
			return startCustomTurn()
		}
		if deliverAs == "nextTurn" {
			m.agent.QueueNextTurn(agentMsg)
			return nil
		}
		if !m.enqueueIfTurnActive(func() {
			if deliverAs == "steer" {
				m.agent.Steer(agentMsg)
			} else {
				m.agent.FollowUp(agentMsg)
			}
		}) {
			// The run settled after routing: take the idle branch instead of
			// queueing into a run that will never drain it.
			if opts.TriggerTurn != nil && *opts.TriggerTurn {
				return startCustomTurn()
			}
			if err := m.currentSession().AppendEntry(entry); err != nil {
				return err
			}
			return nil
		}
		return m.postToMain(m.runCtx, m.updatePendingMessagesDisplay)
	})
	b.SetHostAction("sendUserMessage", func(content any, opts subprocess.SendUserMessageOptions) error {
		deliver := func() { _ = m.deliverUserMessage(content, extension.DeliverAs(opts.DeliverAs)) }
		if m.deferSettledAction(deliver) {
			return nil
		}
		return m.deliverUserMessage(content, extension.DeliverAs(opts.DeliverAs))
	})
	b.SetHostAction("appendEntry", func(customType string, data any) error {
		if m.currentSession() == nil {
			return fmt.Errorf("session not available")
		}
		id, err := generateEntryID()
		if err != nil {
			return err
		}
		entry := CustomEntry{
			SessionEntryBase: SessionEntryBase{
				Type:      "custom",
				ID:        id,
				ParentID:  m.currentSession().LeafID(),
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			},
			CustomType: customType,
			Data:       data,
		}
		if err := m.currentSession().AppendEntry(entry); err != nil {
			return err
		}
		// Live path (upstream interactive-mode.ts "entry_appended"): render the
		// custom entry immediately. The host action runs off the UI loop, so the
		// chat mutation + render marshal back through runOnMain, matching
		// sendMessage's live-display path.
		m.runOnMain(m.runCtx, func() {
			m.addCustomEntryToChat(entry)
			m.tuiInst.Render()
		})
		return nil
	})
	return detachModelRegistry
}

func latestCompactionIndex(entries []SessionEntry) int {
	for i, entrie := range slices.Backward(entries) {
		if entrie.Base.Type == "compaction" {
			return i
		}
	}
	return -1
}

func hasAssistantUsageAfter(entries []SessionEntry, index int) bool {
	for i := len(entries) - 1; i > index; i-- {
		msg, ok := entries[i].AsMessage()
		if !ok || msg.Message.Assistant == nil {
			continue
		}
		assistant := msg.Message.Assistant
		if assistant.StopReason == "aborted" || assistant.StopReason == "error" {
			return false
		}
		usage := assistant.Usage
		if usage == nil {
			return false
		}
		return usage.TotalTokens > 0 || usage.Input+usage.Output+usage.CacheRead+usage.CacheWrite > 0
	}
	return false
}

func sessionEntriesRawPage(entries []SessionEntry, cursor, maxBytes int) ([]json.RawMessage, int) {
	if cursor < 0 || cursor > len(entries) {
		cursor = 0
	}
	if cursor == len(entries) {
		return nil, cursor
	}
	out := make([]json.RawMessage, 0)
	bytes := 0
	next := cursor
	for next < len(entries) {
		raw := entries[next].Raw()
		entryBytes := len(raw) + 1
		if len(out) > 0 && bytes+entryBytes > maxBytes {
			break
		}
		if len(raw) > 0 {
			out = append(out, json.RawMessage(raw))
			bytes += entryBytes
		}
		next++
	}
	return out, next
}

// sessionEntriesRaw passes session entries through as the JSON they already
// are. Decoding each entry into a map only to re-encode it moments later cost
// 159ms on a 7,891-entry session, per push, per extension.
func sessionEntriesRaw(entries []SessionEntry) []json.RawMessage {
	if len(entries) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		if raw := entry.Raw(); len(raw) > 0 {
			out = append(out, json.RawMessage(raw))
		}
	}
	return out
}

func subprocessModelSpec(model map[string]any) string {
	providerID := ""
	switch p := model["provider"].(type) {
	case string:
		providerID = p
	case map[string]any:
		providerID, _ = p["id"].(string)
	}
	modelID, _ := model["modelId"].(string)
	if modelID == "" {
		if id, _ := model["id"].(string); id != "" {
			if before, after, ok := strings.Cut(id, "/"); ok {
				if providerID == "" {
					providerID = before
				}
				modelID = after
			} else {
				modelID = id
			}
		}
	}
	if providerID == "" {
		return modelID
	}
	return providerID + "/" + modelID
}

func subprocessRequiredString(object map[string]any, key, path string) (string, error) {
	value, exists := object[key]
	if !exists {
		return "", fmt.Errorf("%s.%s is required", path, key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s.%s must be a string", path, key)
	}
	return text, nil
}

func subprocessOptionalString(object map[string]any, key, path string) (string, error) {
	value, exists := object[key]
	if !exists {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s.%s must be a string", path, key)
	}
	return text, nil
}

func subprocessOptionalInt64(object map[string]any, key, path string) (int64, error) {
	value, exists := object[key]
	if !exists {
		return 0, nil
	}
	switch number := value.(type) {
	case float64:
		if number != float64(int64(number)) {
			return 0, fmt.Errorf("%s.%s must be an integer", path, key)
		}
		return int64(number), nil
	case int:
		return int64(number), nil
	case int64:
		return number, nil
	default:
		return 0, fmt.Errorf("%s.%s must be an integer", path, key)
	}
}

func subprocessDecodeJSON(value any, target any, path string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func subprocessRequestMessages(raw any) ([]ai.Message, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("model request messages must be an array")
	}
	messages := make([]ai.Message, 0, len(items))
	for index, item := range items {
		message, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("model request messages[%d] must be an object", index)
		}
		role, err := subprocessRequiredString(message, "role", fmt.Sprintf("model request messages[%d]", index))
		if err != nil {
			return nil, err
		}
		path := fmt.Sprintf("model request messages[%d]", index)
		switch role {
		case "system":
			var content ai.SystemContent
			switch rawContent := message["content"].(type) {
			case string:
				content = ai.SystemText(rawContent)
			case []any:
				blocks := make(ai.SystemTextBlocks, 0, len(rawContent))
				for blockIndex, rawBlock := range rawContent {
					block, ok := rawBlock.(map[string]any)
					if !ok || block["type"] != "text" {
						return nil, fmt.Errorf("%s.content[%d] must be a text block", path, blockIndex)
					}
					text, err := subprocessRequiredString(block, "text", fmt.Sprintf("%s.content[%d]", path, blockIndex))
					if err != nil {
						return nil, err
					}
					textSignature, err := subprocessOptionalString(block, "textSignature", fmt.Sprintf("%s.content[%d]", path, blockIndex))
					if err != nil {
						return nil, err
					}
					blocks = append(blocks, ai.TextContent{Text: text, TextSignature: textSignature})
				}
				content = blocks
			default:
				return nil, fmt.Errorf("%s.content must be a string or text-block array", path)
			}
			var sections ai.OrderedSections
			if rawSections, exists := message["sections"]; exists {
				switch rawSections := rawSections.(type) {
				case ai.OrderedSections:
					sections = rawSections
				default:
					if err := subprocessDecodeJSON(rawSections, &sections, path+".sections"); err != nil {
						return nil, err
					}
				}
			}
			var toolsAdded []ai.ToolSchema
			if rawTools, exists := message["toolsAdded"]; exists {
				if err := subprocessDecodeJSON(rawTools, &toolsAdded, path+".toolsAdded"); err != nil {
					return nil, err
				}
			}
			var toolsRemoved []ai.ToolReference
			if rawTools, exists := message["toolsRemoved"]; exists {
				if err := subprocessDecodeJSON(rawTools, &toolsRemoved, path+".toolsRemoved"); err != nil {
					return nil, err
				}
			}
			timestamp, err := subprocessOptionalInt64(message, "timestamp", path)
			if err != nil {
				return nil, err
			}
			messages = append(messages, ai.SystemMessage{Content: content, Sections: sections, ToolsAdded: toolsAdded, ToolsRemoved: toolsRemoved, Timestamp: timestamp})
		case "user":
			content, err := subprocessUserContent(message["content"])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			timestamp, err := subprocessOptionalInt64(message, "timestamp", path)
			if err != nil {
				return nil, err
			}
			messages = append(messages, ai.UserMessage{Content: content, Timestamp: timestamp})
		case "assistant":
			content, err := subprocessAssistantContent(message["content"])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			var fields struct {
				API                   ai.API                          `json:"api"`
				Provider              string                          `json:"provider"`
				Model                 string                          `json:"model"`
				ResponseModel         string                          `json:"responseModel"`
				ResponseID            string                          `json:"responseId"`
				ProviderThinkingLevel string                          `json:"providerThinkingLevel"`
				Diagnostics           []ai.AssistantMessageDiagnostic `json:"diagnostics"`
				Usage                 ai.Usage                        `json:"usage"`
				StopReason            ai.StopReason                   `json:"stopReason"`
				Deferred              *ai.DeferredHandle              `json:"deferred"`
				ErrorMessage          string                          `json:"errorMessage"`
				RawStopReason         string                          `json:"rawStopReason"`
				EndTurn               *bool                           `json:"endTurn"`
				Timestamp             int64                           `json:"timestamp"`
			}
			if err := subprocessDecodeJSON(message, &fields, path); err != nil {
				return nil, err
			}
			messages = append(messages, ai.AssistantMessage{Content: content, API: fields.API, Provider: fields.Provider, Model: fields.Model, ResponseModel: fields.ResponseModel, ResponseID: fields.ResponseID, ProviderThinkingLevel: fields.ProviderThinkingLevel, Diagnostics: fields.Diagnostics, Usage: fields.Usage, StopReason: fields.StopReason, Deferred: fields.Deferred, ErrorMessage: fields.ErrorMessage, RawStopReason: fields.RawStopReason, EndTurn: fields.EndTurn, Timestamp: fields.Timestamp})
		case "toolResult", "tool":
			content, err := subprocessToolResultContent(message["content"])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			var fields struct {
				ToolCallID string    `json:"toolCallId"`
				ToolName   string    `json:"toolName"`
				Details    any       `json:"details"`
				Usage      *ai.Usage `json:"usage"`
				IsError    bool      `json:"isError"`
				Timestamp  int64     `json:"timestamp"`
			}
			if err := subprocessDecodeJSON(message, &fields, path); err != nil {
				return nil, err
			}
			if fields.ToolCallID == "" {
				return nil, fmt.Errorf("%s.toolCallId is required", path)
			}
			messages = append(messages, ai.ToolResultMessage{ToolCallID: fields.ToolCallID, ToolName: fields.ToolName, Content: content, Details: fields.Details, Usage: fields.Usage, IsError: fields.IsError, Timestamp: fields.Timestamp})
		default:
			return nil, fmt.Errorf("%s has unsupported role %q", path, role)
		}
	}
	return messages, nil
}

func subprocessUserContent(raw any) (ai.UserContent, error) {
	switch typed := raw.(type) {
	case ai.UserText:
		return typed, nil
	case ai.UserContentBlocks:
		return slices.Clone(typed), nil
	case []ai.UserContentBlock:
		return ai.UserContentBlocks(slices.Clone(typed)), nil
	}
	if text, ok := raw.(string); ok {
		return ai.UserText(text), nil
	}
	blocks, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("user content must be a string or array")
	}
	content := make(ai.UserContentBlocks, 0, len(blocks))
	for index, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("user content[%d] must be an object", index)
		}
		blockType, err := subprocessRequiredString(block, "type", fmt.Sprintf("user content[%d]", index))
		if err != nil {
			return nil, err
		}
		switch blockType {
		case "text":
			text, err := subprocessRequiredString(block, "text", fmt.Sprintf("user content[%d]", index))
			if err != nil {
				return nil, err
			}
			signature, err := subprocessOptionalString(block, "textSignature", fmt.Sprintf("user content[%d]", index))
			if err != nil {
				return nil, err
			}
			content = append(content, ai.TextContent{Text: text, TextSignature: signature})
		case "image":
			data, err := subprocessRequiredString(block, "data", fmt.Sprintf("user content[%d]", index))
			if err != nil {
				return nil, err
			}
			mimeType, err := subprocessOptionalString(block, "mimeType", fmt.Sprintf("user content[%d]", index))
			if err != nil {
				return nil, err
			}
			if mimeType == "" {
				mimeType, err = subprocessRequiredString(block, "media_type", fmt.Sprintf("user content[%d]", index))
				if err != nil {
					return nil, err
				}
			}
			content = append(content, ai.ImageContent{Data: data, MimeType: mimeType})
		default:
			return nil, fmt.Errorf("unsupported user content type %q", blockType)
		}
	}
	return content, nil
}

func subprocessAssistantContent(raw any) ([]ai.AssistantContentBlock, error) {
	blocks, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("assistant content must be an array")
	}
	content := make([]ai.AssistantContentBlock, 0, len(blocks))
	for index, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("assistant content[%d] must be an object", index)
		}
		path := fmt.Sprintf("assistant content[%d]", index)
		blockType, err := subprocessRequiredString(block, "type", path)
		if err != nil {
			return nil, err
		}
		switch blockType {
		case "text":
			text, err := subprocessRequiredString(block, "text", path)
			if err != nil {
				return nil, err
			}
			signature, err := subprocessOptionalString(block, "textSignature", path)
			if err != nil {
				return nil, err
			}
			content = append(content, ai.TextContent{Text: text, TextSignature: signature})
		case "thinking":
			thinking, err := subprocessRequiredString(block, "thinking", path)
			if err != nil {
				return nil, err
			}
			signature, err := subprocessOptionalString(block, "thinkingSignature", path)
			if err != nil {
				return nil, err
			}
			redacted := false
			if raw, exists := block["redacted"]; exists {
				var ok bool
				redacted, ok = raw.(bool)
				if !ok {
					return nil, fmt.Errorf("%s.redacted must be a boolean", path)
				}
			}
			content = append(content, ai.ThinkingContent{Thinking: thinking, ThinkingSignature: signature, Redacted: redacted})
		case "toolCall":
			id, err := subprocessRequiredString(block, "id", path)
			if err != nil || id == "" {
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("%s.id is required", path)
			}
			name, err := subprocessRequiredString(block, "name", path)
			if err != nil {
				return nil, err
			}
			arguments, ok := block["arguments"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s.arguments must be an object", path)
			}
			thoughtSignature, err := subprocessOptionalString(block, "thoughtSignature", path)
			if err != nil {
				return nil, err
			}
			namespace, err := subprocessOptionalString(block, "namespace", path)
			if err != nil {
				return nil, err
			}
			content = append(content, ai.ToolCall{ID: id, Name: name, Arguments: ai.JsonObject(arguments), ThoughtSignature: thoughtSignature, Namespace: namespace})
		default:
			return nil, fmt.Errorf("unsupported assistant content type %q", blockType)
		}
	}
	return content, nil
}

func subprocessToolResultContent(raw any) ([]ai.ToolResultMessageContent, error) {
	if text, ok := raw.(string); ok {
		return []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, nil
	}
	userContent, err := subprocessUserContent(raw)
	if err != nil {
		return nil, err
	}
	blocks := userContent.(ai.UserContentBlocks)
	content := make([]ai.ToolResultMessageContent, len(blocks))
	for i, block := range blocks {
		content[i] = block.(ai.ToolResultMessageContent)
	}
	return content, nil
}

func subprocessOptionalFloat64(object map[string]any, key, path string) (float64, bool, error) {
	value, exists := object[key]
	if !exists {
		return 0, false, nil
	}
	switch number := value.(type) {
	case float64:
		return number, true, nil
	case int:
		return float64(number), true, nil
	default:
		return 0, true, fmt.Errorf("%s.%s must be a number", path, key)
	}
}

func subprocessStringMap(raw any, path string) (map[string]string, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	values := make(map[string]string, len(object))
	for key, rawValue := range object {
		value, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be a string", path, key)
		}
		values[key] = value
	}
	return values, nil
}

func subprocessProviderHeaders(raw any, path string) (ai.ProviderHeaders, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	values := make(ai.ProviderHeaders, len(object))
	for key, rawValue := range object {
		if rawValue == nil {
			values[key] = nil
			continue
		}
		value, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s must be a string or null", path, key)
		}
		values[key] = new(value)
	}
	return values, nil
}

func subprocessStreamOptions(request map[string]any, defaults ai.StreamOptions) (ai.StreamOptions, error) {
	options := defaults
	if value, exists := request["maxTokens"]; exists {
		number, err := subprocessOptionalInt64(request, "maxTokens", "model request")
		if err != nil {
			return ai.StreamOptions{}, err
		}
		if number < 0 {
			return ai.StreamOptions{}, fmt.Errorf("model request.maxTokens must be non-negative")
		}
		options.MaxTokens = int(number)
		_ = value
	}
	if value, exists, err := subprocessOptionalFloat64(request, "temperature", "model request"); err != nil {
		return ai.StreamOptions{}, err
	} else if exists {
		options.Temperature = value
		options.TemperatureSet = true
	}
	if raw, exists := request["samplingParams"]; exists {
		object, ok := raw.(map[string]any)
		if !ok {
			return ai.StreamOptions{}, fmt.Errorf("model request.samplingParams must be an object")
		}
		options.SamplingParams = object
	}
	if raw, exists := request["thinkingBudgets"]; exists {
		object, ok := raw.(map[string]any)
		if !ok {
			return ai.StreamOptions{}, fmt.Errorf("model request.thinkingBudgets must be an object")
		}
		budgets := &ai.ThinkingBudgets{}
		for key, target := range map[string]*int{"minimal": &budgets.Minimal, "low": &budgets.Low, "medium": &budgets.Medium, "high": &budgets.High} {
			if _, exists := object[key]; !exists {
				continue
			}
			value, err := subprocessOptionalInt64(object, key, "model request.thinkingBudgets")
			if err != nil {
				return ai.StreamOptions{}, err
			}
			*target = int(value)
		}
		options.ThinkingBudgets = budgets
	}
	if value, exists := request["thinking"]; exists {
		text, ok := value.(string)
		if !ok {
			return ai.StreamOptions{}, fmt.Errorf("model request.thinking must be a string")
		}
		options.Thinking = ai.ThinkingLevel(text)
	}
	if value, exists := request["isReasoning"]; exists {
		boolean, ok := value.(bool)
		if !ok {
			return ai.StreamOptions{}, fmt.Errorf("model request.isReasoning must be a boolean")
		}
		options.IsReasoning = boolean
	}
	if raw, exists := request["env"]; exists {
		values, err := subprocessStringMap(raw, "model request.env")
		if err != nil {
			return ai.StreamOptions{}, err
		}
		options.Env = ai.ProviderEnv(values)
	}
	if raw, exists := request["headers"]; exists {
		values, err := subprocessProviderHeaders(raw, "model request.headers")
		if err != nil {
			return ai.StreamOptions{}, err
		}
		options.Headers = values
	}
	if value, exists := request["sessionId"]; exists {
		text, ok := value.(string)
		if !ok {
			return ai.StreamOptions{}, fmt.Errorf("model request.sessionId must be a string")
		}
		options.SessionID = text
	}
	if value, exists := request["transport"]; exists {
		text, ok := value.(string)
		if !ok {
			return ai.StreamOptions{}, fmt.Errorf("model request.transport must be a string")
		}
		options.Transport = ai.Transport(text)
	}
	return options, nil
}

type ModelOperationBridge interface {
	SetHostAction(key string, fn any)
	PublishModelCatalog()
}

type ModelOperationBindings struct {
	CurrentModel  func() *ai.Model
	ModelLookup   func(providerID, modelID string) *ai.Model
	ModelCatalog  func() []*ai.Model
	Registry      *ModelRegistry
	ModelBuilder  func(spec string) (*ai.Model, error)
	SessionHandle InteractiveSessionHandle
	Thinking      ai.ThinkingLevel
	Transport     ai.Transport
}

// WireModelOperations installs one Session-owned extension model surface and returns its catalog-listener detach function.
func WireModelOperations(bridge ModelOperationBridge, bindings ModelOperationBindings) func() {
	if bridge == nil {
		return func() {}
	}
	bridge.SetHostAction("getModelInfo", func() map[string]any {
		if bindings.CurrentModel == nil {
			return nil
		}
		return extension.ModelInfo(bindings.CurrentModel())
	})
	bridge.SetHostAction("getModel", func(providerID, modelID string) map[string]any {
		if bindings.ModelLookup == nil {
			return nil
		}
		return extension.ModelInfo(bindings.ModelLookup(providerID, modelID))
	})
	bridge.SetHostAction("getModels", func() []map[string]any {
		if bindings.ModelCatalog == nil {
			return nil
		}
		models := bindings.ModelCatalog()
		result := make([]map[string]any, 0, len(models))
		for _, model := range models {
			if info := extension.ModelInfo(model); info != nil {
				result = append(result, info)
			}
		}
		return result
	})
	bridge.SetHostAction("getModelAuth", func(ctx context.Context, providerID, modelID string) map[string]any {
		extension.CallInitiated(ctx)
		if bindings.Registry == nil {
			return map[string]any{"ok": false, "error": "model registry not available"}
		}
		entry, ok := bindings.Registry.Resolve(providerID, modelID)
		if !ok {
			return map[string]any{"ok": false, "error": "model not found"}
		}
		result := map[string]any{"ok": true, "apiKey": entry.APIKey, "headers": entry.Headers, "baseURL": entry.BaseURL, "env": entry.Env}
		if entry.APIKey == "" && len(entry.Headers) == 0 && !bindings.Registry.HasConfiguredAuth(providerID) {
			result["ok"] = false
			result["error"] = "auth not configured"
		}
		return result
	})
	stream := func(ctx context.Context, model map[string]any, request map[string]any) (*ai.AssistantMessageEventStream, error) {
		return streamModelForSubprocess(ctx, model, request, bindings)
	}
	bridge.SetHostAction("streamModel", stream)
	bridge.SetHostAction("complete", func(ctx context.Context, model map[string]any, request map[string]any, _ map[string]any) (map[string]any, error) {
		stream, err := stream(ctx, model, request)
		if err != nil {
			return nil, err
		}
		extension.CallInitiated(ctx)
		encoded, err := json.Marshal(stream.Result())
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, err
		}
		return result, nil
	})
	if bindings.Registry == nil {
		return func() {}
	}
	detach := bindings.Registry.SetChangeListener(bridge.PublishModelCatalog)
	bridge.PublishModelCatalog()
	return detach
}

func streamModelForSubprocess(ctx context.Context, model map[string]any, request map[string]any, bindings ModelOperationBindings) (*ai.AssistantMessageEventStream, error) {
	if bindings.ModelBuilder == nil {
		return nil, fmt.Errorf("model builder not available")
	}
	if bindings.SessionHandle == nil {
		return nil, fmt.Errorf("session model runtime not available")
	}
	llmModel, err := bindings.ModelBuilder(subprocessModelSpec(model))
	if err != nil {
		return nil, err
	}
	messages, err := subprocessRequestMessages(request["messages"])
	if err != nil {
		return nil, err
	}
	systemPrompt, err := subprocessOptionalString(request, "systemPrompt", "model request")
	if err != nil {
		return nil, err
	}
	var tools []ai.ToolSchema
	if rawTools, exists := request["tools"]; exists {
		if err := subprocessDecodeJSON(rawTools, &tools, "model request.tools"); err != nil {
			return nil, err
		}
	}
	options, err := subprocessStreamOptions(request, ai.StreamOptions{Thinking: bindings.Thinking, IsReasoning: llmModel.Capabilities.MaxThinking != "", Transport: bindings.Transport})
	if err != nil {
		return nil, err
	}
	return bindings.SessionHandle.StreamModel(ctx, llmModel, ai.Context{SystemPrompt: systemPrompt, Messages: messages, Tools: tools}, options), nil
}

func (m *InteractiveMode) streamForSubprocess(ctx context.Context, model map[string]any, request map[string]any) (*ai.AssistantMessageEventStream, error) {
	return streamModelForSubprocess(ctx, model, request, ModelOperationBindings{
		ModelBuilder: m.opts.ModelBuilder, SessionHandle: m.opts.SessionHandle,
		Thinking: ai.ThinkingLevel(m.thinkingLevel), Transport: ai.Transport(m.opts.Settings.Transport),
	})
}
