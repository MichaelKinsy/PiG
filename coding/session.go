package coding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

// Session is an active agent runtime: one JSONL on disk, one
// agent.Agent in memory, plus the tools+hooks the caller
// configured. Mirrors upstream pi-coding-agent's AgentSession.
//
// A Session is mutable: Send appends messages, Fork moves the leaf,
// Close releases resources. Methods may be called from any goroutine,
// but Send itself is single-threaded with respect to the underlying
// agent loop: concurrent Sends serialise via an internal mutex.
//
// Construct a Session via NewSession or through Runtime.
type Session struct {
	processImage             imageprocessing.ProcessImageFunc
	sessionStartEvent        extension.SessionStartEvent
	rebindSession            func(context.Context, extension.SessionStartEvent) error
	beforeSessionReplacement func(context.Context) error
	services                 *Services
	inner                    *icodingagent.Session
	agent                    *agent.Agent
	tools                    []agent.AgentTool
	toolRegistryMu           sync.RWMutex
	toolRegistry             sessionToolRegistry
	modelRuntime             *ModelRuntime
	modelRegistry            *ModelRegistry
	sessionDir               string
	noSession                bool
	scopedModels             sessionScopedModels
	runnerMu                 sync.RWMutex
	runner                   *inproc.Runner // extension runner for event dispatch
	// sharedRunner is the Runtime- or Clone-source-owned runner this Session borrowed; its owner invalidates it.
	sharedRunner *inproc.Runner
	// extCurrentMessage tracks the in-flight message across message_start →
	// message_update so the session can forward it to extensions on
	// message_update (upstream passes event.message). Only the agent's run
	// goroutine emits message events, so it needs no lock.
	extCurrentMessage   extension.AgentMessage
	rawEvents           chan agent.AgentEvent
	queueEvents         queueEventPublication
	events              chan agent.AgentEvent
	eventsMu            sync.RWMutex
	listenersMu         sync.Mutex
	listeners           atomic.Pointer[[]sessionEventListener]
	nextListenerID      uint64
	closeDone           chan struct{}
	closeOnce           sync.Once
	modelChangeMu       sync.Mutex
	detachModelObserver func()
	detachAgentEvents   func()

	// Only the admitted Agent run reads or changes actionable-boundary state.
	boundaryTurnIndex         int
	boundaryDispatchedMessage *agent.AssistantMessage
	lastActivityOutcome       extension.AgentActivityOutcome
	lastAssistantMessage      *agent.AssistantMessage
	lastAssistantToolResults  []agent.AgentMessage

	mu sync.Mutex // protects Session state

	// runState is the active run's streaming, abort, and idle state.
	runState sessionRunState

	resourceLoader          atomic.Pointer[resourceLoaderRef]
	baseSystemPrompt        atomic.Pointer[string]
	baseSystemPromptOptions atomic.Pointer[extension.BuildSystemPromptOptions]
	systemPromptResources   atomic.Pointer[SystemPromptResources]
	baseSystemSections      ai.OrderedSections
	runSystemSections       atomic.Pointer[ai.OrderedSections]
	structuredSystemPrompt  bool
	// defaultSystemPrompt reports that the Session built its own prompt,
	// which it rebuilds when the active tools change.
	defaultSystemPrompt bool

	// Direct bash execution (ExecuteBash / AbortBash).
	// bashMu protects bashCancels. Each in-flight ExecuteBash registers its
	// cancel function (upstream _bashAbortControllers): runs are concurrent
	// and AbortBash cancels them all.
	bashMu      sync.Mutex
	bashCancels map[*context.CancelFunc]struct{}

	// pendingBashMessages buffers bashExecution records produced by
	// ExecuteBash while an agent turn is streaming, so they don't break
	// tool_use/tool_result ordering. Flushed into agent state + persisted
	// at the end of the turn. Mirrors upstream _pendingBashMessages /
	// _flushPendingBashMessages (agent-session.ts:2607-2660).
	pendingBashMu       sync.Mutex
	pendingBashMessages []pendingBashRecord

	// Compaction state.
	// compacting is true while any compaction (manual or auto) is in flight.
	manualCompacting      bool
	compacting            atomic.Bool        // true while Compact() or runAutoCompaction() is in flight
	compactMu             sync.Mutex         // protects compaction and tree-navigation ownership
	compactCancel         context.CancelFunc // non-nil while Compact() or runAutoCompaction() is in flight
	compactDone           chan struct{}      // closed when the current compaction releases ownership
	compactAbortRequested bool               // cancellation arrived before compactCancel was installed
	branchSumCancel       context.CancelFunc // non-nil while tree navigation owns its abortable operation
	branchSumDone         chan struct{}      // closed when the current tree navigation releases ownership

	// overflowRecoveryAttempted prevents recursive overflow compaction.
	// Set to true on the first overflow recovery attempt; any subsequent
	// overflow emits a CompactionEndEvent with an error instead of retrying.
	// Mirrors upstream _overflowRecoveryAttempted (agent-session.ts).
	overflowRecoveryAttempted atomic.Bool

	// retryAttempt counts committed automatic retries of the current retry
	// sequence (agent-session.ts _retryAttempt).
	retryAttempt  atomic.Int32
	retryMu       sync.Mutex
	retryCancel   *sessionRetryWait
	retrySchedule func(func())

	// assistantEnds carries, in message_end order, what the agent goroutine
	// decided when each assistant message persisted, for forwardAgentEvents to
	// publish at the matching message_end and agent_end.
	assistantEndsMu sync.Mutex
	assistantEnds   []assistantEndNote
	// lastAssistantEnd is the note of the last assistant message_end
	// forwardAgentEvents published. Only that goroutine touches it.
	lastAssistantEnd assistantEndNote

	// entryIDsByMessage maps in-memory messages to the Session entries that
	// persisted or projected them (agent-session.ts _entryIdsByMessage). It is
	// rebuilt on every context refresh, which bounds it to the projection plus
	// messages persisted since.
	entryIDsMu        sync.Mutex
	entryIDsByMessage map[any]string

	queueMu        sync.Mutex
	queuedSteering []string
	queuedFollowUp []string

	// completer is the SimpleCompleter used by Compact and NavigateTree.
	// nil → modelCompleter{} (real LLM call via s.Model()).
	// Override in tests to inject a fake.
	completer compaction.SimpleCompleter
	streamFn  compaction.StreamFn

	// agentStartMu guards agentStartMessages: the custom messages the last
	// before_agent_start dispatch returned, waiting for the prompt they join.
	agentStartMu       sync.Mutex
	agentStartMessages []agent.AgentMessage

	// callerHooks keeps the caller's construction choices so Clone builds an
	// equivalent Session through NewSession.
	callerHooks callerHooks
	warming     sessionCacheWarming
}

type callerHooks struct {
	beforeToolCall  []agent.BeforeToolCallHook
	afterToolCall   []agent.AfterToolCallHook
	eventBufferSize int
	transport       ai.Transport
}

// SessionOptions configures NewSession.
type SessionOptions struct {
	// SessionManager supplies the actual backing log instead of creating or loading another one.
	SessionManager *SessionManager

	// Model is the selected LLM model. Nil allows an existing Session to restore its saved selection.
	Model *ai.Model

	// ThinkingLevel overrides restored and configured preferences before model clamping. Empty uses the Session or settings preference.
	ThinkingLevel ai.ThinkingLevel

	// ScopedModels selects the ordered models and optional thinking preferences for cycling.
	ScopedModels []ScopedModel

	// SystemPrompt supplies an explicit full-text baseline, retained as one preamble section.
	// When omitted, the Session builds the default coding prompt.
	SystemPrompt string

	// SystemPromptSections supplies the ordered prompt state assembled by the caller.
	// When non-nil it takes precedence over SystemPrompt and is diffed on each prompt.
	SystemPromptSections ai.OrderedSections

	// ResourceLoader supplies the skills, prompt templates, context files and system prompt text the Session expands, reports and builds its default system prompt from (Pi's createAgentSession resourceLoader). When nil, the Session uses a DefaultResourceLoader over the Services' cwd, agent directory and settings, which reloads the Services' settings, dropping their transient ApplyOverrides, and discovers resources at construction, as createAgentSession does. Supply NoResources to bind none and leave the system prompt to SystemPrompt, SystemPromptSections and SystemPromptResources.
	ResourceLoader ResourceLoader

	// SystemPromptResources is the resource-loader state (custom prompt, appended text, context files, skills) behind SystemPromptSections. It is what before_agent_start and getSystemPromptOptions report; nil reports SystemPrompt as the custom prompt.
	SystemPromptResources *SystemPromptResources

	// Tools layered on top of the default coding-tool set
	// (read/write/bash/edit/grep/find/ls). Caller-supplied tools
	// take precedence on name collision.
	Tools []agent.AgentTool

	// CustomTools are SDK tool definitions with synthetic SDK source metadata.
	CustomTools []extension.ToolDefinition

	// NoTools disables all tools ("all") or only the initial builtin selection ("builtin").
	NoTools string

	skipExtensionTools bool
	toolRegistry       *sessionToolRegistry

	// SkipBuiltinTools omits the built-in coding tools while still allowing
	// caller-supplied and extension tools.
	SkipBuiltinTools bool

	// AllowedTools, if non-nil, is the allowlist filter applied at
	// agent-loop time. nil = no restriction; non-nil empty = block
	// every tool. Mirrors agent-frontmatter `tools:` field.
	AllowedTools map[string]struct{}

	// ActiveBuiltinTools, when non-nil, restricts which built-in coding
	// tools are active. Unlike AllowedTools it does NOT gate caller/
	// extension tools (opts.Tools). nil uses the defaultTools setting or
	// upstream's default [read, bash, edit, write]. grep/find/ls remain
	// registered but inactive unless requested.
	ActiveBuiltinTools map[string]struct{}

	// ExcludedTools, when non-empty, is a denylist removed from the final
	// tool set after allow/active filtering. It gates built-in AND
	// extension/caller tools, so an excluded tool is non-callable, not just
	// hidden from the prompt. Mirrors upstream excludedToolNames (sdk.ts:246)
	// and isAllowedTool's `&& !excludedToolNames?.has(name)`
	// (agent-session.ts:2288).
	ExcludedTools map[string]struct{}

	// BeforeToolCall hooks fire before any tool executes; can block
	// or transform args. See agent.BeforeToolCallHook.
	BeforeToolCall []agent.BeforeToolCallHook

	// AfterToolCall hooks fire after each tool result. Each hook may
	// return content/isError overrides and a terminate signal. Terminate
	// stops the agent loop when every tool in the batch signals it
	// (upstream shouldTerminateToolBatch semantics).
	AfterToolCall []agent.AfterToolCallHook

	// Runner is the extension runner for dispatching lifecycle events
	// (before_agent_start, etc.). Nil creates an empty runner for the Session context.
	Runner *inproc.Runner

	// ResumePath, when non-empty, loads an existing session JSONL
	// from disk and rebuilds the agent's message history from
	// session.BuildContext(nil). Mutually exclusive with creating a
	// fresh session.
	ResumePath string
	// CWDOverride selects an effective cwd for an opened Session without changing its stored header.
	CWDOverride *string

	// SessionDir overrides the on-disk session directory used for new
	// sessions and resume-path lookups.
	SessionDir string

	// SessionID specifies an exact session ID for new sessions.
	// Mirrors upstream --session-id (v0.76.0).
	SessionID string

	// NoSession disables session persistence while keeping the session id usable
	// for provider cache affinity.
	NoSession bool

	// EventBufferSize controls the buffer for Session.Events(). Zero
	// → 64 (a sensible default that won't block typical UI consumers
	// while still bounding memory).
	EventBufferSize int

	// Transport requests a provider-specific streaming transport.
	Transport ai.Transport

	// existing, when set, is an already created on-disk Session to wrap
	// (Clone). No bootstrap entries are written and nothing is resumed.
	existing *icodingagent.Session
	// resumed owns the file snapshot already opened and cwd-validated by Runtime.Open.
	resumed *icodingagent.Session
	// runnerShared marks Runner as owned by the Runtime or Clone source rather than by this Session.
	runnerShared bool
	// boundResourceLoader is the Clone source's loader binding, including whether that Session builds its default prompt from it. It takes precedence over ResourceLoader.
	boundResourceLoader *resourceLoaderRef
}

// NewSession constructs an active session against the given Services
// container. It uses SessionManager directly when supplied; otherwise it creates a fresh log or opens ResumePath.
// Supplied history restores model and thinking before capability clamping. Explicit thinking wins; history without thinking metadata uses the global default and records the selected level.
//
// Errors:
//
//   - svcs is nil → ErrNoServices
//   - opts.Model is nil → restore the selected model from supplied or resumed history when available; otherwise start without a selected model.
//   - file persistence fails → wrapped error
//   - ResumePath set and the file is missing/corrupt → wrapped error
func NewSession(svcs *Services, opts SessionOptions) (*Session, error) {
	if svcs == nil {
		return nil, ErrNoServices
	}
	if opts.SessionManager != nil {
		opts.NoSession = !opts.SessionManager.IsPersisted()
		if dir := opts.SessionManager.GetSessionDir(); dir != "" {
			opts.SessionDir = dir
		}
	}
	if opts.SessionDir == "" {
		opts.SessionDir = icodingagent.GetDefaultSessionDirPath(svcs.CWD(), svcs.AgentDir())
	}

	if opts.Runner == nil {
		opts.Runner = inproc.NewRunner(nil, svcs.CWD())
	}
	resourceLoader := opts.boundResourceLoader
	if resourceLoader == nil {
		var err error
		if resourceLoader, err = resolveResourceLoader(svcs, opts.ResourceLoader); err != nil {
			return nil, err
		}
	}
	registry, allTools, err := createSessionToolRegistry(svcs, opts)
	if err != nil {
		return nil, err
	}

	bufSize := opts.EventBufferSize
	if bufSize <= 0 {
		bufSize = 64
	}
	rawEventCh := make(chan agent.AgentEvent, bufSize)
	eventCh := make(chan agent.AgentEvent, bufSize)
	// closeDone releases the Session event executor and any awaiting producers.
	closeDone := make(chan struct{})

	// The awaited Agent subscription reads the live Session so replacement redirects persistence and boundary commits together.
	var inner *icodingagent.Session
	var sess *Session

	var thinkingBudgets *ai.ThinkingBudgets
	if configured := svcs.SettingsManager().GetThinkingBudgets(); configured != nil {
		thinkingBudgets = &ai.ThinkingBudgets{}
		if configured.Minimal != nil {
			thinkingBudgets.Minimal = *configured.Minimal
		}
		if configured.Low != nil {
			thinkingBudgets.Low = *configured.Low
		}
		if configured.Medium != nil {
			thinkingBudgets.Medium = *configured.Medium
		}
		if configured.High != nil {
			thinkingBudgets.High = *configured.High
		}
	}

	settings := svcs.Settings()
	thinkingLevel := settings.DefaultThinkingLevel
	// A per-model default outranks the global default, as upstream sdk.ts
	// createAgentSession reads getModelThinkingLevel first.
	if opts.Model != nil {
		if perModel := settings.ModelThinkingLevels[opts.Model.ProviderMeta.ProviderID+"/"+opts.Model.ID]; perModel != "" {
			thinkingLevel = perModel
		}
	}
	if opts.ThinkingLevel != "" {
		thinkingLevel = string(opts.ThinkingLevel)
	}
	if thinkingLevel == "" {
		thinkingLevel = icodingagent.DefaultThinkingLevel
	}
	if opts.ThinkingLevel != "" {
		thinkingLevel = string(opts.ThinkingLevel)
	}
	thinkingLevel = string(ai.ClampThinkingLevel(opts.Model, ai.ThinkingLevel(thinkingLevel)))

	agent := agent.NewAgent(agent.AgentOptions{
		// Read at tool-call time: the file appears once the session first persists.
		SessionFile: func() string {
			if sess == nil {
				return ""
			}
			return sess.Path()
		},
		Model:           opts.Model,
		ThinkingLevel:   ai.ThinkingLevel(thinkingLevel),
		ThinkingBudgets: thinkingBudgets,
		SteeringMode:    agent.QueueMode(svcs.SettingsManager().GetSteeringMode()),
		FollowUpMode:    agent.QueueMode(svcs.SettingsManager().GetFollowUpMode()),
		BeforeToolCall:  opts.BeforeToolCall,
		AfterToolCall:   append([]agent.AfterToolCallHook(nil), opts.AfterToolCall...),
		Transport:       opts.Transport,
		PreparePrompt: func(ctx context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
			return sess.preparePrompt(ctx, messages)
		},
		PrepareToolResult: func(ctx context.Context, result agent.AgentToolResult) agent.AgentToolResult {
			return sess.prepareToolResult(ctx, result)
		},
		DefaultStreamFn: cacheWarmingStreamFn(func() *Session { return sess }),
		PrepareNextTurn: func(ctx context.Context, turn agent.PrepareNextTurnContext) (*agent.AgentLoopTurnUpdate, error) {
			return sess.prepareNextTurn(ctx, turn)
		},
		PrepareRequest: func(ctx context.Context, request agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
			return sess.prepareRequest(ctx, request), nil
		},
		// Read per request so a mid-session change applies (sdk.ts
		// convertToLlmWithBlockImages).
		TransformLLMMessages: func(messages []ai.Message) []ai.Message {
			if !svcs.SettingsManager().GetBlockImages() {
				return messages
			}
			return blockImages(messages)
		},
	})

	agent.SetTools(allTools)

	// Create or resume the on-disk session.
	sm := newSessionManagerForDir(svcs, opts.SessionDir)
	switch {
	case opts.SessionManager != nil:
		inner = opts.SessionManager
		if len(inner.BuildContext(inner.LeafID())) == 0 {
			if opts.Model != nil {
				if err := inner.AppendModelSwitch(providerID(opts.Model), opts.Model.ID, opts.Model.DisplayName); err != nil {
					return nil, err
				}
			}
			if err := inner.AppendThinkingLevelChange(thinkingLevel); err != nil {
				return nil, err
			}
		} else {
			fallbackThinking := ai.ThinkingLevel(settings.DefaultThinkingLevel)
			if fallbackThinking == "" {
				fallbackThinking = ai.ThinkingLevel(icodingagent.DefaultThinkingLevel)
			}
			restoredModel, restoredThinking := restoreSessionRuntimeState(inner, svcs, opts.Model, fallbackThinking, opts.Model == nil)
			if opts.Model == nil && restoredModel != nil {
				opts.Model = restoredModel
				agent.SetModel(restoredModel)
			}
			if opts.ThinkingLevel != "" {
				restoredThinking = opts.ThinkingLevel
			}
			agent.SetThinkingLevel(ai.ClampThinkingLevel(opts.Model, restoredThinking))
			if !slices.ContainsFunc(inner.GetBranch(), func(entry icodingagent.SessionEntry) bool { return entry.Base.Type == "thinking_level_change" }) {
				if err := inner.AppendThinkingLevelChange(string(agent.ThinkingLevel())); err != nil {
					return nil, err
				}
			}
		}
	case opts.existing != nil:
		inner = opts.existing
	case opts.ResumePath != "":
		loaded := opts.resumed
		if loaded == nil {
			var override []string
			if opts.CWDOverride != nil {
				override = []string{*opts.CWDOverride}
			}
			var err error
			loaded, err = sm.Open(opts.ResumePath, override...)
			if err != nil {
				return nil, fmt.Errorf("coding.NewSession: resume %s: %w", opts.ResumePath, err)
			}
		}
		inner = loaded
		// sdk.ts restores the global thinking default before clamping against the resolved model.
		fallbackThinking := ai.ThinkingLevel(settings.DefaultThinkingLevel)
		if fallbackThinking == "" {
			fallbackThinking = ai.ThinkingLevel(icodingagent.DefaultThinkingLevel)
		}
		if restoredModel, restoredThinking := restoreSessionRuntimeState(inner, svcs, opts.Model, fallbackThinking, opts.Model == nil); restoredModel != nil {
			opts.Model = restoredModel
			agent.SetModel(restoredModel)
			agent.SetThinkingLevel(ai.ClampThinkingLevel(restoredModel, restoredThinking))
		} else {
			agent.SetThinkingLevel(restoredThinking)
		}
		if opts.ThinkingLevel != "" {
			agent.SetThinkingLevel(ai.ClampThinkingLevel(opts.Model, opts.ThinkingLevel))
		}
	default:
		var sessID string
		if opts.SessionID != "" {
			sessID = opts.SessionID
		} else {
			var err error
			sessID, err = icodingagentGenerateSessionID()
			if err != nil {
				return nil, fmt.Errorf("coding.NewSession: gen session id: %w", err)
			}
		}
		if opts.NoSession {
			inner = icodingagent.NewSession(sessID, svcs.CWD())
		} else {
			created, err := sm.Create(sessID, "")
			if err != nil {
				return nil, fmt.Errorf("coding.NewSession: create session: %w", err)
			}
			inner = created
		}
		if opts.Model != nil {
			if err := inner.AppendModelSwitch(providerID(opts.Model), opts.Model.ID, opts.Model.DisplayName); err != nil {
				return nil, fmt.Errorf("coding.NewSession: bootstrap model_change: %w", err)
			}
		}
		if err := inner.AppendThinkingLevelChange(thinkingLevel); err != nil {
			return nil, fmt.Errorf("coding.NewSession: bootstrap thinking_level_change: %w", err)
		}
	}

	// Wire session ID for prompt caching (OpenAI prompt_cache_key).
	// Must be set after inner is created/loaded so the stable ID is known.
	agent.SetSessionID(inner.ID())

	sess = &Session{
		processImage:  imageprocessing.ProcessImage,
		services:      svcs,
		inner:         inner,
		agent:         agent,
		tools:         registry.tools(),
		toolRegistry:  registry,
		modelRuntime:  svcs.ModelRuntime(),
		modelRegistry: svcs.Registry(),
		sessionDir:    opts.SessionDir,
		noSession:     opts.NoSession,
		runner:        opts.Runner,
		sharedRunner:  sharedRunner(opts),
		rawEvents:     rawEventCh,
		events:        eventCh,
		closeDone:     closeDone,
		callerHooks: callerHooks{
			beforeToolCall:  slices.Clone(opts.BeforeToolCall),
			afterToolCall:   slices.Clone(opts.AfterToolCall),
			eventBufferSize: opts.EventBufferSize,
			transport:       opts.Transport,
		},
	}
	sess.resourceLoader.Store(resourceLoader)
	sess.SetScopedModels(opts.ScopedModels)
	var initialToolNames []string
	for _, tool := range allTools {
		initialToolNames = append(initialToolNames, tool.Name())
	}
	sess.SetActiveToolsByName(initialToolNames)
	sess.detachModelObserver = svcs.Registry().ObserveChanges(sess.refreshCurrentModelFromRegistry)
	// Initial rebinding follows backend ownership; declared metadata alone does not own a caller-supplied transport.
	if opts.Model != nil && opts.Model.Provider != nil && svcs.Registry().HasRegisteredProvider(opts.Model.Provider.ID()) {
		sess.refreshCurrentModelFromRegistry()
	}
	sess.detachAgentEvents = agent.Subscribe(sess.handleAgentEvent)
	sess.installAgentBoundaryHooks()
	sess.installExtensionHooks()
	if opts.existing == nil {
		// A clone shares its source's runner; the source keeps the binding.
		sess.bindExtensionCommandActions(opts.Runner)
	}
	sess.initSystemPrompt(opts)
	sess.refreshContext()
	sess.installCacheWarmer(inner)
	go sess.forwardAgentEvents()
	return sess, nil
}

// persistMessage records one message the agent produced, as Pi's message_end
// persistence does, and indexes it by its new entry. A write failure is
// returned so the agent fails the run, as Pi's appendMessage throws.
func (s *Session) persistMessage(msg agent.AgentMessage) error {
	if msg.User != nil {
		s.overflowRecoveryAttempted.Store(false)
	}
	var err error
	if in := s.inner; in != nil {
		var id string
		if msg.Custom != nil {
			customType, _ := msg.Custom["customType"].(string)
			display, _ := msg.Custom["display"].(bool)
			id, err = in.AppendCustomMessage(customType, msg.Custom["content"], display, msg.Custom["details"])
		} else {
			id, err = in.AppendMessage(msg)
		}
		if err == nil {
			s.rememberMessageEntry(msg, id)
		}
	}
	if msg.Assistant != nil {
		s.lastAssistantMessage = msg.Assistant
		// The message_end still reaches the forwarder, which pops this note.
		s.pushAssistantEnd(s.noteAssistantMessageEnd(msg.Assistant))
	}
	return err
}

// turnEndWithEntryIDs resolves the entries already persisted by message_end
// before the synchronous turn_end extension dispatch.
func (s *Session) turnEndWithEntryIDs(event agent.TurnEndEvent) agent.TurnEndEvent {
	event.MessageEntryID, _ = s.findPersistedMessageEntryID(event.Message)
	event.ToolResultEntryIDs = make([]string, len(event.ToolResults))
	for i := range event.ToolResults {
		event.ToolResultEntryIDs[i], _ = s.findPersistedMessageEntryID(agent.AgentMessage{ToolResult: &event.ToolResults[i]})
	}
	return event
}

func restoreSessionRuntimeState(inner *icodingagent.Session, services *Services, fallbackModel *ai.Model, fallbackThinking ai.ThinkingLevel, restoreModel bool) (*ai.Model, ai.ThinkingLevel) {
	model := fallbackModel
	thinking := fallbackThinking
	branch := inner.GetBranch()
	thinkingLevel, selectedModel := icodingagent.GetSessionContextSettings(branch)
	if restoreModel && selectedModel != nil && selectedModel.Provider != "" && selectedModel.ModelID != "" {
		if restored, err := BuildModel(selectedModel.Provider+"/"+selectedModel.ModelID, services); err == nil {
			model = restored
		}
	}
	if slices.ContainsFunc(branch, func(entry icodingagent.SessionEntry) bool { return entry.Base.Type == "thinking_level_change" }) {
		thinking = ai.ThinkingLevel(thinkingLevel)
	}
	return model, thinking
}

func newSessionManagerForDir(svcs *Services, sessionDir string) *icodingagent.SessionManager {
	if sessionDir == "" {
		sessionDir = icodingagent.GetDefaultSessionDirPath(svcs.CWD(), svcs.AgentDir())
	}
	return icodingagent.NewSessionManagerWithDir(svcs.CWD(), sessionDir)
}

// ReplaceRunner atomically redirects lifecycle dispatch to the current
// extension generation after reload.
func (s *Session) ReplaceRunner(runner *inproc.Runner) {
	s.runnerMu.Lock()
	s.runner = runner
	s.runnerMu.Unlock()
	s.bindExtensionCommandActions(runner)
}

func sharedRunner(opts SessionOptions) *inproc.Runner {
	if opts.runnerShared {
		return opts.Runner
	}
	return nil
}

// StartEvent returns the session_start event this Session's extensions receive when a mode binds them: startup for the first Session, or the replacement reason and previous file.
func (s *Session) StartEvent() extension.SessionStartEvent {
	if s.sessionStartEvent.Type == "" {
		return extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	}
	return s.sessionStartEvent
}

// ExtensionRunner returns the Session's current extension runner, which reload and replacement change.
func (s *Session) ExtensionRunner() *inproc.Runner { return s.currentRunner() }

func (s *Session) currentRunner() *inproc.Runner {
	s.runnerMu.RLock()
	defer s.runnerMu.RUnlock()
	return s.runner
}

// BuildUserContent constructs the upstream user-content block list: a leading text block, including empty text, followed by any image attachments.
func BuildUserContent(text string, images []ai.ImageContent) ai.UserContentBlocks {
	content := []ai.UserContentBlock{ai.TextContent{Text: text}}
	for _, img := range images {
		content = append(content, img)
	}
	return content
}

// Send appends a text prompt to the session, runs the agent loop until the LLM
// stops calling tools or until ctx cancels, and returns the full message slice
// produced this turn.
func (s *Session) Send(ctx context.Context, prompt string) ([]agent.AgentMessage, error) {
	return s.SendContent(ctx, BuildUserContent(prompt, nil))
}

// SendContent dispatches extension commands before ordinary structured-content turns. Modes that already process commands and input use SendContentWithPreflight instead.
func (s *Session) SendContent(ctx context.Context, content []ai.UserContentBlock) ([]agent.AgentMessage, error) {
	if len(content) > 0 {
		if text, ok := content[0].(ai.TextContent); ok && strings.HasPrefix(text.Text, "/") {
			name, args, _ := strings.Cut(strings.TrimPrefix(text.Text, "/"), " ")
			if runner := s.currentRunner(); runner != nil && runner.ExecuteCommand(ctx, name, args) {
				s.runDeferredSettledActions()
				return nil, nil
			}
		}
	}
	return s.SendContentWithPreflight(ctx, content, nil)
}

// SendContentWithPreflight awaits prompt preparation before acceptance and agent admission. Its input has already passed command/input dispatch. Suspended preflight does not mark the Session streaming or hold its run lock. Once accepted, the run owns cancellation and completes through agent_settled.
func (s *Session) SendContentWithPreflight(ctx context.Context, content []ai.UserContentBlock, preflight func()) ([]agent.AgentMessage, error) {
	run, err := s.prepareContentRun(ctx, content, preflight)
	if err != nil {
		return nil, err
	}
	return run.Run()
}

func (s *Session) prepareContentRun(ctx context.Context, content []ai.UserContentBlock, preflight func()) (*PreparedPromptRun, error) {
	if s.IsStreaming() {
		return nil, errAgentAlreadyProcessing
	}
	if err := s.flushPendingCustomMessages(); err != nil {
		return nil, err
	}
	if err := s.validatePromptModel(); err != nil {
		return nil, err
	}
	if err := s.CheckPromptCompaction(ctx); err != nil {
		return nil, err
	}
	prepared, err := s.PreparePrompt(ctx, content)
	if err != nil {
		return nil, err
	}
	prepared.NormalizeImages()
	if preflight != nil {
		preflight()
	}
	return s.BeginPreparedPrompt(ctx, prepared)
}

// checkPromptCompactionLocked is prompt()'s check before a new user message
// (agent-session.ts: _checkCompaction(lastAssistant, false)). It includes
// aborted responses, so a turn aborted near the window still compacts. The
// overflow guard resets afterwards, with the new user message. The caller
// holds s.mu.
func (s *Session) checkPromptCompactionLocked(ctx context.Context) error {
	if la := lastAssistantMessage(s.agent.Messages()); la != nil {
		if _, err := s.checkCompaction(ctx, la, false, nil); err != nil {
			return err
		}
	}
	s.overflowRecoveryAttempted.Store(false)
	return nil
}

// CheckPromptCompaction runs prompt()'s compaction check before a new user
// message, for a mode that starts the run itself with RunAgentPrompt.
func (s *Session) CheckPromptCompaction(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkPromptCompactionLocked(ctx)
}

// RunAgentPrompt runs one agent run to settlement like Pi's _runAgentPrompt.
// start performs the low-level run that seeds it (a prompt, a custom message,
// or a continuation from queued input). Post-run handling then retries
// retryable errors, recovers from overflow and length stops, compacts over the
// threshold, and continues from input queued during the run, until nothing
// asks for another run or ctx is cancelled. It returns the last low-level
// run's messages and error, and does not emit agent_settled: the caller owns
// settlement.
//
// The Session lock protects preparation and post-run handling, not the low-level
// run or its listeners. Before each continuation and before returning, this path
// waits until the Events consumer has handled every event emitted so far, as
// Pi's session listeners run before agent.continue() and before the run
// settles. The consumer must acknowledge FlushEvents barriers. Extension
// handlers already run synchronously on the agent goroutine. The run remains
// active for IsStreaming and Abort until RunAgentPrompt returns.
func (s *Session) RunAgentPrompt(ctx context.Context, start func(context.Context) ([]agent.AgentMessage, error)) ([]agent.AgentMessage, error) {
	ctx, cancelRun := s.beginAgentRun(ctx)
	defer s.endAgentRun(cancelRun)
	messages, runErr := start(ctx)
	s.mu.Lock()
	messages, runErr = s.runPostAgentRunsWith(ctx, messages, runErr, func(ctx context.Context) ([]agent.AgentMessage, error) {
		s.mu.Unlock()
		defer s.mu.Lock()
		if err := s.FlushEvents(ctx); err != nil {
			return nil, err
		}
		messages, err := s.agent.Continue(ctx)
		return messages, err
	})
	if err := s.flushPendingBashLocked(); err != nil {
		s.mu.Unlock()
		return messages, errors.Join(runErr, err)
	}
	runErr = errors.Join(runErr, s.flushPendingCustomMessages())
	s.mu.Unlock()
	// Cancellation only ends the wait; the run's own result stands.
	_ = s.FlushEvents(ctx)
	return messages, runErr
}

// Events returns the channel of streaming agent events (text deltas,
// tool calls, tool results, etc.) emitted during Send. Buffered;
// consumers that fall behind will see send blocking inside the agent
// loop until they catch up. Unbuffered consumers should drain in a
// dedicated goroutine.
//
// The channel is closed when the Session is Closed.
func (s *Session) Events() <-chan agent.AgentEvent { return s.events }

// Messages returns a snapshot of the agent's in-memory message history.
// The slice is a defensive copy; callers may mutate it without
// affecting future Send calls.
func (s *Session) Messages() []agent.AgentMessage {
	messages := s.agent.MessagesSnapshot()
	if messages == nil {
		return []agent.AgentMessage{}
	}
	return messages
}

// providerID returns the model's declared provider identity, falling back to a caller-owned streaming backend when metadata is absent.
func providerID(m *ai.Model) string {
	if m == nil {
		return ""
	}
	if m.ProviderMeta.ProviderID != "" {
		return m.ProviderMeta.ProviderID
	}
	if m.Provider != nil {
		return m.Provider.ID()
	}
	return ""
}

// ID returns the session identifier (the part after the timestamp in
// the JSONL filename).
func (s *Session) ID() string { return s.inner.ID() }

// Path returns the absolute path to the session's JSONL file on disk.
func (s *Session) Path() string { return s.inner.Path() }

// CWD returns the working directory the session was created with.
func (s *Session) CWD() string { return s.inner.CWD() }

// Tools returns a defensive copy of the active loadout. Use GetAllTools for admitted definitions that are currently inactive.
func (s *Session) Tools() []agent.AgentTool {
	return s.agent.Tools()
}

// Model returns a snapshot of the current model pointer. Do not mutate the
// returned model; publish a replacement with SetModel instead.
func (s *Session) Model() *ai.Model { return s.agent.Model() }

// refreshCurrentModelFromRegistry replaces only the active model metadata without resolving credentials, appending a model-change entry, or changing thinking level. Registry updates invoke it outside the registry lock; Model reads delegate to the Agent.
func (s *Session) refreshCurrentModelFromRegistry() {
	s.modelChangeMu.Lock()
	defer s.modelChangeMu.Unlock()
	current := s.Model()
	if current == nil {
		return
	}
	refreshed := s.modelRuntime.GetModel(providerID(current), current.ID)
	if refreshed == nil {
		return
	}
	if s.modelRuntime.sameBoundModelMetadata(current, refreshed) {
		return
	}
	s.agent.SetModel(refreshed)
}

// sameBoundModelMetadata retains an unchanged runtime-bound model instead of replacing it with another equivalent adapter. A changed catalog still replaces the model, and concrete caller providers are never treated as runtime adapters.
func (runtime *ModelRuntime) sameBoundModelMetadata(current, refreshed *ai.Model) bool {
	switch provider := current.Provider.(type) {
	case *catalogModelBackend:
		if provider.runtime != runtime {
			return false
		}
	case *nativeModelBackend:
		if provider.runtime != runtime {
			return false
		}
	default:
		return false
	}
	left, right := *current, *refreshed
	left.Provider, right.Provider = nil, nil
	return reflect.DeepEqual(left, right)
}

// ModelRuntime returns this Session's mode-independent model execution path.
func (s *Session) ModelRuntime() *ModelRuntime { return s.modelRuntime }

// ModelRegistry returns the facade bound to the same ModelRuntime.
func (s *Session) ModelRegistry() *ModelRegistry { return s.modelRegistry }

// StreamModel starts a mode-independent model operation through this Session's runtime.
func (s *Session) StreamModel(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	if extension.ProviderStreamSimpleSelected(ctx) {
		return s.modelRuntime.StreamSimple(ctx, model, request, options)
	}
	return s.modelRuntime.Stream(ctx, model, request, options)
}

// ModelMutationOptions controls whether a Session mutation also saves a global default.
type ModelMutationOptions = icodingagent.ModelMutationOptions

// SetModel swaps the active LLM model and records a model_change in the Session transcript.
// The next Send uses the new provider/model; tools, system prompt, and history are unchanged.
// Global model defaults change only with Persist. The thinking default never changes here. Runtime-managed providers must have configured auth.
func (s *Session) SetModel(m *ai.Model, options ...ModelMutationOptions) error {
	if err := s.checkModelAuth(m); err != nil {
		return err
	}
	return s.setModel(m, extension.ModelSelectSourceUser, options...)
}

func (s *Session) checkModelAuth(m *ai.Model) error {
	if m != nil && m.ProviderMeta.ProviderID != "" && modelRuntimeRequiresAuth(providerID(m)) {
		check, err := s.modelRuntime.CheckAuth(s.backgroundContext(), providerID(m))
		if err != nil {
			return err
		}
		if check == nil {
			return fmt.Errorf("No API key for %s/%s", providerID(m), m.ID)
		}
	}
	return nil
}

// SetModelOnMain checks auth on the caller, dispatches only the synchronous state mutation to the owner loop, and waits for notifications on the caller. A superseded dispatcher can reject the mutation without executing it.
func (s *Session) SetModelOnMain(m *ai.Model, options ModelMutationOptions, dispatch func(func() error) error) error {
	if err := s.checkModelAuth(m); err != nil {
		return err
	}
	var complete func()
	mutate := func() error {
		var err error
		complete, err = s.beginModelChangeState(s.backgroundContext(), m, extension.ModelSelectSourceUser, nil, true, options)
		return err
	}
	if dispatch == nil {
		dispatch = func(mutate func() error) error { return mutate() }
	}
	if err := dispatch(mutate); err != nil {
		return err
	}
	if complete != nil {
		complete()
	}
	return nil
}

// CycleToModel applies a model-cycle selection, saving global model defaults only with Persist.
func (s *Session) CycleToModel(m *ai.Model, options ...ModelMutationOptions) error {
	return s.setModel(m, extension.ModelSelectSourceCycle, options...)
}

func (s *Session) setModel(m *ai.Model, source extension.ModelSelectSource, options ...ModelMutationOptions) error {
	return s.setModelWithThinking(m, source, nil, options...)
}

func (s *Session) setModelWithThinking(m *ai.Model, source extension.ModelSelectSource, explicit *ai.ThinkingLevel, options ...ModelMutationOptions) error {
	complete, err := s.beginModelChangeWithThinking(context.Background(), m, source, explicit, options...)
	if err != nil {
		return err
	}
	if complete != nil {
		complete()
	}
	return nil
}

// Ports packages/coding-agent/src/core/agent-session.ts

// BeginModelChange applies the synchronous model mutation and returns its awaited extension notification, or nil when it is already complete. RPC admits the next command after this prefix; blocking SDK callers invoke a non-nil completion before returning. The caller owns and joins completion, which must be invoked once.
func (s *Session) BeginModelChange(ctx context.Context, m *ai.Model, source extension.ModelSelectSource, options ...ModelMutationOptions) (func(), error) {
	return s.beginModelChangeWithThinking(ctx, m, source, nil, options...)
}

func (s *Session) beginModelChangeWithThinking(ctx context.Context, m *ai.Model, source extension.ModelSelectSource, explicit *ai.ThinkingLevel, options ...ModelMutationOptions) (func(), error) {
	return s.beginModelChangeState(ctx, m, source, explicit, false, options...)
}

func (s *Session) beginModelChangeState(ctx context.Context, m *ai.Model, source extension.ModelSelectSource, explicit *ai.ThinkingLevel, deferThinking bool, options ...ModelMutationOptions) (func(), error) {
	if m == nil {
		return nil, fmt.Errorf("coding: SetModel: model is nil")
	}
	previous := s.Model()
	thinkingLevel := s.thinkingLevelForModelSwitch(m)
	if explicit != nil {
		thinkingLevel = *explicit
	}
	s.modelChangeMu.Lock()
	if s.agent != nil {
		s.agent.SetModel(m)
	}
	s.modelChangeMu.Unlock()
	if s.inner != nil {
		if err := s.inner.AppendModelSwitch(providerID(m), m.ID, m.DisplayName); err != nil {
			return nil, fmt.Errorf("coding: SetModel: persist audit: %w", err)
		}
	}
	if len(options) > 0 && options[0].Persist {
		if err := s.services.SettingsManager().SetDefaultModelAndProvider(providerID(m), m.ID); err != nil {
			return nil, fmt.Errorf("coding: SetModel: persist default: %w", err)
		}
		if err := s.addPersistedDefaultToNonEmptyScope(m); err != nil {
			return nil, fmt.Errorf("coding: SetModel: persist scope: %w", err)
		}
	}
	notifyThinking, err := s.setThinkingLevelState(thinkingLevel)
	if err != nil {
		return nil, fmt.Errorf("coding: SetModel: thinking level: %w", err)
	}
	if !deferThinking && notifyThinking != nil {
		notifyThinking()
		notifyThinking = nil
	}
	runner := s.currentRunner()
	if runner == nil || ai.ModelsAreEqual(previous, m) || !runner.HasHandlers(icodingagent.EventModelSelect) {
		return notifyThinking, nil
	}
	return func() {
		if notifyThinking != nil {
			notifyThinking()
		}
		_, _ = runner.Emit(ctx, extension.ModelSelectEvent{
			Type: icodingagent.EventModelSelect, Model: m, PreviousModel: previous, Source: source,
		})
	}, nil
}

// thinkingLevelForModelSwitch mirrors upstream _getThinkingLevelForModelSwitch:
// the target model's modelThinkingLevels entry, else defaultThinkingLevel,
// else the current level. SetThinkingLevel clamps it to the model.
func (s *Session) thinkingLevelForModelSwitch(target *ai.Model) ai.ThinkingLevel {
	settings := s.services.SettingsManager()
	if perModel := settings.GetModelThinkingLevel(providerID(target), target.ID); perModel != "" {
		return ai.ThinkingLevel(perModel)
	}
	if level := settings.GetDefaultThinkingLevel(); level != "" {
		return ai.ThinkingLevel(level)
	}
	return s.agent.ThinkingLevel()
}

// Services returns the parent Services container.
func (s *Session) Services() *Services { return s.services }

// Agent returns the underlying *agent.Agent. agent is a public
// package so SDK consumers can use this to inspect timings, hooks, and
// in-memory state. Mutating the agent's tool/hook lists after Send has
// been called is undefined.
func (s *Session) Agent() *agent.Agent { return s.agent }

// Inner returns the underlying internal session handle.
//
// PROVISIONAL: this method exists during the F3–F5 refactor so the
// pig binary's TUI layer (internal/codingagent.InteractiveMode) can
// share the same on-disk session a coding.Session owns. The return
// type is internal/codingagent.Session, which third-party consumers
// cannot import, so this method is effectively pig-internal even
// though it is exported. Likely to be removed once F4–F5 finalise
// the public API surface; SDK consumers should use the public
// Session methods (Path, ID, CWD, Messages, Send, etc.) instead.
func (s *Session) Inner() *icodingagent.Session { return s.inner }

// ReplaceInner aborts work owned by the old Session, then installs the new
// Session state, including whether its manager persists to a file.
func (s *Session) ReplaceInner(sess *icodingagent.Session) {
	// pig divergence (D61): Services, Resources, and built-in tools remain bound
	// to the startup project.
	s.AbortCompaction()
	s.AbortBranchSummary()
	s.installCacheWarmer(sess)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inner = sess
	s.noSession = sess.Path() == ""
	s.agent.SetSessionID(sess.ID())
	s.refreshReplacementContext()
	model, thinking := restoreSessionRuntimeState(sess, s.services, s.Model(), s.agent.ThinkingLevel(), true)
	if model != nil {
		s.agent.SetModel(model)
		thinking = ai.ClampThinkingLevel(model, thinking)
	}
	s.agent.SetThinkingLevel(thinking)
}

// EmitSessionStart dispatches a session_start event to extension handlers.
// reason is one of "startup" | "reload" | "new" | "resume" | "fork".
// Interactive replacements dispatch through the awaited mode rebind; headless startup callers use this helper.
func (s *Session) EmitSessionStart(reason string) {
	s.EmitSessionStartTransition(reason, "")
}

// EmitSessionStartTransition dispatches session_start with the replaced
// Session's previous file when a runtime host changes Sessions.
func (s *Session) EmitSessionStartTransition(reason, previousSessionFile string) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventSessionStart) {
		return
	}
	_, _ = runner.Emit(context.Background(), extension.SessionStartEvent{
		Type:                icodingagent.EventSessionStart,
		Reason:              reason,
		PreviousSessionFile: previousSessionFile,
	})
}

// EmitSessionShutdown dispatches a session_shutdown event to extension handlers.
// reason is one of "quit" | "reload" | "new" | "resume" | "fork".
//
// Mirrors upstream teardownCurrent (agent-session-runtime.ts:167), which emits
// session_shutdown on dispose in all modes. Paired with EmitSessionStart so the
// one-shot modes drive the full extension lifecycle rather than leaving
// start-without-shutdown extensions to leak.
func (s *Session) EmitSessionShutdown(reason string) {
	s.EmitSessionShutdownTransition(reason, "")
}

// EmitSessionShutdownTransition dispatches session_shutdown with the target
// Session file when a runtime host changes Sessions.
func (s *Session) EmitSessionShutdownTransition(reason, targetSessionFile string) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventSessionShutdown) {
		return
	}
	_, _ = runner.Emit(context.Background(), extension.SessionShutdownEvent{
		Type:              icodingagent.EventSessionShutdown,
		Reason:            reason,
		TargetSessionFile: targetSessionFile,
	})
}

// Dispose stops the Session as upstream AgentSession.dispose does
// (agent-session.ts:1170-1185): it aborts retry, compaction, branch summary
// and bash, aborts the Agent, invalidates the extension runner, and
// disconnects the Session from the Agent. Later message, tool, turn and
// agent_end events then reach neither extensions nor persistence nor Session
// subscribers. Hooks installed on the Agent still run, so the aborted run's
// turn_end boundaries and agent_settled still reach extensions through the
// invalidated runner, whose ctx getters report it stale.
//
// Upstream runs these steps synchronously, so the aborted run cannot observe
// a partial dispose. The Agent runs on its own goroutine here, so the Session
// disconnects and invalidates before it aborts the Agent.
func (s *Session) Dispose() {
	if s.detachAgentEvents != nil {
		s.detachAgentEvents()
	}
	if runner := s.currentRunner(); runner != nil {
		runner.Invalidate("")
	}
	s.listenersMu.Lock()
	s.listeners.Store(nil)
	s.listenersMu.Unlock()
	s.AbortRetry()
	s.AbortCompaction()
	s.AbortBranchSummary()
	s.AbortBash()
	s.RequestAbort()
	s.closeCacheWarming()
	_ = ai.CleanupSessionResources(s.ID())
}

// Close invalidates the extension runner this Session owns and releases the Session's resources without deleting its persisted JSONL. A runner borrowed from its Runtime or Clone source stays active; Runtime.Close invalidates the Runtime runner. Further Send calls fail; repeated Close calls are safe.
func (s *Session) Close() error {
	s.AbortRetry()
	s.AbortBash()
	s.AbortCompaction()
	s.AbortBranchSummary()
	s.closeCacheWarming()
	s.closeOnce.Do(func() {
		if s.detachModelObserver != nil {
			s.detachModelObserver()
		}
		close(s.closeDone)
	})
	// Upstream dispose aborts the active run and drains work the Session owns.
	s.RequestAbort()
	// upstream: packages/coding-agent/src/core/agent-session.ts:dispose invalidates the runner the session built; a borrowed Runtime or Clone-source runner stays live for its owner.
	if runner := s.currentRunner(); runner != nil && runner != s.sharedRunner {
		runner.Invalidate("")
	}
	s.shutdownRuns()
	if s.detachAgentEvents != nil {
		s.detachAgentEvents()
	}
	// Drain refreshes after closeDone, which releases a refresh blocked on
	// reporting its usage entry.
	s.waitCacheWarming()
	// Upstream dispose cancels warming before provider cleanup. Join refreshes first so late provider completion cannot recreate resources after cleanup.
	_ = ai.CleanupSessionResources(s.ID())
	return nil
}

// ─── Session metadata ────────────────────────────────────────────────────────

// SessionName returns the current user-defined session name (set via /name
// or SetSessionName), or "" if none has been set.
// Mirrors upstream session.sessionName getter (agent-session.ts:849).
func (s *Session) SessionName() string { return s.inner.GetSessionName() }

// SetSessionName persists the sanitized Session name, immediately notifies subscribers, and admits the extension handler's synchronous prefix before returning. Suspended extension work belongs to the Session lifetime. An empty name clears the current name; RPC and slash-command boundaries validate their own non-empty input.
func (s *Session) SetSessionName(name string) error {
	// Pi reads the name synchronously after its own append (agent-session.ts:3559-3560); a concurrent caller's later append cannot change the name this call publishes.
	_, current, err := s.inner.AppendSessionInfoName(name)
	if err != nil {
		return fmt.Errorf("coding: SetSessionName: append: %w", err)
	}
	// upstream: packages/coding-agent/src/core/agent-session.ts:setSessionName
	event := agent.SessionInfoChangedEvent{Name: current}
	s.notifyAgentEventListeners(event)
	s.emitOrderedEvent(publishedSessionEvent{event})
	s.dispatchSessionNameEvent(event)
	return nil
}

// ─── Message helpers ─────────────────────────────────────────────────────────

// LastAssistantText returns the text of the most-recent non-aborted assistant
// message, or nil if no such message exists.
// Mirrors upstream getLastAssistantText (agent-session.ts:3045).
func (s *Session) LastAssistantText() *string {
	msgs := s.Messages()
	for _, msg := range slices.Backward(msgs) {
		if msg.Assistant == nil {
			continue
		}
		a := msg.Assistant
		if a.StopReason == "aborted" && len(a.Content) == 0 {
			continue
		}
		var sb strings.Builder
		for _, b := range a.Content {
			if tc, ok := b.(ai.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		t := sb.String()
		return &t
	}
	return nil
}

// ForkMessage pairs a session entry ID with the user message text.
// Returned by UserMessagesForForking, used by the `get_fork_messages` RPC
// command. Mirrors upstream getUserMessagesForForking return type
// (agent-session.ts:2856-2875).
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// UserMessagesForForking returns all user messages with their session entry
// IDs, suitable for presenting fork targets to the user.
// Mirrors upstream getUserMessagesForForking (agent-session.ts:2856).
func (s *Session) UserMessagesForForking() []ForkMessage {
	entries := s.inner.Entries()
	out := make([]ForkMessage, 0, len(entries)/4)
	for _, e := range entries {
		me, ok := e.AsMessage()
		if !ok || me.Message.User == nil {
			continue
		}
		text := extractUserMessageText(me.Message.User.Content)
		if text == "" {
			continue
		}
		out = append(out, ForkMessage{EntryID: e.Base.ID, Text: text})
	}
	return out
}

// extractUserMessageText extracts text from either user content variant. Mirrors upstream _extractUserMessageText.
func extractUserMessageText(content ai.UserContent) string {
	if text, ok := content.(ai.UserText); ok {
		return string(text)
	}
	blocks, _ := content.(ai.UserContentBlocks)
	var sb strings.Builder
	for _, b := range blocks {
		if tc, ok := b.(ai.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// ─── Session stats ───────────────────────────────────────────────────────────

// SessionStatsTokens holds per-category token counts.
type SessionStatsTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Total      int `json:"total"`
}

// SessionStats holds aggregate counters for a session.
// Mirrors upstream SessionStats interface (agent-session.ts:200-215).
type SessionStats struct {
	SessionFile       string               `json:"sessionFile,omitempty"`
	SessionID         string               `json:"sessionId"`
	UserMessages      int                  `json:"userMessages"`
	AssistantMessages int                  `json:"assistantMessages"`
	ToolCalls         int                  `json:"toolCalls"`
	ToolResults       int                  `json:"toolResults"`
	TotalMessages     int                  `json:"totalMessages"`
	Tokens            SessionStatsTokens   `json:"tokens"`
	Cost              float64              `json:"cost"`
	ContextUsage      *SessionContextUsage `json:"contextUsage,omitempty"`
}

type SessionContextUsage struct {
	Tokens        *int     `json:"tokens"`
	ContextWindow int      `json:"contextWindow"`
	Percent       *float64 `json:"percent"`
}

// GetSessionStats returns aggregate stats for this session.
// Mirrors upstream getSessionStats (agent-session.ts:2887-2935).
func (s *Session) GetSessionStats() SessionStats {
	accounting := s.inner.Accounting()
	return SessionStats{
		SessionFile:       s.Path(),
		SessionID:         s.ID(),
		UserMessages:      accounting.UserMessages,
		AssistantMessages: accounting.AssistantMessages,
		ToolCalls:         accounting.ToolCalls,
		ToolResults:       accounting.ToolResults,
		TotalMessages:     accounting.TotalMessages,
		Tokens: SessionStatsTokens{
			Input:      accounting.Tokens.Input,
			Output:     accounting.Tokens.Output,
			CacheRead:  accounting.Tokens.CacheRead,
			CacheWrite: accounting.Tokens.CacheWrite,
			Total:      accounting.Tokens.Total,
		},
		Cost:         accounting.Tokens.Cost,
		ContextUsage: s.ContextUsage(),
	}
}

// ContextUsage returns the projected context estimate (agent-session.ts
// getContextUsage). Token and percentage values are null after compaction
// until an assistant response after the latest compaction reports valid usage
// and still contributes to the projection.
func (s *Session) ContextUsage() *SessionContextUsage {
	model := s.Model()
	if model == nil || model.Capabilities.ContextWindow <= 0 {
		return nil
	}
	contextWindow := model.Capabilities.ContextWindow
	projection := s.inner.BuildSessionProjection()
	branch := s.currentBranch()
	latestCompaction := -1
	for i, entry := range branch {
		if entry.Base.Type == "compaction" {
			latestCompaction = i
		}
	}
	if latestCompaction >= 0 {
		projectedAssistants := make(map[string]struct{})
		for _, entry := range projection.Entries {
			for _, message := range entry.Messages {
				if assistant := message.Assistant; assistant != nil && assistant.StopReason != ai.StopReasonAborted &&
					assistant.StopReason != ai.StopReasonError && assistant.Usage != nil && agent.CalculateContextTokens(*assistant.Usage) > 0 {
					projectedAssistants[entry.SourceEntry.Base.ID] = struct{}{}
				}
			}
		}
		validUsage := slices.ContainsFunc(branch[latestCompaction+1:], func(entry icodingagent.SessionEntry) bool {
			_, projected := projectedAssistants[entry.Base.ID]
			return projected
		})
		if !validUsage {
			return &SessionContextUsage{ContextWindow: contextWindow}
		}
	}
	tokens := compaction.EstimateProjectedContextTokens(projection, branch).Tokens
	percent := float64(tokens) / float64(contextWindow) * 100
	return &SessionContextUsage{Tokens: &tokens, ContextWindow: contextWindow, Percent: &percent}
}

// ─── Auto-retry control ──────────────────────────────────────────────────────

// SetAutoRetryEnabled toggles the auto-retry setting.
// Mirrors upstream setAutoRetryEnabled (agent-session.ts:2538).
func (s *Session) SetAutoRetryEnabled(enabled bool) error {
	if sm := s.services.SettingsManager(); sm != nil {
		return sm.UpdateGlobal(func(settings *icodingagent.Settings) {
			if settings.Retry == nil {
				settings.Retry = &icodingagent.RetrySettingsJSON{}
			}
			settings.Retry.Enabled = &enabled
		})
	}
	return nil
}

// AbortRetry cancels an active automatic-retry delay without cancelling the
// current Session.
func (s *Session) AbortRetry() {
	s.retryMu.Lock()
	wait := s.retryCancel
	s.retryMu.Unlock()
	if wait != nil {
		wait.abort()
	}
}

// QueueSteer admits already-processed input before the next model call. Modes that own input dispatch use this boundary to avoid running handlers twice.
func (s *Session) QueueSteer(text string, images []ai.ImageContent) {
	s.queueMu.Lock()
	s.queuedSteering = append(s.queuedSteering, text)
	s.queueMu.Unlock()
	s.emitQueueUpdate()
	s.agent.Steer(agent.AgentMessage{User: &agent.UserMessage{
		Role:      agent.RoleUser,
		Content:   BuildUserContent(text, images),
		Timestamp: time.Now().UnixMilli(),
	}})
}

// QueueFollowUp admits already-processed input after the active turn. Modes that own input dispatch use this boundary to avoid running handlers twice.
func (s *Session) QueueFollowUp(text string, images []ai.ImageContent) {
	s.queueMu.Lock()
	s.queuedFollowUp = append(s.queuedFollowUp, text)
	s.queueMu.Unlock()
	s.emitQueueUpdate()
	s.agent.FollowUp(agent.AgentMessage{User: &agent.UserMessage{
		Role:      agent.RoleUser,
		Content:   BuildUserContent(text, images),
		Timestamp: time.Now().UnixMilli(),
	}})
}

// GetSteeringMessages returns queued steering text in admission order.
func (s *Session) GetSteeringMessages() []string {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return append([]string{}, s.queuedSteering...)
}

// GetFollowUpMessages returns queued follow-up text in admission order.
func (s *Session) GetFollowUpMessages() []string {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return append([]string{}, s.queuedFollowUp...)
}

// ClearQueue removes and returns all queued user-message text.
func (s *Session) ClearQueue() (steering, followUp []string) {
	s.agent.ClearAllQueues()
	s.queueMu.Lock()
	steering = append([]string{}, s.queuedSteering...)
	followUp = append([]string{}, s.queuedFollowUp...)
	s.queuedSteering = nil
	s.queuedFollowUp = nil
	s.queueMu.Unlock()
	s.emitQueueUpdate()
	return steering, followUp
}

// PendingMessageCount returns the number of queued steering and follow-up
// messages.
func (s *Session) PendingMessageCount() int {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	return len(s.queuedSteering) + len(s.queuedFollowUp)
}

// SetSteeringMode updates the active queue and persists its drain mode.
func (s *Session) SetSteeringMode(mode agent.QueueMode) error {
	s.agent.SetSteeringMode(mode)
	return s.services.SettingsManager().SetSteeringMode(string(mode))
}

// SetFollowUpMode updates the active queue and persists its drain mode.
func (s *Session) SetFollowUpMode(mode agent.QueueMode) error {
	s.agent.SetFollowUpMode(mode)
	return s.services.SettingsManager().SetFollowUpMode(string(mode))
}

func (s *Session) emitQueueUpdate() {
	s.queueMu.Lock()
	steering := append([]string{}, s.queuedSteering...)
	followUp := append([]string{}, s.queuedFollowUp...)
	s.queueMu.Unlock()
	event := agent.QueueUpdateEvent{Steering: steering, FollowUp: followUp}
	// Upstream _emitQueueUpdate calls listeners on the mutator's stack, so nested queue changes settle before the outer mutation admits input to the Agent.
	s.notifyAgentEventListeners(event)
	s.publishQueueEvent(event)
}

func (s *Session) consumeQueuedMessage(message agent.AgentMessage) (agent.QueueUpdateEvent, bool) {
	if message.User == nil {
		return agent.QueueUpdateEvent{}, false
	}
	queuedText := extractUserMessageText(message.User.Content)
	if queuedText == "" {
		return agent.QueueUpdateEvent{}, false
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	removed := removeQueuedText(&s.queuedSteering, queuedText)
	if !removed {
		removed = removeQueuedText(&s.queuedFollowUp, queuedText)
	}
	if !removed {
		return agent.QueueUpdateEvent{}, false
	}
	return agent.QueueUpdateEvent{
		Steering: append([]string{}, s.queuedSteering...),
		FollowUp: append([]string{}, s.queuedFollowUp...),
	}, true
}

func removeQueuedText(messages *[]string, text string) bool {
	for i, message := range *messages {
		if message != text {
			continue
		}
		*messages = append((*messages)[:i], (*messages)[i+1:]...)
		return true
	}
	return false
}

// AvailableThinkingLevels returns the levels supported by the current model.
func (s *Session) AvailableThinkingLevels() []ai.ThinkingLevel {
	return ai.GetSupportedThinkingLevels(s.Model())
}

// ThinkingLevel returns the effective reasoning level used for future calls.
func (s *Session) ThinkingLevel() ai.ThinkingLevel {
	return s.agent.ThinkingLevel()
}

// SetThinkingLevel records a changed, clamped level in the Session transcript.
// With Persist, it saves the requested level as the global default even when the effective level is unchanged.
func (s *Session) SetThinkingLevel(level ai.ThinkingLevel, options ...ModelMutationOptions) error {
	notify, err := s.setThinkingLevelState(level, options...)
	if err != nil {
		return err
	}
	if notify != nil {
		notify()
	}
	return nil
}

func (s *Session) setThinkingLevelState(level ai.ThinkingLevel, options ...ModelMutationOptions) (func(), error) {
	model := s.Model()
	effective := ai.ClampThinkingLevel(model, level)
	previous := s.agent.ThinkingLevel()
	s.agent.SetThinkingLevel(effective)
	if len(options) > 0 && options[0].Persist {
		if err := s.services.SettingsManager().SetDefaultThinkingLevel(string(level)); err != nil {
			return nil, err
		}
	}
	if effective == previous {
		return nil, nil
	}
	if err := s.inner.AppendThinkingLevelChange(string(effective)); err != nil {
		return nil, err
	}
	runner := s.currentRunner()
	return func() {
		s.emitOrderedEvent(agent.ThinkingLevelChangedEvent{Level: effective})
		if runner != nil && runner.HasHandlers(icodingagent.EventThinkingLevelSelect) {
			_, _ = runner.Emit(context.Background(), extension.ThinkingLevelSelectEvent{
				Type: icodingagent.EventThinkingLevelSelect, Level: string(effective), PreviousLevel: string(previous),
			})
		}
	}, nil
}

// ─── Direct bash execution ───────────────────────────────────────────────────

// BashResult is the result of a direct bash execution via ExecuteBash.
// Mirrors upstream BashResult (core/bash-executor.ts).
type BashResult struct {
	Output string `json:"output"`
	// ExitCode is nil when the command has no exit status, including cancellation.
	ExitCode       *int   `json:"exitCode,omitempty"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// ExecuteBash runs cmd in a shell outside the LLM agent loop and records
// the result so the agent sees it on the next turn (unless
// excludeFromContext is set). Mirrors upstream executeBash +
// recordBashResult (agent-session.ts:2554-2630). When excludeFromContext
// is true the result is persisted as a `bashExecution` entry but dropped
// from LLM context by bashExecutionToText (the `!!cmd` semantics).
//
// Recording is deferred while an agent turn is streaming so it can't
// orphan a tool_use/tool_result pair; the buffered records are flushed
// at the end of the turn (see flushPendingBashLocked).
//
// A notification's admission is released after Bash setup, before awaiting execution and output.
func (s *Session) ExecuteBash(ctx context.Context, command string, excludeFromContext bool) (BashResult, error) {
	return s.executeBash(ctx, command, excludeFromContext, nil, nil, nil)
}

// ExecuteBashWithUpdates executes Bash and reports each output chunk to the callback and then to Session listeners before returning.
func (s *Session) ExecuteBashWithUpdates(ctx context.Context, command string, excludeFromContext bool, onChunk func(string)) (BashResult, error) {
	return s.executeBash(ctx, command, excludeFromContext, onChunk, nil, nil)
}

// ExecuteBashWithOperations executes Bash through custom operations or the local shell when operations is nil. It reports each output chunk to onChunk and then to Session listeners, with id as the optional correlation identifier.
func (s *Session) ExecuteBashWithOperations(ctx context.Context, command string, excludeFromContext bool, onChunk func(string), operations extension.BashOperations, id *string) (BashResult, error) {
	return s.executeBash(ctx, command, excludeFromContext, onChunk, operations, id)
}

func (s *Session) executeBash(ctx context.Context, command string, excludeFromContext bool, onChunk func(string), operations extension.BashOperations, id *string) (BashResult, error) {
	// Runs are concurrent; each registers its cancel so AbortBash stops all
	// of them (upstream executeBash + _bashAbortControllers).
	bashCtx, bashCancel := context.WithCancel(ctx)
	s.bashMu.Lock()
	if s.bashCancels == nil {
		s.bashCancels = map[*context.CancelFunc]struct{}{}
	}
	s.bashCancels[&bashCancel] = struct{}{}
	s.bashMu.Unlock()
	defer func() {
		s.bashMu.Lock()
		delete(s.bashCancels, &bashCancel)
		s.bashMu.Unlock()
		bashCancel()
	}()

	// Upstream applies the shell command prefix (e.g. "shopt -s
	// expand_aliases") and records the command as typed.
	settings := s.services.Settings()
	resolvedCommand := command
	if prefix := settings.GetCommandPrefix(); prefix != "" {
		resolvedCommand = prefix + "\n" + command
	}
	if operations == nil {
		operations = tools.NewLocalBashOperations(settings, filepath.Join(s.services.AgentDir(), "bin"))
	}
	// The awaited executor can publish output through the Session event funnel, so release notification admission after setup rather than after Bash completion.
	invocation.Acknowledge(ctx)
	// upstream: packages/coding-agent/src/core/agent-session.ts:executeBash
	shared, err := tools.ExecuteBashWithOperations(bashCtx, resolvedCommand, s.inner.CWD(), operations, tools.BashExecOptions{OnChunk: func(delta string) {
		if onChunk != nil {
			onChunk(delta)
		}
		var eventID *string
		if id != nil {
			eventID = new(*id)
		}
		s.emitOrderedEventSync(agent.BashExecutionUpdateEvent{ID: eventID, Delta: delta})
	}})
	result := BashResult{
		Output:         shared.Output,
		ExitCode:       shared.ExitCode,
		Cancelled:      shared.Cancelled,
		Truncated:      shared.Truncated,
		FullOutputPath: shared.FullOutputPath,
	}
	if err != nil && !shared.Cancelled {
		return BashResult{}, err
	}
	return result, s.recordBashResult(command, result, excludeFromContext)
}

// pendingBashRecord captures the fields needed to materialise a
// `bashExecution` message + session entry when flushed.
type pendingBashRecord struct {
	command            string
	result             BashResult
	excludeFromContext bool
	timestamp          int64
}

// RecordBashResult records an extension-produced Bash result and returns immediate persistence failures. An active run defers persistence and owns any later flush error. Records retain their completion timestamp.
func (s *Session) RecordBashResult(command string, result BashResult, excludeFromContext bool) error {
	return s.recordBashResult(command, result, excludeFromContext)
}

// recordBashResult buffers results whenever a run is claimed, including before its first event. Otherwise it appends to the live transcript when the Session lock is available. Buffered results flush after the run's tool/result sequence.
func (s *Session) recordBashResult(command string, result BashResult, excludeFromContext bool) error {
	if result.ExitCode != nil {
		result.ExitCode = new(*result.ExitCode)
	}
	rec := pendingBashRecord{command: command, result: result, excludeFromContext: excludeFromContext, timestamp: time.Now().UnixMilli()}
	// Serialise the queue-vs-direct decision against flushPendingBashLocked
	// under pendingBashMu. TryLock never blocks, so ExecuteBash never waits
	// on an in-flight agent turn (lock order pendingBashMu -> s.mu here vs
	// s.mu -> pendingBashMu in the flush is deadlock-free because TryLock
	// cannot block).
	s.pendingBashMu.Lock()
	if !s.IsStreaming() && s.mu.TryLock() {
		s.pendingBashMu.Unlock()
		err := s.appendBashLocked(rec)
		s.mu.Unlock()
		return err
	}
	s.pendingBashMessages = append(s.pendingBashMessages, rec)
	s.pendingBashMu.Unlock()
	return nil
}

// appendBashLocked refreshes live context only after persistence succeeds. The caller holds s.mu.
func (s *Session) appendBashLocked(rec pendingBashRecord) error {
	if err := s.appendBashEntry(rec); err != nil {
		return err
	}
	s.refreshContext()
	return nil
}

func (s *Session) appendBashEntry(rec pendingBashRecord) error {
	_, err := s.inner.AppendBashExecution(icodingagent.BashExecutionMessage{
		Role: "bashExecution", Command: rec.command, Output: rec.result.Output,
		ExitCode: rec.result.ExitCode, Cancelled: rec.result.Cancelled,
		Truncated: rec.result.Truncated, FullOutputPath: rec.result.FullOutputPath,
		ExcludeFromContext: rec.excludeFromContext, Timestamp: rec.timestamp,
	})
	return err
}

// flushPendingBashLocked preserves the pending list and live context if any append fails. The caller holds s.mu. Like Pi's _flushPendingBashMessages, it refreshes context only after every append succeeds.
func (s *Session) flushPendingBashLocked() error {
	s.pendingBashMu.Lock()
	defer s.pendingBashMu.Unlock()
	if len(s.pendingBashMessages) == 0 {
		return nil
	}
	for _, rec := range s.pendingBashMessages {
		if err := s.appendBashEntry(rec); err != nil {
			return err
		}
	}
	s.pendingBashMessages = nil
	s.refreshContext()
	return nil
}

// AbortBash cancels every in-flight ExecuteBash call. Mirrors upstream
// abortBash.
func (s *Session) AbortBash() {
	s.bashMu.Lock()
	defer s.bashMu.Unlock()
	for cancel := range s.bashCancels {
		(*cancel)()
	}
}

// icodingagentGenerateSessionID is a thin alias avoiding direct symbol
// imports in the public package signature. Calls the internal
// implementation; will collapse when we promote the helper or the
// Runtime takes over construction in F4.
func icodingagentGenerateSessionID() (string, error) {
	return icodingagent.GenerateSessionID()
}

// lastAssistantMessage returns the last assistant message in msgs, or nil.
func lastAssistantMessage(msgs []agent.AgentMessage) *agent.AssistantMessage {
	for _, msg := range slices.Backward(msgs) {
		if msg.Assistant != nil {
			return msg.Assistant
		}
	}
	return nil
}

// latestCompactionTimestamp returns the Unix-millisecond timestamp of
// the latest compaction entry in the branch, or 0 if none exist.
// Mirrors upstream getLatestCompactionEntry (session-manager.ts).
func latestCompactionTimestamp(entries []icodingagent.SessionEntry) int64 {
	for _, entrie := range slices.Backward(entries) {
		if entrie.Base.Type == "compaction" {
			// SessionEntryBase.Timestamp is RFC3339Nano; convert to UnixMilli.
			t, err := time.Parse(time.RFC3339Nano, entrie.Base.Timestamp)
			if err == nil {
				return t.UnixMilli()
			}
			// Try RFC3339 without nanoseconds.
			if t, err = time.Parse(time.RFC3339, entrie.Base.Timestamp); err == nil {
				return t.UnixMilli()
			}
		}
	}
	return 0
}

// ─── Compaction & tree navigation ────────────────────────────────────────────

// NavigateTreeOptions controls Session.NavigateTree behaviour.
type NavigateTreeOptions struct {
	// Summarize, when true, generates a branch summary for the branch being
	// left before navigating. Mirrors upstream navigateTree summarize flag.
	Summarize bool
	// CustomInstructions are forwarded to the branch summarizer prompt.
	CustomInstructions string
	// ReplaceInstructions, when true, makes CustomInstructions replace the
	// default summary prompt instead of being appended to it.
	ReplaceInstructions bool
	// Label is attached to the branch summary entry, or to the target entry
	// when no summary is created.
	Label string
}

// NavigateTreeResult is the return value of Session.NavigateTree.
type NavigateTreeResult struct {
	EditorText string
	Cancelled  bool
	Aborted    bool
	// SummaryEntry is the branch_summary entry created by the navigation, or
	// nil when none was created.
	SummaryEntry *BranchSummaryEntry
}

// BranchSummaryEntry is the persisted branch_summary session entry.
type BranchSummaryEntry = icodingagent.BranchSummaryEntry

// modelCompleter collects one summarization response from the caller's stream override or the model's provider. It preserves provider errors, usage and invalid-summary diagnostics.
type modelCompleter struct {
	streamFn agent.StreamFn
}

func (c modelCompleter) CompleteSimple(
	ctx context.Context,
	model *ai.Model,
	systemPrompt string,
	messages []agent.AgentMessage,
	options ai.StreamOptions,
) (string, *ai.Usage, error) {
	llmMessages := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.User != nil:
			llmMessages = append(llmMessages, message.User.LLMMessage())
		case message.Assistant != nil:
			usage := ai.Usage{}
			if message.Assistant.Usage != nil {
				usage = *message.Assistant.Usage
			}
			llmMessages = append(llmMessages, ai.AssistantMessage{
				Content: message.Assistant.Content, API: message.Assistant.API,
				Provider: message.Assistant.Provider, Model: message.Assistant.ModelID,
				ResponseModel: message.Assistant.ResponseModel, ResponseID: message.Assistant.ResponseID,
				Diagnostics: message.Assistant.Diagnostics, Usage: usage,
				StopReason: message.Assistant.StopReason, Deferred: message.Assistant.Deferred,
				ErrorMessage: message.Assistant.ErrorMessage, RawStopReason: message.Assistant.RawStopReason,
				Timestamp: message.Assistant.Timestamp,
			})
		}
	}

	transcript := ai.NormalizeContext(ai.Context{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
	})
	var stream *ai.AssistantMessageEventStream
	var err error
	if c.streamFn != nil {
		stream, err = c.streamFn(ctx, model, transcript, options)
	} else {
		stream, err = model.Provider.Stream(ctx, transcript, options)
	}
	if err != nil {
		return "", nil, fmt.Errorf("compaction completer: stream: %w", err)
	}
	message := stream.Result()
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if message.StopReason == ai.StopReasonError {
		detail := message.ErrorMessage
		if detail == "" {
			detail = "Unknown error"
		}
		return "", nil, errors.New(detail)
	}
	if message.StopReason == ai.StopReasonLength {
		return "", nil, errors.New("generation hit the token cap and the summary is incomplete")
	}
	for _, block := range message.Content {
		if _, ok := block.(ai.ToolCall); ok {
			return "", nil, compaction.ErrSummarizationToolCall
		}
	}
	var text strings.Builder
	for _, block := range message.Content {
		if block, ok := block.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	usage := message.Usage
	return text.String(), &usage, nil
}

// resolveCompleter uses an injected completer or the Agent's current stream override, falling back to the model provider.
func (s *Session) resolveCompleter() compaction.SimpleCompleter {
	if s.completer != nil {
		return s.completer
	}
	return modelCompleter{streamFn: s.agent.StreamFunction()}
}

type sessionEventListener struct {
	id       uint64
	listener func(agent.AgentEvent)
}

// Subscribe registers a synchronous Session event listener and returns its unsubscribe function.
// Listeners run in registration order before the corresponding event is exposed on Events.
// Queue mutations notify on the caller's stack and may recursively change queues; event publication remains owned by the Session executor.
func (s *Session) Subscribe(listener func(agent.AgentEvent)) func() {
	if listener == nil {
		return func() {}
	}
	s.listenersMu.Lock()
	s.nextListenerID++
	id := s.nextListenerID
	current := s.listeners.Load()
	next := make([]sessionEventListener, 0, 1)
	if current != nil {
		next = make([]sessionEventListener, 0, len(*current)+1)
		next = append(next, (*current)...)
	}
	next = append(next, sessionEventListener{id: id, listener: listener})
	s.listeners.Store(&next)
	s.listenersMu.Unlock()
	return func() {
		s.listenersMu.Lock()
		current := s.listeners.Load()
		if current != nil {
			next := slices.DeleteFunc(slices.Clone(*current), func(entry sessionEventListener) bool { return entry.id == id })
			s.listeners.Store(&next)
		}
		s.listenersMu.Unlock()
	}
}

func (s *Session) notifyListeners(event agent.AgentEvent) {
	listeners := s.listeners.Load()
	if listeners == nil {
		return
	}
	for _, entry := range *listeners {
		entry.listener(event)
	}
}

type synchronousSessionEvent struct {
	agent.AgentEvent
	done          chan struct{}
	listenersOnly bool
	continuation  *ai.StreamContinuation
}

// emitOrderedEvent queues ev on the Session event executor after everything the turn already produced.
func (s *Session) emitOrderedEvent(ev agent.AgentEvent) {
	// rawEvents stays open; closeDone owns executor shutdown.
	select {
	case <-s.closeDone:
	case s.rawEvents <- &synchronousSessionEvent{AgentEvent: ev}:
	}
}

func (s *Session) emitOrderedEventSync(ev agent.AgentEvent) {
	s.emitSynchronousSessionEvent(ev, false)
}

// emitSynchronousSessionEvent reserves a streaming continuation before queueing it on the ordered Session worker. This is the awaited _emitExtensionEvent return boundary before public listeners and persistence.
func (s *Session) emitSynchronousSessionEvent(ev agent.AgentEvent, listenersOnly bool) {
	dispatched := &synchronousSessionEvent{AgentEvent: ev, done: make(chan struct{}), listenersOnly: listenersOnly}
	observation := agent.EventObservation(ev)
	if observation != nil {
		dispatched.continuation = observation.PrepareContinuation()
	}
	wait := func() error {
		select {
		case <-s.closeDone:
			if dispatched.continuation != nil {
				dispatched.continuation.Cancel()
			}
			return nil
		case s.rawEvents <- dispatched:
		}
		select {
		case <-s.closeDone:
			if dispatched.continuation != nil && !dispatched.continuation.Cancel() {
				// A claimed callback still owns its observation; join it instead of abandoning active execution.
				<-dispatched.done
			}
		case <-dispatched.done:
		}
		return nil
	}
	if observation != nil {
		_ = observation.AwaitContinuation(dispatched.continuation, wait)
	} else {
		_ = wait()
	}
}

// emitCompactionEvent preserves the order of compaction and agent events.
func (s *Session) emitCompactionEvent(ev agent.AgentEvent) {
	s.emitOrderedEventSync(ev)
}

// emitEvent queues a Session event behind the agent events already produced.
func (s *Session) emitEvent(ev agent.AgentEvent) { s.emitOrderedEvent(ev) }

func (s *Session) forwardAgentEvents() {
	defer func() {
		s.queueEvents.mu.Lock()
		s.queueEvents.overflow = nil
		s.queueEvents.prefix = 0
		s.queueEvents.mu.Unlock()
		s.eventsMu.Lock()
		close(s.events)
		s.eventsMu.Unlock()
	}()
	for {
		if event := s.nextQueueOverflowEvent(); event != nil {
			if !s.forwardAgentEvent(event) {
				return
			}
			continue
		}
		select {
		case <-s.closeDone:
			return
		case ev, ok := <-s.rawEvents:
			s.queueEvents.mu.Lock()
			if s.queueEvents.prefix > 0 {
				s.queueEvents.prefix--
			}
			s.queueEvents.mu.Unlock()
			if !ok || !s.forwardAgentEvent(ev) {
				return
			}
		}
	}
}

// agentEventErrorPath is the ExtensionError path for a failure raised while
// the session handles an agent event, in the style of upstream's "<boundary>".
const agentEventErrorPath = "<agent-event>"

// forwardAgentEvent executes listener delivery and wire publication in FIFO order. Agent events arrive in an awaited listener-only envelope, then in a publishedSessionEvent after persistence. Other Session-owned events use one envelope for both phases. Panics are reported and awaited producers are released.
func (s *Session) forwardAgentEvent(ev agent.AgentEvent) (open bool) {
	if dispatched, ok := ev.(*synchronousSessionEvent); ok && dispatched.continuation != nil {
		open = true
		err := dispatched.continuation.Run(func(observation *ai.StreamObservation) error {
			inner := *dispatched
			// The awaited extension step has passed; listeners read the stream state at this tick, as a JavaScript listener does.
			inner.AgentEvent = agent.RefreshEvent(agent.WithEventObservation(inner.AgentEvent, observation))
			inner.continuation = nil
			open = s.forwardAgentEvent(&inner)
			return nil
		})
		if err != nil {
			// A canceled reservation did not enter the callback that normally releases the waiting producer.
			close(dispatched.done)
			if !errors.Is(err, context.Canceled) {
				s.reportAgentEventPanic(ev, err)
			}
		}
		return open
	}
	var synchronous *synchronousSessionEvent
	// Raw Agent events already reached listeners on the producer's awaited hook.
	notified := true
	defer func() {
		if recovered := recover(); recovered != nil {
			s.reportAgentEventPanic(ev, recovered)
			if !notified {
				s.notifyOnEventExecutor(ev)
			}
			open = true
		}
		if synchronous != nil && synchronous.done != nil {
			close(synchronous.done)
		}
	}()
	if _, ok := ev.(*sessionEventBarrier); ok {
		return s.sendEvent(ev)
	}
	if value, ok := ev.(*synchronousSessionEvent); ok {
		synchronous = value
		ev = value.AgentEvent
		notified = false
	}
	if value, ok := ev.(publishedSessionEvent); ok {
		ev = value.AgentEvent
		synchronous = nil
		notified = true
	}
	listenersOnly := synchronous != nil && synchronous.listenersOnly
	if end, ok := ev.(agent.AgentEndEvent); ok {
		end.WillRetry = !s.runState.abortRequested.Load() && lastAssistantMessage(end.Messages) != nil && s.lastAssistantEnd.willRetry
		ev = end
	}
	var retryEnded int32
	if end, ok := ev.(agent.MessageEndEvent); ok && end.Message.Assistant != nil && !listenersOnly {
		s.lastAssistantEnd = s.popAssistantEnd()
		retryEnded = s.lastAssistantEnd.retryEnded
	}
	if listenersOnly {
		notified = true
		s.notifyOnEventExecutor(ev)
		return true
	}
	if synchronous != nil {
		notified = true
		s.notifyOnEventExecutor(ev)
		if synchronous.done != nil {
			close(synchronous.done)
			synchronous = nil
		}
	}
	if !s.sendEvent(ev) {
		return false
	}
	if retryEnded > 0 {
		event := agent.AutoRetryEndEvent{Success: true, Attempt: int(retryEnded)}
		s.notifyOnEventExecutor(event)
		return s.sendEvent(event)
	}
	return true
}

// dispatchSessionNameEvent admits the handler's synchronous prefix on the initiating call, without awaiting suspended extension work. The Session cancels and joins the emissions on Close.
func (s *Session) dispatchSessionNameEvent(event agent.SessionInfoChangedEvent) {
	runner := s.currentRunner()
	if runner == nil {
		return
	}
	invoked := make(chan struct{})
	ctx := invocation.WithAcknowledgment(s.backgroundContext(), func() { close(invoked) })
	if err := s.startExtensionTask(func() {
		defer invocation.Acknowledge(ctx)
		_, err := runner.Emit(ctx, extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: event.Name})
		s.reportRuntimeError("session_info_changed", ignoreCancellation(err))
	}); err != nil {
		return // Session shutdown rejects new notifications.
	}
	select {
	case <-invoked:
	case <-ctx.Done():
	}
}

// dispatchAgentEventToExtensions delivers ev to extensions. A panic during
// dispatch is reported and the event still reaches the session's listeners,
// as upstream's runner reports a failing handler and continues.
func (s *Session) dispatchAgentEventToExtensions(ev agent.AgentEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.reportAgentEventPanic(ev, recovered)
		}
	}()
	icodingagent.DispatchAgentLoopEvent(s.currentRunner(), ev, &s.extCurrentMessage)
}

// notifyAgentEventListeners delivers ev to the session's subscribers; a
// listener panic is reported and does not skip the event's forwarding.
func (s *Session) notifyAgentEventListeners(ev agent.AgentEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.reportAgentEventPanic(ev, recovered)
		}
	}()
	s.notifyListeners(ev)
}

// reportAgentEventPanic surfaces a recovered panic through the extension
// error listeners, which the modes display (upstream interactive
// showExtensionError). With no runner, or when a listener itself panics while
// reporting, it goes to stderr like print mode's listener.
func (s *Session) reportAgentEventPanic(ev agent.AgentEvent, recovered any) {
	message := fmt.Sprintf("panic: %v", recovered)
	report := &extension.ExtensionError{
		ExtensionPath: agentEventErrorPath,
		Event:         icodingagent.AgentLoopEventType(ev),
		Error:         message,
		Stack:         message + "\n" + string(debug.Stack()),
	}
	if runner := s.currentRunner(); runner != nil && emitExtensionError(runner, report) {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "Extension error (%s): %s\n", report.ExtensionPath, report.Error)
}

// emitExtensionError reports err to runner's listeners; ok is false when a
// listener panicked, which may be the failure being reported.
func emitExtensionError(runner *inproc.Runner, err *extension.ExtensionError) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	runner.EmitError(err)
	return true
}

// sendEvent forwards ev to the session's event channel, reporting false once
// the session is closing.
func (s *Session) sendEvent(ev agent.AgentEvent) bool {
	select {
	case <-s.closeDone:
		return false
	case s.events <- ev:
		return true
	}
}

// willRetryAfterAgentEnd reports whether post-run handling will retry an
// assistant message that ends a run (agent-session.ts
// _willRetryAfterAgentEnd). It must run before post-run handling commits the
// next retry attempt.
func (s *Session) willRetryAfterAgentEnd(message *agent.AssistantMessage) bool {
	if s.services == nil || s.services.SettingsManager() == nil {
		return false
	}
	retryCfg := s.services.SettingsManager().GetRetrySettings()
	if !retryCfg.Enabled || int(s.retryAttempt.Load()) >= retryCfg.MaxRetries {
		return false
	}
	return icodingagent.IsRetryableError(message, s.contextWindow())
}

// CompactionResult is the result returned by manual compaction.
type CompactionResult struct {
	Summary              string
	FirstKeptEntryID     string
	TokensBefore         int
	EstimatedTokensAfter int
	Usage                *ai.Usage
	Details              any
}

// Compact runs manual compaction. It preserves the historical SDK behavior
// that reports an already-small Session through compaction_end without
// returning an error.
func (s *Session) Compact(ctx context.Context, customInstructions string) error {
	_, err := s.compact(ctx, customInstructions)
	if err != nil && (strings.Contains(err.Error(), "Nothing to compact") || strings.Contains(err.Error(), "Already compacted")) {
		return nil
	}
	return err
}

// CompactForExtension runs an extension's ctx.compact({ onComplete, onError })
// and returns the result in upstream CompactionResult's JSON shape. Unlike
// Compact, a session with nothing to compact is an error: upstream's
// compact() rejects, and ctx.compact reports that through onError.
func (s *Session) CompactForExtension(ctx context.Context, customInstructions string) (any, error) {
	result, err := s.compact(ctx, customInstructions)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"summary":              result.Summary,
		"firstKeptEntryId":     result.FirstKeptEntryID,
		"tokensBefore":         result.TokensBefore,
		"estimatedTokensAfter": result.EstimatedTokensAfter,
	}
	if result.Usage != nil {
		out["usage"] = result.Usage
	}
	if result.Details != nil {
		out["details"] = result.Details
	}
	return out, nil
}

// CompactResult runs manual compaction and returns the generated result.
func (s *Session) CompactResult(ctx context.Context, customInstructions string) (*CompactionResult, error) {
	return s.compact(ctx, customInstructions)
}

func (s *Session) compact(ctx context.Context, customInstructions string) (*CompactionResult, error) {
	// Manual compaction aborts the active run first and never continues it
	// (upstream compact() awaits abort()).
	if err := s.Abort(ctx); err != nil {
		return nil, err
	}
	if err := s.beginManualCompaction(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	entries := s.currentBranch()
	s.mu.Unlock()

	compactCtx, cancel := context.WithCancel(ctx)
	s.compactMu.Lock()
	s.compactCancel = cancel
	abortRequested := s.compactAbortRequested
	s.compactMu.Unlock()
	if abortRequested {
		cancel()
	}
	finish := sync.OnceFunc(func() {
		cancel()
		s.compactMu.Lock()
		s.compactCancel = nil
		s.compactMu.Unlock()
		s.finishCompaction()
	})
	defer finish()

	fromExtension := false
	fail := func(err error, aborted bool) (*CompactionResult, error) {
		errorMessage := ""
		if !aborted {
			errorMessage = "Compaction failed: " + err.Error()
		}
		// Manual compaction is idle before compaction_end listeners can submit another prompt.
		finish()
		s.emitOrderedEventSync(agent.CompactionEndEvent{Reason: "manual", Aborted: aborted, ErrorMessage: errorMessage})
		s.emitSessionCompactFailed(ctx, extension.SessionCompactFailedEvent{Reason: "manual", ErrorMessage: errorMessage, Aborted: aborted, FromExtension: fromExtension})
		return nil, err
	}

	s.emitCompactionEvent(agent.CompactionStartEvent{Reason: "manual"})
	model := s.Model()
	if model == nil {
		return fail(errors.New(icodingagent.FormatNoModelSelectedMessage()), false)
	}
	settings, settingsErr := s.compactionSettings()
	if settingsErr != nil {
		return fail(settingsErr, false)
	}
	if compactCtx.Err() != nil {
		return fail(errCompactionCancelled, true)
	}
	request, authErr := s.prepareSummarizationRequest(compactCtx, model)
	if authErr != nil {
		return fail(authErr, compactCtx.Err() != nil)
	}
	if compactCtx.Err() != nil {
		return fail(errCompactionCancelled, true)
	}

	prep := compaction.PrepareCompaction(entries, settings)
	if prep == nil {
		reasonMsg := "Nothing to compact (session too small)"
		if n := len(entries); n > 0 && entries[n-1].Base.Type == "compaction" {
			reasonMsg = "Already compacted"
		}
		return fail(errors.New(reasonMsg), false)
	}

	result, fromExt, err := s.extensionCompaction(compactCtx, prep, entries, customInstructions, "manual", false)
	fromExtension = fromExt
	cancelledByExtension := isCompactionCancelled(err)
	if err == nil && result == nil {
		generated, compactErr := compaction.Compact(compactCtx, *prep, request.model, request.completer, request.streamFn, customInstructions, s.ThinkingLevel(), s.summarizationRetryOptions("compaction", "manual"), "")
		err = compactErr
		if compactErr == nil {
			result = generatedCompactionData(generated)
		}
	}
	if compactCtx.Err() != nil {
		err = errCompactionCancelled
	}
	if err != nil {
		return fail(err, compactCtx.Err() != nil || cancelledByExtension)
	}

	s.mu.Lock()
	entryID, err := s.inner.AppendCompaction(result.Summary, result.FirstKeptEntryID, result.TokensBefore, result.Details, fromExtension, result.Usage)
	if err != nil {
		s.mu.Unlock()
		return fail(fmt.Errorf("coding: Compact: persist: %w", err), false)
	}
	s.refreshContext()
	estimatedTokensAfter := estimateMessagesTokens(s.agent.Messages())
	entry, haveEntry := s.inner.EntryByID(entryID)
	s.mu.Unlock()

	if haveEntry {
		s.emitSessionCompact(compactCtx, entry, fromExtension, "manual", false)
	}

	finish()
	s.emitCompactionEvent(agent.CompactionEndEvent{
		Reason:               "manual",
		Summary:              result.Summary,
		FirstKeptEntryID:     result.FirstKeptEntryID,
		TokensBefore:         result.TokensBefore,
		EstimatedTokensAfter: estimatedTokensAfter,
		Usage:                result.Usage,
		Details:              result.Details,
	})
	return &CompactionResult{
		Summary:              result.Summary,
		FirstKeptEntryID:     result.FirstKeptEntryID,
		TokensBefore:         result.TokensBefore,
		EstimatedTokensAfter: estimatedTokensAfter,
		Usage:                result.Usage,
		Details:              result.Details,
	}, nil
}

var errCompactionCancelled = errors.New("Compaction cancelled")

// isCompactionCancelled reports whether a session_before_compact handler
// cancelled the compaction.
func isCompactionCancelled(err error) bool {
	return err != nil && err.Error() == errCompactionCancelled.Error()
}

// AbortCompaction cancels an in-flight Compact() call. Safe to call when
// no compaction is running (no-op). Mirrors upstream abortCompaction().
func (s *Session) AbortCompaction() {
	s.compactMu.Lock()
	cancel := s.compactCancel
	s.compactMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsCompacting reports whether compaction or tree navigation is currently in flight.
func (s *Session) IsCompacting() bool {
	s.compactMu.Lock()
	defer s.compactMu.Unlock()
	return s.compacting.Load() || s.branchSumCancel != nil
}

func (s *Session) isManualCompacting() bool {
	s.compactMu.Lock()
	defer s.compactMu.Unlock()
	return s.manualCompacting
}

func (s *Session) beginCompaction() bool {
	s.compactMu.Lock()
	defer s.compactMu.Unlock()
	if s.compacting.Load() || s.branchSumCancel != nil {
		return false
	}
	s.compacting.Store(true)
	s.compactDone = make(chan struct{})
	s.compactAbortRequested = false
	return true
}

// beginManualCompaction mirrors compact() calling abort() before it claims the
// compaction controller: an active tree navigation is cancelled and allowed to
// release its branch-summary controller before manual compaction starts.
func (s *Session) beginManualCompaction(ctx context.Context) error {
	for {
		s.compactMu.Lock()
		if s.compacting.Load() {
			cancel := s.compactCancel
			done := s.compactDone
			if cancel == nil {
				s.compactAbortRequested = true
			}
			s.compactMu.Unlock()
			if cancel != nil {
				cancel()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
			}
			continue
		}
		cancel := s.branchSumCancel
		done := s.branchSumDone
		if cancel == nil {
			s.manualCompacting = true
			s.compacting.Store(true)
			s.compactDone = make(chan struct{})
			s.compactAbortRequested = false
			s.compactMu.Unlock()
			return nil
		}
		s.compactMu.Unlock()

		cancel()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
}

func (s *Session) finishCompaction() {
	s.compactMu.Lock()
	s.compacting.Store(false)
	s.manualCompacting = false
	done := s.compactDone
	s.compactDone = nil
	s.compactAbortRequested = false
	if done != nil {
		close(done)
	}
	s.compactMu.Unlock()
	s.notifyIdleWaiters()
}

const treeNavigationInProgressError = "Wait for the current compaction or tree navigation to finish before navigating the session tree."

// navigateTree is the internal implementation. NavigateTree (public SDK) and
// NavigateTreeHandle (InteractiveSessionHandle bridge) both call this.
func (s *Session) navigateTree(ctx context.Context, targetID string, opts NavigateTreeOptions) (NavigateTreeResult, error) {
	if s.agent.IsStreaming() {
		return NavigateTreeResult{}, errors.New("Wait for the current response to finish before navigating the session tree.")
	}

	branchCtx, cancel := context.WithCancel(ctx)
	s.compactMu.Lock()
	if s.compacting.Load() || s.branchSumCancel != nil {
		s.compactMu.Unlock()
		cancel()
		return NavigateTreeResult{}, errors.New(treeNavigationInProgressError)
	}
	done := make(chan struct{})
	s.branchSumCancel = cancel
	s.branchSumDone = done
	s.compactMu.Unlock()
	// Navigation now owns the Session operation. Promise-shaped extension
	// callers may release their ordered initiation lane while this admitted
	// operation continues asynchronously.
	extension.CallInitiated(ctx)
	defer func() {
		cancel()
		s.compactMu.Lock()
		s.branchSumCancel = nil
		s.branchSumDone = nil
		close(done)
		s.compactMu.Unlock()
		s.notifyIdleWaiters()
	}()

	// Snapshot current leaf under lock.
	s.mu.Lock()
	oldLeafID := s.inner.LeafID()
	s.mu.Unlock()

	// No-op if already at target.
	if oldLeafID != nil && *oldLeafID == targetID {
		return NavigateTreeResult{}, nil
	}

	if opts.Summarize && s.Model() == nil {
		return NavigateTreeResult{}, errors.New("No model available for summarization")
	}
	targetEntry, entryFound := s.inner.EntryByID(targetID)
	if !entryFound {
		return NavigateTreeResult{}, fmt.Errorf("Entry %s not found", targetID)
	}

	// Collect entries to summarize (from old leaf to common ancestor).
	collected := compaction.CollectEntriesForBranchSummary(s.inner, derefLeafID(oldLeafID), targetID)

	// Extensions may override the instructions and label.
	customInstructions := opts.CustomInstructions
	replaceInstructions := opts.ReplaceInstructions
	label := opts.Label
	preparation := &TreePreparation{
		TargetID:            targetID,
		OldLeafID:           oldLeafID,
		EntriesToSummarize:  collected.Entries,
		UserWantsSummary:    opts.Summarize,
		CustomInstructions:  customInstructions,
		ReplaceInstructions: replaceInstructions,
		Label:               label,
	}
	if preparation.EntriesToSummarize == nil {
		preparation.EntriesToSummarize = []icodingagent.SessionEntry{}
	}
	if collected.CommonAncestorID != "" {
		preparation.CommonAncestorID = &collected.CommonAncestorID
	}

	var summary *treeBranchSummary
	before, err := s.emitSessionBeforeTree(branchCtx, preparation)
	if err != nil {
		return NavigateTreeResult{}, err
	}
	if before != nil {
		if before.Cancel {
			return NavigateTreeResult{Cancelled: true}, nil
		}
		if before.Summary != nil && opts.Summarize {
			if summary, err = extensionTreeSummary(before); err != nil {
				return NavigateTreeResult{}, err
			}
		}
		if before.CustomInstructions != nil {
			customInstructions = *before.CustomInstructions
		}
		if before.ReplaceInstructions != nil {
			replaceInstructions = *before.ReplaceInstructions
		}
		if before.Label != nil {
			label = *before.Label
		}
	}

	if opts.Summarize && len(collected.Entries) > 0 && summary == nil {
		request, authErr := s.prepareSummarizationRequest(branchCtx, s.Model())
		if authErr != nil {
			return NavigateTreeResult{}, authErr
		}
		bsResult := compaction.GenerateBranchSummary(branchCtx, collected.Entries, compaction.GenerateBranchSummaryOptions{
			Model:               request.model,
			Completer:           request.completer,
			StreamFn:            request.streamFn,
			CustomInstructions:  customInstructions,
			ReplaceInstructions: replaceInstructions,
			ReserveTokens:       s.services.SettingsManager().GetBranchSummarySettings().ReserveTokens,
			Retry:               s.summarizationRetryOptions("branchSummary", ""),
		})
		if bsResult.Aborted {
			return NavigateTreeResult{Cancelled: true, Aborted: true}, nil
		}
		if bsResult.Error != "" {
			return NavigateTreeResult{}, fmt.Errorf("coding: NavigateTree: branch summary: %s", bsResult.Error)
		}
		summary = &treeBranchSummary{
			Summary: bsResult.Summary,
			Details: compaction.BranchSummaryDetails{ReadFiles: bsResult.ReadFiles, ModifiedFiles: bsResult.ModifiedFiles},
			Usage:   bsResult.Usage,
		}
	}

	// Determine the new leaf position based on the target type.
	newLeafID, editorText := treeNavigationTarget(targetEntry)

	s.mu.Lock()
	summaryID, err := s.moveTreeLeaf(targetID, newLeafID, summary, label)
	if err != nil {
		s.mu.Unlock()
		return NavigateTreeResult{}, fmt.Errorf("coding: NavigateTree: %w", err)
	}
	s.refreshContext()
	s.restoreToolsFromTranscript()
	currentLeafID := s.inner.LeafID()
	s.mu.Unlock()

	s.emitSessionTree(ctx, currentLeafID, oldLeafID, summaryID, summary != nil && summary.FromExtension)
	return NavigateTreeResult{EditorText: editorText, SummaryEntry: s.branchSummaryEntry(summaryID)}, nil
}

// AbortBranchSummary cancels an in-flight branch-summary LLM call within
// NavigateTree. Safe to call when no summary is running. Mirrors upstream
// abortBranchSummary().
func (s *Session) AbortBranchSummary() {
	s.compactMu.Lock()
	cancel := s.branchSumCancel
	s.compactMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ─── Auto-compaction (3.2g) ──────────────────────────────────────────────────

// isRetryableCompactionError classifies a summarization error message with
// pi-ai's isRetryableAssistantError, which upstream retryAssistantCall applies
// to the failed summarization response.
func isRetryableCompactionError(errMsg string) bool {
	return ai.IsRetryableAssistantError(ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: errMsg})
}

// summarizationRetryOptions builds compaction retry options from the current
// retry settings, wiring callbacks to emit summarization_retry_* events. source
// is "compaction" or "branchSummary"; reason ("manual"|"threshold"|"overflow")
// applies to compaction. Mirrors upstream _summarizationRetryCallbacks.
func (s *Session) summarizationRetryOptions(source, reason string) *compaction.RetryOptions {
	cfg := s.services.SettingsManager().GetRetrySettings()
	return &compaction.RetryOptions{
		Policy: compaction.RetryPolicy{
			Enabled:         cfg.Enabled,
			MaxRetries:      cfg.MaxRetries,
			BaseDelayMs:     cfg.BaseDelayMs,
			MaxAgentDelayMs: &cfg.MaxDelayMs,
		},
		IsRetryable: isRetryableCompactionError,
		Callbacks: compaction.RetryCallbacks{
			OnRetryScheduled: func(attempt, maxAttempts, delayMs int, errMsg string) {
				s.emitEvent(agent.SummarizationRetryScheduledEvent{
					Attempt:      attempt,
					MaxAttempts:  maxAttempts,
					DelayMs:      delayMs,
					ErrorMessage: errMsg,
				})
			},
			OnRetryAttemptStart: func() {
				s.emitEvent(agent.SummarizationRetryAttemptStartEvent{Source: source, Reason: reason})
			},
			OnRetryFinished: func() {
				s.emitEvent(agent.SummarizationRetryFinishedEvent{})
			},
		},
	}
}

// checkCompaction dispatches automatic compaction after a run or before a
// prompt and reports whether the run should continue (agent-session.ts
// _checkCompaction):
//
//  1. an overflow error or recoverable length stop omits the failed attempt
//     and its tool results, compacts, and retries once;
//  2. a successful response over the window compacts without retrying;
//  3. usage over the threshold compacts without retrying.
//
// Usage measured before a later context edit or compaction does not count.
// The caller serializes with the agent loop.
func (s *Session) checkCompaction(ctx context.Context, assistantMsg *agent.AssistantMessage, skipAbortedCheck bool, toolResults []agent.AgentMessage) (bool, error) {
	return s.checkCompactionDecision(ctx, assistantMsg, skipAbortedCheck, toolResults, s.autoCompactAndDecide)
}

// checkCompactionDecision resolves recovery and threshold policy before delegating the admitted operation.
func (s *Session) checkCompactionDecision(ctx context.Context, assistantMsg *agent.AssistantMessage, skipAbortedCheck bool, toolResults []agent.AgentMessage, compact func(context.Context, string, bool) (bool, error)) (bool, error) {
	model := s.Model()
	settings, err := s.compactionSettings()
	if err != nil {
		return false, err
	}
	if !settings.Enabled {
		return false, nil
	}
	if skipAbortedCheck && assistantMsg.StopReason == ai.StopReasonAborted {
		return false, nil
	}
	contextWindow := 0
	if model != nil {
		contextWindow = model.Capabilities.ContextWindow
	}
	// An overflow from another model (the user switched to a larger window)
	// does not apply to the current model.
	sameModel := model != nil && model.Provider != nil &&
		assistantMsg.Provider == model.Provider.ID() &&
		assistantMsg.ModelID == model.ID

	branch := s.currentBranch()
	// A message from before the latest compaction carries stale usage.
	latestCompTS := latestCompactionTimestamp(branch)
	hasCompaction := slices.ContainsFunc(branch, func(entry icodingagent.SessionEntry) bool { return entry.Base.Type == "compaction" })
	if hasCompaction && assistantMsg.Timestamp <= latestCompTS {
		return false, nil
	}

	projection := s.inner.BuildSessionProjection()
	recovery := s.assistantRecoveryState(assistantMsg, projection, branch)
	explicitOverflow := assistantMsg.StopReason == ai.StopReasonError && icodingagent.IsContextOverflow(assistantMsg, 0)
	contextOverflow := sameModel && ((explicitOverflow && recovery.retainedForExplicitRecovery) ||
		(recovery.usageMatchesProjection && icodingagent.IsContextOverflow(assistantMsg, contextWindow)))
	recoverableLength := sameModel && recovery.projected && icodingagent.IsRecoverableLength(assistantMsg, model.Capabilities.MaxOutputTokens)
	if contextOverflow || recoverableLength {
		willRetry := assistantMsg.StopReason != ai.StopReasonStop
		if !willRetry {
			return compact(ctx, "overflow", false)
		}
		if s.overflowRecoveryAttempted.Load() {
			errorMessage := "Truncated response recovery failed after one compact-and-retry attempt."
			if contextOverflow {
				errorMessage = "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model."
			}
			s.emitOrderedEventSync(agent.CompactionEndEvent{Reason: "overflow", ErrorMessage: errorMessage})
			s.emitSessionCompactFailed(ctx, extension.SessionCompactFailedEvent{Reason: "overflow", ErrorMessage: errorMessage})
			return false, nil
		}
		s.overflowRecoveryAttempted.Store(true)
		if err := s.omitRecoveryAttempt(assistantMsg, toolResults); err != nil {
			return false, err
		}
		return compact(ctx, "overflow", willRetry)
	}

	var contextTokens int
	directContextTokens := 0
	if assistantMsg.Usage != nil {
		directContextTokens = agent.CalculateContextTokens(*assistantMsg.Usage)
	}
	hasContextEdits := slices.ContainsFunc(projection.Entries, func(entry icodingagent.ProjectedSessionEntry) bool {
		return entry.SourceEntry.Base.Type == "context_edit"
	})
	switch {
	case hasContextEdits:
		contextTokens = compaction.EstimateProjectedContextTokens(projection, branch).Tokens
	case assistantMsg.StopReason == ai.StopReasonError || directContextTokens == 0:
		// Estimate from the last valid response so persistent API errors and
		// zero-usage responses still compact. Only a usage-backed estimate
		// needs the stale pre-compaction check.
		messages := s.agent.Messages()
		estimate := compaction.EstimateContextTokens(messages)
		if estimate.LastUsageIndex >= 0 && hasCompaction {
			if usage := messages[estimate.LastUsageIndex].Assistant; usage != nil && usage.Timestamp <= latestCompTS {
				return false, nil
			}
		}
		contextTokens = estimate.Tokens
	default:
		contextTokens = directContextTokens
	}
	if compaction.ShouldCompact(contextTokens, contextWindow, settings) {
		return compact(ctx, "threshold", false)
	}
	return false, nil
}

// assistantRecovery describes how a checked assistant message relates to the
// current projection and to entries appended after it.
type assistantRecovery struct {
	// projected reports that the assistant still contributes to the
	// projection (or has no resolvable entry).
	projected bool
	// usageMatchesProjection reports that no context edit follows the
	// assistant, so its usage still measures the projected context.
	usageMatchesProjection bool
	// retainedForExplicitRecovery reports that no later compaction or omission
	// of the assistant has already handled its explicit overflow error.
	retainedForExplicitRecovery bool
}

func (s *Session) assistantRecoveryState(assistantMsg *agent.AssistantMessage, projection icodingagent.SessionProjection, branch []icodingagent.SessionEntry) assistantRecovery {
	entryID, resolved := s.findPersistedMessageEntryID(agent.AgentMessage{Assistant: assistantMsg})
	if !resolved {
		return assistantRecovery{projected: true, usageMatchesProjection: true, retainedForExplicitRecovery: true}
	}
	projected := slices.ContainsFunc(projection.Entries, func(entry icodingagent.ProjectedSessionEntry) bool {
		return entry.SourceEntry.Base.ID == entryID && slices.ContainsFunc(entry.Messages, func(message agent.AgentMessage) bool { return message.Assistant != nil })
	})
	var after []icodingagent.SessionEntry
	if index := slices.IndexFunc(branch, func(entry icodingagent.SessionEntry) bool { return entry.Base.ID == entryID }); index >= 0 {
		after = branch[index+1:]
	}
	postAssistantEdit, compactedAfter, latestEditOmits := false, false, false
	for _, entry := range after {
		switch entry.Base.Type {
		case "compaction":
			compactedAfter = true
		case "context_edit":
			postAssistantEdit = true
			var edit icodingagent.ContextEditEntry
			if json.Unmarshal(entry.Raw(), &edit) == nil && edit.TargetID == entryID {
				latestEditOmits = edit.Replacement == nil
			}
		}
	}
	return assistantRecovery{
		projected:                   projected,
		usageMatchesProjection:      projected && !postAssistantEdit,
		retainedForExplicitRecovery: !compactedAfter && !latestEditOmits,
	}
}

// autoCompactAndDecide runs automatic compaction and returns Pi's
// continuation decision: retry an interrupted run, or deliver messages that
// queued during compaction.
func (s *Session) autoCompactAndDecide(ctx context.Context, reason string, willRetry bool) (bool, error) {
	compacted, err := s.runAutoCompaction(ctx, reason, willRetry)
	if err != nil || !compacted {
		return false, err
	}
	return willRetry || s.agent.HasQueuedMessages(), nil
}

// runAutoCompaction performs threshold or overflow compaction and reports
// whether it appended a compaction entry (agent-session.ts
// _runAutoCompaction). Nothing to compact returns false without events. A
// failed, aborted, or cancelled compaction notifies compaction_end listeners
// before awaiting session_compact_failed handlers, then returns false. The caller serializes with the
// agent loop.
func (s *Session) runAutoCompaction(ctx context.Context, reason string, willRetry bool) (bool, error) {
	model := s.Model()
	settings, err := s.compactionSettings()
	if err != nil {
		return false, err
	}
	if !s.beginCompaction() {
		return false, nil
	}
	defer s.finishCompaction()
	if model == nil {
		return false, nil
	}

	entries := s.currentBranch()
	prep := compaction.PrepareCompaction(entries, settings)
	if prep == nil {
		return false, nil
	}

	compactCtx, cancel := context.WithCancel(ctx)
	s.compactMu.Lock()
	s.compactCancel = cancel
	abortRequested := s.compactAbortRequested
	s.compactMu.Unlock()
	if abortRequested {
		cancel()
	}
	defer func() {
		cancel()
		s.compactMu.Lock()
		s.compactCancel = nil
		s.compactMu.Unlock()
	}()

	s.emitCompactionEvent(agent.CompactionStartEvent{Reason: reason})
	if compactCtx.Err() != nil {
		s.emitOrderedEventSync(agent.CompactionEndEvent{Reason: reason, Aborted: true})
		s.emitSessionCompactFailed(ctx, extension.SessionCompactFailedEvent{Reason: reason, Aborted: true})
		return false, nil
	}

	request, err := s.prepareSummarizationRequest(compactCtx, model)
	var result *sessionCompactionData
	fromExtension := false
	if err == nil {
		result, fromExtension, err = s.extensionCompaction(compactCtx, prep, entries, "", reason, willRetry)
	}
	cancelledByExtension := isCompactionCancelled(err)
	if err == nil && result == nil {
		generated, compactErr := compaction.Compact(compactCtx, *prep, request.model, request.completer, request.streamFn, "", s.ThinkingLevel(), s.summarizationRetryOptions("compaction", reason), "")
		err = compactErr
		if compactErr == nil {
			result = generatedCompactionData(generated)
		}
	}
	if err == nil && compactCtx.Err() != nil {
		err = errCompactionCancelled
	}
	var entryID string
	if err == nil {
		entryID, err = s.inner.AppendCompaction(result.Summary, result.FirstKeptEntryID, result.TokensBefore, result.Details, fromExtension, result.Usage)
	}
	if err != nil {
		aborted := compactCtx.Err() != nil || cancelledByExtension
		errorMessage := ""
		if !aborted {
			errorMessage = "Auto-compaction failed: " + err.Error()
			if reason == "overflow" {
				errorMessage = "Context overflow recovery failed: " + err.Error()
			}
		}
		s.emitOrderedEventSync(agent.CompactionEndEvent{Reason: reason, Aborted: aborted, ErrorMessage: errorMessage})
		s.emitSessionCompactFailed(ctx, extension.SessionCompactFailedEvent{Reason: reason, ErrorMessage: errorMessage, Aborted: aborted, FromExtension: fromExtension})
		return false, nil
	}

	s.refreshContext()
	estimatedTokensAfter := estimateMessagesTokens(s.agent.Messages())
	if entry, haveEntry := s.inner.EntryByID(entryID); haveEntry {
		s.emitSessionCompact(compactCtx, entry, fromExtension, reason, willRetry)
	}
	s.emitCompactionEvent(agent.CompactionEndEvent{
		Reason:               reason,
		Summary:              result.Summary,
		FirstKeptEntryID:     result.FirstKeptEntryID,
		TokensBefore:         result.TokensBefore,
		EstimatedTokensAfter: estimatedTokensAfter,
		Usage:                result.Usage,
		Details:              result.Details,
		WillRetry:            willRetry,
	})
	return true, nil
}

// NavigateTree forks the session to targetID, optionally generating a branch
// summary. This is the public SDK entry point (takes NavigateTreeOptions struct).
// The InteractiveSessionHandle bridge is NavigateTreeHandle below.
//
// Mirrors upstream AgentSession.navigateTree() (agent-session.ts).
func (s *Session) NavigateTree(ctx context.Context, targetID string, opts NavigateTreeOptions) (NavigateTreeResult, error) {
	return s.navigateTree(ctx, targetID, opts)
}

// NavigateTreeHandle implements the icodingagent.InteractiveSessionHandle interface.
// Bridges to navigateTree using flat args (summarize bool, customInstructions string)
// instead of NavigateTreeOptions struct, matching the interface signature.
// Returns icodingagent.NavigateTreeResult (structurally identical to coding.NavigateTreeResult).
func (s *Session) NavigateTreeHandle(ctx context.Context, targetID string, summarize bool, customInstructions string) (icodingagent.NavigateTreeResult, error) {
	res, err := s.navigateTree(ctx, targetID, NavigateTreeOptions{
		Summarize:          summarize,
		CustomInstructions: customInstructions,
	})
	return icodingagent.NavigateTreeResult{
		EditorText: res.EditorText,
		Cancelled:  res.Cancelled,
		Aborted:    res.Aborted,
	}, err
}
