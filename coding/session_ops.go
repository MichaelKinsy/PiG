package coding

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ─── Session-management methods ──────────────────────────────────────────────
//
// These mirror the slash commands /fork, /clone, /tree, /name, /resume,
// /new exposed to the user, but as direct Go API calls so SDK consumers
// can drive sessions without going through the slash layer. They also
// underpin DispatchSlash's session-related callbacks below.

// Fork moves the session leaf to entryID. The next Send creates a new
// branch off that point. The agent's in-memory message history is
// rebuilt from the new path-to-leaf so the LLM sees only the new
// branch on the next turn.
//
// Returns an error if entryID is unknown.
func (s *Session) Fork(entryID string) error {
	if err := s.inner.Fork(entryID); err != nil {
		return fmt.Errorf("coding: fork %s: %w", entryID, err)
	}
	s.refreshContext()
	return nil
}

// ForkToNewSession forks the given user message into a NEW session file
// (branched at the message's parent) and switches this session to it,
// rebuilding agent history from the new file. Mirrors upstream /fork
// (agent-session-runtime.ts fork(), position "before"). The selected
// message text is discarded here (headless has no editor to prefill);
// interactive callers use SessionManager.ForkToNewSession directly.
func (s *Session) ForkToNewSession(userMsgEntryID string) error {
	_, err := s.ForkToNewSessionWithText(userMsgEntryID)
	return err
}

// ForkToNewSessionWithText forks before a user message, switches this Session
// to the new file, and returns the selected message text.
func (s *Session) ForkToNewSessionWithText(userMsgEntryID string) (string, error) {
	sm := newSessionManagerForDir(s.services.CWD(), s.sessionDir)
	newSess, selectedText, err := sm.ForkToNewSession(s.inner, userMsgEntryID)
	if err != nil {
		return "", fmt.Errorf("coding: fork %s: %w", userMsgEntryID, err)
	}
	s.ReplaceInner(newSess)
	return selectedText, nil
}

// NewSession replaces the active Session with a fresh Session.
func (s *Session) NewSession(parentSession string) error {
	id, err := icodingagent.GenerateSessionID()
	if err != nil {
		return fmt.Errorf("coding: new session id: %w", err)
	}
	var next *icodingagent.Session
	if s.noSession {
		next = icodingagent.NewSession(id, s.services.CWD())
	} else {
		next, err = newSessionManagerForDir(s.services.CWD(), s.sessionDir).Create(id, parentSession)
		if err != nil {
			return fmt.Errorf("coding: new session: %w", err)
		}
	}
	if model := s.Model(); model != nil {
		if err := next.AppendModelSwitch(providerID(model), model.ID, model.DisplayName); err != nil {
			return fmt.Errorf("coding: new session model: %w", err)
		}
	}
	if err := next.AppendThinkingLevelChange(string(s.ThinkingLevel())); err != nil {
		return fmt.Errorf("coding: new session thinking level: %w", err)
	}
	s.ReplaceInner(next)
	return nil
}

// SwitchSession loads a Session file and makes it active.
func (s *Session) SwitchSession(path string) error {
	next, err := newSessionManagerForDir(s.services.CWD(), s.sessionDir).Load(path)
	if err != nil {
		return fmt.Errorf("coding: switch session: %w", err)
	}
	s.ReplaceInner(next)
	return nil
}

// CloneInPlace duplicates the active branch and switches to the clone.
func (s *Session) CloneInPlace() error {
	cloned, err := s.Clone()
	if err != nil {
		return err
	}
	next := cloned.Inner()
	if err := cloned.Close(); err != nil {
		return err
	}
	s.ReplaceInner(next)
	return nil
}

// Clone snapshots the current path-to-leaf as a new session JSONL on
// disk. Returns the new Session; the caller should Close the new
// session when done. The original session is unchanged.
//
// Per row 3.1 contract: clone writes a separate file with parentSession
// metadata pointing at the source. Linear path only: orphan branches
// are dropped.
func (s *Session) Clone() (*Session, error) {
	leaf := s.inner.LeafID()
	if leaf == nil {
		return nil, fmt.Errorf("coding: clone: session is empty")
	}
	sm := newSessionManagerForDir(s.services.CWD(), s.sessionDir)
	cloned, err := sm.Clone(s.inner, *leaf)
	if err != nil {
		return nil, fmt.Errorf("coding: clone: %w", err)
	}
	// The clone is built through NewSession, so it has the same extension and
	// caller hooks, thinking budgets, model runtime and event buffer as any
	// other Session; its agent state is the cloned path-to-leaf.
	opts := SessionOptions{
		Model:            s.Model(),
		Tools:            s.tools,
		SkipBuiltinTools: true,
		BeforeToolCall:   s.callerHooks.beforeToolCall,
		AfterToolCall:    s.callerHooks.afterToolCall,
		Runner:           s.currentRunner(),
		SessionDir:       s.sessionDir,
		NoSession:        s.noSession,
		EventBufferSize:  s.callerHooks.eventBufferSize,
		Transport:        s.callerHooks.transport,
		existing:         cloned,
	}
	if s.structuredSystemPrompt {
		opts.SystemPromptSections = cloneSystemSections(s.baseSystemSections)
	} else {
		opts.SystemPrompt = s.baseSystemPrompt
	}
	cloneSession, err := NewSession(s.services, opts)
	if err != nil {
		return nil, fmt.Errorf("coding: clone: %w", err)
	}
	cloneSession.agent.SetThinkingLevel(s.agent.ThinkingLevel())
	cloneSession.agent.SetSteeringMode(s.agent.SteeringMode())
	cloneSession.agent.SetFollowUpMode(s.agent.FollowUpMode())
	cloneSession.SetActiveToolsByName(s.ActiveToolNames())
	return cloneSession, nil
}

// Entries returns all session entries in append order. The returned slice is a
// defensive copy; mutating it does not affect the session.
func (s *Session) Entries() []icodingagent.SessionEntry {
	return s.inner.Entries()
}

// Tree returns the session's branch tree as a SessionTreeNode. The
// returned structure is a defensive copy; mutating it does not affect
// the session's state. The node uses the internal codingagent tree type.
func (s *Session) Tree() *icodingagent.SessionTreeNode {
	return s.inner.Tree()
}

// LeafID returns the current leaf entry id, or nil if the session is
// empty (no entries yet).
func (s *Session) LeafID() *string {
	return s.inner.LeafID()
}

// SetName persists a session name as a SessionInfoEntry on disk.
// The name surfaces in ListSessions / Runtime.ListSessions output and
// in the resume picker UI. Empty name removes the most recent name
// (silent no-op on filesystems where the entry doesn't exist).
func (s *Session) SetName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("coding: SetName: name is empty")
	}
	return s.SetSessionName(name)
}

// ListSessions returns the SessionInfo summaries for every JSONL in
// this session's working directory. Newest mtime first; corrupted
// files silently skipped.
func (s *Session) ListSessions() ([]SessionInfo, error) {
	sm := icodingagent.NewSessionManager(s.services.CWD())
	return sm.ListSessions()
}

// SessionInfo is the picker-display summary for a session JSONL.
// Re-exported alias from internal/codingagent.
type SessionInfo = icodingagent.SessionInfo

// SessionTreeNode is the branched tree of session entries. Re-exported
// alias from internal/codingagent.
type SessionTreeNode = icodingagent.SessionTreeNode

// ─── Slash dispatch ──────────────────────────────────────────────────────────

// DispatchSlash runs a slash-command line through the session's
// registry and returns each line of output the handler emitted, plus
// any error.
//
// Unlike the TUI dispatch path, DispatchSlash does NOT render to a
// chat scrollback; every Append call is captured into the returned
// []string. Slash commands that depend on TUI overlays (the
// interactive picker variants of /resume, /fork, /tree) automatically
// fall back to their text path because no PickSession /
// PickUserMessage / PickTreeEntry callbacks are wired in headless
// mode.
//
// The same registry that powers the interactive TUI is used here, so
// whatever extension-registered slash commands the session knows
// about are reachable from SDK consumers too (provided their handlers
// are headless-safe).
//
// Behavioural notes:
//
//   - /quit, /clear are no-ops in headless mode (they emit a single
//     line acknowledging the call) since the SDK consumer controls the
//     scrollback, not the session.
//   - /reset clears the agent's in-memory messages (does NOT delete
//     the on-disk JSONL).
//   - Unknown commands return an error rather than silently no-op.
func (s *Session) DispatchSlash(line string) ([]string, error) {
	var output []string
	sc := &SlashContextHandle{
		Args: "",
		Append: func(msg string) {
			output = append(output, msg)
		},
		AppendText: func(msg string) {
			output = append(output, msg)
		},
		Clear: func() {
			// Headless: chat-clear is a no-op; record an acknowledgement.
			output = append(output, "(headless: clear is a no-op)")
		},
		Quit: func() {
			// Headless: caller controls lifecycle; record but do nothing.
			output = append(output, "(headless: quit is a no-op; call Session.Close)")
		},
		Reset: func() {
			restoreAgentMessages(s.agent, nil)
		},
		ModelName: func() string {
			model := s.Model()
			if model == nil {
				return ""
			}
			return model.DisplayName
		},
		ToolNames: func() []string {
			names := make([]string, 0, len(s.tools))
			for _, t := range s.tools {
				names = append(names, t.Name())
			}
			return names
		},
		SkillNames: func() []string { return nil },
		SessionInfo: func() (string, string, int) {
			return s.inner.ID(), s.inner.CWD(), len(s.agent.Messages())
		},
		LastAssistant: func() string { return s.lastAssistantText() },
		CopyClipboard: func(_ string) error { return fmt.Errorf("clipboard not available in headless mode") },
		CostSummary:   func() string { return "" },

		// Session-management callbacks (text-path versions; no overlays).
		CurrentSession: func() *icodingagent.Session { return s.inner },
		SetSessionName: func(name string) error { return s.SetName(name) },
		ForkAtEntry:    func(entryID string) error { return s.Fork(entryID) },
		ForkToNewSession: func(entryID string) error {
			return s.ForkToNewSession(entryID)
		},
		CloneCurrent: func() (string, error) {
			cloned, err := s.Clone()
			if err != nil {
				return "", err
			}
			// Don't keep the new session alive in headless mode; just
			// return its path. Caller can re-open via Runtime.Open.
			path := cloned.Path()
			_ = cloned.Close()
			return path, nil
		},
		ListSessions: func() ([]SessionInfo, error) { return s.ListSessions() },
		RenderTree:   func() string { return icodingagent.RenderTreeASCII(s.inner.Tree()) },

		// Picker callbacks left nil → handlers fall back to text mode.
		PickSession:     nil,
		PickUserMessage: nil,
		PickTreeEntry:   nil,
		LoadSessionPath: nil,
	}

	registry := icodingagent.NewSlashRegistry()
	internalSC := sc.toInternal()
	if err := registry.Dispatch(internalSC, line, nil); err != nil {
		return output, err
	}
	return output, nil
}

// SlashContextHandle is the SDK-facing form of the slash-context
// callbacks. SDK consumers don't construct these directly; they're
// produced inside DispatchSlash and forwarded to the internal
// registry.
//
// PROVISIONAL: this type exists to bridge the internal SlashContext
// to the public API during F3-F5. May be replaced with a leaner
// public surface (e.g., a single `Output []string` callback) once
// F4-F5 narrow the SDK contract.
type SlashContextHandle struct {
	Args             string
	Append           func(msg string)
	AppendText       func(msg string)
	Clear            func()
	Quit             func()
	Reset            func()
	ModelName        func() string
	ToolNames        func() []string
	SkillNames       func() []string
	SessionInfo      func() (string, string, int)
	LastAssistant    func() string
	CopyClipboard    func(string) error
	CostSummary      func() string
	CurrentSession   func() *icodingagent.Session
	SetSessionName   func(string) error
	ForkAtEntry      func(string) error
	ForkToNewSession func(string) error
	CloneCurrent     func() (string, error)
	ListSessions     func() ([]SessionInfo, error)
	RenderTree       func() string
	PickSession      func() (string, bool)
	PickUserMessage  func() (string, bool)
	PickTreeEntry    func(initialSelectedID string) (string, bool)
	LoadSessionPath  func(string) error
}

func (h *SlashContextHandle) toInternal() *icodingagent.SlashContext {
	return &icodingagent.SlashContext{
		Args:             h.Args,
		Append:           h.Append,
		AppendText:       h.AppendText,
		Clear:            h.Clear,
		Quit:             h.Quit,
		Reset:            h.Reset,
		ModelName:        h.ModelName,
		ToolNames:        h.ToolNames,
		SkillNames:       h.SkillNames,
		SessionInfo:      h.SessionInfo,
		LastAssistant:    h.LastAssistant,
		CopyClipboard:    h.CopyClipboard,
		CostSummary:      h.CostSummary,
		CurrentSession:   h.CurrentSession,
		SetSessionName:   h.SetSessionName,
		ForkAtEntry:      h.ForkAtEntry,
		ForkToNewSession: h.ForkToNewSession,
		CloneCurrent:     h.CloneCurrent,
		ListSessions:     h.ListSessions,
		RenderTree:       h.RenderTree,
		PickSession:      h.PickSession,
		PickUserMessage:  h.PickUserMessage,
		PickTreeEntry:    h.PickTreeEntry,
		LoadSessionPath:  h.LoadSessionPath,
	}
}

// lastAssistantText extracts the most recent assistant message's text
// content. Used by the /save slash and a few extension callbacks.
func (s *Session) lastAssistantText() string {
	msgs := s.agent.Messages()
	for _, msg := range slices.Backward(msgs) {
		if msg.Assistant == nil {
			continue
		}
		var b strings.Builder
		for _, c := range msg.Assistant.Content {
			if tc, ok := c.(ai.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return b.String()
	}
	return ""
}
