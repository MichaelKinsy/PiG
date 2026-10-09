package agent

// Ports packages/agent/src/agent-loop.ts

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// AgentContext is the state a standalone loop starts from. Mirrors upstream AgentContext (types.ts): the transcript visible to the model and the tools available in the run.
type AgentContext struct {
	Messages []AgentMessage
	Tools    []AgentTool
}

// AgentEventSink receives every event of a standalone run in order and returns before the loop continues, as upstream awaits its sink. An error stops the run and is returned from it. Mirrors upstream AgentEventSink.
type AgentEventSink func(AgentEvent) error

// AgentLoopConfig configures a standalone loop run. Mirrors upstream AgentLoopConfig, which extends SimpleStreamOptions: StreamOptions carries the base request options. The Agent builds one for every run it starts.
type AgentLoopConfig struct {
	Model *ai.Model
	// StreamOptions are the base request options upstream AgentLoopConfig inherits from SimpleStreamOptions: API key, reasoning, thinking budgets, session id, transport, retry limits, payload and response hooks. They are embedded, so each is also a field of the config. Thinking (upstream reasoning) is the run's thinking level: empty is "off" for PrepareRequest, and PrepareRequest or PrepareNextTurn may replace it for later requests. The loop sets model cost and the output-token limit per request.
	ai.StreamOptions

	// ConvertToLlm converts the transformed context to provider messages. Nil uses the standard conversion.
	ConvertToLlm func([]AgentMessage) ([]ai.Message, error)
	// TransformLLMMessages post-processes the provider messages that the standard conversion produced.
	TransformLLMMessages func([]ai.Message) []ai.Message
	// TransformContext runs before ConvertToLlm on every request.
	TransformContext func(context.Context, []AgentMessage) ([]AgentMessage, error)
	// GetAPIKey resolves the credential for the model's provider before every request, for tokens that expire during a long run. An empty result keeps StreamOptions.APIKey. An error fails the run, as upstream awaits getApiKey unguarded. Mirrors upstream getApiKey.
	GetAPIKey func(provider string) (string, error)
	// SystemPromptOverride projects a replacement system prompt onto every request without rewriting the transcript.
	SystemPromptOverride func() *string

	FinishTurn      FinishTurn
	PrepareRequest  PrepareRequest
	PrepareNextTurn PrepareNextTurn
	// GetSteeringMessages and GetFollowUpMessages supply queued messages. Nil means none.
	GetSteeringMessages func() []AgentMessage
	GetFollowUpMessages func() []AgentMessage
	ToolExecution       ToolExecutionMode
	// BeforeToolCall and AfterToolCall are the AgentLoopConfig properties of Pi (types.ts:123-124); the hook lists run after them.
	BeforeToolCall      BeforeToolCallFunc
	AfterToolCall       AfterToolCallFunc
	BeforeToolCallHooks []BeforeToolCallHook
	AfterToolCallHooks  []AfterToolCallHook
	PrepareToolResult   func(context.Context, AgentToolResult) AgentToolResult

	// MaxTurns stops a run after that many turns; zero is unlimited, as upstream.
	MaxTurns int
	// SessionFile reports the session file tools may expose; nil or empty exports nothing.
	SessionFile func() string

	// agentStarted is set when the owning Agent already emitted agent_start.
	agentStarted bool
}

// loopHost is what one run reads and writes besides its transcript: the event sink, the tools, the stream function, and the owner's live state. A standalone run keeps these constant; the Agent supplies accessors for state that changes while a run is active.
type loopHost struct {
	cfg  AgentLoopConfig
	sink AgentEventSink
	// tools returns the tools available now.
	tools func() []AgentTool
	// streamFn returns the stream function for the next request.
	streamFn func() StreamFn
	// refreshModel reports the model and thinking level the owner selected since the previous call; nil means they never change during a run.
	refreshModel func() (*ai.Model, ai.ModelThinkingLevel, bool)
	// requestOptions adjusts the options of every request with state the owner may replace while the run is active.
	requestOptions func(*ai.StreamOptions)
	timings        *Recorder
	// agentLifecycle is set for the Agent, which turns a failed request setup or turn preparation into a failed assistant turn (upstream Agent.runWithLifecycle). A standalone run fails instead, as upstream runAgentLoop rejects.
	agentLifecycle bool
	// emitMu delivers one event at a time: parallel tool calls emit from their own goroutines, and upstream's sink never runs twice at once.
	emitMu sync.Mutex
}

func newStandaloneHost(config AgentLoopConfig, tools []AgentTool, sink AgentEventSink, streamFn StreamFn) *loopHost {
	return &loopHost{
		cfg:      config,
		sink:     sink,
		tools:    func() []AgentTool { return tools },
		streamFn: func() StreamFn { return streamFn },
		timings:  NewRecorder(),
	}
}

// emit delivers an event to the sink on the run goroutine. A sink failure unwinds the run as a run failure.
func (h *loopHost) emit(event AgentEvent) {
	if err := h.deliver(event); err != nil {
		panic(runFailure{err: err})
	}
}

// deliver passes an event to the sink after every earlier delivery returned and reports the sink's failure. Goroutines other than the run's use it, so a failure reaches the run instead of unwinding a goroutine that cannot recover it.
func (h *loopHost) deliver(event AgentEvent) error {
	h.emitMu.Lock()
	defer h.emitMu.Unlock()
	returned, raised := catchRunFailure(func() error { return h.sink(event) })
	if raised != nil {
		return raised
	}
	return returned
}

func (h *loopHost) steering() []AgentMessage {
	if h.cfg.GetSteeringMessages == nil {
		return nil
	}
	return h.cfg.GetSteeringMessages()
}

func (h *loopHost) followUps() []AgentMessage {
	if h.cfg.GetFollowUpMessages == nil {
		return nil
	}
	return h.cfg.GetFollowUpMessages()
}

func validateContinuation(messages []AgentMessage) error {
	if len(messages) == 0 {
		return errors.New("Cannot continue: no messages in context")
	}
	if messages[len(messages)-1].Assistant != nil {
		return errors.New("Cannot continue from message role: assistant")
	}
	return nil
}

func resolveStandaloneStreamFn(streamFn StreamFn) (StreamFn, error) {
	if streamFn != nil {
		return streamFn, nil
	}
	return GetDefaultStreamFn()
}

// RunAgentLoop starts a loop with new prompt messages and returns the messages the run produced, the prompts first. The prompts are added to a copy of the context; the caller's context is not modified. A failure of the sink, ConvertToLlm, a hook callback, or a missing stream function is returned instead of becoming a failed turn; the Agent owns that conversion. A nil streamFn uses the host's default stream function. Mirrors upstream runAgentLoop.
func RunAgentLoop(ctx context.Context, prompts []AgentMessage, agentContext AgentContext, config AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) ([]AgentMessage, error) {
	// upstream evaluates `streamFn ?? getDefaultStreamFn()` after the opening events, so a missing default fails after them.
	streamFn, missing := resolveStandaloneStreamFn(streamFn)
	host := newStandaloneHost(config, agentContext.Tools, emit, streamFn)
	initial := host.declareToolChanges(agentContext.Messages, prompts)
	run := newLoopRun(ctx, host, slices.Concat(agentContext.Messages, initial), slices.Clone(initial))
	err, failure := run.execute(initial, missing)
	if failure != nil {
		return nil, failure
	}
	return run.newMessages, standaloneRunError(ctx, err)
}

// standaloneRunError drops the cancellation a run already recorded as its aborted response: upstream runAgentLoop resolves with the messages after an abort.
func standaloneRunError(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}

// RunAgentLoopContinue continues a loop from the context without adding a message and returns the messages the run produced. The last context message must convert to a user or tool-result message; the provider rejects the request otherwise. Mirrors upstream runAgentLoopContinue.
func RunAgentLoopContinue(ctx context.Context, agentContext AgentContext, config AgentLoopConfig, emit AgentEventSink, streamFn StreamFn) ([]AgentMessage, error) {
	if err := validateContinuation(agentContext.Messages); err != nil {
		return nil, err
	}
	streamFn, missing := resolveStandaloneStreamFn(streamFn)
	host := newStandaloneHost(config, agentContext.Tools, emit, streamFn)
	run := newLoopRun(ctx, host, slices.Clone(agentContext.Messages), nil)
	err, failure := run.execute(nil, missing)
	if failure != nil {
		return nil, failure
	}
	return run.newMessages, standaloneRunError(ctx, err)
}

// AgentEventStream is the EventStream upstream agentLoop returns (EventStream<AgentEvent, AgentMessage[]>, agent-loop.ts:153-157): the events of a standalone run in order, buffered without bound until read. The agent_end event completes it, so Result resolves with that event's messages whether or not anyone reads the events.
type AgentEventStream = ai.EventStream[AgentEvent, []AgentMessage]

// newAgentEventStream is createAgentStream (agent-loop.ts:153-157).
func newAgentEventStream() *AgentEventStream {
	return ai.NewEventStream(
		func(event AgentEvent) bool { _, ok := event.(AgentEndEvent); return ok },
		func(event AgentEvent) []AgentMessage {
			if end, ok := event.(AgentEndEvent); ok {
				return end.Messages
			}
			return []AgentMessage{}
		},
	)
}

// finishAgentEventStream ends the stream after its run returned. A run that completed pushed agent_end, which already completed the stream. Upstream starts the run without a rejection handler, so a failing run is an unhandled rejection that Node reports on the process and the stream never completes; Go has no such channel, so the failure goes to the process log and the stream ends and Result resolves with the messages the run produced before it failed (none when it failed at the start), because a consumer of Events or Result would otherwise wait forever. A cancelled run ends with its aborted response, not with a failure. Use RunAgentLoop to receive the error.
func finishAgentEventStream(ctx context.Context, stream *AgentEventStream, messages []AgentMessage, err error) {
	if err != nil && ctx.Err() == nil {
		slog.Error("agent loop failed", "error", err)
	}
	stream.End(messages)
}

// AgentLoop is RunAgentLoop with the events delivered through a stream. The run proceeds on its own goroutine, which ends with the run; cancel ctx to stop it. Mirrors upstream agentLoop.
func AgentLoop(ctx context.Context, prompts []AgentMessage, agentContext AgentContext, config AgentLoopConfig, streamFn StreamFn) *AgentEventStream {
	stream := newAgentEventStream()
	go func() {
		messages, err := RunAgentLoop(ctx, prompts, agentContext, config, func(event AgentEvent) error {
			stream.Push(event)
			return nil
		}, streamFn)
		finishAgentEventStream(ctx, stream, messages, err)
	}()
	return stream
}

// AgentLoopContinue is RunAgentLoopContinue with the events delivered through a stream. It rejects an empty context and a context that ends with an assistant message before starting. Mirrors upstream agentLoopContinue.
func AgentLoopContinue(ctx context.Context, agentContext AgentContext, config AgentLoopConfig, streamFn StreamFn) (*AgentEventStream, error) {
	if err := validateContinuation(agentContext.Messages); err != nil {
		return nil, err
	}
	stream := newAgentEventStream()
	go func() {
		messages, err := RunAgentLoopContinue(ctx, agentContext, config, func(event AgentEvent) error {
			stream.Push(event)
			return nil
		}, streamFn)
		finishAgentEventStream(ctx, stream, messages, err)
	}()
	return stream, nil
}

// execute emits the run's opening events, runs the loop, and reports a failure of the sink or a callback separately from an ordinary loop error. initial are the prompt messages already in the run's context; startErr, when set, fails the run after the opening events instead of running the loop.
func (r *loopRun) execute(initial []AgentMessage, startErr error) (err, failure error) {
	return catchRunFailure(func() error {
		if !r.h.cfg.agentStarted {
			r.h.emit(AgentStartEvent{})
		}
		r.h.emit(TurnStartEvent{TurnIndex: 0, Timestamp: time.Now()})
		for _, msg := range initial {
			// message_start carries its own copy: a message_end replacement is applied in place to the message the transcript and message_end share.
			r.h.emit(MessageStartEvent{Message: startEventMessage(msg)})
			r.h.emit(MessageEndEvent{Message: msg})
		}
		if startErr != nil {
			panic(runFailure{err: startErr})
		}
		return r.loop()
	})
}
