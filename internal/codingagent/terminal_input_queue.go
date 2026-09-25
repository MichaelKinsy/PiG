package codingagent

import (
	"context"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// terminalInputListener is one raw terminal-input listener, kept in
// registration order like upstream's inputListeners set. An in-process
// listener answers synchronously on the owner loop through handler. A
// subprocess extension's listener answers across a socket through remote,
// which never runs on the owner loop.
type terminalInputListener struct {
	id      uint64
	handler func(data string) extension.TerminalInputResult

	remote extension.RemoteTerminalInputHandler
}

// addRemoteTerminalInputHandler registers a subprocess extension's listener.
func (m *InteractiveMode) addRemoteTerminalInputHandler(_ string, handler extension.RemoteTerminalInputHandler) func() {
	return m.addTerminalInputListenerEntry(terminalInputListener{remote: handler})
}

// inputChunk is one parsed terminal sequence routed to the owner loop. The
// loop settles ticket once the chunk's terminal-input listeners are done.
type inputChunk struct {
	data   []byte
	ticket *inputTicket
}

type inputTicketState uint8

const (
	// inputTicketRouted: the owner loop is handling the chunk.
	inputTicketRouted inputTicketState = iota
	// inputTicketPending: a remote listener's verdict is outstanding.
	inputTicketPending
	// inputTicketSettled: the listeners are done; the next chunk may follow.
	inputTicketSettled
)

// inputTicket follows one chunk from the input pump to the owner loop until its
// terminal-input listeners settle. The pump routes nothing after the chunk
// until then, so listeners see chunks, and the owner loop handles them, in
// input order, as upstream's synchronous listeners do. A nil ticket belongs to
// a chunk no pump is waiting on.
type inputTicket struct {
	mu    sync.Mutex
	state inputTicketState
	// done closes when the chunk settles.
	done chan struct{}
}

func newInputTicket() *inputTicket {
	return &inputTicket{done: make(chan struct{})}
}

// await records that the chunk waits on a remote listener's verdict.
func (t *inputTicket) await() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == inputTicketRouted {
		t.state = inputTicketPending
	}
}

// resume hands a pending chunk back to the owner loop with its verdict.
func (t *inputTicket) resume() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == inputTicketPending {
		t.state = inputTicketRouted
	}
}

// settle releases the pump to route the next chunk. It does nothing while the
// chunk waits on a remote verdict or after it settled.
func (t *inputTicket) settle() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != inputTicketRouted {
		return
	}
	t.state = inputTicketSettled
	close(t.done)
}

// passTerminalInput runs data through the extension shortcut and the
// terminal-input listeners, then gives handle what they pass on. It returns
// handle's error when the pass ends without waiting for a remote verdict; a
// pass that resumes after one reports handle's error to the input loop.
func (m *InteractiveMode) passTerminalInput(ctx context.Context, data string, ticket *inputTicket, handle func(context.Context, string) error) error {
	m.terminalInputMu.Lock()
	shortcutListener := m.extensionShortcutListener
	listeners := slices.Clone(m.terminalInputListeners)
	m.terminalInputMu.Unlock()
	// Upstream runs extension shortcuts inside the focused editor, after its key-release delivery check. Raw terminal-input listeners below still receive releases.
	if shortcutListener != nil && tui.ShouldDeliverKey(m.editor, data) && shortcutListener(data) {
		ticket.settle()
		return nil
	}
	var err error
	resumed := false
	pass := &terminalInputPass{m: m, listeners: listeners, data: data, ticket: ticket}
	pass.finish = func(data string, consumed bool) {
		if consumed {
			return
		}
		handleErr := handle(ctx, data)
		if !resumed {
			err = handleErr
			return
		}
		m.failInputLoop(handleErr)
	}
	pass.run()
	resumed = true
	return err
}

// terminalInputPass carries one chunk through the terminal-input listeners on
// the owner loop, as upstream tui.ts handleTerminalInput does: a listener that
// consumes the chunk ends the pass, a listener's data replaces the chunk for
// the listeners after it and for normal handling, and a chunk rewritten to
// empty is dropped. At a remote listener the pass asks off the loop and
// resumes on the loop with the verdict.
type terminalInputPass struct {
	m         *InteractiveMode
	listeners []terminalInputListener
	next      int
	data      string
	ticket    *inputTicket
	finish    func(data string, consumed bool)
}

func (p *terminalInputPass) run() {
	for p.next < len(p.listeners) {
		listener := p.listeners[p.next]
		p.next++
		if listener.remote != nil {
			// Without a running mode there is no loop to take the verdict.
			if p.m.backgroundCtx == nil {
				continue
			}
			p.await(listener)
			return
		}
		if p.apply(listener.handler(p.data)) {
			return
		}
	}
	if len(p.listeners) > 0 && p.data == "" {
		p.end("", true)
		return
	}
	p.end(p.data, false)
}

// apply records one listener's verdict and reports whether it ended the pass.
func (p *terminalInputPass) apply(result extension.TerminalInputResult) bool {
	if result.Consume {
		p.end("", true)
		return true
	}
	if result.Data != nil {
		p.data = *result.Data
	}
	return false
}

func (p *terminalInputPass) end(data string, consumed bool) {
	p.ticket.settle()
	p.finish(data, consumed)
}

// await asks remote listener l for its verdict on a background task owned by
// the mode and resumes the pass on the owner loop when it arrives. The mode's
// shutdown cancels the request.
func (p *terminalInputPass) await(l terminalInputListener) {
	m := p.m
	ctx := m.backgroundCtx
	data := p.data
	p.ticket.await()
	m.backgroundTasks.Go(func() {
		result := l.remote(ctx, data)
		m.runOnMain(ctx, func() {
			p.ticket.resume()
			if !p.apply(result) {
				p.run()
			}
			m.tuiInst.Render()
		})
	})
}

// failInputLoop ends the input loop with err, which a keystroke handled after
// a remote verdict returns there instead of to the loop directly.
func (m *InteractiveMode) failInputLoop(err error) {
	if err != nil && m.inputLoopErr == nil {
		m.inputLoopErr = err
	}
}

// inputBacklog is the input pump's ordered queue: parsed input not yet routed,
// and the routed chunk whose terminal-input listeners have not settled.
type inputBacklog struct {
	held    []string
	waiting *inputTicket
}
