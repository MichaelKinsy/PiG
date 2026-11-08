package clienttest

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

func treeNode(entry, parent, conversation float64, role, text string) *delta.JsonObject {
	var parentValue any
	if parent != 0 {
		parentValue = parent
	}
	return delta.JsonObjectOf("entry", entry, "parent", parentValue, "conversation", conversation, "role", role, "text", text)
}

func TestTreeSwitchesBranchesWithTheChosenMessageInTheEditorDoubleEscapeFollowingASwitch(t *testing.T) {
	var mu sync.Mutex
	var navigated []float64
	type rename struct {
		conversation float64
		label        any
	}
	var renamed []rename
	var rows *p2test.Cell
	setMain := func(main float64) {
		patchRows(rows, func(row *delta.JsonObject) { row.Set("main", main) })
	}
	sessions := p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		rows = self.Cell(rowsOf(fixtureSession()))
		methods := map[string]p2test.StubMethod{}
		for _, name := range []string{"relocate", "rename", "pin", "archive", "restore", "dismissOperation"} {
			methods[name] = noop
		}
		for _, name := range []string{"create", "clone", "fork"} {
			methods[name] = func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return fixtureSession().JSON(), nil }
		}
		return p2test.StubMembers{
			Variant: "owner", Methods: methods,
			State: map[string]any{"sessions": rows, "defaultCwd": "/work", "defaultEnv": nil, "operations": []any{}},
		}
	}}
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{
		Views: func(conversation int) delta.JsonValue {
			return tuitest.View(t, conversation, nil, tuitest.DocsOf(t, "pi.agent", delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"))), 0)
		},
		Model: metaJSON(),
	})
	// Conversation 1: first → Echo first → second → Echo second; a branch after "Echo first": other → Echo other.
	nodes := []any{
		treeNode(1, 0, 1, "user", "first question"), treeNode(2, 1, 1, "assistant", "Echo: first"),
		treeNode(3, 2, 1, "user", "second question"), treeNode(4, 3, 1, "assistant", "Echo: second"),
		treeNode(6, 2, 5, "user", "other"), treeNode(7, 6, 5, "assistant", "Echo: other"),
	}
	// Conversation 1 has no label of its own (the session title shows); the branch is named after its fork point.
	branches := []any{delta.JsonObjectOf("conversation", float64(1), "label", nil), delta.JsonObjectOf("conversation", float64(5), "label", "Echo: first")}
	tree := p2test.Stub{Contract: treecontract.SessionTree, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "owner", Methods: map[string]p2test.StubMethod{
			"tree": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				mu.Lock()
				defer mu.Unlock()
				return delta.JsonObjectOf("nodes", nodes, "branches", branches, "main", float64(1), "current", float64(4)), nil
			},
			"select": noop,
			"rename": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				object := args.(*delta.JsonObject)
				mu.Lock()
				renamed = append(renamed, rename{object.Value("conversation").(float64), object.Value("label")})
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
	f := tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{sessions}, Worker: []p2test.Stub{chat.Stub, tree},
		Facets: []host.Facet{tui.CoreFacet, chattui.Facet, sessionstui.Facet}, Columns: 100,
	})
	f.Do(func() { f.Runtime.Navigate("chat", "s", true, nil) })
	// surface is the chat surface; call it on the owner loop (inside Do).
	surface := func() *chattui.ChatSurface { return f.Runtime.Ctx.Surface("chat").(*chattui.ChatSurface) }
	editable := func() bool {
		ok := false
		f.Do(func() { ok = surface().Editable() })
		return ok
	}
	counts := func() (int, int) { mu.Lock(); defer mu.Unlock(); return len(navigated), len(renamed) }
	tuitest.Poll(t, editable)
	// pi's layout: the current branch first, a connector where "Echo: first" branches, the current one selected.
	typed(f.Fixture, "/tree")
	f.Send(enter)
	pollContains(t, f.Fixture, "Session Tree")
	shown := f.Screen()
	// Each branch's label on its first own message: the session title for conversation 1.
	for _, pattern := range []string{`• \[Fixture\] user: first question`, `├─ • user: second question`, `› .*• assistant: Echo: second`, `└─ \[Echo: first\] user: other`, `\(4/6\)`} {
		if !matches(pattern)(shown) {
			t.Fatalf("the tree lacks %s:\n%s", pattern, shown)
		}
	}
	// pi's label key renames the branch holding the selected message.
	f.Send("L")
	pollContains(t, f.Fixture, "Branch label (empty: the session title):")
	typed(f.Fixture, strings.Repeat(backspace, len("Fixture")))
	typed(f.Fixture, "Main line")
	f.Send(enter)
	tuitest.Poll(t, func() bool { _, r := counts(); return r == 1 })
	mu.Lock()
	if renamed[0] != (rename{1, "Main line"}) {
		t.Fatalf("renamed = %v", renamed)
	}
	mu.Unlock()
	pollContains(t, f.Fixture, "[Main line] user: first question")
	// Emptied: back to the session title.
	f.Send("L")
	pollContains(t, f.Fixture, "Branch label (empty: the session title):")
	typed(f.Fixture, strings.Repeat(backspace, len("Main line")))
	f.Send(enter)
	tuitest.Poll(t, func() bool { _, r := counts(); return r == 2 })
	mu.Lock()
	if renamed[1] != (rename{1, nil}) {
		t.Fatalf("renamed = %v", renamed)
	}
	mu.Unlock()
	pollContains(t, f.Fixture, "[Fixture] user: first question")
	// Escape leaves the label input, not the tree.
	f.Send(down)
	f.Send("L")
	pollScreen(t, f.Fixture, matches(`Branch label:`))
	f.Send(esc)
	pollContains(t, f.Fixture, "(5/6)")
	if _, r := counts(); r != 2 {
		t.Fatalf("renames = %d", r)
	}
	// A user message: the session continues before it, its text in the editor of the conversation it switched to.
	f.Send(up)
	f.Send(up)
	f.Send(enter)
	tuitest.Poll(t, func() bool { n, _ := counts(); return n == 1 })
	mu.Lock()
	if navigated[0] != 3 {
		t.Fatalf("navigated = %v", navigated)
	}
	mu.Unlock()
	identity := func() string {
		id := ""
		f.Do(func() { id = f.Runtime.Identity() })
		return id
	}
	tuitest.Poll(t, func() bool { return identity() == "s#5" })
	tuitest.Poll(t, editable)
	text, main := "", 0
	f.Do(func() { text = surface().Editor.Text(); main = f.Runtime.Ctx.Identity().Main })
	if text != "second question" || main != 5 {
		t.Fatalf("editor %q, main %d", text, main)
	}
	// Another device switches back: this TUI, viewing the previous main conversation, follows.
	setMain(1)
	tuitest.Poll(t, func() bool { return identity() == "s#1" })
	// Double Escape on an empty editor opens the tree (pi's default action).
	tuitest.Poll(t, editable)
	f.Do(func() { surface().Editor.SetText("") })
	f.Send(esc)
	f.Send(esc)
	pollContains(t, f.Fixture, "Session Tree")
	f.Send(esc)
	pollScreen(t, f.Fixture, func(screen string) bool { return !strings.Contains(screen, "Session Tree") })
}
