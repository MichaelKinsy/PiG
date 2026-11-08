package tree

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/ai"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/tree/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/tree/worker.ts: `/tree` in the session worker (`docs/tree.md`): the tree of the top-level
// conversations' messages, switching the current main conversation (forking a new branch where none ends at the chosen
// point), and the rules that come with it: older branches are read-only, branches carry stored labels, and the session
// catalog follows `p2.main`, pushed by the worker inside the method that changed it.

// notCurrent is why an earlier branch refuses input.
const notCurrent = "This is an earlier branch of the session: switch to it with /tree to continue it"

// page is the entries per page when walking a conversation.
const page = 500

// treeAccepts are the `SessionTree` v1 consumers (a page or TUI loaded before a deploy): the tree without branch labels.
func treeAccepts() contracts.AcceptsTable {
	return contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{Methods: []contracts.Entry[contracts.MethodMapping]{{
		Name: "tree",
		Value: contracts.MethodMapping{Result: func(value delta.JsonValue) (delta.JsonValue, error) {
			tree, ok := value.(*delta.JsonObject)
			if !ok {
				return value, nil
			}
			nodes, _ := tree.Value("nodes").([]any)
			mapped := make([]any, len(nodes))
			for i, each := range nodes {
				node, _ := each.(*delta.JsonObject)
				mapped[i] = delta.JsonObjectOf("entry", node.Value("entry"), "parent", node.Value("parent"), "role", node.Value("role"), "text", node.Value("text"))
			}
			return delta.JsonObjectOf("nodes", mapped, "main", tree.Value("main"), "current", tree.Value("current")), nil
		}},
	}}}}}
}

// durableOf opens the session's Harness.
func durableOf(parts func() (*p2runtime.HarnessParts, error)) (harness.Harness, error) {
	held, err := parts()
	if err != nil {
		return nil, err
	}
	return held.Durable()
}

// Worker is the tree plugin's worker facet.
var Worker = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := p2runtime.Contribute(facet, "p2.tree-branches", serveBranches); err != nil {
		return err
	}
	return facet.Serve(contract.SessionTree, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("the tree is the owner's")
		}
		parts := p2runtime.Cached(p2runtime.HarnessOf(self))
		worker := p2runtime.Cached(p2runtime.WorkerOf(self))
		// call runs fn as one active call of the worker, with the Harness; the calls wait for the followed Durable session, so
		// they park the section that admitted them first. The worker's natives are read before: they are the section's.
		call := func(ctx context.Context, fn func(d harness.Harness) (delta.JsonValue, error)) (delta.JsonValue, error) {
			process, err := worker()
			if err != nil {
				return nil, err
			}
			d, err := durableOf(parts)
			if err != nil {
				return nil, err
			}
			chordctx.Suspend(ctx)
			var result delta.JsonValue
			err = process.Liveness.Call(func() (err error) {
				result, err = fn(d)
				return err
			})
			return result, err
		}
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"tree": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return call(ctx, func(d harness.Harness) (delta.JsonValue, error) { return readTree(ctx, d) })
			},
			"navigate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				entry, _ := args.(*delta.JsonObject).Value("entry").(float64)
				return call(ctx, func(d harness.Harness) (delta.JsonValue, error) {
					switched, err := navigate(ctx, d, durable.EntryId(entry))
					if err != nil {
						return nil, err
					}
					if err := reportTree(ctx, d); err != nil {
						return nil, err
					}
					return switched, nil
				})
			},
			"select": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				conversation, _ := args.(*delta.JsonObject).Value("conversation").(float64)
				return call(ctx, func(d harness.Harness) (delta.JsonValue, error) {
					if err := selectBranch(ctx, d, durable.ConversationId(conversation)); err != nil {
						return nil, err
					}
					return nil, reportTree(ctx, d)
				})
			},
			"rename": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := args.(*delta.JsonObject)
				conversation, _ := in.Value("conversation").(float64)
				label, _ := in.Value("label").(string)
				return call(ctx, func(d harness.Harness) (delta.JsonValue, error) {
					if err := rename(ctx, d, int(conversation), label); err != nil {
						return nil, err
					}
					return nil, reportTree(ctx, d)
				})
			},
		}}}, nil
	}), host.ServeOptions{Accepts: treeAccepts()})
})

// serveBranches is the object that makes earlier branches read-only and keeps the catalog's branches current.
func serveBranches(self host.StoreSelf) error {
	parts := p2runtime.Cached(p2runtime.HarnessOf(self))
	// Only the current main conversation takes input: an earlier branch shows its history, and a run it still has goes on in
	// the background (Stop stays allowed).
	p2runtime.ContributeKeyed(self, p2runtime.ReadOnlyConversations, p2runtime.Entry("tree", p2runtime.ReadOnlyCheck(
		func(ctx host.Ctx, conversation int) (string, error) {
			d, err := durableOf(parts)
			if err != nil {
				return "", err
			}
			// The check runs in its caller's section; the Durable reads need the loop.
			chordctx.Suspend(ctx)
			id := durable.ConversationId(conversation)
			main, err := p2runtime.MainConversation(ctx, d)
			if err != nil || id == main {
				return "", err
			}
			top, err := p2runtime.IsTopLevel(ctx, d, id)
			if err != nil || !top {
				return "", err
			}
			return notCurrent, nil
		})))
	// The catalog follows `p2.main` (the main conversation, the branches and their labels): the methods changing it push it
	// before they answer (`reportTree`); at start the worker pushes it once, which also covers a worker that stopped between a
	// change and its push. Nothing else changes `p2.main`.
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		// The Harness is read here, in the routine's section; the work waits for Durable off the loop.
		d, err := durableOf(parts)
		if err != nil {
			return host.Threw(err)
		}
		return host.AwaitingCtx(ctx, func() error {
			if err := fillLabels(ctx, d); err != nil {
				return err
			}
			return reportTree(ctx, d)
		})
	})
	return nil
}

// branchesOf are the branches of the stored state in creation order, with conversation 1 alone before the first change.
func branchesOf(state *p2runtime.MainState) []p2runtime.MainBranch {
	if state == nil {
		return []p2runtime.MainBranch{{Conversation: int(durable.ROOT_CONVERSATION_ID)}}
	}
	return state.Branches
}

func labelJSON(label *string) delta.JsonValue {
	if label == nil {
		return nil
	}
	return *label
}

// reportTree tells the catalog the current main conversation and the branches, newest first (it ignores an unchanged
// report).
func reportTree(ctx context.Context, d harness.Harness) error {
	state, err := durable.Snapshot(ctx, d, p2runtime.MainDoc)
	if err != nil {
		return err
	}
	main := int(durable.ROOT_CONVERSATION_ID)
	if state != nil {
		main = state.Conversation
	}
	branches := slices.Clone(branchesOf(state))
	slices.Reverse(branches)
	reported := make([]any, len(branches))
	for i, branch := range branches {
		reported[i] = delta.JsonObjectOf("conversation", float64(branch.Conversation), "label", labelJSON(branch.Label))
	}
	root := p2runtime.CtxOf(ctx)
	if root == nil {
		return errors.New("the tree reports from a Chord call")
	}
	sessions, err := root.Get(sessionscontract.Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	_, err = sessions.Call("treeSelf", delta.JsonObjectOf("main", float64(main), "branches", reported), root)
	return err
}

// fillLabels labels the branches `p2.main` version 1 listed without one (conversation 1 keeps null, the session title): the
// fork point's message, else the branch's first own user message. Runs at start; a branch `/tree` creates is labelled in the
// commit that creates it, so nothing is derived after this.
func fillLabels(ctx context.Context, d harness.Harness) error {
	state, err := durable.Snapshot(ctx, d, p2runtime.MainDoc)
	if err != nil {
		return err
	}
	unlabeled := map[durable.ConversationId]bool{}
	for _, branch := range branchesOf(state) {
		if branch.Label == nil && branch.Conversation != int(durable.ROOT_CONVERSATION_ID) {
			unlabeled[durable.ConversationId(branch.Conversation)] = true
		}
	}
	if len(unlabeled) == 0 {
		return nil
	}
	records, err := p2runtime.TopLevelRecords(ctx, d)
	if err != nil {
		return err
	}
	labels := map[int]string{}
	for _, record := range records {
		if !unlabeled[record.Id] {
			continue
		}
		label, found, err := forkLabel(ctx, d, record)
		if err != nil {
			return err
		}
		if found {
			labels[int(record.Id)] = label
		}
	}
	if len(labels) == 0 {
		return nil
	}
	_, err = d.Commit(ctx, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc(tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		branches := draft.Array("branches")
		for i := range branches.Len() {
			branch := branches.Object(i)
			if branch.Get("label") != nil {
				continue
			}
			conversation, _ := branch.Get("conversation").(float64)
			if label, ok := labels[int(conversation)]; ok {
				if err := branch.Set("label", label); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	})
	return err
}

// forkLabel is a version 1 branch's label: its fork point's message, else its first own user message; found is false
// without either.
func forkLabel(ctx context.Context, d harness.Harness, record durable.ConversationRecord) (label string, found bool, err error) {
	parent := record.Parent
	if parent != nil {
		at := parent.At
		point, err := d.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Entry(at) })
		if err != nil {
			return "", false, err
		}
		if entry, _ := point.(*durable.EntryRecord); entry != nil {
			if _, text, ok := previewOf(*entry); ok {
				return text, true, nil
			}
		}
	}
	conversation, err := d.Conversation(ctx, record.Id)
	if err != nil || conversation == nil {
		return "", false, err
	}
	first := durable.EntryId(1)
	if parent != nil {
		first = parent.At + 1
	}
	var cursor durable.Cursor
	for {
		ascending := durable.ScanAscending
		entries, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: record.Id, MinEntryId: &first, Order: &ascending}, page, cursor)
		if err != nil {
			return "", false, err
		}
		for _, entry := range entries.Items {
			if role, text, ok := previewOf(entry); ok && role == "user" {
				return text, true, nil
			}
		}
		if entries.Next == nil {
			return "", false, nil
		}
		cursor = *entries.Next
	}
}

// treeNode is a message of the tree; parent is 0 for the first message of a branch with no history before.
type treeNode struct {
	entry, parent, conversation int64
	role, text                  string
}

func nodeJSON(node treeNode) *delta.JsonObject {
	var parent delta.JsonValue
	if node.parent != 0 {
		parent = float64(node.parent)
	}
	return delta.JsonObjectOf("entry", float64(node.entry), "parent", parent, "role", node.role, "text", node.text, "conversation", float64(node.conversation))
}

// readTree is the messages of every top-level conversation, each hung below the node its branch continues from.
func readTree(ctx context.Context, d harness.Harness) (delta.JsonValue, error) {
	records, err := p2runtime.TopLevelRecords(ctx, d)
	if err != nil {
		return nil, err
	}
	parents := map[durable.ConversationId]*durable.ConversationParent{}
	for _, record := range records {
		parents[record.Id] = record.Parent
	}
	// own is each conversation's own nodes (entries after its fork point), in order.
	own := map[durable.ConversationId][]treeNode{}
	// lastOn is the last node at or before `at` on id's line: its own nodes, then its parent's up to the fork point (0:
	// none).
	var lastOn func(id durable.ConversationId, at int64) int64
	lastOn = func(id durable.ConversationId, at int64) int64 {
		nodes := own[id]
		for i := len(nodes) - 1; i >= 0; i-- {
			if nodes[i].entry <= at {
				return nodes[i].entry
			}
		}
		if parent := parents[id]; parent != nil {
			return lastOn(parent.ConversationId, min(at, int64(parent.At)))
		}
		return 0
	}
	// Ascending IDs: a fork is created after its parent, so the parent's nodes are known first.
	for _, record := range records {
		conversation, err := d.Conversation(ctx, record.Id)
		if err != nil {
			return nil, err
		}
		if conversation == nil {
			continue
		}
		var nodes []treeNode
		var previous int64
		first := durable.EntryId(1)
		if record.Parent != nil {
			previous = lastOn(record.Parent.ConversationId, int64(record.Parent.At))
			first = record.Parent.At + 1
		}
		var cursor durable.Cursor
		for {
			ascending := durable.ScanAscending
			entries, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: record.Id, MinEntryId: &first, Order: &ascending}, page, cursor)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries.Items {
				role, text, ok := previewOf(entry)
				if !ok {
					continue
				}
				nodes = append(nodes, treeNode{entry: int64(entry.Id), parent: previous, conversation: int64(record.Id), role: role, text: text})
				previous = int64(entry.Id)
			}
			if entries.Next == nil {
				break
			}
			cursor = *entries.Next
		}
		own[record.Id] = nodes
	}
	state, err := durable.Snapshot(ctx, d, p2runtime.MainDoc)
	if err != nil {
		return nil, err
	}
	main := durable.ROOT_CONVERSATION_ID
	if state != nil {
		main = durable.ConversationId(state.Conversation)
	}
	var all []treeNode
	for _, record := range records {
		all = append(all, own[record.Id]...)
	}
	slices.SortStableFunc(all, func(a, b treeNode) int { return int(a.entry - b.entry) })
	nodes := make([]any, len(all))
	for i, node := range all {
		nodes[i] = nodeJSON(node)
	}
	stored := branchesOf(state)
	branches := make([]any, len(stored))
	for i, branch := range stored {
		branches[i] = delta.JsonObjectOf("conversation", float64(branch.Conversation), "label", labelJSON(branch.Label))
	}
	var current delta.JsonValue
	if at := lastOn(main, 1<<53-1); at != 0 {
		current = float64(at)
	}
	return delta.JsonObjectOf("nodes", nodes, "branches", branches, "main", float64(main), "current", current), nil
}

// previewOf is a user message's or an assistant answer's text on one line, at most PreviewLength characters (a tree node,
// a branch label); not ok for every other entry and for messages without text.
func previewOf(entry durable.EntryRecord) (role, text string, ok bool) {
	role = roleOf(entry)
	if role == "" {
		return "", "", false
	}
	if role == "user" {
		text = oneLine(userText(entry))
	} else {
		text = oneLine(assistantText(entry))
	}
	return role, text, text != ""
}

// isJSSpace is what JavaScript's `\s` and `trim` take as white space.
func isJSSpace(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

// collapse is text with each run of white space one space, trimmed.
func collapse(text string) string { return strings.Join(strings.FieldsFunc(text, isJSSpace), " ") }

// oneLine is text on one line, trimmed, at most PreviewLength characters.
func oneLine(text string) string { return p2runtime.UTF16Slice(collapse(text), contract.PreviewLength) }

func userMessage(message ai.Message) (ai.UserMessage, bool) {
	switch typed := message.(type) {
	case ai.UserMessage:
		return typed, true
	case *ai.UserMessage:
		if typed != nil {
			return *typed, true
		}
	}
	return ai.UserMessage{}, false
}

func assistantMessage(message ai.Message) (ai.AssistantMessage, bool) {
	switch typed := message.(type) {
	case ai.AssistantMessage:
		return typed, true
	case *ai.AssistantMessage:
		if typed != nil {
			return *typed, true
		}
	}
	return ai.AssistantMessage{}, false
}

// roleOf is "assistant" for an answer and "user" for a message a person wrote; "" for every other entry.
func roleOf(entry durable.EntryRecord) string {
	if entry.Kind == "pi.assistant" {
		return "assistant"
	}
	// Head markers (resets, compaction summaries) carry user messages that no one typed.
	if len(entry.Model) > 0 && entry.Head == nil {
		if _, ok := userMessage(entry.Model[0]); ok {
			return "user"
		}
	}
	return ""
}

// userText is what a person, a peer session or a subagent wrote (structured input data), else the user message's text.
func userText(entry durable.EntryRecord) string {
	if message, ok := p2runtime.MessageDataOf(entry.Data); ok {
		return message.Text
	}
	if message, ok := p2runtime.PeerMessageOf(entry.Kind, entry.Data); ok {
		return message.Text
	}
	if message, ok := p2runtime.AgentMessageOf(entry.Kind, entry.Data); ok {
		return message.Text
	}
	if len(entry.Model) == 0 {
		return ""
	}
	user, _ := userMessage(entry.Model[0])
	switch content := user.Content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		var parts []string
		for _, block := range content {
			if text, ok := block.(ai.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func assistantText(entry durable.EntryRecord) string {
	if len(entry.Model) == 0 {
		return ""
	}
	message, _ := assistantMessage(entry.Model[0])
	var parts []string
	for _, block := range message.Content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

var ownerless = durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}

// navigate switches the main conversation so the session continues at `entry`: after an answer, before a user message
// (whose text comes back). A top-level conversation already ending there is reused, its label unchanged; else a fork of the
// conversation storing the point, or a new empty conversation before the first message, labelled with the chosen message
// (what the owner picked: the answer, or the user message typed again). The new conversation, its label, the carried
// documents and the main pointer are one commit. A run of the conversation left keeps going in the background, read-only.
func navigate(ctx context.Context, d harness.Harness, entry durable.EntryId) (delta.JsonValue, error) {
	main, err := p2runtime.MainConversation(ctx, d)
	if err != nil {
		return nil, err
	}
	found, err := d.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Entry(entry) })
	if err != nil {
		return nil, err
	}
	chosen, _ := found.(*durable.EntryRecord)
	if chosen == nil {
		return nil, errors.New("Unknown message")
	}
	top, err := p2runtime.IsTopLevel(ctx, d, chosen.ConversationId)
	if err != nil {
		return nil, err
	}
	if !top {
		return nil, errors.New("Unknown message")
	}
	role := roleOf(*chosen)
	if role == "" {
		return nil, errors.New("Only messages can be chosen")
	}
	storing, err := d.Conversation(ctx, chosen.ConversationId)
	if err != nil {
		return nil, err
	}
	if storing == nil {
		return nil, errors.New("Unknown message")
	}
	// A user message is typed again: the session continues before it.
	point := chosen
	var text delta.JsonValue
	if role == "user" {
		point = nil
		before := entry - 1
		previous, err := storing.Entries(ctx, durable.EntryQuery{ConversationId: chosen.ConversationId, MaxEntryId: &before}, 1, nil)
		if err != nil {
			return nil, err
		}
		if len(previous.Items) > 0 {
			point = &previous.Items[0]
		}
		text = userText(*chosen)
	}
	var pointID durable.EntryId
	if point != nil {
		pointID = point.Id
	}
	target, err := branchEndingAt(ctx, d, main, pointID)
	if err != nil {
		return nil, err
	}
	if target == main {
		return delta.JsonObjectOf("conversation", float64(main), "text", text), nil
	}
	// Before the first message: a new conversation with the agent as it was then (a fork needs an entry to start at).
	var agent *delta.JsonObject
	if target == 0 && point == nil {
		agent, err = d.SnapshotAsOfErased(ctx, harness.AgentDoc, entry, chosen.ConversationId)
		if err != nil {
			return nil, err
		}
	}
	label := delta.JsonValue(nil)
	if _, preview, ok := previewOf(*chosen); ok {
		label = preview
	}
	switched, err := d.Commit(ctx, func(tx durable.Tx) (any, error) {
		to := target
		switch {
		case to == 0 && point != nil:
			forked, err := tx.ForkConversation(point.ConversationId, point.Id, ownerless)
			if err != nil {
				return nil, err
			}
			to = forked.Id
		case to == 0:
			created, err := tx.CreateConversation(ownerless)
			if err != nil {
				return nil, err
			}
			to = created.Id
			draft, err := durable.TxDoc(tx, harness.AgentDoc, to)
			if err != nil {
				return nil, err
			}
			if agent != nil {
				for key, value := range agent.All() {
					if err := draft.Set(key, value); err != nil {
						return nil, err
					}
				}
			}
		}
		state, err := durable.TxDoc(tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		if err := state.Set("conversation", float64(to)); err != nil {
			return nil, err
		}
		branches := state.Array("branches")
		for i := range branches.Len() {
			if conversation, _ := branches.Object(i).Get("conversation").(float64); durable.ConversationId(conversation) == to {
				return to, nil
			}
		}
		_, err = branches.Push(delta.JsonObjectOf("conversation", float64(to), "label", label))
		return to, err
	})
	if err != nil {
		return nil, err
	}
	return delta.JsonObjectOf("conversation", float64(switched.(durable.ConversationId)), "text", text), nil
}

// selectBranch switches the main conversation to the top-level conversation id (its latest message); a run there goes on.
func selectBranch(ctx context.Context, d harness.Harness, id durable.ConversationId) error {
	top, err := p2runtime.IsTopLevel(ctx, d, id)
	if err != nil {
		return err
	}
	if !top {
		return errors.New("Unknown branch")
	}
	main, err := p2runtime.MainConversation(ctx, d)
	if err != nil || id == main {
		return err
	}
	_, err = d.Commit(ctx, func(tx durable.Tx) (any, error) {
		state, err := durable.TxDoc(tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		return nil, state.Set("conversation", float64(id))
	})
	return err
}

// rename labels the branch id: one line, 1 to PreviewLength characters. An empty label resets conversation 1 to the session
// title; another branch needs a label (it has a fork point to be named after).
func rename(ctx context.Context, d harness.Harness, id int, label string) error {
	// Schema-checked arguments: the length and the empty rule are this method's own.
	text := collapse(label)
	if p2runtime.UTF16Length(text) > contract.PreviewLength {
		return fmt.Errorf("A branch label has at most %d characters", contract.PreviewLength)
	}
	if text == "" && id != int(durable.ROOT_CONVERSATION_ID) {
		return errors.New("A branch label needs at least one character")
	}
	_, err := d.Commit(ctx, func(tx durable.Tx) (any, error) {
		state, err := durable.TxDoc(tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		branches := state.Array("branches")
		for i := range branches.Len() {
			branch := branches.Object(i)
			if conversation, _ := branch.Get("conversation").(float64); int(conversation) != id {
				continue
			}
			if text == "" {
				return nil, branch.Set("label", nil)
			}
			return nil, branch.Set("label", text)
		}
		return nil, errors.New("Unknown branch")
	})
	return err
}

// branchEndingAt is a top-level conversation whose history ends at point (0: has no entries), the main conversation
// first; 0 when there is none.
func branchEndingAt(ctx context.Context, d harness.Harness, main durable.ConversationId, point durable.EntryId) (durable.ConversationId, error) {
	branches, err := p2runtime.TopLevelConversations(ctx, d)
	if err != nil {
		return 0, err
	}
	order := []durable.ConversationId{main}
	for _, id := range branches {
		if id != main {
			order = append(order, id)
		}
	}
	for _, id := range order {
		conversation, err := d.Conversation(ctx, id)
		if err != nil {
			return 0, err
		}
		var tail durable.EntryId
		if conversation != nil {
			entries, err := conversation.Entries(ctx, durable.EntryQuery{ConversationId: id}, 1, nil)
			if err != nil {
				return 0, err
			}
			if len(entries.Items) > 0 {
				tail = entries.Items[0].Id
			}
		}
		if tail == point {
			return id, nil
		}
	}
	return 0, nil
}
