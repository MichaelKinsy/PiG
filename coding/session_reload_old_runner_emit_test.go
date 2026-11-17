package coding

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// emitCounter is one extension instance: it counts the events each of its handlers receives and what its handlers' ctx answers.
type emitCounter struct {
	mu       sync.Mutex
	received map[string]int
	ctxErr   map[string]error
}

var emitCounterEvents = []string{
	"input", "before_agent_start", "tool_call", "tool_result", "context", "context_with_system",
	"before_provider_request", "before_provider_headers", "message_end", "cache_warming_decision", "resources_discover",
}

func newEmitCounter() *emitCounter {
	return &emitCounter{received: map[string]int{}, ctxErr: map[string]error{}}
}

func (c *emitCounter) extension(path string) extension.Extension {
	handlers := map[string][]extension.HandlerFn{}
	for _, event := range emitCounterEvents {
		handlers[event] = []extension.HandlerFn{func(args ...any) (any, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.received[event]++
			if ctx, ok := args[len(args)-1].(context.Context); ok {
				if extCtx := extension.FromContext(ctx); extCtx != nil {
					_, c.ctxErr[event] = extCtx.CWD()
				}
			}
			return nil, nil
		}}
	}
	return extension.Extension{Path: path, Handlers: handlers}
}

func (c *emitCounter) count(event string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received[event]
}

func (c *emitCounter) contextError(event string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctxErr[event]
}

// driveEveryEmitOfTheSession runs each path that makes the Session dispatch one of emitCounterEvents, the way a prompt, a tool call, a provider request and a settled
// turn do. Every one of them reads the Session's current runner when it runs.
func driveEveryEmitOfTheSession(t *testing.T, session *Session) {
	t.Helper()
	ctx := t.Context()
	if _, _, _, err := session.RunInputHandlers(ctx, "hello", nil, extension.InputSourceUser, ""); err != nil {
		t.Fatalf("input: %v", err)
	}
	if _, err := session.PreparePrompt(ctx, BuildUserContent("hello", nil)); err != nil {
		t.Fatalf("before_agent_start: %v", err)
	}
	args := json.RawMessage(`{}`)
	if hook := session.extensionToolCallHook(ctx, "call-1", "read", args); hook.Err != nil {
		t.Fatalf("tool_call: %v", hook.Err)
	}
	session.extensionToolResultHook(ctx, "call-1", "read", args, agent.AgentToolResult{})
	if _, err := session.extensionContextHook(ctx, []agent.AgentMessage{{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}}}); err != nil {
		t.Fatalf("context: %v", err)
	}
	if _, err := session.extensionProviderRequestHook(map[string]any{}, nil); err != nil {
		t.Fatalf("before_provider_request: %v", err)
	}
	if _, err := session.extensionProviderHeadersHook(ctx, ai.ProviderHeaders{}); err != nil {
		t.Fatalf("before_provider_headers: %v", err)
	}
	session.extensionEventHook(agent.MessageEndEvent{Message: agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}}})
	if _, err := session.decideCacheWarming(ctx, icodingagent.CacheWarmingDecisionEvent{Type: "cache_warming_decision", Action: extension.CacheWarmingActionWarm}); err != nil {
		t.Fatalf("cache_warming_decision: %v", err)
	}
	if err := session.extendResourcesFromExtensions(ctx, "reload"); err != nil {
		t.Fatalf("resources_discover: %v", err)
	}
}

// swapLoader is a resource loader whose reload fails, or loads the extensions it holds, registered against runtime when it has one. during, when set, runs inside
// Reload: between the old runner's invalidation and the swap, where the extension factories of a real loader run.
type swapLoader struct {
	ResourceLoader
	extensions []extension.Extension
	runtime    *extension.ExtensionRuntime
	err        error
	during     func()
}

func (l *swapLoader) Reload(...ResourceLoaderReloadOptions) error {
	if l.during != nil {
		l.during()
	}
	return l.err
}

func (l *swapLoader) GetExtensions() LoadExtensionsResult {
	return LoadExtensionsResult{Extensions: l.extensions, Runtime: l.runtime}
}

func newReloadableEmitSession(t *testing.T, instance *emitCounter, loader *swapLoader) *Session {
	t.Helper()
	services := newTestServices(t)
	session, err := NewSession(services, SessionOptions{
		Model: fakeModel(), NoSession: true, ResourceLoader: loader,
		Runner: inproc.NewRunner([]extension.Extension{instance.extension("<old>")}, services.CWD()),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// Pi's reload invalidates the old runner right after session_shutdown (agent-session.ts:3825-3826) and builds the new one only at _buildRuntime (:3844), so a reload that
// throws leaves the session on the old, stale runner. Every hook of the session reads this._extensionRunner when it runs (agent-session.ts:722 _beforeToolCall, :746
// _afterToolCall, :1404 message_end, :2005 before_agent_start, :2121 input), and no emit method of the runner looks at staleness (runner.ts:1135-1560): the next prompt, tool
// call and provider request reach the old instance's handlers, whose ctx throws the stale message. Probed on Pi 1.5.2: after a failed /reload the old instance's input,
// before_agent_start, tool_call and tool_result handlers run and its tool executes.
//
// mutation-checked: restoring the assertActive entry guard of EmitInput, EmitToolCall, EmitToolResult, EmitBeforeAgentStart, EmitContext, EmitBeforeProviderRequest, EmitBeforeProviderHeaders, EmitMessageEnd or EmitResourcesDiscover
// fails the event's line.
func TestAFailedReloadLeavesTheSessionDeliveringToTheOldInstance(t *testing.T) {
	old := newEmitCounter()
	loader := &swapLoader{ResourceLoader: NoResources, err: errors.New("resource reload failed")}
	session := newReloadableEmitSession(t, old, loader)
	previous := session.ExtensionRunner()
	if err := session.Reload(t.Context()); !errors.Is(err, loader.err) {
		t.Fatalf("Reload error = %v, want %v", err, loader.err)
	}
	if session.ExtensionRunner() != previous || !previous.IsStale() {
		t.Fatal("a reload that failed after session_shutdown must leave the session on its old, stale runner")
	}
	driveEveryEmitOfTheSession(t, session)
	for _, event := range emitCounterEvents {
		if got := old.count(event); got != 1 {
			t.Errorf("old instance's %s handler ran %d times after the failed reload, want 1", event, got)
		}
		if err := old.contextError(event); !errors.Is(err, extension.ErrStaleContext) {
			t.Errorf("old instance's %s ctx.CWD() = %v, want the stale error", event, err)
		}
	}
}

// The other direction: once a reload built a new runner the session's field is swapped, and nothing emits on the old one (a runner that lost its place is reached
// only by a caller that kept it: the cache warmer, timers and the RPC admission read the Session's current runner or are rebuilt by the rebind). Pi refuses a reload
// while a response streams (agent-session.ts:3816-3818), so no tool call is in flight across a reload that succeeds.
func TestASuccessfulReloadDeliversOnlyToTheNewInstance(t *testing.T) {
	old, fresh := newEmitCounter(), newEmitCounter()
	loader := &swapLoader{ResourceLoader: NoResources, extensions: []extension.Extension{fresh.extension("<new>")}}
	session := newReloadableEmitSession(t, old, loader)
	previous := session.ExtensionRunner()
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if session.ExtensionRunner() == previous || !previous.IsStale() {
		t.Fatal("the reload must swap the runner and leave the old one stale")
	}
	driveEveryEmitOfTheSession(t, session)
	for _, event := range emitCounterEvents {
		if got := old.count(event); got != 0 {
			t.Errorf("old instance's %s handler ran %d times after the reload that replaced it, want 0", event, got)
		}
		if got := fresh.count(event); got != 1 {
			t.Errorf("new instance's %s handler ran %d times, want 1", event, got)
		}
		if err := fresh.contextError(event); err != nil {
			t.Errorf("new instance's %s ctx.CWD() = %v, want live", event, err)
		}
	}
}
