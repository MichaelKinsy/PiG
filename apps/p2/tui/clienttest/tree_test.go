package clienttest

// test/p2/tui-tree.test.ts: `/tree` in the TUI (docs/tree.md): pi's tree selector over the session's branches with their labels (the session title for a null
// one) and pi's label key renaming the selected message's branch, `SessionTree.navigate` with the chosen user message in the editor of the conversation it
// switched to, double Escape (pi's default "tree"), and a TUI following a switch made elsewhere (the session row's `main`).

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	treecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

const backspace = "\x7f"

// treeNodes: conversation 1: first → Echo first → second → Echo second; a branch after "Echo first": other → Echo other.
func treeNodes() []any {
	node := func(entry int, parent any, conversation int, role, text string) *delta.JsonObject {
		return delta.JsonObjectOf("entry", float64(entry), "parent", parent, "conversation", float64(conversation), "role", role, "text", text)
	}
	return []any{
		node(1, nil, 1, "user", "first question"), node(2, float64(1), 1, "assistant", "Echo: first"),
		node(3, float64(2), 1, "user", "second question"), node(4, float64(3), 1, "assistant", "Echo: second"),
		node(6, float64(2), 5, "user", "other"), node(7, float64(6), 5, "assistant", "Echo: other"),
	}
}

func TestTreeSwitchesBranchesWithTheChosenMessageInTheEditorDoubleEscapeFollowingASwitch(t *testing.T) {
	var mu sync.Mutex
	var navigated []float64
	var renamed []*delta.JsonObject
	var rows *p2test.Cell
	setMain := func(main int) {
		row := tuitest.Session
		row.Main = main
		_ = rows.Replace([]any{row.JSON()})
	}
	sessions := p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		rows = self.Cell([]any{tuitest.Session.JSON()})
		created := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return tuitest.Session.JSON(), nil }
		return p2test.StubMembers{Variant: "owner", State: map[string]any{"sessions": rows, "defaultCwd": "/work", "defaultEnv": nil, "operations": []any{}},
			Methods: map[string]p2test.StubMethod{"create": created, "clone": created, "fork": created, "relocate": noop, "rename": noop, "pin": noop, "archive": noop, "restore": noop, "dismissOperation": noop}}
	}}
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{Views: func(conversation int) delta.JsonValue {
		return tuitest.View(t, conversation, nil, tuitest.DocsOf(t, "pi.agent", map[string]any{"model": map[string]any{"provider": "faux", "modelId": "faux-1"}}), 0)
	}, Model: metaJSON()})
	tree := p2test.Stub{Contract: treecontract.SessionTree, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "owner", Methods: map[string]p2test.StubMethod{
			"tree": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("nodes", treeNodes(), "branches", []any{
					delta.JsonObjectOf("conversation", float64(1), "label", nil), delta.JsonObjectOf("conversation", float64(5), "label", "Echo: first"),
				}, "main", float64(1), "current", float64(4)), nil
			},
			"select": noop,
			"rename": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				mu.Lock()
				renamed = append(renamed, args.(*delta.JsonObject))
				mu.Unlock()
				return nil, nil
			},
			"navigate": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				mu.Lock()
				navigated = append(navigated, args.(*delta.JsonObject).Value("entry").(float64))
				mu.Unlock()
				setMain(5)
				return delta.JsonObjectOf("conversation", float64(5), "text", "second question"), nil
			},
		}}
	}}
	s := tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{sessions}, Worker: []p2test.Stub{chat.Stub, tuitest.DraftsStub(), tree},
		Facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), sessionstui.Facet}, Columns: 100,
	})
	surface := func() (surface *chattui.ChatSurface) {
		s.Do(func() { surface, _ = s.Runtime.Surface("chat").(*chattui.ChatSurface) })
		return surface
	}
	renames := func() []*delta.JsonObject {
		mu.Lock()
		defer mu.Unlock()
		return append([]*delta.JsonObject(nil), renamed...)
	}
	identity := func() (identity string) {
		s.Do(func() { identity = s.Runtime.Identity() })
		return identity
	}
	editable := func() (ok bool) {
		s.Do(func() {
			if surface, _ := s.Runtime.Surface("chat").(*chattui.ChatSurface); surface != nil {
				ok = surface.Editable()
			}
		})
		return ok
	}
	tuitest.Poll(t, editable)
	_ = surface
	// pi's layout: the current branch first, a connector where "Echo: first" branches, the current one selected.
	typedEnter(s.Fixture, "/tree")
	pollContains(t, s.Fixture, "Session Tree")
	shown := s.Screen()
	// Each branch's label on its first own message: the session title for conversation 1.
	mustMatch(t, `• \[Fixture\] user: first question`, shown)
	mustMatch(t, `├─ • user: second question`, shown)
	mustMatch(t, `› .*• assistant: Echo: second`, shown)
	mustMatch(t, `└─ \[Echo: first\] user: other`, shown)
	mustMatch(t, `\(4/6\)`, shown)
	// pi's label key renames the branch holding the selected message.
	s.Send("L")
	pollContains(t, s.Fixture, "Branch label (empty: the session title):")
	typed(s.Fixture, strings.Repeat(backspace, len("Fixture")))
	typedEnter(s.Fixture, "Main line")
	tuitest.Poll(t, func() bool { return len(renames()) == 1 })
	if got := fmtJSON(renames()[0]); got != "map[conversation:1 label:Main line]" {
		t.Fatalf("rename = %s", got)
	}
	pollContains(t, s.Fixture, "[Main line] user: first question")
	// Emptied: back to the session title.
	s.Send("L")
	pollContains(t, s.Fixture, "Branch label (empty: the session title):")
	typedEnter(s.Fixture, strings.Repeat(backspace, len("Main line")))
	tuitest.Poll(t, func() bool { return len(renames()) == 2 })
	if got := fmtJSON(renames()[1]); got != "map[conversation:1 label:<nil>]" {
		t.Fatalf("rename = %s", got)
	}
	pollContains(t, s.Fixture, "[Fixture] user: first question")
	// Escape leaves the label input, not the tree.
	s.Send(down)
	s.Send("L")
	pollScreen(t, s.Fixture, matches(`Branch label:`))
	s.Send(esc)
	pollContains(t, s.Fixture, "(5/6)")
	if len(renames()) != 2 {
		t.Fatal("Escape renamed")
	}
	// A user message: the session continues before it, its text in the editor of the conversation it switched to.
	s.Send(up)
	s.Send(up)
	s.Send(enter)
	tuitest.Poll(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(navigated) == 1 })
	if navigated[0] != 3 {
		t.Fatalf("navigated = %v", navigated)
	}
	tuitest.Poll(t, func() bool { return identity() == "s#5" })
	tuitest.Poll(t, func() bool {
		text := ""
		s.Do(func() { text = s.Runtime.Surface("chat").(*chattui.ChatSurface).Editor.Text() })
		return text == "second question"
	})
	var main int
	s.Do(func() { main = s.Runtime.Ctx.Identity().Main })
	if main != 5 {
		t.Fatalf("main = %d", main)
	}
	// Another device switches back: this TUI, viewing the previous main conversation, follows.
	setMain(1)
	tuitest.Poll(t, func() bool { return identity() == "s#1" })
	// Double Escape on an empty editor opens the tree (pi's default action).
	tuitest.Poll(t, editable)
	s.Do(func() { s.Runtime.Surface("chat").(*chattui.ChatSurface).Editor.SetText("") })
	s.Send(esc)
	s.Send(esc)
	pollContains(t, s.Fixture, "Session Tree")
	s.Send(esc)
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Session Tree") })
}
