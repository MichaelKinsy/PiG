// Package tree is p2's tree plugin (the worker facet of apps/p2/plugins/tree/worker.ts): `/tree` in the session worker
// (docs/tree.md), the tree of the top level conversations' messages, switching the current main conversation (forking
// a new branch where none ends at the chosen point), and the rules that come with it: older branches are read-only,
// branches carry stored labels, and the session catalog follows `p2.main`, pushed by the worker inside the method that
// changed it.
package tree

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

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

const notCurrent = "This is an earlier branch of the session: switch to it with /tree to continue it"

// page is the entries per page when walking a conversation.
const page = 500

var ownerless = durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}

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

// Worker serves `SessionTree` (the owner's) and contributes the read-only rule of earlier branches.
var Worker = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := p2runtime.Contribute(facet, "p2.tree-branches", serveBranches); err != nil {
		return err
	}
	return facet.Serve(contract.SessionTree, host.RemoteFactory(serveTree), host.ServeOptions{Accepts: treeAccepts()})
})

func openDurable(parts func() (*p2runtime.HarnessParts, error)) (harness.Harness, error) {
	held, err := parts()
	if err != nil {
		return nil, err
	}
	return held.Durable()
}

// serveBranches makes every top level conversation but the current main one read-only (an earlier branch shows its
// history, and a run it still has goes on in the background; Stop stays allowed), and has the catalog follow `p2.main`:
// the methods changing it push it before they answer (`reportTree`); at start the worker pushes it once, which also
// covers a worker that stopped between a change and its push. Nothing else changes `p2.main`.
func serveBranches(self host.StoreSelf) error {
	parts := p2runtime.HarnessOf(self)
	sessions := self.Get(sessionscontract.Sessions, delta.NewJsonObject(0))
	p2runtime.ContributeKeyed(self, p2runtime.ReadOnlyConversations, p2runtime.Entry("tree", p2runtime.ReadOnlyCheck(
		func(ctx host.Ctx, conversation int) (string, error) {
			opened, err := openDurable(parts)
			if err != nil {
				return "", err
			}
			id := durable.ConversationId(conversation)
			main, err := p2runtime.MainConversation(ctx, opened)
			if err != nil || id == main {
				return "", err
			}
			top, err := p2runtime.IsTopLevel(ctx, opened, id)
			if err != nil || !top {
				return "", err
			}
			return notCurrent, nil
		})))
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			opened, err := openDurable(parts)
			if err != nil {
				return err
			}
			if err := fillLabels(ctx, opened); err != nil {
				return err
			}
			return reportTree(ctx, sessions, opened)
		})
	})
	return nil
}

func serveTree(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.IsOwner(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("the tree is the owner's")
	}
	parts, worker := p2runtime.HarnessOf(self), p2runtime.WorkerOf(self)
	sessions := self.Get(sessionscontract.Sessions, delta.NewJsonObject(0))
	// run is a call of the owner: refused while the session moves, which waits for running calls (`Liveness.Call`). It
	// waits for the Durable session, which the caller's section must not hold.
	run := func(ctx context.Context, fn func(opened harness.Harness) (delta.JsonValue, error)) (delta.JsonValue, error) {
		chordctx.Suspend(ctx)
		held, err := worker()
		if err != nil {
			return nil, err
		}
		var result delta.JsonValue
		err = held.Liveness.Call(func() error {
			opened, err := openDurable(parts)
			if err != nil {
				return err
			}
			result, err = fn(opened)
			return err
		})
		return result, err
	}
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
		"tree": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return run(ctx, func(opened harness.Harness) (delta.JsonValue, error) { return readTree(ctx, opened) })
		},
		"navigate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			entry, _ := args.(*delta.JsonObject).Value("entry").(float64)
			return run(ctx, func(opened harness.Harness) (delta.JsonValue, error) {
				switched, err := navigate(ctx, opened, durable.EntryId(entry))
				if err != nil {
					return nil, err
				}
				return switched, reportTree(ctx, sessions, opened)
			})
		},
		"select": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			conversation, _ := args.(*delta.JsonObject).Value("conversation").(float64)
			return run(ctx, func(opened harness.Harness) (delta.JsonValue, error) {
				if err := selectBranch(ctx, opened, durable.ConversationId(conversation)); err != nil {
					return nil, err
				}
				return nil, reportTree(ctx, sessions, opened)
			})
		},
		"rename": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			object := args.(*delta.JsonObject)
			conversation, _ := object.Value("conversation").(float64)
			label, _ := object.Value("label").(string)
			return run(ctx, func(opened harness.Harness) (delta.JsonValue, error) {
				if err := rename(ctx, opened, int(conversation), label); err != nil {
					return nil, err
				}
				return nil, reportTree(ctx, sessions, opened)
			})
		},
	}}}, nil
}

// reportTree tells the catalog the current main conversation and the branches, newest first (it ignores an unchanged
// report).
func reportTree(ctx context.Context, sessions *host.Reference, opened harness.Harness) error {
	state, err := durable.Snapshot(ctx, opened, p2runtime.MainDoc)
	if err != nil {
		return err
	}
	main, branches := int(durable.ROOT_CONVERSATION_ID), []p2runtime.MainBranch{{Conversation: int(durable.ROOT_CONVERSATION_ID)}}
	if state != nil {
		main, branches = state.Conversation, state.Branches
	}
	newestFirst := make([]any, len(branches))
	for i, branch := range branches {
		newestFirst[len(branches)-1-i] = branchValue(branch)
	}
	_, err = sessions.Call("treeSelf", delta.JsonObjectOf("main", float64(main), "branches", newestFirst), p2runtime.CtxOf(ctx))
	return err
}

func branchValue(branch p2runtime.MainBranch) *delta.JsonObject {
	if branch.Label == nil {
		return delta.JsonObjectOf("conversation", float64(branch.Conversation), "label", nil)
	}
	return delta.JsonObjectOf("conversation", float64(branch.Conversation), "label", *branch.Label)
}

// fillLabels labels the branches `p2.main` version 1 listed without one (conversation 1 keeps null, the session title):
// the fork point's message, else the branch's first own user message. Runs at start; a branch `/tree` creates is labelled
// in the commit that creates it, so nothing is derived after this.
func fillLabels(ctx context.Context, opened harness.Harness) error {
	state, err := durable.Snapshot(ctx, opened, p2runtime.MainDoc)
	if err != nil || state == nil {
		return err
	}
	unlabeled := map[int]bool{}
	for _, branch := range state.Branches {
		if branch.Label == nil && branch.Conversation != int(durable.ROOT_CONVERSATION_ID) {
			unlabeled[branch.Conversation] = true
		}
	}
	if len(unlabeled) == 0 {
		return nil
	}
	labels := map[int]string{}
	records, err := p2runtime.TopLevelRecords(ctx, opened)
	if err != nil {
		return err
	}
	for _, record := range records {
		if !unlabeled[int(record.Id)] {
			continue
		}
		label, found, err := forkLabel(ctx, opened, record)
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
	_, err = opened.Commit(ctx, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc[p2runtime.MainState](tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		branches := draft.Array("branches")
		for i := 0; i < branches.Len(); i++ {
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

// forkLabel is a version 1 branch's label: its fork point's message, else its first own user message; none without either.
func forkLabel(ctx context.Context, opened harness.Harness, record durable.ConversationRecord) (string, bool, error) {
	if parent := record.Parent; parent != nil {
		point, err := entryOf(ctx, opened, parent.At)
		if err != nil {
			return "", false, err
		}
		if point != nil {
			if _, text, ok := previewOf(*point); ok {
				return text, true, nil
			}
		}
	}
	conversation, err := opened.Conversation(ctx, record.Id)
	if err != nil || conversation == nil {
		return "", false, err
	}
	var after durable.EntryId = 1
	if record.Parent != nil {
		after = record.Parent.At + 1
	}
	order := durable.ScanAscending
	var cursor durable.Cursor
	for {
		entries, err := conversation.Entries(ctx, durable.EntryQuery{MinEntryId: &after, Order: &order}, page, cursor)
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

func entryOf(ctx context.Context, opened harness.Harness, id durable.EntryId) (*durable.EntryRecord, error) {
	found, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Entry(id) })
	if err != nil {
		return nil, err
	}
	entry, _ := found.(*durable.EntryRecord)
	return entry, nil
}

// readTree is the messages of every top level conversation, each hung below the node its branch continues from.
func readTree(ctx context.Context, opened harness.Harness) (delta.JsonValue, error) {
	records, err := p2runtime.TopLevelRecords(ctx, opened)
	if err != nil {
		return nil, err
	}
	parents := map[durable.ConversationId]*durable.ConversationParent{}
	for _, record := range records {
		parents[record.Id] = record.Parent
	}
	// Each conversation's own nodes (entries after its fork point), in order.
	own := map[durable.ConversationId][]contract.TreeNode{}
	var lastOn func(id durable.ConversationId, at durable.EntryId) *int64
	lastOn = func(id durable.ConversationId, at durable.EntryId) *int64 {
		nodes := own[id]
		for i := len(nodes) - 1; i >= 0; i-- {
			if durable.EntryId(nodes[i].Entry) <= at {
				entry := nodes[i].Entry
				return &entry
			}
		}
		if parent := parents[id]; parent != nil {
			return lastOn(parent.ConversationId, min(at, parent.At))
		}
		return nil
	}
	// Ascending IDs: a fork is created after its parent, so the parent's nodes are known first.
	order := durable.ScanAscending
	for _, record := range records {
		conversation, err := opened.Conversation(ctx, record.Id)
		if err != nil {
			return nil, err
		}
		if conversation == nil {
			continue
		}
		var previous *int64
		var after durable.EntryId = 1
		if record.Parent != nil {
			previous = lastOn(record.Parent.ConversationId, record.Parent.At)
			after = record.Parent.At + 1
		}
		nodes := []contract.TreeNode{}
		var cursor durable.Cursor
		for {
			entries, err := conversation.Entries(ctx, durable.EntryQuery{MinEntryId: &after, Order: &order}, page, cursor)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries.Items {
				role, text, ok := previewOf(entry)
				if !ok {
					continue
				}
				id := int64(entry.Id)
				nodes = append(nodes, contract.TreeNode{Entry: id, Parent: previous, Role: role, Text: text, Conversation: int64(record.Id)})
				previous = &id
			}
			if entries.Next == nil {
				break
			}
			cursor = *entries.Next
		}
		own[record.Id] = nodes
	}
	state, err := durable.Snapshot(ctx, opened, p2runtime.MainDoc)
	if err != nil {
		return nil, err
	}
	main, branches := durable.ROOT_CONVERSATION_ID, []p2runtime.MainBranch{{Conversation: int(durable.ROOT_CONVERSATION_ID)}}
	if state != nil {
		main, branches = durable.ConversationId(state.Conversation), state.Branches
	}
	var all []contract.TreeNode
	for _, nodes := range own {
		all = append(all, nodes...)
	}
	slices.SortFunc(all, func(a, b contract.TreeNode) int { return int(a.Entry - b.Entry) })
	nodes := make([]any, len(all))
	for i, node := range all {
		nodes[i] = nodeValue(node)
	}
	branchValues := make([]any, len(branches))
	for i, branch := range branches {
		branchValues[i] = branchValue(branch)
	}
	var current any
	if last := lastOn(main, durable.EntryId(1<<53-1)); last != nil {
		current = float64(*last)
	}
	return delta.JsonObjectOf("nodes", nodes, "branches", branchValues, "main", float64(main), "current", current), nil
}

func nodeValue(node contract.TreeNode) *delta.JsonObject {
	var parent any
	if node.Parent != nil {
		parent = float64(*node.Parent)
	}
	return delta.JsonObjectOf("entry", float64(node.Entry), "parent", parent, "role", node.Role, "text", node.Text, "conversation", float64(node.Conversation))
}

// previewOf is a user message's or an assistant answer's text on one line, at most PreviewLength characters (a tree node,
// a branch label); false for every other entry and for messages without text.
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
	if text == "" {
		return "", "", false
	}
	return role, text, true
}

// jsSpace is what JavaScript's `\s` and `trim` take as white space.
func jsSpace(r rune) bool {
	return unicode.Is(unicode.Zs, r) || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == '\u2028' || r == '\u2029' || r == '\uFEFF'
}

// oneLine is text on one line, trimmed, at most PreviewLength UTF-16 code units (a surrogate pair the limit falls inside
// is cut in two, which a string of ours holds as U+FFFD).
func oneLine(text string) string {
	return utf16Slice(strings.Join(strings.FieldsFunc(text, jsSpace), " "), contract.PreviewLength)
}

func utf16Slice(s string, n int) string {
	units := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			if units < n {
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += width
	}
	return s
}

func roleOf(entry durable.EntryRecord) string {
	if entry.Kind == "pi.assistant" {
		return "assistant"
	}
	// Head markers (resets, compaction summaries) carry user messages that no one typed.
	if len(entry.Model) > 0 && entry.Model[0].MessageRole() == "user" && entry.Head == nil {
		return "user"
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
	var content ai.UserContent
	switch message := entry.Model[0].(type) {
	case ai.UserMessage:
		content = message.Content
	case *ai.UserMessage:
		content = message.Content
	}
	switch content := content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		var texts []string
		for _, block := range content {
			switch part := block.(type) {
			case ai.TextContent:
				texts = append(texts, part.Text)
			case *ai.TextContent:
				texts = append(texts, part.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func assistantText(entry durable.EntryRecord) string {
	var content []ai.AssistantContentBlock
	if len(entry.Model) > 0 {
		switch message := entry.Model[0].(type) {
		case ai.AssistantMessage:
			content = message.Content
		case *ai.AssistantMessage:
			content = message.Content
		}
	}
	var texts []string
	for _, block := range content {
		switch part := block.(type) {
		case ai.TextContent:
			texts = append(texts, part.Text)
		case *ai.TextContent:
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// navigate switches the main conversation so the session continues at `entry`: after an answer, before a user message
// (whose text comes back). A top level conversation already ending there is reused, its label unchanged; else a fork of
// the conversation storing the point, or a new empty conversation before the first message, labelled with the chosen
// message (what the owner picked: the answer, or the user message typed again). The new conversation, its label, the
// carried documents and the main pointer are one commit. A run of the conversation left keeps going in the background,
// read-only.
func navigate(ctx context.Context, opened harness.Harness, entry durable.EntryId) (delta.JsonValue, error) {
	main, err := p2runtime.MainConversation(ctx, opened)
	if err != nil {
		return nil, err
	}
	chosen, err := entryOf(ctx, opened, entry)
	if err != nil {
		return nil, err
	}
	if chosen == nil {
		return nil, errors.New("Unknown message")
	}
	if top, err := p2runtime.IsTopLevel(ctx, opened, chosen.ConversationId); err != nil || !top {
		return nil, firstError(err, errors.New("Unknown message"))
	}
	role := roleOf(*chosen)
	if role == "" {
		return nil, errors.New("Only messages can be chosen")
	}
	storing, err := opened.Conversation(ctx, chosen.ConversationId)
	if err != nil {
		return nil, err
	}
	if storing == nil {
		return nil, errors.New("Unknown message")
	}
	// A user message is typed again: the session continues before it.
	point := chosen
	var text any
	if role == "user" {
		before := entry - 1
		entries, err := storing.Entries(ctx, durable.EntryQuery{MaxEntryId: &before}, 1, nil)
		if err != nil {
			return nil, err
		}
		point = nil
		if len(entries.Items) > 0 {
			point = &entries.Items[0]
		}
		text = userText(*chosen)
	}
	var pointID *durable.EntryId
	if point != nil {
		pointID = &point.Id
	}
	target, err := branchEndingAt(ctx, opened, main, pointID)
	if err != nil {
		return nil, err
	}
	if target != nil && *target == main {
		return switched(main, text), nil
	}
	// Before the first message: a new conversation with the agent as it was then (a fork needs an entry to start at).
	var agent delta.JsonValue
	if target == nil && point == nil {
		state, err := durable.SnapshotAsOf(ctx, opened, harness.AgentDoc, entry, chosen.ConversationId)
		if err != nil {
			return nil, err
		}
		if state != nil {
			if agent, err = durable.ToJsonValue(state); err != nil {
				return nil, err
			}
		}
	}
	var label any
	if _, preview, ok := previewOf(*chosen); ok {
		label = preview
	}
	created, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
		to := target
		switch {
		case to != nil:
		case point != nil:
			record, err := tx.ForkConversation(point.ConversationId, point.Id, ownerless)
			if err != nil {
				return nil, err
			}
			to = &record.Id
		default:
			record, err := tx.CreateConversation(ownerless)
			if err != nil {
				return nil, err
			}
			to = &record.Id
			if values, ok := agent.(*delta.JsonObject); ok {
				draft, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, record.Id)
				if err != nil {
					return nil, err
				}
				for key, value := range values.All() {
					if err := draft.Set(key, value); err != nil {
						return nil, err
					}
				}
			}
		}
		state, err := durable.TxDoc[p2runtime.MainState](tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		if err := state.Set("conversation", float64(*to)); err != nil {
			return nil, err
		}
		branches := state.Array("branches")
		for i := 0; i < branches.Len(); i++ {
			if conversation, _ := branches.Object(i).Get("conversation").(float64); durable.ConversationId(conversation) == *to {
				return *to, nil
			}
		}
		_, err = branches.Push(delta.JsonObjectOf("conversation", float64(*to), "label", label))
		return *to, err
	})
	if err != nil {
		return nil, err
	}
	return switched(created.(durable.ConversationId), text), nil
}

func switched(conversation durable.ConversationId, text any) delta.JsonValue {
	return delta.JsonObjectOf("conversation", float64(conversation), "text", text)
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// selectBranch switches the main conversation to the top level conversation `id` (its latest message); a run there goes on.
func selectBranch(ctx context.Context, opened harness.Harness, id durable.ConversationId) error {
	top, err := p2runtime.IsTopLevel(ctx, opened, id)
	if err != nil {
		return err
	}
	if !top {
		return errors.New("Unknown branch")
	}
	main, err := p2runtime.MainConversation(ctx, opened)
	if err != nil || id == main {
		return err
	}
	_, err = opened.Commit(ctx, func(tx durable.Tx) (any, error) {
		state, err := durable.TxDoc[p2runtime.MainState](tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		return nil, state.Set("conversation", float64(id))
	})
	return err
}

// rename labels the branch `id`: one line, 1 to PreviewLength characters. Null or empty resets conversation 1 to the
// session title; another branch needs a label (it has a fork point to be named after).
func rename(ctx context.Context, opened harness.Harness, id int, label string) error {
	// Schema-checked arguments: the length and the null rule are this method's own.
	text := strings.Join(strings.FieldsFunc(label, jsSpace), " ")
	if len(utf16Units(text)) > contract.PreviewLength {
		return errors.New("A branch label has at most 200 characters")
	}
	if text == "" && id != int(durable.ROOT_CONVERSATION_ID) {
		return errors.New("A branch label needs at least one character")
	}
	_, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
		state, err := durable.TxDoc[p2runtime.MainState](tx, p2runtime.MainDoc)
		if err != nil {
			return nil, err
		}
		branches := state.Array("branches")
		for i := 0; i < branches.Len(); i++ {
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

func utf16Units(text string) []uint16 { return utf16.Encode([]rune(text)) }

// branchEndingAt is a top level conversation whose history ends at `point` (nil: has no entries), the main conversation
// first; nil when none does.
func branchEndingAt(ctx context.Context, opened harness.Harness, main durable.ConversationId, point *durable.EntryId) (*durable.ConversationId, error) {
	branches, err := p2runtime.TopLevelConversations(ctx, opened)
	if err != nil {
		return nil, err
	}
	candidates := []durable.ConversationId{main}
	for _, id := range branches {
		if id != main {
			candidates = append(candidates, id)
		}
	}
	for _, id := range candidates {
		conversation, err := opened.Conversation(ctx, id)
		if err != nil {
			return nil, err
		}
		if conversation == nil {
			continue
		}
		entries, err := conversation.Entries(ctx, durable.EntryQuery{}, 1, nil)
		if err != nil {
			return nil, err
		}
		var tail *durable.EntryId
		if len(entries.Items) > 0 {
			tail = &entries.Items[0].Id
		}
		if (tail == nil) == (point == nil) && (tail == nil || *tail == *point) {
			found := id
			return &found, nil
		}
	}
	return nil, nil
}
