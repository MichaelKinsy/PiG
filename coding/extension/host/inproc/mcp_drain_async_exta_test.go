package inproc_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// mcpChangeProbe is a runner whose mcp_servers_change handler reports each delivery and then blocks until the test lets it go on.
type mcpChangeProbe struct {
	runner     *inproc.Runner
	runtime    *extension.ExtensionRuntime
	proceed    chan struct{}
	letGo      func()
	mu         sync.Mutex
	delivered  []int
	active     atomic.Int32
	maxActive  atomic.Int32
	registered int
}

func newMcpChangeProbe(t *testing.T) *mcpChangeProbe {
	t.Helper()
	p := &mcpChangeProbe{runtime: extension.CreateExtensionRuntime(), proceed: make(chan struct{})}
	p.letGo = sync.OnceFunc(func() { close(p.proceed) })
	t.Cleanup(p.letGo)
	p.runner = inproc.NewRunner([]extension.Extension{{Path: "/ext/mcp.ts", Handlers: map[string][]extension.HandlerFn{
		"mcp_servers_change": {func(args ...any) (any, error) {
			now := p.active.Add(1)
			for {
				seen := p.maxActive.Load()
				if now <= seen || p.maxActive.CompareAndSwap(seen, now) {
					break
				}
			}
			p.mu.Lock()
			p.delivered = append(p.delivered, len(args[0].(extension.McpServersChangeEvent).Servers))
			p.mu.Unlock()
			<-p.proceed
			p.active.Add(-1)
			return nil, nil
		}},
	}}}, t.TempDir(), p.runtime)
	p.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	return p
}

func (p *mcpChangeProbe) register(t *testing.T) {
	t.Helper()
	p.registered++
	if err := p.runtime.RegisterMcpServer("/ext/plugin.ts", "server-"+string(rune('a'+p.registered)), mcpConfig("http://x.invalid")); err != nil {
		t.Fatal(err)
	}
}

func (p *mcpChangeProbe) deliveredSizes() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.delivered...)
}

// runner.ts:466 `void this.emit(...)`: registering a server never waits for the mcp_servers_change handlers, and a burst of
// registrations reaches the handler in order, one event at a time, each with the servers registered when it fired.
func TestRunnerMcpServersChangeDrainsAnUnawaitedBurstInOrderOneEventAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMcpChangeProbe(t)
		p.register(t)
		synctest.Wait()
		if got := p.deliveredSizes(); len(got) != 1 {
			t.Fatalf("delivered %v, want the first event in the handler", got)
		}
		// The first handler is still running: these registrations return at once and are queued behind it.
		p.register(t)
		p.register(t)
		synctest.Wait()
		if got := p.deliveredSizes(); len(got) != 1 {
			t.Fatalf("delivered %v while the first handler was running, want only the first event", got)
		}
		p.letGo()
		synctest.Wait()
		got := p.deliveredSizes()
		if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
			t.Fatalf("server counts of the delivered events = %v, want [1 2 3] in registration order", got)
		}
		if peak := p.maxActive.Load(); peak != 1 {
			t.Fatalf("%d handlers ran at once, want one drain goroutine delivering one event at a time", peak)
		}
	})
}

// Go addition (D3 cancellation): the drain runs on the runner's background context. Once the runner is invalidated, queued events and
// events of later registrations are dropped instead of reaching a stale extension.
func TestRunnerMcpServersChangeDropsQueuedEventsOnceTheRunnerIsInvalidated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMcpChangeProbe(t)
		p.register(t)
		synctest.Wait()
		p.register(t)
		p.register(t)
		p.runner.InvalidateKeepingRuntime("stale")
		p.letGo()
		synctest.Wait()
		p.register(t)
		synctest.Wait()
		if got := p.deliveredSizes(); len(got) != 1 || got[0] != 1 {
			t.Fatalf("delivered %v, want only the event whose handler was already running", got)
		}
	})
}
