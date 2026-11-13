package inproc_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/handleryield"
)

// mcpOrderProbe is a runner whose first mcp_servers_change handler writes its start, the end of its synchronous part and its end into
// one log with the registrations. Each delivery reports that it awaits, as a Pi handler reaches its first await, and then waits for a
// gate of its own.
type mcpOrderProbe struct {
	runner  *inproc.Runner
	runtime *extension.ExtensionRuntime
	first   *extension.Extension
	mu      sync.Mutex
	log     []string
	gates   []chan struct{}
	// inside runs in the synchronous part of the delivery with that number.
	inside func(id int)
	// returnsAtOnce makes the handler return at the end of its synchronous part instead of awaiting.
	returnsAtOnce bool
}

func newMcpOrderProbe(t *testing.T, later ...extension.Extension) *mcpOrderProbe {
	t.Helper()
	p := &mcpOrderProbe{runtime: extension.CreateExtensionRuntime(), first: &extension.Extension{Path: "/ext/mcp.ts", ResolvedPath: "/ext/mcp.ts"}}
	p.first.InitializeEventHandlers()
	p.first.AddEventHandler("mcp_servers_change", 1, func(args ...any) (any, error) {
		id, gate := p.begin(len(args[0].(extension.McpServersChangeEvent).Servers))
		if p.inside != nil {
			p.inside(id)
		}
		if p.returnsAtOnce {
			return nil, nil
		}
		p.note(fmt.Sprintf("handler %d awaits", id))
		handleryield.Report(args[1].(context.Context))
		<-gate
		p.note(fmt.Sprintf("handler %d end", id))
		return nil, nil
	})
	p.runner = inproc.NewRunner(append([]extension.Extension{*p.first}, later...), t.TempDir(), p.runtime)
	p.runner.AddErrorListener(func(e *extension.ExtensionError) {
		p.note(fmt.Sprintf("error listener: %s: %.21s", e.Event, e.Error))
	})
	p.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	t.Cleanup(p.releaseAll)
	return p
}

func (p *mcpOrderProbe) begin(servers int) (int, chan struct{}) {
	gate := make(chan struct{})
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gates = append(p.gates, gate)
	id := len(p.gates)
	p.log = append(p.log, fmt.Sprintf("handler %d start: %d server(s)", id, servers))
	return id, gate
}

func (p *mcpOrderProbe) note(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, line)
}

func (p *mcpOrderProbe) lines() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.log)
}

// release lets the delivery with that number finish; releasing twice is harmless.
func (p *mcpOrderProbe) release(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id > len(p.gates) || p.gates[id-1] == nil {
		return
	}
	close(p.gates[id-1])
	p.gates[id-1] = nil
}

func (p *mcpOrderProbe) releaseAll() {
	p.mu.Lock()
	count := len(p.gates)
	p.mu.Unlock()
	for id := range count {
		p.release(id + 1)
	}
}

// registerServer registers server-<name> and writes the call and its return into the log.
func (p *mcpOrderProbe) registerServer(name string) error {
	p.note("register " + name + ": call")
	err := p.runtime.RegisterMcpServer("/ext/plugin.ts", "server-"+name, mcpConfig("http://"+name+".invalid"))
	p.note("register " + name + ": returned")
	return err
}

func (p *mcpOrderProbe) register(t *testing.T, name string) {
	t.Helper()
	if err := p.registerServer(name); err != nil {
		t.Fatal(err)
	}
}

func wantMcpOrder(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("log:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// runner.ts:465-468, 1103-1110: the listener calls emit without awaiting it, and emit has no await before its handler loop, so the first
// handler runs to its first await inside the registration: the registering code goes on only after the handler's synchronous part. Each
// `void this.emit(...)` is a chain of its own: the second event's handler starts while the first event's is still pending, and the two
// end in the order their awaits settle (Pi 1.5.2, probe case 1).
func TestRunnerMcpServersChangeRunsTheFirstHandlersSynchronousPartInsideTheRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMcpOrderProbe(t)
		p.register(t, "a")
		p.register(t, "b")
		synctest.Wait()
		p.note("settled")
		p.release(2)
		synctest.Wait()
		p.release(1)
		synctest.Wait()
		wantMcpOrder(t, p.lines(), []string{
			"register a: call", "handler 1 start: 1 server(s)", "handler 1 awaits", "register a: returned",
			"register b: call", "handler 2 start: 2 server(s)", "handler 2 awaits", "register b: returned",
			"settled", "handler 2 end", "handler 1 end",
		})
	})
}

// emit suspends at `await handler(event, ctx)` even when the handler returns without awaiting (runner.ts:1110), so only the first
// handler's synchronous part is inside the registration; a second extension's handler gets each event after it (Pi 1.5.2, probe case 2).
// Pi starts the second extension's handlers after the registering code's synchronous run, in event order; here each event's later
// handlers run on that event's own goroutine beside the registering code, so their place in the log is not asserted.
func TestRunnerMcpServersChangeRunsOnlyTheFirstHandlerInsideTheRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var p *mcpOrderProbe
		second := extension.Extension{Path: "/ext/second.ts", ResolvedPath: "/ext/second.ts"}
		second.InitializeEventHandlers()
		second.AddEventHandler("mcp_servers_change", 1, func(args ...any) (any, error) {
			p.note(fmt.Sprintf("second handler start: %d server(s)", len(args[0].(extension.McpServersChangeEvent).Servers)))
			return nil, nil
		})
		p = newMcpOrderProbe(t, second)
		p.returnsAtOnce = true
		p.register(t, "a")
		p.register(t, "b")
		synctest.Wait()
		var first, later []string
		for _, line := range p.lines() {
			if strings.HasPrefix(line, "second handler") {
				later = append(later, line)
			} else {
				first = append(first, line)
			}
		}
		wantMcpOrder(t, first, []string{
			"register a: call", "handler 1 start: 1 server(s)", "register a: returned",
			"register b: call", "handler 2 start: 2 server(s)", "register b: returned",
		})
		slices.Sort(later)
		wantMcpOrder(t, later, []string{"second handler start: 1 server(s)", "second handler start: 2 server(s)"})
		lines := p.lines()
		for servers := 1; servers <= 2; servers++ {
			started := slices.Index(lines, fmt.Sprintf("handler %d start: %d server(s)", servers, servers))
			if got := slices.Index(lines, fmt.Sprintf("second handler start: %d server(s)", servers)); got < started {
				t.Fatalf("the second extension's handler got the event with %d server(s) before the first extension's: %q", servers, lines)
			}
		}
	})
}

// A handler that removes itself in its synchronous part leaves no handler when the listener reports (runner.ts:467, 756-770): the
// server is reported inside its registration, and so is the next one, whose event reaches no handler (Pi 1.5.2, probe case 3).
func TestRunnerMcpServersChangeReportsUnhandledServersOnceTheHandlerRemovedItself(t *testing.T) {
	p := newMcpOrderProbe(t)
	p.inside = func(int) { p.first.RemoveEventHandler("mcp_servers_change", 1) }
	p.register(t, "a")
	p.register(t, "b")
	wantMcpOrder(t, p.lines(), []string{
		"register a: call", "handler 1 start: 1 server(s)", "handler 1 awaits",
		`error listener: register_mcp_server: MCP server "server-a"`, "register a: returned",
		"register b: call", `error listener: register_mcp_server: MCP server "server-b"`, "register b: returned",
	})
}

// A handler that registers another server inside its synchronous part: Pi runs the nested listener and the nested emit on the same
// stack, so the second delivery's synchronous part and the inner registration's return come before the first delivery reaches its await
// (Pi 1.5.2, probe case 4).
func TestRunnerMcpServersChangeNestsARegistrationMadeInTheHandlersSynchronousPart(t *testing.T) {
	p := newMcpOrderProbe(t)
	p.inside = func(id int) {
		if id != 1 {
			return
		}
		if err := p.registerServer("b"); err != nil {
			p.note("register b failed: " + err.Error())
		}
	}
	p.register(t, "a")
	wantMcpOrder(t, p.lines(), []string{
		"register a: call", "handler 1 start: 1 server(s)",
		"register b: call", "handler 2 start: 2 server(s)", "handler 2 awaits", "register b: returned",
		"handler 1 awaits", "register a: returned",
	})
}

// ownerLoopUI is a UI context whose host runs one input and render loop, as interactive mode's does.
type ownerLoopUI struct {
	extension.UIContext
}

func (ownerLoopUI) OnOwnerLoop() bool { return true }

// Go addition: Pi's handler runs on the stack of the registration, so a registration made on the event loop cannot wait for the loop.
// Here the handler runs on a goroutine of its own, and a caller on the input and render loop that waited for it would wait for a
// handler that may need that loop. Such a caller starts the emission and goes on at once; the emission still runs to its end.
func TestRunnerMcpServersChangeDoesNotWaitForTheHandlerOnTheOwnerLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMcpOrderProbe(t)
		p.runner.SetUIContext(ownerLoopUI{extension.NoopUIContext}, extension.ModeTUI)
		hold := make(chan struct{})
		p.inside = func(int) { <-hold }
		p.register(t, "a")
		p.note("the caller goes on")
		synctest.Wait()
		// The handler is inside its synchronous part, on its own goroutine: the registration returned without it.
		got := p.lines()
		slices.Sort(got)
		wantMcpOrder(t, got, []string{"handler 1 start: 1 server(s)", "register a: call", "register a: returned", "the caller goes on"})
		close(hold)
		synctest.Wait()
		p.release(1)
		synctest.Wait()
		if got := p.lines(); !slices.Equal(got[len(got)-2:], []string{"handler 1 awaits", "handler 1 end"}) {
			t.Fatalf("the emission did not run to its end: %q", got)
		}
	})
}

// Go addition (D3 cancellation): the emissions run on the runner's background context. Once the runner is invalidated a registration
// starts no handler and does not wait for one, instead of reaching a stale extension; a handler that was already running goes on.
func TestRunnerMcpServersChangeStartsNoHandlerOnceTheRunnerIsInvalidated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMcpOrderProbe(t)
		p.register(t, "a")
		p.runner.InvalidateKeepingRuntime("stale")
		p.register(t, "b")
		synctest.Wait()
		p.release(1)
		synctest.Wait()
		wantMcpOrder(t, p.lines(), []string{
			"register a: call", "handler 1 start: 1 server(s)", "handler 1 awaits", "register a: returned",
			"register b: call", "register b: returned", "handler 1 end",
		})
	})
}

// Go addition: the runner owns its emissions. DrainMcpServersChanges waits for the handlers that still run, and no emission starts
// after it.
func TestRunnerMcpServersChangeDrainWaitsForTheRunningEmissions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newMcpOrderProbe(t)
		p.register(t, "a")
		p.runner.InvalidateKeepingRuntime("stale")
		drained := make(chan struct{})
		go func() {
			p.runner.DrainMcpServersChanges()
			p.note("drained")
			close(drained)
		}()
		synctest.Wait()
		p.note("settled")
		p.release(1)
		<-drained
		p.register(t, "b")
		synctest.Wait()
		wantMcpOrder(t, p.lines(), []string{
			"register a: call", "handler 1 start: 1 server(s)", "handler 1 awaits", "register a: returned",
			"settled", "handler 1 end", "drained", "register b: call", "register b: returned",
		})
	})
}
