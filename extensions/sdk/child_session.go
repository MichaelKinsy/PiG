package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Ports packages/coding-agent/src/core/sdk.ts createAgentSession for an extension: a child agent session the host owns for this extension's connection. The extension prompts it, listens to its events, aborts it and disposes it; the host runs the agent loop and calls back into the extension for the tools it serves (customTools). The Node runtime serves Pi's own createAgentSession in the extension process; this is the same call for the native SDKs, with the same observable behavior (`TestChildAgentSessionsAcrossSDKs`).
//
// Async contract (Pi's `Promise`s, kept as blocking calls):
//   - CreateAgentSession is awaited: resources are loaded and the model resolved before it returns, and a failure is its error.
//   - Prompt returns when the run has settled, after every listener has run through the run's last event; a prompt that is rejected (no model, no credentials, a streaming session without a delivery) is its error. Cancelling its context aborts the run, as an extension ties its AbortSignal to session.abort(); the run then settles and Prompt returns nil, as Pi's does after an abort.
//   - Abort returns once the session is idle. Dispose is synchronous and idempotent.
//   - Listeners run in event order on one goroutine of the session, never on the connection's reader, so a listener may call the host or the session. A slow listener delays later events, not the agent.
//   - The session ends with the connection: its run is aborted, its tools' contexts are cancelled and no listener runs afterwards.

// CreateAgentSessionOptions are the members of Pi's CreateAgentSessionOptions (core/sdk.ts) that cross the process boundary. The zero value of a member is Pi's `undefined`.
//
// The child shares the model runtime of the host's Session (Pi's `modelRuntime: ctx.modelRegistry.runtime`), so every provider the extensions registered is available to it.
type CreateAgentSessionOptions struct {
	// Cwd is the working directory for project-local discovery. Empty is the host's.
	Cwd string
	// AgentDir is the global config directory. Empty is the host's.
	AgentDir string
	// Model is the model: a model object, such as the one [ModelRegistry.Find] returns; resolve a name or pattern through the registry first, as Pi's option takes a Model. Nil is Pi's default: the settings' default, else the first available model.
	Model any
	// ThinkingLevel is the requested level, clamped to the model. Empty is the settings' default.
	ThinkingLevel string
	// Tools is the allowlist of tool names or `*` patterns, or a list of only `+name` and `-name` entries that changes the default selection. Nil is Pi's undefined, an empty non-nil list is an allowlist of nothing.
	Tools []string
	// ExcludeTools is the denylist applied after Tools.
	ExcludeTools []string
	// NoTools is "all" (no tools) or "builtin" (no default built-in tools; extension and custom tools stay enabled).
	NoTools string
	// CustomTools are tools this extension serves to the session. Execute, PrepareArguments, ExecutionMode and the model-facing metadata apply; renderers, PrepareLoadout and ReachesTools have no meaning in a child. The ToolFunc's Context is the extension's, with the tool call's cancellation.
	CustomTools []ToolDefinition
	// Settings are in-memory settings, Pi's `SettingsManager.inMemory(settings)`: the session reads no settings file, so `{"compaction": {"enabled": false}}` turns compaction off. Nil is the settings files of Cwd and AgentDir.
	Settings map[string]any
	// InMemory is `SessionManager.inMemory(cwd)`: the session log is never written. False is `SessionManager.create(cwd)`.
	InMemory bool
	// Resources selects what the session's resource loader loads. The zero value is Pi's default loader.
	Resources ResourceOptions
	// SessionManager, SettingsManager, ResourceLoader and ModelRuntime are Pi's `sessionManager`, `settingsManager`, `resourceLoader` and `modelRuntime` options: objects this extension built (SessionManagerCreate, SettingsManagerCreate, NewDefaultResourceLoader, ModelRuntimeCreate). The session uses the very objects, so what the extension does to them later is seen by the session. Each replaces the option that builds its own (InMemory, Settings, Resources; the host's model runtime), and naming both is an error.
	SessionManager  *SessionManager
	SettingsManager *SettingsManager
	ResourceLoader  *DefaultResourceLoader
	ModelRuntime    *ModelRuntime
	// ScopedModels is Pi's `scopedModels`: the models the session cycles through, each a model object with an optional thinking level.
	ScopedModels []ScopedModel
}

// ResourceOptions are the DefaultResourceLoader options of Pi (resource-loader.ts) that select resources.
type ResourceOptions struct {
	NoExtensions      bool
	NoSkills          bool
	NoPromptTemplates bool
	NoThemes          bool
	NoContextFiles    bool
	// AdditionalExtensionPaths, AdditionalSkillPaths and AdditionalPromptTemplatePaths are loaded after the resolved resources.
	AdditionalExtensionPaths      []string
	AdditionalSkillPaths          []string
	AdditionalPromptTemplatePaths []string
	// ExtensionNames keeps only the extensions whose path contains one of the names, compared without case (Pi's `extensionsOverride`). Nil keeps all, an empty non-nil list keeps none.
	ExtensionNames []string
	// SkillNames keeps only the skills named exactly by one of the names (Pi's `skillsOverride`). Nil keeps all, an empty non-nil list keeps none.
	SkillNames []string
	// SystemPrompt replaces SYSTEM.md discovery, AppendSystemPrompt replaces APPEND_SYSTEM.md discovery; each entry is text or the path of a file holding it. Nil discovers.
	SystemPrompt       *string
	AppendSystemPrompt []string
}

// PromptOptions are the members of Pi's PromptOptions that cross the process boundary.
type PromptOptions struct {
	// ExpandPromptTemplates is Pi's option; nil is true.
	ExpandPromptTemplates *bool
	// Images are attached to the prompt.
	Images []PromptImage
	// StreamingBehavior is "steer" or "followUp"; a prompt to a session that is running needs it.
	StreamingBehavior string
	// Source is the input source: "interactive" (default), "rpc" or "extension".
	Source string
}

// PromptImage is an image of a prompt.
type PromptImage struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// AgentSessionState is the session's state at the moment of the read.
type AgentSessionState struct {
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile,omitempty"`
	// Model is the session's model object, nil without one.
	Model         map[string]any `json:"model"`
	IsStreaming   bool           `json:"isStreaming"`
	IsIdle        bool           `json:"isIdle"`
	ThinkingLevel string         `json:"thinkingLevel"`
	// ActiveTools is `session.getActiveToolNames()`.
	ActiveTools  []string `json:"activeTools"`
	SystemPrompt string   `json:"systemPrompt"`
	// PendingMessageCount is `session.pendingMessageCount`.
	PendingMessageCount int `json:"pendingMessageCount"`
	// LastAssistantText is `session.getLastAssistantText()`, nil when there is none.
	LastAssistantText *string `json:"lastAssistantText,omitempty"`
}

// AgentSessionEventListener receives the AgentSessionEvents of a session in Pi's JSON shape (`type`, then the event's members): agent_start, turn_start, message_start, message_update, message_end, tool_execution_start, tool_execution_update, tool_execution_end, turn_end, agent_end, compaction_*, auto_retry_*, and the rest of Pi's union. The map is shared by the session's listeners.
type AgentSessionEventListener func(event map[string]any)

// Wire names of the child session calls (coding/extension/host/subprocess/protocol_child_session.go).
const (
	callChildSessionCreate         = "childSession.create"
	callChildSessionPrompt         = "childSession.prompt"
	callChildSessionQueue          = "childSession.queue"
	callChildSessionAbort          = "childSession.abort"
	callChildSessionDispose        = "childSession.dispose"
	callChildSessionBindExtensions = "childSession.bindExtensions"
	callChildSessionState          = "childSession.state"
	callChildSessionMessages       = "childSession.messages"
	notifyChildSessionEvent        = "childSession.event"
)

type childSessionCreateMsg struct {
	ID              string                 `json:"id"`
	Cwd             string                 `json:"cwd,omitempty"`
	AgentDir        string                 `json:"agentDir,omitempty"`
	Model           any                    `json:"model,omitempty"`
	ThinkingLevel   string                 `json:"thinkingLevel,omitempty"`
	Tools           *[]string              `json:"tools,omitempty"`
	ExcludeTools    []string               `json:"excludeTools,omitempty"`
	NoTools         string                 `json:"noTools,omitempty"`
	CustomTools     []toolDef              `json:"customTools,omitempty"`
	Settings        map[string]any         `json:"settings,omitempty"`
	InMemory        bool                   `json:"inMemory,omitempty"`
	Resources       *childSessionResources `json:"resources,omitempty"`
	SessionManager  string                 `json:"sessionManager,omitempty"`
	SettingsManager string                 `json:"settingsManager,omitempty"`
	ResourceLoader  string                 `json:"resourceLoader,omitempty"`
	ModelRuntime    string                 `json:"modelRuntime,omitempty"`
	ScopedModels    []ScopedModel          `json:"scopedModels,omitempty"`
}

type childSessionResources struct {
	NoExtensions                  bool      `json:"noExtensions,omitempty"`
	NoSkills                      bool      `json:"noSkills,omitempty"`
	NoPromptTemplates             bool      `json:"noPromptTemplates,omitempty"`
	NoThemes                      bool      `json:"noThemes,omitempty"`
	NoContextFiles                bool      `json:"noContextFiles,omitempty"`
	AdditionalExtensionPaths      []string  `json:"additionalExtensionPaths,omitempty"`
	AdditionalSkillPaths          []string  `json:"additionalSkillPaths,omitempty"`
	AdditionalPromptTemplatePaths []string  `json:"additionalPromptTemplatePaths,omitempty"`
	ExtensionNames                *[]string `json:"extensionNames,omitempty"`
	SkillNames                    *[]string `json:"skillNames,omitempty"`
	SystemPrompt                  *string   `json:"systemPrompt,omitempty"`
	AppendSystemPrompt            *[]string `json:"appendSystemPrompt,omitempty"`
}

type childSessionInfo struct {
	SessionID            string         `json:"sessionId"`
	SessionFile          string         `json:"sessionFile"`
	Model                map[string]any `json:"model"`
	ThinkingLevel        string         `json:"thinkingLevel"`
	ActiveTools          []string       `json:"activeTools"`
	SystemPrompt         string         `json:"systemPrompt"`
	ModelFallbackMessage string         `json:"modelFallbackMessage"`
	ExtensionErrors      []string       `json:"extensionErrors"`
}

func resourcesWire(r ResourceOptions) *childSessionResources {
	wire := &childSessionResources{
		NoExtensions: r.NoExtensions, NoSkills: r.NoSkills, NoPromptTemplates: r.NoPromptTemplates, NoThemes: r.NoThemes, NoContextFiles: r.NoContextFiles,
		AdditionalExtensionPaths: r.AdditionalExtensionPaths, AdditionalSkillPaths: r.AdditionalSkillPaths, AdditionalPromptTemplatePaths: r.AdditionalPromptTemplatePaths,
		SystemPrompt: r.SystemPrompt,
	}
	if r.ExtensionNames != nil {
		wire.ExtensionNames = &r.ExtensionNames
	}
	if r.SkillNames != nil {
		wire.SkillNames = &r.SkillNames
	}
	if r.AppendSystemPrompt != nil {
		wire.AppendSystemPrompt = &r.AppendSystemPrompt
	}
	if reflect.DeepEqual(*wire, childSessionResources{}) {
		return nil
	}
	return wire
}

// childSessions is the extension's live child sessions.
type childSessions struct {
	mu     sync.Mutex
	byID   map[string]*AgentSession
	closed bool
	nextID atomic.Uint64
}

// toolHandler finds the execute of a tool_call: a registered tool, or a customTool of the child session the request names.
func (e *Extension) toolHandler(req *requestMsg) (ToolFunc, bool) {
	if req.ChildSession != "" {
		if session := e.children.lookup(req.ChildSession); session != nil {
			handler, ok := session.tools[req.Tool]
			return handler, ok
		}
		return nil, false
	}
	e.toolMu.RLock()
	defer e.toolMu.RUnlock()
	handler, ok := e.toolFuncs[req.Tool]
	return handler, ok
}

// toolPrepare finds the prepareArguments of a request; see toolHandler.
func (e *Extension) toolPrepare(req *requestMsg) ToolPrepareArgumentsFunc {
	if req.ChildSession != "" {
		if session := e.children.lookup(req.ChildSession); session != nil {
			return session.prepares[req.Tool]
		}
		return nil
	}
	e.toolMu.RLock()
	defer e.toolMu.RUnlock()
	return e.toolPrepareFuncs[req.Tool]
}

func (t *childSessions) lookup(id string) *AgentSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.byID[id]
}

// add records a created session; it reports false once the connection has ended.
func (t *childSessions) add(session *AgentSession) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	if t.byID == nil {
		t.byID = map[string]*AgentSession{}
	}
	t.byID[session.id] = session
	return true
}

func (t *childSessions) remove(id string) {
	t.mu.Lock()
	delete(t.byID, id)
	t.mu.Unlock()
}

// closeAll ends every session without telling the host: it is disposing them as the connection closes.
func (t *childSessions) closeAll() {
	t.mu.Lock()
	t.closed = true
	sessions := t.byID
	t.byID = nil
	t.mu.Unlock()
	for _, session := range sessions {
		session.stop()
	}
}

// deliver queues one childSession.event notification on its session. The reader calls it, so it only queues.
func (t *childSessions) deliver(args json.RawMessage) {
	var event struct {
		ID     string          `json:"id"`
		Seq    uint64          `json:"seq"`
		Event  json.RawMessage `json:"event"`
		Stream *eventStreamMsg `json:"stream"`
	}
	if json.Unmarshal(args, &event) != nil {
		return
	}
	session := t.lookup(event.ID)
	if session == nil {
		return
	}
	if event.Stream == nil {
		session.enqueue(queuedEvent{seq: event.Seq, raw: event.Event})
		return
	}
	// A message_update of the session's sequence: the full event is the keyframe, or the replica the operations turned into it.
	full, err := session.applyUpdate(event.Stream, event.Event)
	if err != nil {
		// The event is not delivered; its number still counts, so a prompt does not wait for it.
		fmt.Fprintf(os.Stderr, "pig: child session %s: %v\n", session.id, err)
		session.enqueue(queuedEvent{seq: event.Seq})
		return
	}
	session.enqueue(queuedEvent{seq: event.Seq, event: full})
}

// applyUpdate returns the full message_update event the stream member produces, and keeps it as the payload the next patch applies to. Only the connection's reader calls it, in arrival order.
func (s *AgentSession) applyUpdate(stream *eventStreamMsg, keyframe json.RawMessage) (map[string]any, error) {
	var root any
	if len(keyframe) > 0 {
		if err := json.Unmarshal(keyframe, &root); err != nil {
			s.replica = nil
			return nil, fmt.Errorf("event stream: decode keyframe: %w", err)
		}
	} else {
		if s.replica == nil || s.replicaSeq != stream.Base {
			s.replica = nil
			return nil, fmt.Errorf("message_update delta applies to payload %d, the replica holds a different one", stream.Base)
		}
		var err error
		if root, err = applyStreamOps(s.replica, stream.Ops); err != nil {
			s.replica = nil
			return nil, err
		}
	}
	s.replica, s.replicaSeq = root, stream.Seq
	full, _ := cloneStreamValue(root).(map[string]any)
	if full == nil {
		return nil, errors.New("event stream: the payload is not an object")
	}
	return full, nil
}

// AgentSession is a child agent session: Pi's AgentSession as an extension holds it.
type AgentSession struct {
	ext      *Extension
	conn     *conn
	id       string
	tools    map[string]ToolFunc
	prepares map[string]ToolPrepareArgumentsFunc

	// SessionID and SessionFile are `session.sessionId` and `session.sessionFile` (empty for an in-memory session).
	SessionID   string
	SessionFile string
	// Model, ThinkingLevel, ActiveTools and SystemPrompt are what the session held when CreateAgentSession returned. Read the current values with [AgentSession.State].
	Model         map[string]any
	ThinkingLevel string
	ActiveTools   []string
	SystemPrompt  string
	// ModelFallbackMessage is CreateAgentSessionResult.modelFallbackMessage.
	ModelFallbackMessage string
	// ExtensionErrors are the errors of CreateAgentSessionResult.extensionsResult.errors, as "path: error".
	ExtensionErrors []string

	mu        sync.Mutex
	cond      *sync.Cond
	listeners []*childListener
	pending   []queuedEvent
	// replica is the message_update payload the host last sent and replicaSeq its number; the connection's reader owns them.
	replica    any
	replicaSeq uint64
	// delivered is the number of the last event whose listeners ran.
	delivered uint64
	stopped   bool
	running   bool
	disposed  atomic.Bool
}

type childListener struct{ fn AgentSessionEventListener }

// queuedEvent is one event: its JSON, or the object a message_update's stream produced.
type queuedEvent struct {
	seq   uint64
	raw   json.RawMessage
	event map[string]any
}

// CreateAgentSession is Pi's createAgentSession: it creates a child agent session in the host and returns once it is ready. The session belongs to this extension's connection and is disposed with it.
func (c Context) CreateAgentSession(options CreateAgentSessionOptions) (*AgentSession, error) {
	e := c.ext
	e.toolMu.RLock()
	conn := e.toolConn
	e.toolMu.RUnlock()
	if conn == nil {
		return nil, errors.New("the extension is not connected to the host")
	}
	create := childSessionCreateMsg{
		ID: fmt.Sprintf("c%d", e.children.nextID.Add(1)), Cwd: options.Cwd, AgentDir: options.AgentDir, Model: options.Model, ThinkingLevel: options.ThinkingLevel,
		ExcludeTools: options.ExcludeTools, NoTools: options.NoTools, Settings: options.Settings, InMemory: options.InMemory, Resources: resourcesWire(options.Resources),
	}
	if options.Tools != nil {
		create.Tools = &options.Tools
	}
	if options.SessionManager != nil {
		create.SessionManager = options.SessionManager.Handle()
	}
	if options.SettingsManager != nil {
		create.SettingsManager = options.SettingsManager.Handle()
	}
	if options.ResourceLoader != nil {
		create.ResourceLoader = options.ResourceLoader.Handle()
	}
	if options.ModelRuntime != nil {
		create.ModelRuntime = options.ModelRuntime.Handle()
	}
	create.ScopedModels = options.ScopedModels
	session := &AgentSession{ext: e, conn: conn, id: create.ID, tools: map[string]ToolFunc{}, prepares: map[string]ToolPrepareArgumentsFunc{}}
	session.cond = sync.NewCond(&session.mu)
	for _, definition := range options.CustomTools {
		if definition.Parameters == nil {
			return nil, fmt.Errorf(`Tool "%s" registered by extension "%s" must define an object parameter schema.`, definition.Name, e.name)
		}
		if definition.Execute == nil {
			return nil, fmt.Errorf("custom tool %q has no Execute", definition.Name)
		}
		create.CustomTools = append(create.CustomTools, toolDef{
			Name: definition.Name, Label: definition.Label, Description: definition.Description, Parameters: definition.Parameters,
			PromptGuidelines: definition.PromptGuidelines, PromptSnippet: definition.PromptSnippet, ExecutionMode: definition.ExecutionMode,
			ConstrainedSampling: constrainedSamplingWire(definition.ConstrainedSampling),
			OutputSchema:        definition.OutputSchema, Exposure: definition.Exposure, Namespace: definition.Namespace, Annotations: definition.Annotations,
			DefaultActive: definition.DefaultActive, PreparesArguments: definition.PrepareArguments != nil,
		})
		session.tools[definition.Name] = definition.Execute
		if definition.PrepareArguments != nil {
			session.prepares[definition.Name] = definition.PrepareArguments
		}
	}
	// The tools are served from the moment the host knows the session, which can be before the call returns.
	if !e.children.add(session) {
		return nil, errors.New("Extension connection is closed")
	}
	result, err := c.callHost(callChildSessionCreate, create)
	if err == nil {
		err = callResultError(result, nil)
	}
	var info childSessionInfo
	if err == nil {
		err = json.Unmarshal(result.Result, &info)
	}
	if err != nil {
		e.children.remove(session.id)
		return nil, err
	}
	session.SessionID, session.SessionFile, session.Model = info.SessionID, info.SessionFile, info.Model
	session.ThinkingLevel, session.ActiveTools, session.SystemPrompt = info.ThinkingLevel, info.ActiveTools, info.SystemPrompt
	session.ModelFallbackMessage, session.ExtensionErrors = info.ModelFallbackMessage, info.ExtensionErrors
	return session, nil
}

// call is a host call that belongs to the session, not to the request that happens to use it: the session outlives the command that created it.
func (s *AgentSession) call(method string, args any) (json.RawMessage, error) {
	result, err := s.conn.call(method, args)
	if err := callResultError(result, err); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Result, nil
}

type sessionRef struct {
	ID string `json:"id"`
}

type promptMsg struct {
	ID                    string        `json:"id"`
	Text                  string        `json:"text"`
	Images                []PromptImage `json:"images,omitempty"`
	ExpandPromptTemplates *bool         `json:"expandPromptTemplates,omitempty"`
	StreamingBehavior     string        `json:"streamingBehavior,omitempty"`
	Source                string        `json:"source,omitempty"`
}

// Prompt is AgentSession.prompt: it runs the agent on text and returns when the run has settled, after the listeners have seen its last event. Cancelling ctx aborts the run, and the call then returns nil once it has settled.
func (s *AgentSession) Prompt(ctx context.Context, text string, options ...PromptOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	msg := promptMsg{ID: s.id, Text: text}
	if len(options) > 0 {
		msg.Images, msg.ExpandPromptTemplates, msg.StreamingBehavior, msg.Source = options[0].Images, options[0].ExpandPromptTemplates, options[0].StreamingBehavior, options[0].Source
	}
	if ctx.Done() != nil {
		stop := context.AfterFunc(ctx, func() {
			if err := s.Abort(); err != nil && !s.disposed.Load() {
				fmt.Fprintf(os.Stderr, "pig: abort of child session %s failed: %v\n", s.id, err)
			}
		})
		defer stop()
	}
	raw, err := s.call(callChildSessionPrompt, msg)
	if err != nil {
		return err
	}
	var result struct {
		Events uint64 `json:"events"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	s.waitDelivered(result.Events)
	return nil
}

// Steer queues text to the running session after the current turn's tool calls, before its next model call (AgentSession.steer).
func (s *AgentSession) Steer(text string, images ...PromptImage) error {
	return s.queue("steer", text, images)
}

// FollowUp queues text for after the run's current work finishes (AgentSession.followUp).
func (s *AgentSession) FollowUp(text string, images ...PromptImage) error {
	return s.queue("followUp", text, images)
}

func (s *AgentSession) queue(behavior, text string, images []PromptImage) error {
	_, err := s.call(callChildSessionQueue, struct {
		ID       string        `json:"id"`
		Behavior string        `json:"behavior"`
		Text     string        `json:"text"`
		Images   []PromptImage `json:"images,omitempty"`
	}{s.id, behavior, text, images})
	return err
}

// Abort is AgentSession.abort: it aborts the run and returns once the session is idle. A run that was aborted settles normally, so its Prompt returns nil.
func (s *AgentSession) Abort() error {
	_, err := s.call(callChildSessionAbort, sessionRef{s.id})
	return err
}

// BindExtensions is AgentSession.bindExtensions({}): the extensions the session's resources loaded start, headless, and receive session_start. Call it before prompting a session that has them.
func (s *AgentSession) BindExtensions() error {
	_, err := s.call(callChildSessionBindExtensions, sessionRef{s.id})
	return err
}

// State reads the session's current state.
func (s *AgentSession) State() (AgentSessionState, error) {
	var state AgentSessionState
	raw, err := s.call(callChildSessionState, sessionRef{s.id})
	if err == nil {
		err = json.Unmarshal(raw, &state)
	}
	return state, err
}

// Messages is `session.messages`: the transcript the session holds, in Pi's message shape.
func (s *AgentSession) Messages() ([]map[string]any, error) {
	var messages []map[string]any
	raw, err := s.call(callChildSessionMessages, sessionRef{s.id})
	if err == nil {
		err = json.Unmarshal(raw, &messages)
	}
	return messages, err
}

// Subscribe is AgentSession.subscribe: listener receives the session's events in order until the returned function unsubscribes it, the session is disposed or the connection ends. An unsubscribe takes effect before the next event.
func (s *AgentSession) Subscribe(listener AgentSessionEventListener) func() {
	entry := &childListener{fn: listener}
	s.mu.Lock()
	s.listeners = append(s.listeners, entry)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, candidate := range s.listeners {
			if candidate == entry {
				s.listeners = append(s.listeners[:i:i], s.listeners[i+1:]...)
				return
			}
		}
	}
}

// Dispose is AgentSession.dispose: it aborts the run, releases the session in the host and removes its listeners. It is idempotent, and a failure of the host call is reported where the host shows extension output.
func (s *AgentSession) Dispose() {
	if !s.disposed.CompareAndSwap(false, true) {
		return
	}
	s.stop()
	if _, err := s.call(callChildSessionDispose, sessionRef{s.id}); err != nil {
		fmt.Fprintf(os.Stderr, "pig: dispose of child session %s failed: %v\n", s.id, err)
	}
}

// stop ends the delivery: queued events are dropped and no listener runs again.
func (s *AgentSession) stop() {
	s.disposed.Store(true)
	s.mu.Lock()
	s.stopped = true
	s.listeners = nil
	s.pending = nil
	s.mu.Unlock()
	s.cond.Broadcast()
}

// enqueue adds an event; the session's delivery goroutine runs the listeners.
func (s *AgentSession) enqueue(event queuedEvent) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.pending = append(s.pending, event)
	start := !s.running
	s.running = true
	s.mu.Unlock()
	if start {
		go s.deliverLoop()
	}
}

// deliverLoop runs the listeners of each queued event in order, and exits when the queue is empty.
func (s *AgentSession) deliverLoop() {
	for {
		s.mu.Lock()
		if s.stopped || len(s.pending) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		next := s.pending[0]
		s.pending[0] = queuedEvent{}
		s.pending = s.pending[1:]
		listeners := make([]*childListener, len(s.listeners))
		copy(listeners, s.listeners)
		s.mu.Unlock()
		if len(listeners) != 0 {
			event := next.event
			var err error
			if event == nil {
				err = json.Unmarshal(next.raw, &event)
			}
			if err == nil {
				for _, listener := range listeners {
					s.call1(listener, event)
				}
			}
		}
		s.mu.Lock()
		s.delivered = next.seq
		s.mu.Unlock()
		s.cond.Broadcast()
	}
}

// call1 runs one listener; a panic in it ends that call, as a throwing listener does not stop Pi's emission.
func (s *AgentSession) call1(listener *childListener, event map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "pig: child session %s listener panicked: %v\n", s.id, r)
		}
	}()
	listener.fn(event)
}

// waitDelivered blocks until the listeners have run through event number seq, or the session ended.
func (s *AgentSession) waitDelivered(seq uint64) {
	s.mu.Lock()
	for s.delivered < seq && !s.stopped {
		s.cond.Wait()
	}
	s.mu.Unlock()
}
