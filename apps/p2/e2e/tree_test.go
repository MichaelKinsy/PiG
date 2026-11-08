// Ports test/p2/tree.test.ts (Pi main c23b9ec1): `/tree` (docs/tree.md) on the real server and session worker with the fake
// provider: the tree of the main conversation and its branches, switching (forking, reusing a branch, before the first
// message), the session row's `main`, read-only earlier branches, branch labels (stored at the fork, renamed, conversation 1's
// null for the session title), switching while busy and `p2.main` version 1's migration. The cases run in order on one
// session, as the TypeScript's.
//
// Four cases need plugins this port has not reached and are not in this file: "gives a branch the main agent's tools" (the
// ports plugin's `ports` tool), "keeps the main agent's artifacts list across branches" (the artifacts plugin's `artifact`
// tool), "registers a branch's subagents on conversation 1 and reports to the branch" and "reports to the branch that asked,
// also after a switch; team send to parent goes there too" (the subagents plugin's `subagent` and `team` tools): see
// rows/p2-chattree.md.

package e2e_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	treecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree"
)

// viewText is the text of every model message of a conversation view's entries, joined by newlines.
func viewText(view delta.JsonValue) string {
	if view == nil {
		return ""
	}
	var parts []string
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		model, _ := object(entry).Value("model").([]any)
		for _, message := range model {
			switch content := object(message).Value("content").(type) {
			case string:
				parts = append(parts, content)
			case []any:
				for _, part := range content {
					if object(part).Value("type") == "text" {
						parts = append(parts, object(part).Value("text").(string))
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// treeNodeWith is the node of tree with the role and text; the test fails without one.
func treeNodeWith(t *testing.T, tree delta.JsonValue, role, value string) *delta.JsonObject {
	t.Helper()
	nodes, _ := object(tree).Value("nodes").([]any)
	for _, node := range nodes {
		if object(node).Value("role") == role && object(node).Value("text") == value {
			return object(node)
		}
	}
	t.Fatalf("no %s node %q", role, value)
	return nil
}

// labels is "<conversation>:<label>" ("null" for the session title) of each branch, in the list's order.
func labels(branches []any) []string {
	out := make([]string, len(branches))
	for i, branch := range branches {
		label := "null"
		if text, ok := object(branch).Value("label").(string); ok {
			label = text
		}
		out[i] = fmt.Sprintf("%d:%s", int(object(branch).Value("conversation").(float64)), label)
	}
	return out
}

func equalStrings(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// mainDocV1 is `p2.main` as version 1 stored it: the top-level conversations' IDs, no labels.
type mainDocV1 struct {
	Conversation int   `json:"conversation"`
	Branches     []int `json:"branches"`
}

var mainV1 = durable.DefineDoc(durable.DocDefinition[mainDocV1]{
	CommonDocDefinition: durable.CommonDocDefinition[mainDocV1]{
		Kind: "p2.main", Version: 1,
		Initial: func() mainDocV1 {
			return mainDocV1{Conversation: int(durable.ROOT_CONVERSATION_ID), Branches: []int{int(durable.ROOT_CONVERSATION_ID)}}
		},
		CheckpointWhen: func(mainDocV1, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
})

func TestTreeFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	owner := fixture.Browser()
	until := func(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	var sessionID string
	chatOf := func(t *testing.T, conversation float64) *host.Reference {
		t.Helper()
		ref, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", sessionID, "conversation", conversation), "participant")
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	treeRef := func(t *testing.T) *host.Reference {
		t.Helper()
		ref, err := p2test.HoldIn(owner, treecontract.SessionTree, delta.JsonObjectOf("session", sessionID), "owner")
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	sessionsRef := func(t *testing.T) *host.Reference {
		t.Helper()
		ref, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	idle := func(t *testing.T) {
		t.Helper()
		status, err := p2test.HoldIn(owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		until(t, status.Member("statuses"), func(value delta.JsonValue) bool {
			entry := object(object(value).Value(sessionID))
			return entry != nil && entry.Value("tasks") == float64(0)
		})
		status.Release()
	}
	rowOf := func(rows delta.JsonValue) *delta.JsonObject {
		list, _ := rows.([]any)
		for _, row := range list {
			if object(row).Value("id") == sessionID {
				return object(row)
			}
		}
		return nil
	}
	// branchesWhen is the session row's branches (newest first) once ready accepts them.
	branchesWhen := func(t *testing.T, ready func(branches []any) bool) []any {
		t.Helper()
		sessions := sessionsRef(t)
		defer sessions.Release()
		rows := until(t, sessions.Member("sessions"), func(value delta.JsonValue) bool {
			row := rowOf(value)
			if row == nil {
				return false
			}
			branches, _ := row.Value("branches").([]any)
			return ready(branches)
		})
		branches, _ := rowOf(rows).Value("branches").([]any)
		return branches
	}
	send := func(t *testing.T, conversation float64, value string) delta.JsonValue {
		t.Helper()
		chat := chatOf(t, conversation)
		if _, err := chat.Call("send", delta.JsonObjectOf("text", value, "requestId", fmt.Sprintf("%d:%s", int(conversation), value)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view := until(t, chat.Member("view"), func(next delta.JsonValue) bool { return strings.Contains(viewText(next), "Echo: "+value) })
		chat.Release()
		idle(t)
		return view
	}
	treeOf := func(t *testing.T, ref *host.Reference) *delta.JsonObject {
		t.Helper()
		tree, err := ref.Call("tree", nil, p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(tree)
	}
	navigate := func(t *testing.T, ref *host.Reference, entry any) *delta.JsonObject {
		t.Helper()
		switched, err := ref.Call("navigate", delta.JsonObjectOf("entry", entry), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(switched)
	}
	selectBranch := func(t *testing.T, ref *host.Reference, conversation float64) {
		t.Helper()
		if _, err := ref.Call("select", delta.JsonObjectOf("conversation", conversation), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	rename := func(ref *host.Reference, conversation float64, label any) error {
		_, err := ref.Call("rename", delta.JsonObjectOf("conversation", conversation, "label", label), p2test.Background)
		return err
	}
	rejects := func(t *testing.T, err error, pattern string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), pattern) {
			t.Fatalf("got %v, want an error with %q", err, pattern)
		}
	}
	mainOf := func(t *testing.T, want float64) {
		t.Helper()
		sessions := sessionsRef(t)
		defer sessions.Release()
		until(t, sessions.Member("sessions"), func(value delta.JsonValue) bool {
			row := rowOf(value)
			return row != nil && row.Value("main") == want
		})
	}

	{
		sessions := sessionsRef(t)
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Tree"), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		sessionID, _ = object(created).Value("id").(string)
		sessions.Release()
	}
	send(t, 1, "first")
	send(t, 1, "second")

	t.Run("lists the main conversation's messages as one line", func(t *testing.T) {
		ref := treeRef(t)
		defer ref.Release()
		tree := treeOf(t, ref)
		if tree.Value("main") != float64(1) {
			t.Fatalf("main %v", tree.Value("main"))
		}
		nodes, _ := tree.Value("nodes").([]any)
		var got []string
		for i, node := range nodes {
			got = append(got, fmt.Sprintf("%s:%s", object(node).Value("role"), object(node).Value("text")))
			var wantParent any
			if i > 0 {
				wantParent = object(nodes[i-1]).Value("entry")
			}
			if object(node).Value("parent") != wantParent {
				t.Fatalf("node %d parent %v, want %v", i, object(node).Value("parent"), wantParent)
			}
		}
		want := []string{"user:first", "assistant:Echo: first", "user:second", "assistant:Echo: second"}
		if !equalStrings(got, want) {
			t.Fatalf("nodes %v, want %v", got, want)
		}
		if tree.Value("current") != object(nodes[len(nodes)-1]).Value("entry") {
			t.Fatalf("current %v", tree.Value("current"))
		}
	})

	var other float64
	t.Run("forks before a chosen user message; the earlier branch is read-only", func(t *testing.T) {
		ref := treeRef(t)
		defer ref.Release()
		before := treeOf(t, ref)
		second := treeNodeWith(t, before, "user", "second")
		switched := navigate(t, ref, second.Value("entry"))
		other = switched.Value("conversation").(float64)
		if other == 1 {
			t.Fatal("no fork")
		}
		if switched.Value("text") != "second" {
			t.Fatalf("text %v", switched.Value("text"))
		}
		mainOf(t, other)
		// Labelled at once with the message the owner chose to edit (not the answer before it); conversation 1 has none.
		got := labels(branchesWhen(t, func(branches []any) bool { return len(branches) == 2 }))
		if want := []string{fmt.Sprintf("%d:second", int(other)), "1:null"}; !equalStrings(got, want) {
			t.Fatalf("branches %v, want %v", got, want)
		}
		// The branch holds the history before "second" and goes on from there.
		view := send(t, other, "other")
		if !strings.Contains(viewText(view), "Echo: first") || strings.Contains(viewText(view), "Echo: second") {
			t.Fatalf("branch history: %q", viewText(view))
		}
		// Conversation 1 is an earlier branch now.
		earlier := chatOf(t, 1)
		_, err := earlier.Call("send", delta.JsonObjectOf("text", "no", "requestId", "no"), p2test.Background)
		rejects(t, err, "earlier branch of the session")
		earlier.Release()
		tree := treeOf(t, ref)
		if tree.Value("main") != other {
			t.Fatalf("main %v", tree.Value("main"))
		}
		otherNode := treeNodeWith(t, tree, "user", "other")
		if otherNode.Value("parent") != treeNodeWith(t, tree, "assistant", "Echo: first").Value("entry") {
			t.Fatalf("other's parent %v", otherNode.Value("parent"))
		}
		if tree.Value("current") != treeNodeWith(t, tree, "assistant", "Echo: other").Value("entry") {
			t.Fatalf("current %v", tree.Value("current"))
		}
		// Both answers to "first"'s branch point stay in the tree.
		siblings := 0
		nodes, _ := tree.Value("nodes").([]any)
		for _, node := range nodes {
			if object(node).Value("parent") == otherNode.Value("parent") {
				siblings++
			}
		}
		if siblings != 2 {
			t.Fatalf("%d answers hang below the branch point, want 2", siblings)
		}
	})

	t.Run("lists the branches in the session row and switches with select", func(t *testing.T) {
		sessions := sessionsRef(t)
		defer sessions.Release()
		rows := until(t, sessions.Member("sessions"), func(value delta.JsonValue) bool {
			row := rowOf(value)
			if row == nil {
				return false
			}
			branches, _ := row.Value("branches").([]any)
			return len(branches) == 2
		})
		row := rowOf(rows)
		branches, _ := row.Value("branches").([]any)
		// Newest first; the branch's first own message ("other") does not relabel it.
		if got := labels(branches); !equalStrings(got, []string{fmt.Sprintf("%d:second", int(other)), "1:null"}) {
			t.Fatalf("branches %v", got)
		}
		if object(branches[0]).Value("conversation") != row.Value("main") {
			t.Fatalf("main %v, first branch %v", row.Value("main"), branches[0])
		}
		ref := treeRef(t)
		defer ref.Release()
		selectBranch(t, ref, 1)
		mainOf(t, 1)
		selectBranch(t, ref, row.Value("main").(float64))
		mainOf(t, row.Value("main").(float64))
		_, err := ref.Call("select", delta.JsonObjectOf("conversation", float64(999)), p2test.Background)
		rejects(t, err, "Unknown branch")
	})

	t.Run("switches back to a branch ending at the chosen answer without forking", func(t *testing.T) {
		ref := treeRef(t)
		defer ref.Release()
		tree := treeOf(t, ref)
		switched := navigate(t, ref, treeNodeWith(t, tree, "assistant", "Echo: second").Value("entry"))
		if switched.Value("conversation") != float64(1) || switched.Value("text") != nil {
			t.Fatalf("switched %v", switched)
		}
		after := treeOf(t, ref)
		if after.Value("main") != float64(1) {
			t.Fatalf("main %v", after.Value("main"))
		}
		// No fork: the labels stay.
		if got := labels(after.Value("branches").([]any)); !equalStrings(got, []string{"1:null", fmt.Sprintf("%d:second", int(other))}) {
			t.Fatalf("branches %v", got)
		}
		if after.Value("current") != treeNodeWith(t, after, "assistant", "Echo: second").Value("entry") {
			t.Fatalf("current %v", after.Value("current"))
		}
		send(t, 1, "third")
	})

	var empty float64
	t.Run("starts an empty conversation before the first message", func(t *testing.T) {
		ref := treeRef(t)
		defer ref.Release()
		tree := treeOf(t, ref)
		switched := navigate(t, ref, treeNodeWith(t, tree, "user", "first").Value("entry"))
		empty = switched.Value("conversation").(float64)
		if switched.Value("text") != "first" {
			t.Fatalf("text %v", switched.Value("text"))
		}
		branches := treeOf(t, ref).Value("branches").([]any)
		if got := labels(branches[len(branches)-1:]); !equalStrings(got, []string{fmt.Sprintf("%d:first", int(empty))}) {
			t.Fatalf("last branch %v", got)
		}
		chat := chatOf(t, empty)
		view := until(t, chat.Member("view"), func(next delta.JsonValue) bool { return next != nil })
		if entries, _ := object(view).Value("entries").([]any); len(entries) != 0 {
			t.Fatalf("%d entries before the first message", len(entries))
		}
		chat.Release()
		// Its agent works: the model came along.
		fresh := send(t, empty, "fresh")
		if strings.Contains(viewText(fresh), "Echo: first") {
			t.Fatalf("the empty conversation holds the first answer: %q", viewText(fresh))
		}
		after := treeOf(t, ref)
		if treeNodeWith(t, after, "user", "fresh").Value("parent") != nil {
			t.Fatalf("fresh has a parent: %v", treeNodeWith(t, after, "user", "fresh"))
		}
	})

	var answer float64
	t.Run("labels a branch after an answer with that answer", func(t *testing.T) {
		ref := treeRef(t)
		defer ref.Release()
		tree := treeOf(t, ref)
		node := treeNodeWith(t, tree, "assistant", "Echo: first")
		switched := navigate(t, ref, node.Value("entry"))
		answer = switched.Value("conversation").(float64)
		if switched.Value("text") != nil {
			t.Fatalf("text %v", switched.Value("text"))
		}
		branches := branchesWhen(t, func(list []any) bool { return len(list) == 4 })
		if got := labels(branches[:1]); !equalStrings(got, []string{fmt.Sprintf("%d:Echo: first", int(answer))}) {
			t.Fatalf("newest branch %v", got)
		}
		// The nodes name the branch storing them: the fork's first message shows its label.
		send(t, answer, "after the answer")
		after := treeOf(t, ref)
		if treeNodeWith(t, after, "user", "after the answer").Value("conversation") != answer {
			t.Fatal("the answer's branch does not store its message")
		}
		if treeNodeWith(t, after, "user", "first").Value("conversation") != float64(1) {
			t.Fatal("conversation 1 does not store the first message")
		}
	})

	t.Run("renames branches; conversation 1 goes back to the session title", func(t *testing.T) {
		ref := treeRef(t)
		defer ref.Release()
		branches := branchesWhen(t, func(list []any) bool { return len(list) == 4 })
		second := object(branches[2])
		if second.Value("label") != "second" {
			t.Fatalf("third branch %v", second)
		}
		secondID := second.Value("conversation").(float64)
		if err := rename(ref, secondID, "  Second\n  try "); err != nil {
			t.Fatal(err)
		}
		if err := rename(ref, 1, "Main line"); err != nil {
			t.Fatal(err)
		}
		got := labels(branchesWhen(t, func(list []any) bool { return object(list[3]).Value("label") == "Main line" }))
		if want := []string{fmt.Sprintf("%d:Second try", int(secondID)), "1:Main line"}; !equalStrings(got[2:], want) {
			t.Fatalf("branches %v, want ... %v", got, want)
		}
		if err := rename(ref, 1, nil); err != nil {
			t.Fatal(err)
		}
		branchesWhen(t, func(list []any) bool { return object(list[3]).Value("label") == nil })
		if err := rename(ref, 1, "Again"); err != nil {
			t.Fatal(err)
		}
		if err := rename(ref, 1, " "); err != nil {
			t.Fatal(err)
		}
		if first := object(treeOf(t, ref).Value("branches").([]any)[0]); first.Value("label") != nil {
			t.Fatalf("conversation 1's label %v", first.Value("label"))
		}
		rejects(t, rename(ref, secondID, " "), "at least one character")
		rejects(t, rename(ref, secondID, nil), "at least one character")
		rejects(t, rename(ref, secondID, strings.Repeat("x", 201)), "at most 200 characters")
		rejects(t, rename(ref, 999, "x"), "Unknown branch")
		if label := object(treeOf(t, ref).Value("branches").([]any)[1]).Value("label"); label != "Second try" {
			t.Fatalf("second branch's label %v", label)
		}
	})

	t.Run("switches while the main conversation works; the branch left runs on, read-only, and stops", func(t *testing.T) {
		ref := treeRef(t)
		defer ref.Release()
		left := treeOf(t, ref).Value("main").(float64)
		chat := chatOf(t, left)
		defer chat.Release()
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "slow answer", "requestId", "slow"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		until(t, chat.Member("view"), func(next delta.JsonValue) bool { return next != nil && running(next) })
		selectBranch(t, ref, 1)
		mainOf(t, 1)
		status, err := p2test.HoldIn(owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		until(t, status.Member("statuses"), func(value delta.JsonValue) bool {
			entry := object(object(value).Value(sessionID))
			return entry != nil && busyHas(entry, left)
		})
		status.Release()
		_, err = chat.Call("send", delta.JsonObjectOf("text", "no", "requestId", "no-left"), p2test.Background)
		rejects(t, err, "earlier branch of the session")
		if _, err := chat.Call("stop", nil, p2test.Background); err != nil {
			t.Fatal(err)
		}
		until(t, chat.Member("view"), func(next delta.JsonValue) bool { return next != nil && !running(next) })
		idle(t)
		selectBranch(t, ref, left)
		mainOf(t, left)
		_, err = ref.Call("navigate", delta.JsonObjectOf("entry", float64(999_999)), p2test.Background)
		rejects(t, err, "Unknown message")
	})

	t.Run("migrates p2.main version 1: labels start null, the worker fills the branches once from their fork points", func(t *testing.T) {
		migrated, err := p2runtime.MainDoc.AnyDefinition().Migrate(delta.JsonObjectOf("conversation", float64(3), "branches", []any{float64(1), float64(3)}), 1)
		if err != nil {
			t.Fatal(err)
		}
		want := delta.JsonObjectOf("conversation", float64(3), "branches", []any{
			delta.JsonObjectOf("conversation", float64(1), "label", nil),
			delta.JsonObjectOf("conversation", float64(3), "label", nil),
		})
		gotJSON, _ := json.Marshal(migrated)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("migrated %s, want %s", gotJSON, wantJSON)
		}
		before := branchesWhen(t, func(list []any) bool { return len(list) == 4 })
		answerID, emptyID, editID := object(before[0]).Value("conversation").(float64), object(before[1]).Value("conversation").(float64), object(before[2]).Value("conversation").(float64)
		if answerID != answer || emptyID != empty || editID != other {
			t.Fatalf("branches %v", labels(before))
		}
		// The worker stops (archived); its store gets `p2.main` as version 1 wrote it, then the session comes back.
		sessions := sessionsRef(t)
		defer sessions.Release()
		if _, err := sessions.Call("archive", delta.JsonObjectOf("id", sessionID), p2test.Background); err != nil {
			t.Fatal(err)
		}
		stored, err := p2runtime.OpenSessionStorage(p2test.Background, fixture.DataDir, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		opened := session.CreateSession(stored.Storage)
		if _, err := opened.Commit(p2test.Background, func(tx durable.Tx) (any, error) {
			if err := tx.RetireDoc(p2runtime.MainDoc); err != nil {
				return nil, err
			}
			draft, err := durable.TxDoc(tx, mainV1)
			if err != nil {
				return nil, err
			}
			if err := draft.Set("conversation", answerID); err != nil {
				return nil, err
			}
			return nil, draft.Set("branches", []any{float64(1), editID, emptyID, answerID})
		}); err != nil {
			t.Fatal(err)
		}
		if err := opened.Close(p2test.Background); err != nil {
			t.Fatal(err)
		}
		if err := stored.Close(p2test.Background); err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.Call("restore", delta.JsonObjectOf("id", sessionID), p2test.Background); err != nil {
			t.Fatal(err)
		}
		ref := treeRef(t)
		defer ref.Release()
		treeOf(t, ref)
		// Fork points: the answer before "second" (Edit forked there), none (before the first message: its first own user
		// message), "Echo: first". Conversation 1 keeps null: the session title.
		got := labels(branchesWhen(t, func(list []any) bool {
			for _, branch := range list {
				if object(branch).Value("conversation") == emptyID {
					return object(branch).Value("label") != "first"
				}
			}
			return false
		}))
		wantLabels := []string{
			fmt.Sprintf("%d:Echo: first", int(answerID)), fmt.Sprintf("%d:fresh", int(emptyID)), fmt.Sprintf("%d:Echo: first", int(editID)), "1:null",
		}
		if !equalStrings(got, wantLabels) {
			t.Fatalf("branches %v, want %v", got, wantLabels)
		}
	})
}
