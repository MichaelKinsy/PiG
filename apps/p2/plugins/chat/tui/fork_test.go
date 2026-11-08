package tui_test

// `/fork`, `/clone` and `doubleEscapeAction: "fork"` in the TUI (p2-tui-port.md section 10.8): pi's message picker, `Sessions.fork` before the chosen message with
// its text in the new session's editor, `Sessions.clone`.
//
// pi: apps/p2/test/p2/tui-fork.test.ts

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

func TestForkPicksAMessageAndOpensTheForkWithItInTheEditorCloneDoubleEscapeSubagentsRefused(t *testing.T) {
	var mu sync.Mutex
	var forks, clones []*delta.JsonObject
	count := func(list *[]*delta.JsonObject) int { mu.Lock(); defer mu.Unlock(); return len(*list) }
	view := func(conversation int) delta.JsonValue {
		var entries []durable.EntryRecord
		if conversation == 1 {
			entries = []durable.EntryRecord{tuitest.InputEntry(1, "first question"), tuitest.InputEntry(3, "second question")}
		}
		return tuitest.View(t, conversation, entries, tuitest.DocsOf(t, "pi.agent", delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"))), 0)
	}
	sessions := p2test.Stub{Contract: sessionscontract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		rows := self.Cell([]any{tuitest.Session.JSON()})
		copyOf := func(id, mark string) (delta.JsonValue, error) {
			created := tuitest.Session
			created.ID, created.Title = id, tuitest.Session.Title+" ("+mark+")"
			current, _ := rows.Value().([]any)
			_ = rows.Replace(append(append([]any(nil), current...), created.JSON()))
			return created.JSON(), nil
		}
		none := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
		return p2test.StubMembers{Variant: "owner",
			State: map[string]any{"sessions": rows, "defaultCwd": self.Cell("/work"), "defaultEnv": self.Cell(nil), "operations": self.Cell([]any{})},
			Methods: map[string]p2test.StubMethod{
				"dismissOperation": none, "relocate": none, "rename": none, "pin": none, "archive": none, "restore": none,
				"create": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return tuitest.Session.JSON(), nil },
				// The copy's ID is the TUI's (client-chosen, Sessions v6).
				"clone": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					mu.Lock()
					clones = append(clones, object)
					mu.Unlock()
					return copyOf(object.Value("copy").(string), "copy")
				},
				"fork": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					mu.Lock()
					forks = append(forks, object)
					mu.Unlock()
					return copyOf(object.Value("copy").(string), "fork")
				},
			}}
	}}
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{Views: view, Model: meta})
	top := tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{sessions}, Worker: []p2test.Stub{chat.Stub},
		Facets: []host.Facet{p2tui.CoreFacet, chattui.Facet}, Columns: 100,
	})
	// surface is the chat surface; call it on the owner loop (inside Do).
	surface := func() *chattui.ChatSurface {
		found, _ := top.Runtime.Surface("chat").(*chattui.ChatSurface)
		return found
	}
	editable := func() (ok bool) {
		top.Do(func() {
			found := surface()
			ok = found != nil && found.Editable()
		})
		return ok
	}
	tuitest.Poll(t, editable)
	// /fork: pi's picker, the newest message selected; Up picks the first.
	typed(top.Fixture, "/fork")
	top.Send(enter)
	waitFor(t, top.Fixture, "Fork from Message")
	if !regexp.MustCompile(`(?s)› second question.*Message 2 of 2`).MatchString(top.Screen()) {
		t.Fatalf("the picker:\n%s", top.Screen())
	}
	top.Send(up)
	top.Send(enter)
	tuitest.Poll(t, func() bool { return count(&forks) == 1 })
	forked := forks[0]
	copyID, _ := forked.Value("copy").(string)
	if forked.Value("id") != "s" || forked.Value("conversation") != float64(1) || forked.Value("before") != float64(1) || copyID == "" {
		t.Fatalf("fork args = %v", forked)
	}
	// The fork opened at once with the chosen message in its editor (this fixture's single worker serves only "s").
	var identity, draft string
	top.Do(func() { identity, draft = top.Runtime.Identity(), top.Runtime.Drafts[copyID+"#1"] })
	if identity != copyID+"#1" || draft != "first question" {
		t.Fatalf("identity %q draft %q", identity, draft)
	}
	var text string
	top.Do(func() { text = surface().Editor.Text() })
	if text != "first question" {
		t.Fatalf("editor %q", text)
	}
	// /clone opens the copy at once.
	ran := false
	top.Do(func() { ran = top.Runtime.Ctx.Commands().Run("p2.session.clone", "") })
	if !ran {
		t.Fatal("p2.session.clone did not run")
	}
	tuitest.Poll(t, func() bool { return count(&clones) == 1 })
	cloned := clones[0]
	cloneID, _ := cloned.Value("copy").(string)
	if cloned.Value("id") != copyID || cloneID == "" {
		t.Fatalf("clone args = %v", cloned)
	}
	top.Do(func() { identity = top.Runtime.Identity() })
	if identity != cloneID+"#1" {
		t.Fatalf("identity %q", identity)
	}
	one := 1
	top.Do(func() { top.Runtime.Navigate("chat", "s", true, &one) })
	tuitest.Poll(t, editable)
	// doubleEscapeAction "fork": a second Escape within 500 ms on an empty editor opens the picker; "none" never.
	setting := func(action string) {
		top.Do(func() { top.Runtime.Appearance.Settings = prefs.TuiSettings{DoubleEscapeAction: &action} })
	}
	setting("none")
	top.Send(escape)
	top.Send(escape)
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(top.Screen(), "Fork from Message") {
		t.Fatal("none opened the picker")
	}
	setting("fork")
	top.Send(escape)
	top.Send(escape)
	waitFor(t, top.Fixture, "Fork from Message")
	top.Send(escape)
	waitGone(t, top.Fixture, "Fork from Message")
	// In a subagent conversation: refused for now.
	two := 2
	top.Do(func() { top.Runtime.Navigate("chat", "s", true, &two) })
	tuitest.Poll(t, editable)
	typed(top.Fixture, "/fork")
	top.Send(enter)
	tuitest.Poll(t, func() bool {
		refused := false
		top.Do(func() {
			for _, notice := range top.Runtime.Notices {
				refused = refused || strings.Contains(notice.Text, "subagent conversation is not supported")
			}
		})
		return refused
	})
	if count(&forks) != 1 {
		t.Fatalf("forks = %d", count(&forks))
	}
}
