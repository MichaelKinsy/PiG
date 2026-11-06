package codingagent

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// commandOrder records when the two extension commands of the prompt-loop tests start and end.
type commandOrder struct {
	mu     sync.Mutex
	events []string
}

func (c *commandOrder) record(event string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *commandOrder) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.events)
}

// newPromptCommandMode is a mode whose extension commands are "hold", which starts, waits for release and ends, and "after", which only runs.
func newPromptCommandMode(t *testing.T, release <-chan struct{}) (*InteractiveMode, context.Context, *commandOrder) {
	t.Helper()
	m, ctx := newCustomEditorDispatchMode(t)
	t.Cleanup(m.abortFn)
	order := &commandOrder{}
	command := func(name string, wait bool) extension.RegisteredCommand {
		return extension.RegisteredCommand{Name: name, Handler: func(ctx context.Context, _ string) error {
			order.record(name + ":start")
			if wait {
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			order.record(name + ":end")
			return nil
		}}
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:         "commands",
		Commands:     map[string]extension.RegisteredCommand{"hold": command("hold", true), "after": command("after", false)},
		CommandOrder: []string{"hold", "after"},
	}}, "")
	m.slashRegistry = NewSlashRegistry()
	m.extCtx = &ExtensionContext{HasUI: true}
	m.runCtx = ctx
	m.isIdle = true
	m.uiTaskCh = make(chan func(), 8)
	m.inputReadCh = make(chan inputChunk, 4)
	m.inputErrCh = make(chan error, 1)
	return m, ctx, order
}

// stopPromptLoop ends the input loop a test started.
func stopPromptLoop(t *testing.T, m *InteractiveMode, done <-chan error) {
	t.Helper()
	m.inputErrCh <- io.EOF
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

// interactive-mode.ts run() awaits `this.session.prompt(message)` for the initial message and each of initialMessages, and prompt() awaits an extension
// command's handler (agent-session.ts prompt -> _tryExecuteExtensionCommand): `pi "/hold" "/after"` runs /after only after /hold returned, whether /hold is the
// initial message or one of the later ones.
func TestInitialExtensionCommandsRunInOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   string
		rest    []string
		wantEnd []string
	}{
		{"hold is the initial message", "/hold", []string{"/after"}, []string{"hold:start", "hold:end", "after:start", "after:end"}},
		{"hold is a later message", "", []string{"/hold", "/after"}, []string{"hold:start", "hold:end", "after:start", "after:end"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				m, ctx, order := newPromptCommandMode(t, release)
				m.opts.InitialMessage = tc.first
				m.opts.InitialMessages = tc.rest
				m.startInitialMessages(ctx)
				done := make(chan error, 1)
				go func() { done <- m.inputLoop(ctx, strings.NewReader("")) }()
				synctest.Wait()
				if got := order.snapshot(); !slices.Equal(got, []string{"hold:start"}) {
					t.Fatalf("commands = %q while /hold waits, want only its start: the next positional message ran before /hold returned", got)
				}
				close(release)
				synctest.Wait()
				if got := order.snapshot(); !slices.Equal(got, tc.wantEnd) {
					t.Fatalf("commands = %q, want %q", got, tc.wantEnd)
				}
				stopPromptLoop(t, m, done)
			})
		})
	}
}

// A command among the positional messages holds the prompt after it too: the input typed meanwhile waits in pendingUserInputs for the loop, as it does while Pi's
// run() is blocked in `await this.session.prompt(...)`, and runs once the command returned.
func TestTypedExtensionCommandHoldsTheNextPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		m, ctx, order := newPromptCommandMode(t, release)
		m.setupEditorSubmitHandler(ctx)
		done := make(chan error, 1)
		go func() { done <- m.inputLoop(ctx, strings.NewReader("")) }()
		synctest.Wait()
		for _, data := range []string{"/hold", "\r"} {
			m.inputReadCh <- inputChunk{data: []byte(data)}
		}
		synctest.Wait()
		for _, data := range []string{"/after", "\r"} {
			m.inputReadCh <- inputChunk{data: []byte(data)}
		}
		synctest.Wait()
		if got := order.snapshot(); !slices.Equal(got, []string{"hold:start"}) {
			t.Fatalf("commands = %q while /hold waits, want only its start: the typed /after ran before /hold returned", got)
		}
		if !slices.Equal(m.pendingUserInputs, []string{"/after"}) {
			t.Fatalf("pendingUserInputs = %q, want /after queued behind the running command", m.pendingUserInputs)
		}
		close(release)
		synctest.Wait()
		if got, want := order.snapshot(), []string{"hold:start", "hold:end", "after:start", "after:end"}; !slices.Equal(got, want) {
			t.Fatalf("commands = %q, want %q", got, want)
		}
		stopPromptLoop(t, m, done)
	})
}
