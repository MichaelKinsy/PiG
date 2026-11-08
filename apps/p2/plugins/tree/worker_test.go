package tree_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/tree"
	contract "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The tree worker over a real Durable harness (memory storage): reading the tree, navigating (reuse, fork, new
// conversation before the first message), selecting, renaming, the read-only rule, and the catalog's report.

type reports struct {
	mu    sync.Mutex
	calls []*delta.JsonObject
}

func (r *reports) add(call *delta.JsonObject) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
}
func (r *reports) last() *delta.JsonObject {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

func user(text string) durable.EntryDraft {
	return durable.EntryDraft{Kind: "p2.input", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(text)}}}
}

func answer(text string) durable.EntryDraft {
	return durable.EntryDraft{Kind: "pi.assistant", Model: []ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}}}}
}

type fixture struct {
	opened  harness.Harness
	reports *reports
	tree    *host.Reference
	test    *p2test.TestHost
}

// newFixture: conversation 1 holds "first" (entry 1), "first answer" (2), "second" (3), "second answer" (4).
func newFixture(t *testing.T) *fixture {
	t.Helper()
	p2test.NoLeakedGoroutines(t)
	opened := p2test.OpenHarness(t)
	if _, err := opened.Root(p2test.Background, &harness.RootOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Commit(p2test.Background, func(tx durable.Tx) (any, error) {
		for _, draft := range []durable.EntryDraft{user("first"), answer("first answer"), user("second\n  line"), answer("second answer")} {
			if _, err := tx.AppendEntry(durable.ROOT_CONVERSATION_ID, draft); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{opened: opened, reports: &reports{}}
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "s1"))
	f.test = p2test.NewTestHost(t, p2test.TestHostOptions{
		Address: &address, Worker: true,
		Facets: []host.Facet{p2test.WorkerFacet(p2test.HarnessParts(opened), &p2runtime.WorkerParts{Liveness: p2runtime.NewLiveness()}, nil), tree.Worker},
		Stubs: []p2test.Stub{{Contract: sessionscontract.Sessions, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "worker", State: map[string]any{"session": nil}, Methods: map[string]p2test.StubMethod{
				"treeSelf": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					f.reports.add(args.(*delta.JsonObject))
					return nil, nil
				},
			}}
		}}},
	})
	client := f.test.Client(p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal})
	hold, err := client.Hold(p2test.Background, contract.SessionTree, delta.JsonObjectOf("session", "s1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 1)
	go func() { waiting <- p2runtime.WhenLive(p2test.Background, hold) }()
	select {
	case err := <-waiting:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		state := hold.State().Value()
		t.Fatalf("the tree never went live: %+v cause %+v; host errors: %+v", state.Kind, state.Cause, f.test.Errors())
	}
	if variant, _, err := hold.Variant(); err != nil || variant != "owner" {
		t.Fatalf("variant %q: %v", variant, err)
	}
	ref := hold
	t.Cleanup(ref.Release)
	f.tree = ref
	return f
}

func (f *fixture) call(t *testing.T, method string, args delta.JsonValue) *delta.JsonObject {
	t.Helper()
	result, err := f.tree.Call(method, args, p2test.Background)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	object, _ := result.(*delta.JsonObject)
	return object
}

func list(object *delta.JsonObject, key string) []any {
	items, _ := object.Value(key).([]any)
	return items
}

func field(value any, key string) any { return value.(*delta.JsonObject).Value(key) }

func TestReadsTheTreeOfTheMainConversation(t *testing.T) {
	f := newFixture(t)
	got := f.call(t, "tree", nil)
	nodes := list(got, "nodes")
	if len(nodes) != 4 || got.Value("main") != float64(1) || got.Value("current") != float64(4) {
		t.Fatalf("tree = %+v", got)
	}
	for i, node := range nodes {
		var parent any
		if i > 0 {
			parent = float64(i)
		}
		if field(node, "entry") != float64(i+1) || field(node, "parent") != parent || field(node, "conversation") != float64(1) {
			t.Fatalf("node %d = %+v", i, node)
		}
	}
	if field(nodes[0], "role") != "user" || field(nodes[1], "role") != "assistant" || field(nodes[2], "text") != "second line" {
		t.Fatalf("nodes = %+v", nodes)
	}
	branches := list(got, "branches")
	if len(branches) != 1 || field(branches[0], "label") != nil {
		t.Fatalf("branches = %+v", branches)
	}
}

func TestNavigatesToAnAnswerForkingABranchThenReusesIt(t *testing.T) {
	f := newFixture(t)
	switched := f.call(t, "navigate", delta.JsonObjectOf("entry", float64(2)))
	if switched.Value("conversation") != float64(2) || switched.Value("text") != nil {
		t.Fatalf("navigate = %+v", switched)
	}
	report := f.reports.last()
	if report == nil || report.Value("main") != float64(2) {
		t.Fatalf("report = %+v", report)
	}
	// Newest first, the fork labelled with the chosen message.
	branches := list(report, "branches")
	if len(branches) != 2 || field(branches[0], "conversation") != float64(2) || field(branches[0], "label") != "first answer" || field(branches[1], "label") != nil {
		t.Fatalf("branches = %+v", branches)
	}
	tree := f.call(t, "tree", nil)
	if tree.Value("main") != float64(2) || tree.Value("current") != float64(2) {
		t.Fatalf("tree = %+v", tree)
	}
	// The same point again continues the branch ending there: no new conversation.
	again := f.call(t, "navigate", delta.JsonObjectOf("entry", float64(2)))
	if again.Value("conversation") != float64(2) || len(list(f.call(t, "tree", nil), "branches")) != 2 {
		t.Fatalf("again = %+v", again)
	}
	// Back to the end of conversation 1 by its row.
	f.call(t, "select", delta.JsonObjectOf("conversation", float64(1)))
	if got := f.call(t, "tree", nil); got.Value("main") != float64(1) || got.Value("current") != float64(4) {
		t.Fatalf("after select = %+v", got)
	}
	if f.reports.last().Value("main") != float64(1) {
		t.Fatalf("report after select = %+v", f.reports.last())
	}
}

func TestNavigatingToAUserMessageReturnsItsTextAndContinuesBefore(t *testing.T) {
	f := newFixture(t)
	switched := f.call(t, "navigate", delta.JsonObjectOf("entry", float64(3)))
	if switched.Value("conversation") != float64(2) || switched.Value("text") != "second\n  line" {
		t.Fatalf("navigate = %+v", switched)
	}
	if got := f.call(t, "tree", nil); got.Value("current") != float64(2) {
		t.Fatalf("current = %v", got.Value("current"))
	}
	// Before the first message: a new empty conversation with no entries of its own.
	first := f.call(t, "navigate", delta.JsonObjectOf("entry", float64(1)))
	if first.Value("conversation") != float64(3) || first.Value("text") != "first" {
		t.Fatalf("first = %+v", first)
	}
	if got := f.call(t, "tree", nil); got.Value("main") != float64(3) || got.Value("current") != nil {
		t.Fatalf("tree = %+v", got)
	}
	// Every call again: the empty conversation is reused.
	if again := f.call(t, "navigate", delta.JsonObjectOf("entry", float64(1))); again.Value("conversation") != float64(3) {
		t.Fatalf("again = %+v", again)
	}
}

func TestRefusesWhatIsNotAMessage(t *testing.T) {
	f := newFixture(t)
	if _, err := f.tree.Call("navigate", delta.JsonObjectOf("entry", float64(99)), p2test.Background); err == nil || !strings.Contains(err.Error(), "Unknown message") {
		t.Fatalf("unknown entry: %v", err)
	}
	if _, err := f.tree.Call("select", delta.JsonObjectOf("conversation", float64(7)), p2test.Background); err == nil || !strings.Contains(err.Error(), "Unknown branch") {
		t.Fatalf("unknown branch: %v", err)
	}
}

func TestRenamesBranchesAndResetsTheRootToTheSessionTitle(t *testing.T) {
	f := newFixture(t)
	f.call(t, "navigate", delta.JsonObjectOf("entry", float64(2)))
	f.call(t, "rename", delta.JsonObjectOf("conversation", float64(2), "label", "  my \n branch "))
	f.call(t, "rename", delta.JsonObjectOf("conversation", float64(1), "label", "main line"))
	branches := list(f.reports.last(), "branches")
	if field(branches[0], "label") != "my branch" || field(branches[1], "label") != "main line" {
		t.Fatalf("branches = %+v", branches)
	}
	f.call(t, "rename", delta.JsonObjectOf("conversation", float64(1), "label", nil))
	if field(list(f.reports.last(), "branches")[1], "label") != nil {
		t.Fatalf("conversation 1 keeps a label: %+v", f.reports.last())
	}
	for _, label := range []any{nil, "   ", strings.Repeat("x", contract.PreviewLength+1)} {
		if _, err := f.tree.Call("rename", delta.JsonObjectOf("conversation", float64(2), "label", label), p2test.Background); err == nil {
			t.Fatalf("label %v was accepted", label)
		}
	}
	if _, err := f.tree.Call("rename", delta.JsonObjectOf("conversation", float64(9), "label", "x"), p2test.Background); err == nil {
		t.Fatal("an unknown branch was renamed")
	}
}

func TestEarlierBranchesAreReadOnly(t *testing.T) {
	f := newFixture(t)
	f.call(t, "navigate", delta.JsonObjectOf("entry", float64(2)))
	source, err := f.test.Host.Registry(p2test.Background, p2runtime.ReadOnlyConversations.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var check p2runtime.ReadOnlyCheck
	if err := p2test.Poll(func() (bool, error) {
		table, err := p2runtime.ReadOnlyConversations.Table(source)
		if err != nil {
			return false, nil
		}
		check, _ = table.Get("tree")
		return check != nil, nil
	}, 0, "the tree's read-only check"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.test.Root(func(ctx host.Ctx) (any, error) {
		for conversation, want := range map[int]bool{1: true, 2: false, 99: false} {
			reason, err := check(ctx, conversation)
			if err != nil {
				return nil, err
			}
			if (reason != "") != want {
				t.Errorf("conversation %d: reason %q", conversation, reason)
			}
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReportsTheCatalogAtStart(t *testing.T) {
	f := newFixture(t)
	if err := p2test.Poll(func() (bool, error) { return f.reports.last() != nil, nil }, 0, "the start report"); err != nil {
		t.Fatal(err)
	}
	report := f.reports.last()
	if report.Value("main") != float64(1) || len(list(report, "branches")) != 1 {
		t.Fatalf("report = %+v", report)
	}
}
