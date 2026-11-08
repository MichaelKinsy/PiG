package tui_test

// Optimistic UI in the TUI (docs/optimistic-ui.md): what a person did shows before the server answers and rolls back when it refuses. The session selector's two
// tests are the sessions plugin's.
//
// pi: apps/p2/test/p2/tui-optimistic.test.ts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
)

// gate is a call the test settles: call waits until pass or refuse.
type gate struct{ answer chan error }

func newGate() *gate { return &gate{answer: make(chan error, 1)} }

func (g *gate) call(ctx context.Context) error {
	select {
	case err := <-g.answer:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (g *gate) pass()                 { g.answer <- nil }
func (g *gate) refuse(message string) { g.answer <- errors.New(message) }

func TestChatASentMessageShowsAndLeavesTheEditorAtOnceARefusedOneComesBackToTheEditor(t *testing.T) {
	held := newGate()
	var mu sync.Mutex
	var sent []string
	view := tuitest.View(t, 1, nil, tuitest.DocsOf(t, "pi.agent", delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"))), 0)
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{
		Views: func(int) delta.JsonValue { return view }, Model: meta,
		Methods: map[string]p2test.StubMethod{"send": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			mu.Lock()
			sent = append(sent, args.(*delta.JsonObject).Value("text").(string))
			count := len(sent)
			mu.Unlock()
			if err := held.call(ctx); err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("submissionId", float64(count)), nil
		}},
	})
	top := tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{tuitest.SessionsStub(tuitest.Session)}, Worker: []p2test.Stub{chat.Stub},
		Facets: []host.Facet{p2tui.CoreFacet, chattui.Facet}, Columns: 100,
	})
	sentCount := func() int { mu.Lock(); defer mu.Unlock(); return len(sent) }
	tuitest.Poll(t, func() (ok bool) {
		top.Do(func() {
			found, _ := top.Runtime.Surface("chat").(*chattui.ChatSurface)
			ok = found != nil && found.Editable()
		})
		return ok
	})
	typed(top.Fixture, "hello there")
	top.Send(enter)
	tuitest.Poll(t, func() bool { return sentCount() == 1 })
	// Held open: the editor is empty and the message is in the transcript.
	if text := (&setup{Topology: top}).editorText(); text != "" {
		t.Fatalf("editor %q", text)
	}
	waitFor(t, top.Fixture, "hello there")
	held.refuse("not now")
	// Refused: back in the editor, out of the transcript, the error on a status line.
	tuitest.Poll(t, func() bool { return (&setup{Topology: top}).editorText() == "hello there" })
	tuitest.Poll(t, func() (ok bool) {
		top.Do(func() {
			for _, notice := range top.Runtime.Notices {
				ok = ok || notice.Kind == "error" && strings.Contains(notice.Text, "not now")
			}
		})
		return ok
	})
	// Only the editor shows it now, not the transcript.
	tuitest.Poll(t, func() bool { return len(strings.Split(top.Screen(), "hello there")) == 2 })
}
