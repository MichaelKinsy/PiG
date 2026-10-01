package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/term"

	pig "github.com/MichaelKinsy/PiG"
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/tui"
)

// SubprocessUIBridge is the interface satisfied by *subprocess.UIBridge
// for wiring subprocess extension UI after TUI creation.
//
// pig-specific: no upstream equivalent.
type SubprocessUIBridge interface {
	SetInvalidate(fn func())
	SetNotifyFunc(fn func(message, level string))
	SetUIContext(ctx extension.UIContext)
	// SetUIPromptScope binds the runner that reports subprocess ui_prompt_start
	// and ui_prompt_end events.
	SetUIPromptScope(scope subprocess.UIPromptScope)
	// SetHostAction sets a named host callback. Known keys:
	// "getFlag", "getActiveTools", "getAllTools", "getCommands", "getThinkingLevel",
	// "setThinkingLevel", "getContextUsage", "getSystemPrompt", "isIdle",
	// "abort", "hasPendingMessages", "shutdown", "waitForIdle", "reload",
	// "sendUserMessage", "getSessionName", "setSessionName", "setLabel".
	SetHostAction(key string, fn any)
	PublishModelCatalog()
	// SetWidgetSyncFunc sets a callback invoked whenever an extension sets
	// or clears a widget. The callback receives all current widget proxies
	// as *subprocess.PushProxy values (keyed by "extName:widgetKey").
	SetWidgetSyncFunc(fn func(widgets map[string]*subprocess.PushProxy))
}

// SubprocessHost is the interface for managing subprocess extension lifecycle
// during reload. Satisfied by *subprocess.Host.
//
// pig-specific: no upstream equivalent.
type SubprocessHost interface {
	Reload(ctx context.Context) ([]extension.Extension, error)
	ExtensionCount() int
	LastReloadReport() *subprocess.ReloadReport
	LoadErrors() []string
	SetWidthFunc(fn func() int)
	SetHeightFunc(fn func() int)
	NotifyWidth(width int)
	NotifyHeight(height int)
	SetCrashHandler(fn func(name string, delay time.Duration, disabled bool, reason string))
	IsShuttingDown() bool
}

type extensionDialog struct {
	component tui.Component
	handle    func(data string)
}

type expandableCustomMessageComponent interface {
	tui.Component
	SetExpanded(bool)
}

type rerenderingCustomMessageComponent struct {
	renderer  extension.MessageRenderer
	message   extension.CustomMessageRef
	expanded  bool
	outputPad int
	component tui.Component
	fallback  *tui.CustomMessageComponent
}

func (c *rerenderingCustomMessageComponent) Render(width int) []string {
	return c.component.Render(width)
}

func (c *rerenderingCustomMessageComponent) Invalidate() { c.component.Invalidate() }

func (c *rerenderingCustomMessageComponent) SetExpanded(expanded bool) {
	if c.expanded == expanded {
		return
	}
	c.expanded = expanded
	c.rebuild()
}

func (c *rerenderingCustomMessageComponent) SetOutputPad(padding int) {
	if c.outputPad == padding {
		return
	}
	c.outputPad = padding
	c.rebuild()
}

func (c *rerenderingCustomMessageComponent) rebuild() {
	component := c.renderer(c.message, extension.MessageRenderOptions{Expanded: c.expanded, OutputPad: c.outputPad}, nil)
	if rendered, ok := component.(tui.Component); ok && rendered != nil {
		c.component = rendered
		return
	}
	c.fallback.SetExpanded(c.expanded)
	c.component = c.fallback
}

// pendingToolArg accumulates streaming tool call fragments for one tool call index.
type pendingToolArg struct {
	id   string
	name string
	args strings.Builder
}

type compactionQueueMode string

const (
	compactionQueueSteer    compactionQueueMode = "steer"
	compactionQueueFollowUp compactionQueueMode = "followUp"
)

type compactionQueuedMessage struct {
	text   string
	images []ai.ImageContent
	mode   compactionQueueMode
}

// InteractiveMode runs the full interactive TUI session.
type InteractiveMode struct {
	opts      InteractiveOptions
	tuiInst   tui.Renderer
	newRunner *inproc.Runner
	// detachModelRegistry detaches the model operations wired to the current build's bridge.
	detachModelRegistry func()
	// runEnded is set once Run returned and the owner loop no longer runs.
	runEnded atomic.Bool
	extCtx   *ExtensionContext // shared state for slash commands
	agent    *agent.Agent
	// extensionAgent is the agent the extension host reads from its own goroutines; setAgent publishes it with agent. stopRunSignalReports ends the report of its runs to the host and belongs to the main loop.
	extensionAgent       atomic.Pointer[agent.Agent]
	stopRunSignalReports func()
	// The live session is owned by the SessionHandle (coding.Session.inner) and
	// is the single source of truth. Read it via m.currentSession() so display
	// (/session, /tree, /compact) and persistence (the agent's OnMessagePersist
	// hook, which also reads coding.Session.inner) can never diverge across a
	// /new, /clone, or /resume swap: a swap goes through SessionHandle.ReplaceInner
	// and is visible everywhere at once.
	// resourceSourceInfo annotates loaded prompts/skills/extensions/themes
	// with their package/top-level origin. Populated via the callback in
	// InteractiveOptions and refreshed on /reload.
	resourceSourceInfo map[string]ResourceSourceInfo

	// UI components
	chatContainer       *tui.Container
	extHeader           *specialLinesComponent
	extFooter           *specialLinesComponent
	widgetContainer     *tui.Container
	editor              *tui.Editor
	editorSnapshotOwner *tui.Editor
	editorSnapshot      atomic.Pointer[string]
	statusLine          *StatusLine

	// loadedResourcesContainer holds the loaded-resources listing between the
	// header and the transcript, so clearing the transcript keeps it.
	loadedResourcesContainer *tui.Container
	// loadedResourceSections are the listing's sections, which Ctrl+O
	// expands and collapses with tool output. Owned by the UI loop.
	loadedResourceSections []*expandableText

	// State
	isIdle bool
	// turnActive is true from the synchronous commit of a turn in runPromptTurn
	// until that turn's goroutine returns. Unlike isIdle (reset via a queued
	// runOnMain, so it can read stale-false right after a turn ends) and unlike
	// Agent.IsStreaming() (false during the pre-stream window: goroutine
	// dispatch, before_agent_start hook, pre-prompt auto-compaction), it marks
	// exactly the interval in which a run goroutine exists to drain the steering
	// queue. hasActiveAgentTurn uses it so a submit in the pre-stream window
	// steers instead of starting a second concurrent turn. Mirrors upstream
	// _isAgentRunActive (agent-session.ts:874). Set on the main loop, cleared on
	// the turn goroutine; read from both, hence atomic.
	turnActive atomic.Bool
	// queueMu serializes the decision to queue input into the active run
	// (turnActive set) against settleTurn clearing turnActive, so input is
	// never queued into a run that has already made its last queue check.
	queueMu sync.Mutex
	// runGen counts runs started on the owner loop; a run's queued UI
	// cleanup applies only while it is still the latest run. Owner loop only.
	runGen uint64
	// turnSettled is closed when the active run settles; waitForIdle waits
	// on it. Guarded by queueMu; nil while no run is active.
	turnSettled chan struct{}

	// agent_settled handlers finish as one awaited dispatch before an action
	// they start may begin another run. The mutex covers subprocess host-call
	// goroutines racing the settlement goroutine.
	settledMu              sync.Mutex
	emittingAgentSettled   bool
	deferredSettledActions []func()

	requestExit  atomic.Bool // /quit / /exit sets this; input loop notices and returns. Atomic: extension shutdown may set it off the owner loop.
	fatalRuntime atomic.Bool // fatal session replacement errors exit 1 after the input loop restores the terminal.

	// suspended is true while the session is parked by SIGTSTP. SIGINT is
	// ignored then, mirroring upstream's ignoreSigint listener.
	suspended atomic.Bool

	// Shutdown paths share an emission guard and join in-flight cleanup before owner-loop terminal teardown.
	signalShutdownDone atomic.Bool
	shutdownMu         sync.Mutex
	// reloadIssues collects extension/tool reload failures from the most recent
	// /reload so ReloadDiagnostics can surface them in-session as "Extension
	// issues" instead of dropping them to a TUI-clobbered stderr. Reset at the
	// start of each Reload. Written and read on the main loop.
	reloadIssues []string
	abortCtx     context.Context
	abortFn      context.CancelFunc
	// runCtx is the root context passed to Run. Stored so goroutines that must
	// start a turn outside the input loop (e.g. flushing the compaction queue
	// from the agent-event handler) have a live parent context.
	runCtx context.Context
	// resumeStatus is the status the last /resume replacement earned, read once by the resume handler on the owner loop.
	resumeStatus     string
	backgroundCtx    context.Context
	backgroundCancel context.CancelFunc
	backgroundTasks  sync.WaitGroup
	arminComponents  []*arminComponent
	markdownQueue    tui.MarkdownTransformQueue
	markdownBlocks   []tui.Disposable
	clipboardCtx     context.Context
	clipboardReads   *sync.WaitGroup
	// openURL opens a URL in the default browser for OSC 8 hyperlink activation.
	// Injected so a test can supply a capturing opener; nil falls back to
	// openBrowser. Mirrors upstream passing openBrowser to the fullscreen renderer.
	openURL func(url string) error
	// copyClipboard writes user-requested text to the host clipboard.
	// Injected for deterministic tests; nil uses the platform helper.
	copyClipboard func(text string) error
	// rendererOut, when non-nil, is the fixed-size output the interactive renderer
	// writes to instead of the process terminal (both regular and fullscreen). It
	// lets a test drive the real render/input path: including a live tui-mode
	// switch: hermetically; production leaves it nil.
	rendererOut io.Writer
	// currentTuiCleanup tears down the currently-installed renderer's owner-scoped
	// background work (the fullscreen auto-scroll tick context). InteractiveMode
	// owns it across renderer swaps so teardown always runs the current cleanup,
	// not an initially captured closure.
	currentTuiCleanup func()
	// rendererMu guards m.tuiInst/m.altScreen across a live tui-mode switch. The
	// invalidation closures that resolve m.tuiInst dynamically can fire from
	// subprocess/extension goroutines; switchTuiMode holds the write lock across
	// stop+swap so no invalidation lands on a renderer after its preserve-screen
	// stop. Owner-loop reads before any switch are safe without it (goroutine
	// start happens-before), so only the dynamic invalidation paths take RLock.
	//
	// INVARIANT: this is a plain (non-reentrant) sync.RWMutex. No code reachable
	// while switchTuiMode holds the write lock: teardownCurrentTui,
	// createInteractiveTui, mountInteractiveTui, and the re-apply wiring: may call
	// renderNow, requestRender, invalidate, or TUIUIContext.withRenderer, because
	// those take RLock and would self-deadlock on the same goroutine. In
	// particular, do not push extension header/footer lines via
	// specialLinesComponent.SetLines under the write lock: its invalidate callback
	// is m.renderNow. Paint under the write lock only via the raw m.tuiInst.Render()
	// call switchTuiMode makes at the end. TestSwitchTuiModeMountDoesNotReenterRendererLock
	// fails fast if a future edit violates this.
	rendererMu sync.RWMutex
	// mainScreenRenderState persists the regular renderer's captured render state
	// across switch calls so a fullscreen round trip restores scrollback position.
	// Mirrors upstream InteractiveMode.mainScreenRenderState.
	mainScreenRenderState          *tui.TUIRenderState
	workingMessage                 string
	workingVisible                 bool
	statusContainer                *tui.Container // standalone fallback when the editor does not embed status
	pendingMessagesContainer       *tui.Container // queued steering/follow-up display (between chat and status)
	activeStatusIndicator          *tui.StatusIndicator
	activeWorkingIndicatorEmbedded bool
	workingIndicatorOptions        *workingIndicatorOptions
	statusLastFrame                time.Time
	spinnerIntervalCh              chan time.Duration
	// spinnerTickQueued is set while a spinner tick waits in uiTaskCh.
	spinnerTickQueued atomic.Bool

	branchSummaryCancel context.CancelFunc

	// Bash-prefix execution state.
	// bashCancel cancels the currently-running `!cmd` (Esc routes
	// here when isBashRunning). pendingBashBlocks queue components
	// dispatched while the agent is mid-turn; they promote to chat
	// after the bash goroutine finishes.
	bashCancel          context.CancelFunc
	userBashTasks       map[*userBashTask]struct{}
	settlingUserBash    bool
	pendingBashBlocks   []*tui.BashExecutionBlock
	pendingBashBlocksMu sync.Mutex

	// Double-Esc opens /tree (mirrors upstream
	// interactive-mode.ts:2300-2316). Tracks the wall-clock time of
	// the last idle Esc so a second within 500ms fires the action.
	lastEscapeTime time.Time

	// Tracks the wall-clock time of the last Ctrl+C so a second within
	// 500ms exits, mirroring upstream handleCtrlC (interactive-mode.ts:3262):
	// first Ctrl+C clears the editor, a second within 500ms shuts down.
	lastSigintTime time.Time

	modelSelectionGeneration uint64 // owner-loop generation for pending post-login selection

	// Last assistant message text (for /copy).
	lastAssistantText string
	lastStatusSpacer  *tui.Spacer
	lastStatusText    *tui.ThemedText
	// lastStatusMessage is the text lastStatusText renders (interactive-mode.ts lastStatusMessage).
	lastStatusMessage string

	// Countdown goroutine cancel function for auto-retry.
	// Called on AutoRetryEndEvent or when a new retry starts.
	retryCountdownStop func()

	// Slash-command registry. Owns 14 builtins + the dynamic
	// extension-registered set. Constructed in Run().
	slashRegistry *SlashRegistry

	// App-level keybinding registry loaded from <agentDir>/keybindings.json.
	// Mirrors upstream core/keybindings.ts for interactive-mode bindings.
	keybindings *KeybindingsManager

	// toolByID holds only pending calls so completion updates the streaming card and releases its ID for later calls. toolOrder retains transcript order for Ctrl+O; toolsExpanded is the global expansion choice.
	toolMu        sync.Mutex
	toolByID      map[string]*tui.ToolExecutionComponent
	toolStarts    map[string]time.Time
	toolFileCalls map[string]ai.ToolCall
	toolOrder     []*tui.ToolExecutionComponent
	bashOrder     []*tui.BashExecutionBlock // row 2.8a: Ctrl+O drives bash blocks too
	toolsExpanded bool
	// builtInHeaderExpanded starts from verbose || toolsExpanded, then follows explicit expansion and restoration. Guarded by toolMu.
	builtInHeaderExpanded bool

	// pendingArgs accumulates ToolCallDelta fragments keyed by index.
	// Used to build progressive args preview during streaming.
	pendingArgs map[int]*pendingToolArg

	// Compaction state.
	// isCompacting gates message queueing during an in-flight compact.
	// compactionQueue holds messages typed while compaction runs; drained
	// by flushCompactionQueue on CompactionEndEvent.
	// compactionOrder tracks rendered CompactionSummaryComponents so
	// Ctrl+O expand/collapse applies to them alongside tool components.
	// Mirrors upstream isCompacting / compactionQueuedMessages
	// (interactive-mode.ts:236-266).
	isCompacting    bool
	compactionQueue []compactionQueuedMessage
	compactionOrder []*tui.CompactionSummaryComponent

	// uiTaskCh carries closures posted by background workers (e.g. the async
	// autocomplete fd search) to run on the main input loop. Editor and
	// component state is mutated only on that goroutine, single-threaded
	// with keystroke handling, so workers never race the lock-free TUI
	// component tree (upstream runs this on one JS event loop).
	uiTaskCh chan func()

	// eventCh is the agent's live event stream (m.opts.SessionHandle.Events()).
	// The inputLoop select drains it and calls handleAgentEvent on the main
	// goroutine. evCurrentBlock/evTurnIndex are that handler's
	// cross-event state (one assistant block per message; mirrors upstream's
	// per-stream locals), only ever touched on the main goroutine.
	eventCh            <-chan agent.AgentEvent
	evCurrentBlock     *tui.AssistantMessageBlock
	bugReportHintShown bool
	evTurnIndex        int

	// branchSummaryOrder tracks rendered BranchSummaryComponents so
	// Ctrl+O expand/collapse applies to them alongside tool components.
	branchSummaryOrder []*tui.BranchSummaryComponent
	customMessageOrder []expandableCustomMessageComponent

	// anthropicSubWarningShown gates the one-time Anthropic subscription
	// auth warning. Mirrors upstream anthropicSubscriptionWarningShown.
	anthropicSubWarningShown     bool
	previousThinkingDroppedCount int

	// inputLoopErr, owned by the input loop, ends it with the error of a
	// keystroke handled after a subprocess listener's verdict.
	inputLoopErr error
	inputReadCh  chan inputChunk
	inputErrCh   chan error
	inputOwner   *terminalInputOwner
	themeState   interactiveThemeState

	// terminalInputListeners: extension-registered raw input handlers.
	// Mirrors upstream extensionTerminalInputUnsubscribers.
	// Each handler receives raw keystrokes and may consume them.
	terminalInputMu           sync.Mutex
	terminalInputListenerID   uint64
	terminalInputListeners    []terminalInputListener
	extensionShortcutListener func(data string) (consume bool)
	// remoteEditor is the extension editor component installed in place of
	// the editor (ctx.ui.setEditorComponent), or nil.
	remoteEditor *remoteEditor
	// remoteEditorEvents runs the component's events on the owner loop.
	remoteEditorEvents remoteEventQueue
	// tuiStopped is set once the TUI stopped, for writers off the owner loop.
	tuiStopped atomic.Bool
	// extensionKeybindings is the keybinding table extension state
	// snapshots carry, republished when the keybindings reload.
	extensionKeybindings atomic.Pointer[map[string]any]
	modalInputMu         sync.RWMutex
	modalInputCh         chan []byte
	modalInputDepth      int
	// modalDoneCh is closed by setModalInputChannel(nil) when a modal tears
	// down. The stdin reader selects on it so a send to the outgoing modal
	// channel can never block forever when the modal stops draining mid-spam.
	modalDoneCh chan struct{}
	// modalChangedCh is closed (and replaced) on every setModalInputChannel
	// call. The input pump selects on it while holding a chunk for the main
	// loop so an arming modal can reclaim that chunk instead of the pump
	// deadlocking on the unbuffered readCh that a busy main loop cannot drain.
	modalChangedCh  chan struct{}
	extensionDialog *extensionDialog

	// Inference timing for the spinner. workStart is the wall-clock
	// time the current LLM call began; zero when idle.
	workStart time.Time

	// turnSystemPrompt is the active run's rendered system prompt, which
	// replaces the base prompt until the run settles. Protected by
	// turnSystemPromptMu because extension and turn goroutines read it
	// while the turn goroutine and tool changes update it.
	baseSystemPromptOptions atomic.Pointer[extension.BuildSystemPromptOptions]
	turnSystemPromptMu      sync.RWMutex
	turnSystemPrompt        string
	hasTurnSystemPrompt     bool
	// runPrompt holds the active run's before_agent_start inputs, as Pi's _runSystemPromptOptions; nil outside a run. runPromptMu serializes installing, re-rendering and clearing it with the prompt forced on the agent.
	runPromptMu sync.Mutex
	runPrompt   *interactiveRunPrompt
	// runPromptTurnAgent is the agent whose next-turn hook refreshes runPrompt.
	runPromptTurnAgent *agent.Agent

	// rawDrain consumes late protocol input before renderer teardown; temporary handoffs restore without draining.
	rawDrain             func()
	rawRestore           func()
	inputReader          *interactiveTerminalReader
	externalEditorActive bool
	externalEditorInput  func()

	// File-based prompt templates loaded from
	// <agentDir>/prompts/ (user) and <cwd>/.pig/prompts/ (project).
	// Populated in Run() before the input loop starts. Slash dispatch
	// falls through to template expansion when no builtin matches.
	promptTemplates   []PromptTemplate
	promptDiagnostics []extension.ResourceDiagnostic
	// Loaded theme resources and diagnostics are retained independently of the selectable theme registry.
	loadedThemes     []loadedTheme
	themeDiagnostics []extension.ResourceDiagnostic
	// extensionConflicts are the tool and flag conflicts the last reload found; Pi lists them with the extension load errors.
	extensionConflicts []ExtensionConflict
	// slashCatalog is the resource side of pi.getCommands() (templates,
	// skills, provenance), republished whole whenever the owner loop changes
	// one, because extension host calls read it off the owner loop.
	slashCatalog atomic.Pointer[SlashCommandCatalog]

	// Thinking level and visibility state.
	// thinkingLevel is the user-facing cycle string ("off"/"low"/"medium"/"high").
	// hideThinking mirrors upstream's hideThinkingBlock boolean.
	// assistantBlocks tracks every AssistantMessageBlock added during this session
	// so Ctrl+T can call SetHiddenThinking on all of them: the single coupling
	// point replacing the old thinkingOrder[] slice.
	// Sync note: upstream iterates chatContainer children that are AssistantMessageComponent
	// instances (interactive-mode.ts:1638-1643). Same approach, one type.
	thinkingLevel   string
	hideThinking    bool
	assistantBlocks []*tui.AssistantMessageBlock
	userBlocks      []*tui.UserMessageBlock
	outputPad       int

	// layout is the main-screen render root used for editor-slot swaps.
	layout *tui.Container

	// altScreen is the alternate-screen renderer, non-nil only when the TuiMode
	// setting is "fullscreen"; it also backs tuiInst in that mode.
	altScreen *tui.TuiAltScreen
	// editorContainer wraps the editor so a selector/input/overlay can take the
	// editor's slot via SetChildren in both the flat and fullscreen layouts.
	// Mirrors upstream editorContainer.
	editorContainer *tui.Container
	// transcriptScrollView is the scrollable conversation view in fullscreen mode.
	transcriptScrollView *tui.ScrollView
	// tuiTornDown guards stopInteractiveTui so the renderer is stopped and the
	// transcript view disposed exactly once across the explicit-quit, ctx.Done,
	// and input-error exit paths. Mirrors upstream's single stopInteractiveTui().
	tuiTornDown bool

	// Scoped model IDs for Ctrl+P cycling.
	// nil = all models enabled (no filter).
	// Populated from settings on startup; updated by /scoped-models.
	scopedModelIDs []string

	// scheduledRender holds the latest throttled render the renderer handed to
	// dispatchScheduledRender; renderWakeCh (capacity 1) wakes the main loop to
	// run it. A pending render coalesces with later ones instead of competing
	// for uiTaskCh space, so it is never dropped.
	// managedToolStatusStarted mirrors upstream managedToolStatusStarted:
	// the first managed-tool status report is preceded by a spacer.
	managedToolStatusStarted bool

	scheduledRenderMu sync.Mutex
	scheduledRender   func()
	renderWakeCh      chan struct{}

	// Extension errors reported from any goroutine wait here until the main
	// loop shows them; extensionErrorWakeCh (capacity 1) wakes the loop.
	extensionErrorsMu      sync.Mutex
	pendingExtensionErrors []extension.ExtensionError
	extensionErrorWakeCh   chan struct{}

	autocompleteFactories   []autocompleteRegistration
	autocompleteProvider    *extension.AutocompleteProvider
	autocompleteEpoch       context.Context
	autocompleteEpochCancel context.CancelFunc
	autocompletePending     <-chan struct{}
	currentInputTicket      *inputTicket
	pendingUserInputs       []string
	onInputCallback         func(string)
	initialMessagesDone     <-chan struct{}
}

// postUITask hands a closure to the main input loop to run
// single-threaded with keystroke handling. Used by background workers
// (async autocomplete) so they never mutate the lock-free TUI component
// tree from their own goroutine. The send is non-blocking: if the queue
// is saturated or the loop has exited, the (cosmetic) task is dropped
// and the next keystroke refreshes, avoiding worker-goroutine leaks on
// shutdown. It reports whether fn was queued.
func (m *InteractiveMode) postUITask(fn func()) bool {
	if fn == nil {
		return false
	}
	select {
	case m.uiTaskCh <- fn:
		return true
	default:
		return false
	}
}

// dispatchScheduledRender is the renderer's dispatch hook for throttled
// renders. It never blocks the timer goroutine and never drops the render:
// the newest render replaces a pending one, and the main loop runs it on its
// next wake. Mirrors upstream, where the requestRender timer callback always
// runs on the event loop.
func (m *InteractiveMode) dispatchScheduledRender(render func()) {
	m.scheduledRenderMu.Lock()
	m.scheduledRender = render
	m.scheduledRenderMu.Unlock()
	select {
	case m.renderWakeCh <- struct{}{}:
	default:
	}
}

// installRenderDispatcher runs throttled scheduled renders on the main input
// loop instead of the throttle-timer goroutine, so doRender never reads the
// lock-free component tree concurrently with the main loop's mutations (e.g.
// the working-spinner frame advanced by tickSpinner). Mirrors upstream's
// single-event-loop setTimeout render callback.
func (m *InteractiveMode) installRenderDispatcher() {
	m.tuiInst.SetRenderDispatcher(m.dispatchScheduledRender)
}

// runScheduledRender runs the pending throttled render, if any, on the main
// loop.
func (m *InteractiveMode) runScheduledRender() {
	m.scheduledRenderMu.Lock()
	render := m.scheduledRender
	m.scheduledRender = nil
	m.scheduledRenderMu.Unlock()
	if render != nil {
		render()
	}
}

// runOnMain hands fn to the main input loop and blocks until the loop accepts
// it (backpressure) or ctx is cancelled (shutdown/abort). Unlike postUITask it
// never drops fn, so it is the tool for state the loop must not miss: streamed
// bash output, login results, turn-end state. It does not wait for fn to run;
// the loop executes queued tasks FIFO, so a caller that posts in order sees its
// mutations applied in order. Callers must not hold a lock the loop also takes.
// Mirrors upstream pi's single-event-loop model, where a worker's result is
// delivered to the loop rather than mutating UI state from the worker
// (Bubble Tea's p.Send has the same blocking-with-cancel semantics).
func (m *InteractiveMode) runOnMain(ctx context.Context, fn func()) {
	_ = m.postToMain(ctx, fn)
}

// errOwnerLoopUnavailable reports a task the owner loop could not accept.
var errOwnerLoopUnavailable = errors.New("interactive mode is not accepting work")

// postToMain is runOnMain for callers that must report a task the loop never
// accepted, such as an extension's sendUserMessage: it never drops fn, and
// returns an error only when ctx ends first.
func (m *InteractiveMode) postToMain(ctx context.Context, fn func()) error {
	if fn == nil {
		return nil
	}
	if ctx == nil {
		// No cancellation source (only happens in tests with no running loop).
		// Degrade to the non-blocking post rather than risk a permanent block.
		if !m.postUITask(fn) {
			return errOwnerLoopUnavailable
		}
		return nil
	}
	select {
	case m.uiTaskCh <- fn:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", errOwnerLoopUnavailable, context.Cause(ctx))
	}
}

func (m *InteractiveMode) showCacheMissNotices() bool {
	if m.opts.SettingsManager != nil {
		return m.opts.SettingsManager.GetShowCacheMissNotices()
	}
	return m.opts.Settings.GetShowCacheMissNotices()
}

func (m *InteractiveMode) setModalInputChannel(ch chan []byte) {
	m.modalInputMu.Lock()
	defer m.modalInputMu.Unlock()
	m.setModalInputChannelLocked(ch)
	m.modalInputDepth = 0
}

func (m *InteractiveMode) setModalInputChannelLocked(ch chan []byte) {
	// Every route change wakes the input pump if it is parked holding a chunk
	// for a busy main loop (see routeInputChunk): an arming modal must be able
	// to reclaim that chunk rather than let it deadlock on readCh.
	if m.modalChangedCh != nil {
		close(m.modalChangedCh)
	}
	m.modalChangedCh = make(chan struct{})
	if ch == nil {
		// Tear-down: wake any reader blocked sending to the outgoing modal
		// channel so it re-routes the parsed sequence to the next focus target.
		if m.modalDoneCh != nil {
			close(m.modalDoneCh)
			m.modalDoneCh = nil
		}
		m.modalInputCh = nil
		return
	}
	if m.modalDoneCh != nil {
		close(m.modalDoneCh)
	}
	m.modalInputCh = ch
	m.modalDoneCh = make(chan struct{})
}

// acquireModalInputChannel returns the shared parsed-input route for a modal
// scope. Nested modal flows reuse it, so focus can move from a parent selector
// to a child selector without racing a second stdin reader or dropping type-ahead.
func (m *InteractiveMode) acquireModalInputChannel() (chan []byte, func()) {
	m.modalInputMu.Lock()
	if m.modalInputCh == nil {
		m.setModalInputChannelLocked(make(chan []byte))
	}
	m.modalInputDepth++
	ch := m.modalInputCh
	m.modalInputMu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.modalInputMu.Lock()
			defer m.modalInputMu.Unlock()
			m.modalInputDepth--
			if m.modalInputDepth == 0 {
				m.setModalInputChannelLocked(nil)
			}
		})
	}
}

// modalRoute returns the active modal input channel and its paired done
// channel as a consistent snapshot. The reader sends to the channel but
// aborts the send if done fires (the modal was torn down concurrently).
func (m *InteractiveMode) modalRoute() (chan []byte, chan struct{}) {
	m.modalInputMu.RLock()
	defer m.modalInputMu.RUnlock()
	return m.modalInputCh, m.modalDoneCh
}

// modalRouteWatch is modalRoute plus the change-signal channel, captured in
// one locked snapshot so the input pump never misses a route change that
// races its select.
func (m *InteractiveMode) modalRouteWatch() (chan []byte, chan struct{}, <-chan struct{}) {
	m.modalInputMu.RLock()
	defer m.modalInputMu.RUnlock()
	return m.modalInputCh, m.modalDoneCh, m.modalChangedCh
}

// routeInputChunk delivers one stdin chunk to the active modal selector, or
// to the main loop via readCh when no modal is active. If a modal arms while
// the chunk is waiting for the main loop to drain readCh, the chunk is
// re-routed to that modal. It returns the ticket of a chunk the main loop
// took, which settles once the chunk's terminal-input listeners are done.
//
// This closes the /tree → "Summarize branch?" freeze: between two chained
// modal selectors the route is briefly nil, and the whole chain runs
// synchronously on the main loop, so the main loop is not draining readCh. A
// keystroke read in that window must not park the pump forever on the
// unbuffered readCh: when the next selector arms, modalChangedCh fires and
// the chunk routes to it.
func (m *InteractiveMode) routeInputChunk(ctx context.Context, chunk []byte, readCh chan<- inputChunk) *inputTicket {
	return m.routeInputSequence(ctx, chunk, nil, readCh)
}

// routeInputSequence routes a decoded event with its terminal-read completion boundary. A protocol-only suffix closes readDone without manufacturing another input event.
func (m *InteractiveMode) routeInputSequence(ctx context.Context, chunk []byte, readDone <-chan struct{}, readCh chan<- inputChunk) *inputTicket {
	ticket := newInputTicket()
	for {
		modalCh, modalDone, changed := m.modalRouteWatch()
		if modalCh != nil {
			select {
			case modalCh <- chunk:
				return nil
			case <-modalDone:
				// Focus changed while this parsed sequence was waiting. Re-read
				// the route and deliver it to the newly focused component, as
				// upstream's single input handler does.
				continue
			case <-ctx.Done():
				return nil
			}
		}
		select {
		case readCh <- inputChunk{data: chunk, ticket: ticket, readDone: readDone}:
			return ticket
		case <-changed:
			// A modal armed (or the route otherwise changed) while the busy
			// main loop could not receive on readCh. Re-evaluate and deliver
			// to the modal instead of deadlocking.
			continue
		case <-ctx.Done():
			return nil
		}
	}
}

// InteractiveOptions configures the interactive mode.
type InteractiveOptions struct {
	CWD      string
	AgentDir string
	Model    *ai.Model
	Settings Settings
	// InitialThemeSetting selects a theme for this run without changing the SettingsManager. A non-nil empty name is a selection that fails and falls back to dark. Explicit selections replace it; absent selections follow the current manager.
	InitialThemeSetting *string
	// TuiMode selects "regular" or "fullscreen" for this run without changing the SettingsManager. Empty captures Settings.TuiMode at construction; only a live mode switch changes it afterwards.
	TuiMode string
	// SettingsManager provides read/write access to global settings.
	// Wired by main.go via coding.Services.
	SettingsManager *SettingsManager
	// SystemPrompt overrides the assembled system prompt. If empty,
	// callers should build one via prompts.BuildDefaultPrompt and pass
	// it in.
	SystemPrompt string
	// SystemPromptOptions mirrors upstream
	// `BuildSystemPromptOptions` (system-prompt.ts:8-25). It carries
	// the structured inputs that produced SystemPrompt (custom prompt,
	// tools, append text, context files, skills) and is passed verbatim
	// to extensions on every before_agent_start event so they can
	// inspect what pi has loaded without re-discovering resources.
	//
	// upstream: agent-session.ts:_rebuildSystemPrompt
	SystemPromptOptions extension.BuildSystemPromptOptions
	// AllowedTools restricts which tool names may execute. nil means no
	// restriction; a non-nil empty map blocks every tool. Mirrors the
	// `tools:` allowlist on agent .md frontmatter.
	AllowedTools map[string]struct{}
	// ActiveBuiltinTools, when non-nil, restricts which built-in coding
	// tools are active (caller/extension tools unaffected). nil = all
	// built-in tools. Mirrors upstream defaultActiveToolNames (sdk.ts:244):
	// CLI default [read, bash, edit, write]; grep/find/ls inactive unless
	// requested via --tools.
	ActiveBuiltinTools map[string]struct{}
	// ExcludedTools is a denylist of tool names made non-callable, gating
	// built-in and extension tools alike. Mirrors upstream excludedToolNames
	// (sdk.ts:246) + isAllowedTool (agent-session.ts:2288).
	ExcludedTools map[string]struct{}
	// ToolRegistryAllowed is the --tools allowlist bounding the tool registry
	// pi.getAllTools() reports (upstream _allowedToolNames): nil admits every
	// tool, an empty map none. Unlike AllowedTools, active-tool changes never
	// modify it.
	ToolRegistryAllowed map[string]struct{}
	// NoBuiltinTools hides built-in tools while leaving extension/custom tools.
	NoBuiltinTools bool
	// PromptPaths lists additional prompt-template directories.
	// Mirrors upstream `additionalPromptTemplatePaths`.
	// Directories are searched after the default <agentDir>/prompts/ and
	// <cwd>/.pig/prompts/ (last-wins by name, same as project scope).
	PromptPaths []string
	// SessionDir overrides the on-disk session directory used by /new,
	// /resume, and other interactive session-management flows.
	SessionDir string
	// SessionID specifies an exact session ID for new sessions.
	// Mirrors upstream --session-id (v0.76.0). When non-empty and no
	// existing session matches, Create uses this ID instead of
	// generating a random one.
	SessionID string
	// ThemePaths lists resolved theme files/directories in resource precedence order.
	ThemePaths []string
	// NoPromptTemplates disables prompt template discovery.
	NoPromptTemplates bool
	// NoThemes disables custom theme discovery from disk.
	NoThemes bool

	// UnknownFlags carries CLI flags not claimed by the core parser so
	// extensions can resolve their registered values at runtime.
	UnknownFlags map[string]any
	// BeforeToolCall hooks run at agent-loop time and can block tool
	// execution. The allowlist hook provides defense in depth alongside
	// the registration-time filter.
	BeforeToolCall []agent.BeforeToolCallHook

	// SessionHandle, when non-nil, is the pre-constructed agent +
	// on-disk session that InteractiveMode wraps for the TUI.
	// Constructed by the caller via coding.NewSession from the public
	// SDK package; main.go does the bridging between coding.* and
	// internal/codingagent.*. When nil, Run() returns an error: the
	// SDK is the only supported construction path post-Chunk F.
	//
	// Mutually exclusive with ResumePath: if SessionHandle is set,
	// the caller is responsible for having loaded the session via
	// coding.NewSession's ResumePath field.
	SessionHandle InteractiveSessionHandle

	// ContextUsage returns the live Session projection estimate for stock footer and extension usage reads.
	ContextUsage func() (tokens *int, contextWindow int)

	// ModelBuilder constructs an *ai.Model from a "<provider>/<id>"
	// spec. Wired by main.go to coding.BuildModel: the public SDK
	// can't be imported from internal/codingagent (cycle), so the
	// caller injects the constructor. nil disables /model switching
	// (handler falls back to print-only).
	ModelBuilder func(spec string) (*ai.Model, error)

	// DefaultModelPerProvider shares the startup resolver's provider defaults with /login.
	DefaultModelPerProvider map[string]string

	// ModelLookup resolves a provider/model identity through the Session-owned runtime.
	ModelLookup  func(providerID, modelID string) *ai.Model
	ModelCatalog func() []*ai.Model
	// ModelClassify is the Session-owned runtime classify an extension's ctx.modelRegistry.classify reaches.
	ModelClassify func(context.Context, *ai.ClassifierModel, ai.ClassifierContext, ...ai.ModelsClassifierOptions) ai.ClassifierResult

	// RequestAuthRuntime is the composed checkAuth/getAuth surface used by
	// warning-only auth checks. Wired by main.go from the same credential and
	// provider configuration used for model requests.
	RequestAuthRuntime *RequestAuthRuntime

	// ModelRegistry is the model registry for auth-aware operations
	// (Refresh, GetAvailable, HasConfiguredAuth). Wired by main.go.
	// nil is safe; post-login refresh is skipped.
	// Mirrors upstream session.modelRegistry (interactive-mode.ts:4394).
	ModelRegistry *ModelRegistry

	// ExtensionRunner is the new-style (`coding/extension`) runner.
	// May be nil during tests or when no extensions are loaded.
	// Events are dispatched through this runner via event_bridge.go.
	ExtensionRunner *inproc.Runner

	// SessionStartEvent is the factory-selected startup/new/resume/fork event for the current Session.
	SessionStartEvent *extension.SessionStartEvent

	// ExtensionContext is the live extension context shared with the
	// runner. Caller-supplied to ensure extCtx.Session points at the
	// SAME on-disk session SessionHandle wraps.
	ExtensionContext *ExtensionContext

	// ResumePath, when non-empty, is loaded as the active session at
	// startup. The agent's message history is rebuilt from
	// session.BuildContext() and the transcript is replayed inline so
	// the user sees prior conversation.
	ResumePath string
	// InitialMessage, when non-empty, is auto-submitted to the agent
	// immediately after the TUI initialises. Mirrors upstream's
	// `initialMessage` plumbing at
	// `.upstream/current/packages/coding-agent/src/cli/initial-message.ts`.
	// Sourced by main.go from positional args + piped stdin so users
	// can `pig "hello"`, `pig < file.txt`, or pipe + run.
	InitialMessage string
	InitialImages  []ai.ImageContent
	// InitialMessages are the positional messages after the first. Each is
	// sent as its own prompt once the previous one settles (upstream
	// initialMessages).
	InitialMessages []string

	// AppVersion is the binary version string used to detect
	// changelog-seen state. Set to UpstreamVersion (the port target).
	// gate the startup "what's new" changelog notification.
	// Mirrors upstream this.version in InteractiveMode constructor.
	AppVersion string

	// PackageUpdateChecker, if set, is invoked asynchronously at startup
	// to check for available package updates. The returned strings are
	// the display names of packages with available updates. When the
	// returned slice is non-empty, an "X package updates available"
	// banner is shown. Mirrors upstream checkForPackageUpdates +
	// showPackageUpdateNotification (interactive-mode.ts:660-665, 3449).
	PackageUpdateChecker func() []string

	// BinaryUpdateChecker, if set, is invoked asynchronously at startup and
	// returns a newer-release notice for the pig binary itself, or nil. pig
	// divergence (D39): standalone-binary self-update. The notice names the
	// command that applies it (e.g. `pig update self`).
	BinaryUpdateChecker func() *BinaryUpdate

	// Llama is Pi's built-in llama.cpp provider: /llama manages its router
	// models, /login configures it, and startup refreshes its catalog.
	Llama *llama.Host
	// OfflineMode skips startup network catalog refreshes, like PI_OFFLINE.
	OfflineMode bool

	// ResourceSourceInfoProvider returns source metadata for currently known
	// prompts/skills/extensions/themes, keyed by absolute path. Used to enrich
	// verbose startup output and /reload diagnostics with package origin.
	// Mirrors upstream resource-loader.ts PathMetadata/SourceInfo plumbing.
	ResourceSourceInfoProvider func() map[string]ResourceSourceInfo
	// ReloadResourceProvider recomputes settings-backed prompt/theme/skill/
	// context-file inputs on /reload so the interactive session mirrors
	// upstream resourceLoader.reload() instead of reusing startup snapshots.
	ReloadResourceProvider func() ReloadResourceSnapshot

	// Verbose forces verbose startup output (overrides quietStartup).
	// Mirrors upstream InteractiveOptions.verbose (interactive-mode.ts:184).
	Verbose bool

	// Skills is the loaded set of skill definitions, used to expand
	// /skill:name commands in user prompts.
	// Mirrors upstream resourceLoader.getSkills() (agent-session.ts:1124-1151).
	Skills []*SkillDef
	// SkillDiagnostics retains ordered load-time collisions for the startup listing.
	SkillDiagnostics []extension.ResourceDiagnostic
	// RebuildSystemPrompt reconstructs the base system prompt + structured
	// prompt options after /reload from the current skills/context files.
	// Mirrors upstream session.reload() → _rebuildSystemPrompt.
	RebuildSystemPrompt func(skills []*SkillDef, contextFiles []ContextFile) (string, extension.BuildSystemPromptOptions)
	// BridgeExtensionTools converts registered extension tools into
	// agent.AgentTool values so /reload can refresh the live tool registry.
	BridgeExtensionTools func([]extension.RegisteredTool) ([]agent.AgentTool, []error)
	// SkillPaths are the source paths from which Skills were loaded.
	// Used by /reload to re-discover skills from disk. If empty, skills
	// are not reloaded (only the initial set from startup is used).
	SkillPaths []string
	// NoSkills disables automatic skill discovery; explicitly selected and extension-discovered paths still load on reload.
	NoSkills bool

	// ContextFiles lists the loaded AGENTS.md/CLAUDE.md project context files.
	// The loaded-resources [Context] section lists them after
	// SystemPromptSourcePaths.
	ContextFiles []ContextFile

	// SystemPromptSourcePaths are the existing files the system prompt and
	// then each appended system prompt were read from, as upstream
	// getSystemPromptSource and getAppendSystemPromptSources report them.
	SystemPromptSourcePaths []string

	// Runtime, when set, owns Session replacement: new, resume, fork, clone and import build the destination Session through its factory. ReplacementResources then supplies the destination build's options.
	Runtime InteractiveRuntime
	// ReplacementResources returns the cwd-bound options of the build that owns session.
	ReplacementResources func(session InteractiveSessionHandle) InteractiveReplacement

	// SubprocessUIBridge, when non-nil, is the UI bridge for subprocess
	// extensions. Run() wires it to the TUI after creation via
	// SetUIContext and SetInvalidate.
	//
	// pig-specific: no upstream equivalent.
	SubprocessUIBridge SubprocessUIBridge

	// SubprocessHost, when non-nil, manages subprocess extension lifecycle.
	// Used by /reload to rebuild and re-spawn changed extensions.
	//
	// pig-specific: no upstream equivalent.
	SubprocessHost SubprocessHost

	// StageExtensionSDKs, when non-nil, materializes the SDKs this binary
	// embeds before /reload recompiles out-of-tree extensions, and prunes
	// builds whose fingerprint proves they came from an older SDK.
	//
	// Injected so this package does not depend on the SDK bundle implementation
	// and so a test can observe that reload stages before it rebuilds.
	//
	// pig-specific: no upstream equivalent, because upstream extensions are
	// in-process TypeScript and there is no SDK to stage.
	StageExtensionSDKs func() error

	// BuiltinExtensions contains generic in-process runtime extensions, such as
	// Piglet capability scoping, in configured load order after subprocess
	// extensions.
	BuiltinExtensions []extension.Extension

	// ReloadBuiltinExtensions reconstructs BuiltinExtensions for /reload. A
	// factory callback is required because copying an Extension would retain the
	// handler closures and state that upstream discards on reload.
	ReloadBuiltinExtensions func() []extension.Extension

	// NoModelWarning carries the interactive-only model-selection diagnostic, including a failed saved-model restoration followed by a fallback.
	NoModelWarning string
	// StartupDiagnostics are shown in the chat after the welcome banner.
	// Mirrors upstream InteractiveModeOptions.startupDiagnostics.
	StartupDiagnostics []AgentSessionRuntimeDiagnostic

	// StartupMark, when non-nil, is called at key phases during Run()
	// for startup timing. See cmd/pig/startup_trace.go.
	StartupMark func(label string)

	// LoginHeaderOptions contains host-owned operational text and terminal
	// rendering capabilities for an extension-provided login.
	LoginHeaderOptions LoginHeaderOptions

	// BuiltInHeaderLines are the stock text header restored when an extension
	// clears its custom header.
	BuiltInHeaderLines []string

	// LoginVisible applies the startup silence gate to built-in and native
	// extension login presentations.
	LoginVisible bool
}

func (m *InteractiveMode) currentSystemPrompt() string {
	m.turnSystemPromptMu.RLock()
	defer m.turnSystemPromptMu.RUnlock()
	if m.hasTurnSystemPrompt {
		return m.turnSystemPrompt
	}
	return m.opts.SystemPrompt
}

func (m *InteractiveMode) rebuildToolSystemPrompt() {
	opts := new(extension.NormalizeBuildSystemPromptOptions(m.opts.SystemPromptOptions))
	if opts.Cwd == "" {
		opts.Cwd = m.opts.CWD
	}
	if m.agent != nil {
		opts.SelectedTools = m.activeToolNames()
	}
	if m.newRunner != nil {
		options := prompts.WithToolDefinitions(prompts.FromExtensionOptions(*opts), m.newRunner.Tools())
		opts.ToolSnippets, opts.ToolGuidelines = WithoutHiddenSnippets(options.ToolHints, m.hiddenDeclarations()), options.ToolGuidelines
	}
	m.baseSystemPromptOptions.Store(opts)
	if m.structuredSystemPrompt() {
		text := prompts.BuildDefaultPrompt(prompts.FromExtensionOptions(*opts))
		m.turnSystemPromptMu.Lock()
		m.opts.SystemPrompt = text
		m.turnSystemPromptMu.Unlock()
	}
}

// structuredSystemPrompt reports whether the prompt is built from SystemPromptOptions. A caller that supplied no tool metadata owns an opaque prompt string.
func (m *InteractiveMode) structuredSystemPrompt() bool {
	return m.opts.SystemPromptOptions.ToolSnippets != nil || m.opts.SystemPromptOptions.ToolGuidelines != nil
}

// currentSystemPromptOptions returns the stored base construction inputs without changing command edits. Tool activation and resource reload replace the object; per-turn overrides do not.
func (m *InteractiveMode) currentSystemPromptOptions() *extension.BuildSystemPromptOptions {
	if opts := m.baseSystemPromptOptions.Load(); opts != nil {
		return opts
	}
	opts := new(extension.NormalizeBuildSystemPromptOptions(m.opts.SystemPromptOptions))
	if opts.Cwd == "" {
		opts.Cwd = m.opts.CWD
	}
	if m.baseSystemPromptOptions.CompareAndSwap(nil, opts) {
		return opts
	}
	return m.baseSystemPromptOptions.Load()
}

func (m *InteractiveMode) setTurnSystemPrompt(prompt string) {
	m.turnSystemPromptMu.Lock()
	defer m.turnSystemPromptMu.Unlock()
	m.turnSystemPrompt = prompt
	m.hasTurnSystemPrompt = true
}

func (m *InteractiveMode) clearTurnSystemPrompt() {
	m.turnSystemPromptMu.Lock()
	defer m.turnSystemPromptMu.Unlock()
	m.turnSystemPrompt = ""
	m.hasTurnSystemPrompt = false
}

// NewInteractiveMode creates an interactive session.
func NewInteractiveMode(opts InteractiveOptions) *InteractiveMode {
	if opts.CWD == "" {
		opts.CWD, _ = os.Getwd()
	}
	if opts.AgentDir == "" {
		opts.AgentDir = DefaultAgentDir()
	}
	// Mirrors the upstream InteractiveMode constructor's
	// setCapabilityOverrides(settingsManager.getTerminalCapabilityOverrides()).
	tui.SetCapabilityOverrides(opts.Settings.GetTerminalCapabilityOverrides())
	// Pi's constructor uses options.tuiMode ?? settingsManager.getTuiMode().
	if opts.TuiMode == "" {
		opts.TuiMode = (&SettingsManager{merged: opts.Settings}).GetTuiMode()
	}

	m := &InteractiveMode{
		opts:                 opts,
		isIdle:               true,
		resourceSourceInfo:   map[string]ResourceSourceInfo{},
		toolByID:             make(map[string]*tui.ToolExecutionComponent),
		toolStarts:           make(map[string]time.Time),
		uiTaskCh:             make(chan func(), 64),
		renderWakeCh:         make(chan struct{}, 1),
		extensionErrorWakeCh: make(chan struct{}, 1),
		pendingArgs:          make(map[int]*pendingToolArg),
		workingVisible:       true,
		spinnerIntervalCh:    make(chan time.Duration, 1),
		outputPad:            opts.Settings.GetOutputPad(),
	}
	if opts.InitialThemeSetting != nil {
		setting := *opts.InitialThemeSetting
		m.themeState.currentThemeSetting.Store(&setting)
	}
	return m
}

func (m *InteractiveMode) newSessionManager() *SessionManager {
	var sm *SessionManager
	if m.opts.SessionDir != "" {
		sm = NewSessionManagerWithDir(m.opts.CWD, m.opts.SessionDir)
	} else {
		sm = NewSessionManager(m.opts.CWD)
	}
	return sm
}

// currentSession returns the live on-disk session, owned by the SessionHandle.
// It is the single source of truth: a /new, /clone, or /resume swap calls
// SessionHandle.ReplaceInner, and both this accessor and the agent's
// persistence hook read the same underlying coding.Session.inner, so the
// displayed session and the persistence target are always identical.
func (m *InteractiveMode) currentSession() *Session {
	if m.opts.SessionHandle == nil {
		return nil
	}
	return m.opts.SessionHandle.Inner()
}

// quoteIfNeeded shell-quotes a value only when it contains characters
// outside the safe set. Mirrors upstream quoteIfNeeded
// (interactive-mode.ts:198).
func quoteIfNeeded(value string) string {
	if value != "" && !quoteUnsafeRE.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

var quoteUnsafeRE = lazyregexp.New(`[^a-zA-Z0-9_\-./~:@]`)

// resumeCommandSession is the SessionManager state read when formatting a resume command.
type resumeCommandSession struct {
	persisted             bool
	sessionFile           string
	sessionID             string
	sessionDir            string
	usesDefaultSessionDir bool
}

// formatResumeCommand returns no command for non-TTY output, in-memory sessions, or an absent session file. stdoutIsTTY is the current stdout terminal state.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts:309-321.
func formatResumeCommand(sessionManager resumeCommandSession, stdoutIsTTY bool) string {
	if !stdoutIsTTY || !sessionManager.persisted {
		return ""
	}
	return resumeCommand(sessionManager.sessionFile, sessionManager.sessionID, sessionManager.sessionDir, !sessionManager.usesDefaultSessionDir)
}

// resumeCommand formats a session ID and optional custom directory only when the session file exists. includeSessionDir preserves an explicitly empty custom directory.
func resumeCommand(sessionPath, sessionID, sessionDir string, includeSessionDir bool) string {
	if sessionPath == "" {
		return "" // not persisted (--no-session)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		return ""
	}
	parts := []string{AppName}
	if includeSessionDir {
		parts = append(parts, "--session-dir", quoteIfNeeded(sessionDir))
	}
	parts = append(parts, "--session", sessionID)
	return strings.Join(parts, " ")
}

var stdoutIsTTY = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// printResumeHint writes the resume command after the terminal is restored on an interactive quit, only for a persisted session with an existing file and TTY stdout.
func (m *InteractiveMode) printResumeHint() {
	session := m.currentSession()
	if session == nil {
		return
	}
	resolvedDir, _ := nodepath.Resolve(m.opts.SessionDir)
	defaultDir, _ := nodepath.Resolve(defaultSessionDir(session.CWD()))
	cmd := formatResumeCommand(resumeCommandSession{
		persisted:             session.Path() != "",
		sessionFile:           session.Path(),
		sessionID:             session.ID(),
		sessionDir:            m.opts.SessionDir,
		usesDefaultSessionDir: m.opts.SessionDir == "" || resolvedDir == defaultDir,
	}, stdoutIsTTY())
	if cmd == "" {
		return
	}
	_, _ = fmt.Printf("\x1b[2mTo resume this session:\x1b[22m %s\n", cmd)
}

// Run renders the initial editor and footer even with quiet startup, then blocks in the interactive loop until the user exits.
func (m *InteractiveMode) Run(ctx context.Context) (err error) {
	// After Run returns nothing services owner tasks, so a runtime closed later resets the UI in place.
	defer m.runEnded.Store(true)
	// Registered first so it runs last, after the deferred TUI teardown has
	// restored the terminal. Mirrors upstream's uncaughtException handler.
	defer func() {
		if value := recover(); value != nil {
			m.uncaughtCrash(value, debug.Stack(), os.Stderr)
			err = ErrInteractiveCrashed
		}
	}()
	m.runCtx = ctx
	m.backgroundCtx, m.backgroundCancel = context.WithCancel(ctx)
	defer func() {
		m.backgroundCancel()
		m.disposeArminComponents()
		m.disposeMarkdownBlocks()
		m.backgroundTasks.Wait()
		m.userBashTasks = nil
		m.backgroundCtx = nil
	}()
	mark := m.opts.StartupMark
	if mark == nil {
		mark = func(string) {} // no-op
	}
	m.initStartupTheme()
	mark("theme-detected")

	// Set up TUI. Fullscreen mode uses the alternate-screen renderer; both
	// satisfy tui.Renderer so the rest of the driver is renderer-agnostic.
	// createInteractiveTui owns renderer construction plus the owner-loop-scoped
	// lifetime of the fullscreen tick seam, storing the cleanup on m; teardown
	// runs the current renderer's cleanup on every Run return (so a future live
	// renderer swap tears down the replacement, not this initial closure).
	m.createInteractiveTui(ctx)
	defer m.teardownCurrentTui()
	m.tuiInst.SetShowHardwareCursor(m.opts.Settings.GetShowHardwareCursor())
	m.tuiInst.SetClearOnShrink(m.opts.Settings.GetClearOnShrink())
	m.installRenderDispatcher()
	themeWatcher := m.startThemeWatcher(ctx)
	defer themeWatcher.Close()
	m.tuiInst.SetOverlayCommandDispatcher(func(command func()) {
		m.runOnMain(m.runCtx, command)
	})
	mark("pre-raw-mode")
	restore, drain, err := tui.EnterRawModeWithDrain()
	if err != nil {
		return fmt.Errorf("interactive: raw mode: %w", err)
	}
	defer func() {
		if m.rawRestore != nil {
			m.rawRestore()
		}
	}()
	mark("raw-mode-entered")
	m.rawRestore = restore
	m.rawDrain = drain
	m.tuiInst.HideCursor()
	defer m.tuiInst.ShowCursor()
	// Centralized, idempotent renderer teardown runs before the deferred cursor and cooked-mode restoration on normal quit or graceful signal shutdown. A dead-terminal emergency exits without unwinding these terminal writes.
	defer m.stopInteractiveTui()

	// Clear screen
	// Inline-flow rendering: do NOT clear the screen. The first call to
	// tui.Render() will emit our chat using `\r\n` so the user's pre-
	// pig shell history (motd, last command output) stays put above us
	// and our content scrolls into the same scrollback as a normal
	// shell session.

	// Build the shared header before extensions bind so session_start handlers
	// replace an already-mounted built-in identity.
	// extHeader/extFooter invalidate through m.renderNow, which resolves
	// m.tuiInst under rendererMu at call time. A live tui-mode switch (switchTuiMode)
	// that replaces the renderer repaints through the new one, and the RLock keeps
	// an off-owner-loop invalidation from racing the swap or hitting a stopped
	// renderer.
	m.keybindings = NewKeybindingsManager(m.opts.AgentDir)
	m.publishExtensionKeybindings()
	m.extHeader = newSpecialLinesComponent(m.renderNow)
	m.setBuiltInHeader(m.opts.Verbose || m.toolsExpanded)
	m.loadedResourcesContainer = tui.NewContainer()
	m.chatContainer = tui.NewContainer()
	m.extFooter = newSpecialLinesComponent(m.renderNow)
	mark("tui-layout-built")
	m.editor = tui.NewEditor()
	m.bindEditorSnapshot()
	m.editor.EmbedWorkingStatus = true
	defer m.clearStatusIndicator("")
	m.editorContainer = tui.NewContainer()
	m.editorContainer.Add(m.editor)
	m.tuiInst.SetFocus(m.editor)
	m.editor.SetPaddingX(m.opts.Settings.GetEditorPaddingX())
	m.editor.SetAutocompleteMaxVisible(m.opts.Settings.GetAutocompleteMaxVisible())
	m.startTerminalInput(ctx, os.Stdin)
	defer func() {
		if inputErr := m.stopTerminalInput(); inputErr != nil {
			err = errors.Join(err, inputErr)
		}
	}()
	m.beginStartupSubmitWindow()
	m.applyThemeFromSettings(ctx)
	if err := m.waitForTerminalColors(ctx); err != nil {
		return err
	}
	// Run the @-file fd search off the input thread so a deep tree walk
	// (e.g. @~/<rare-name>) can't freeze typing. The worker posts its
	// result back here via uiTaskCh so the editor is mutated and rendered
	// only on the main loop, never from the worker goroutine (which would
	// race the lock-free editor state).
	m.editor.SetAsyncApply(func(apply func()) {
		m.postUITask(func() { apply(); m.tuiInst.RequestRender() })
	})
	m.editor.SetAutocompleteTaskOwner(m.backgroundCtx, m.backgroundTasks.Go, m.reportAutocompleteError)
	m.editor.SetAutocomplete(m.buildAutocompleteProvider())
	m.applyEditorMaxVisible()
	m.statusLine = m.newFooter() // timings rebound after agent constructed
	m.statusLine.SetStatusHook(m.showStatus)
	m.statusLine.SetCwd(m.opts.CWD)
	// Set auto-compact indicator from settings (nil == true default).
	autoCompact := m.opts.Settings.Compaction == nil ||
		m.opts.Settings.Compaction.Enabled == nil ||
		*m.opts.Settings.Compaction.Enabled
	m.statusLine.SetAutoCompactEnabled(autoCompact)
	// Provider count + subscription indicator for footer.
	// Mirrors upstream updateAvailableProviderCount (interactive-mode.ts:3875).
	m.updateProviderInfo()

	// Warn if Anthropic subscription auth is active. Auth resolution can run
	// commands or refresh credential state, so keep it off the owner loop.
	// Mirrors upstream's intentionally unawaited call at interactive-mode.ts:690.
	m.maybeWarnAboutAnthropicSubscriptionAuthAsync()

	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.widgetContainer = tui.NewContainer(tui.NewSpacer(1))
	m.mountInteractiveTui()
	// Pi sets up managed tools after the startup header and before extensions, still under handleStartupSubmit; setupEditorSubmitHandler follows (interactive-mode.ts:1017-1028).
	m.ensureManagedTools(ctx, tools.NewToolsManager(m.opts.AgentDir))
	m.endStartupSubmitWindow()

	// Build slash registry up front so any extension's RegisterCommand
	// can mirror its registration into the registry as it loads.
	m.slashRegistry = NewSlashRegistry()

	// Load file-based prompt templates from <agentDir>/prompts and
	// <cwd>/.pig/prompts. They remain fixed for this Session.
	m.loadPromptTemplates()
	if m.opts.ResourceSourceInfoProvider != nil {
		m.resourceSourceInfo = m.opts.ResourceSourceInfoProvider()
	}
	m.publishSlashCommandCatalog()

	// Set up extension context
	uiCtx := NewTUIUIContext(m.tuiInst)
	uiCtx.interactiveMode = m

	// pig-specific: wire subprocess extension UIBridge to the live TUI, and the
	// terminal width to the host, so extensions receive the real width in their
	// ready payload and get width_change notifications on resize. Mirrors
	// upstream where setWidget factory components receive width at render time.
	m.attachSubprocess()
	defer func() {
		if m.detachModelRegistry != nil {
			m.detachModelRegistry()
		}
	}()
	// Registered unconditionally and outside the SubprocessHost guard: the
	// extension-dialog chat cap is derived from terminal height, so it must be
	// recomputed on resize even with no subprocess extensions loaded.
	m.tuiInst.SetOnHeightChange(m.onTerminalHeightChange)

	abortCtx, abortFn := context.WithCancel(ctx)
	m.abortCtx = abortCtx
	m.abortFn = abortFn

	// Set up extension context (caller-supplied if pre-loaded)
	var extCtx *ExtensionContext
	if m.opts.ExtensionContext != nil {
		extCtx = m.opts.ExtensionContext
		// Re-bind UI/abort to this Run's TUI even though the caller
		// pre-loaded extensions; the TUI handle is per-Run.
		extCtx.UI = uiCtx
		extCtx.HasUI = true
		extCtx.AbortSignal = abortCtx
		extCtx.AbortFunc = abortFn
		extCtx.IsIdle = m.extensionIsIdle
	} else {
		extCtx = &ExtensionContext{
			UI:               uiCtx,
			HasUI:            true,
			CWD:              m.opts.CWD,
			IsIdle:           m.extensionIsIdle,
			IsProjectTrusted: func() bool { return m.projectTrusted() },
			AbortSignal:      abortCtx,
			AbortFunc:        abortFn,
		}
	}

	m.extCtx = extCtx

	// Wire the extension runner. May be nil; event_bridge.go helpers are nil-safe.
	m.newRunner = m.opts.ExtensionRunner
	m.installRuntimeHooks()
	// The rebind owner installs UI/actions before dispatching session_start.

	// Wire agent + on-disk session: the SDK is the source of truth.
	// SessionHandle MUST be supplied by the caller (cmd/pig/main.go
	// constructs it via coding.NewSession). This is the F-2 contract
	// post-Chunk F: there is no parallel TUI-only construction path.
	if m.opts.SessionHandle == nil {
		return fmt.Errorf("interactive: SessionHandle is required: construct via coding.NewSession")
	}
	m.setAgent(m.opts.SessionHandle.Agent())
	m.installRunPromptTurnRefresh()
	m.rebuildToolSystemPrompt()
	m.eventCh = m.opts.SessionHandle.Events()
	extCtx.Session = m.currentSession()

	m.reconfigureRemoteEditor()

	// pig divergence (D55): ignore Kitty key releases for the debug hotkey.
	m.addKeyPressListener(func(data string) bool {
		if !tui.MatchesKeyID(data, "ctrl+shift+d") {
			return false
		}
		m.dispatchSlash(ctx, "/debug")
		return true
	})

	// Wire steering/follow-up queue modes from settings.
	if sm := m.opts.SettingsManager; sm != nil {
		if mode := sm.GetSteeringMode(); mode != "" {
			m.agent.SetSteeringMode(agent.QueueMode(mode))
		}
		if mode := sm.GetFollowUpMode(); mode != "" {
			m.agent.SetFollowUpMode(agent.QueueMode(mode))
		}
	}

	// Bind the agent's timings recorder to the status line so the
	// elapsed/spinner column reads live from the same source the cost
	// summary does.
	m.statusLine.timings = m.agent.Timings()

	// Set terminal title. Mirrors upstream
	// interactive-mode.ts:634-642 (updateTerminalTitle). Deferred clear
	// restores the shell's own title on exit.
	defer tui.SetTerminalTitle("")
	defer tui.SetTerminalProgress(false)
	// Pre-populate status-line name from session on resume.
	if n := m.currentSession().GetSessionName(); n != "" {
		m.statusLine.SetName(n)
	}

	// Bind thinking presentation to the Session's effective startup state.
	m.initThinkingLevel()

	m.initScopedModels()

	// Dispatch session_start through the extension runner. The bridge performs
	// the typed event conversion.
	mark("pre-emit-session-start")
	if err := m.awaitExtensionUI(ctx, func(ctx context.Context) error {
		return m.rebindCurrentSession(ctx, false)
	}); err != nil {
		return err
	}
	mark("post-emit-session-start")

	// Loaded resources follow the built-in or extension-supplied header.
	m.showLoadedResources(false, true)
	// Quiet startup suppresses help and resource listings, not the initial editor and footer.
	m.tuiInst.Render()
	mark("first-render-done")

	m.showStartupDiagnostics()
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:init displays modelFallbackMessage after startup diagnostics, even under quiet startup.
	if m.opts.NoModelWarning != "" {
		m.showWarning(m.opts.NoModelWarning)
	}
	m.showCrashNotice()

	// Render resumed messages after loaded resources without clearing either.
	if m.opts.ResumePath != "" {
		m.renderSessionEntries()
	}
	m.renderProjectTrustWarningIfNeeded()
	m.tuiInst.Render()

	// Show "what's new" on fresh sessions (no prior messages) when the
	// binary version differs from the last recorded version, and send the
	// install-telemetry ping on the same two occasions upstream does: a
	// fresh install and an update with new changelog entries.
	// Mirrors upstream getChangelogForDisplay + showStartupNoticesIfNeeded
	// (interactive-mode.ts:816-843, 487-522, 1265-1307).
	if m.opts.SettingsManager != nil && m.opts.AppVersion != "" && m.opts.ResumePath == "" {
		allEntries := ParseChangelog(pig.Changelog)
		if newEntries := recordChangelogVersionAndMaybeReportInstall(m.opts.SettingsManager, m.opts.AppVersion, allEntries); len(newEntries) > 0 {
			var body strings.Builder
			for i, newEntrie := range slices.Backward(newEntries) {
				body.WriteString(newEntrie.Content)
				if i > 0 {
					body.WriteString("\n\n")
				}
			}
			m.appendToChat(tui.NewMarkdown(
				"---\n\n**What's New**\n\n" + body.String() + "\n\n---"))
			m.tuiInst.Render()
		}
	}

	// Check tmux keyboard setup asynchronously (mirrors upstream
	// checkTmuxKeyboardSetup, interactive-mode.ts:667-675, 768-818).
	go func() {
		if warning := checkTmuxKeyboardSetup(); warning != "" {
			// appendChatBlock mutates the render tree and Render reads it;
			// both are owned by the main input loop. Post the work there
			// instead of touching the tree from this goroutine, which races
			// the loop's editor/tree access (invalidatable.dirty and the
			// editor's unsynchronized fields).
			m.postUITask(func() { m.showWarning(warning) })
		}
	}()

	// Check for package updates asynchronously. Mirrors upstream
	// checkForPackageUpdates + showPackageUpdateNotification
	// (interactive-mode.ts:660-665, 3449).
	if m.opts.PackageUpdateChecker != nil {
		go func() {
			updates := m.opts.PackageUpdateChecker()
			m.postUITask(func() { m.finishPackageUpdateCheck(runtime.GOOS, updates) })
		}()
	}

	// Refresh dynamic model catalogs after TUI initialization unless offline,
	// abandoning the refresh after 15 s. Mirrors upstream interactive-mode.ts
	// run() → refreshModelCatalogs(...).then(updateAvailableProviderCount).
	if m.opts.Llama != nil && !m.opts.OfflineMode {
		go func() {
			refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			m.opts.Llama.Refresh(refreshCtx, true)
			m.postUITask(m.updateProviderInfo)
		}()
	}

	// Check for a newer pig binary asynchronously. pig divergence (D39):
	// standalone-binary self-update; upstream pi relies on the package manager.
	if m.opts.BinaryUpdateChecker != nil {
		go func() {
			update := m.opts.BinaryUpdateChecker()
			if update == nil {
				return
			}
			m.postUITask(func() { m.showNewVersionNotification(update) })
		}()
	}

	// Share the interactive catalog refresh with selectors, with independent cancellation for each caller.
	if m.opts.ModelRegistry != nil && ModelNetworkEnabled() {
		m.backgroundTasks.Go(func() {
			// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:run
			refreshCtx, cancel := context.WithTimeout(m.backgroundCtx, 15*time.Second)
			defer cancel()
			if _, err := RefreshModelCatalogs(refreshCtx, m.opts.ModelRegistry); err == nil {
				_ = m.postToMain(m.backgroundCtx, m.updateProviderInfo)
			}
		})
	}

	// Agent events are drained by the inputLoop select (case <-m.eventCh →
	// handleAgentEvent), so they run on the main goroutine single-threaded with
	// keystrokes and posted UI tasks. No separate event-processor goroutine.

	// Start spinner ticker
	go m.tickSpinner(ctx)

	m.startGitBranchWatcher(ctx)

	// Upstream registers no general SIGINT handler, so an external SIGINT
	// terminates pi (interactive-mode.ts registers SIGTERM, plus SIGHUP off
	// win32, and takes SIGINT only to ignore it while suspended). Terminate
	// too, but hand the terminal back first: Node's default handler does not,
	// so `kill -INT` leaves pi's user with ISIG off and a dead Ctrl+C at the
	// shell. Ctrl+C itself never arrives here, because pig holds the terminal
	// in raw mode and the keymap consumes \x03.
	intCh := make(chan os.Signal, 1)
	signal.Notify(intCh, syscall.SIGINT)
	defer signal.Stop(intCh)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-intCh:
				m.handleInterruptSignal()
			}
		}
	}()

	// Terminal resize re-render. The TUI re-queries term.GetSize on every
	// Render() but renders only fire on input/stream/flash events, so a resize
	// while pig is idle leaves stale-width rules until the next keystroke. Unix
	// drives this from SIGWINCH; Windows (no SIGWINCH) polls the console size.
	// Mirrors upstream `process.stdout.on("resize", ...)`.
	stopResize := m.installResizeHandler(ctx)
	defer stopResize()

	// Auto-submit initial message (positional arg / piped
	// stdin) once the TUI is up. Mirrors upstream's initial-message
	// dispatch in coding-agent/cli/initial-message.ts: the message
	// is fed through the same handleSubmit path the user's keystrokes
	// hit, so transcript rendering / session writes / extension
	// hooks all see it identically.
	if strings.TrimSpace(m.opts.InitialMessage) != "" || len(m.opts.InitialImages) > 0 {
		m.handleSubmitWithImages(ctx, m.opts.InitialMessage, m.opts.InitialImages)
	}
	if len(m.opts.InitialMessages) > 0 {
		done := make(chan struct{})
		m.initialMessagesDone = done
		m.backgroundTasks.Go(func() {
			defer close(done)
			m.submitInitialMessages(ctx, m.opts.InitialMessages)
		})
	}
	mark("interactive-ready")
	return m.inputLoop(ctx, os.Stdin)
}

// LoadThemePaths adds theme files and directories in upstream precedence order, the first theme of a name winning. Report receives each unreadable or invalid path.
func LoadThemePaths(registry *tui.ThemeRegistry, paths []string, report func(error)) {
	_, diagnostics := loadThemeResources(registry, paths, tui.GetTerminalColorMode())
	if report == nil {
		return
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Type == extension.DiagnosticWarning {
			report(errors.New(diagnostic.Message))
		}
	}
}
