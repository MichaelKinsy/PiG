package codingagent

import (
	"encoding/binary"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// treeFacts are what the /tree selector reads of an assistant message for every row before it formats any: whether the row hides when it is
// not the current leaf (tree-selector.ts applyFilter: no text and a stop reason other than an error or abort) and the ids of its tool calls,
// which name the calls a tool result row shows (tree-selector.ts:255-264). A Session keeps them once read, so a selector over a long
// history opens without decoding the messages it does not show.
type treeFacts struct {
	suppressible bool
	callIDs      []string
}

// treeFactsOf reads the facts of a decoded message entry as the formatter does: a message that does not decode hides nothing and calls
// nothing.
func treeFactsOf(me MessageEntry, ok bool) *treeFacts {
	facts := &treeFacts{}
	if !ok || me.Message.Assistant == nil {
		return facts
	}
	if r := me.Message.Assistant.StopReason; r == "" || r == "stop" || r == "toolUse" {
		facts.suppressible = jsTrim(extractMessageText(me)) == ""
	}
	for _, block := range me.Message.ContentBlocks() {
		if call, isCall := block.(ai.ToolCall); isCall && call.ID != "" {
			facts.callIDs = append(facts.callIDs, call.ID)
		}
	}
	return facts
}

// treeFactsOfLine reads the facts of an assistant message line without decoding its message: text blocks and tool_result wrappers give the
// text, toolCall and tool_use blocks the call ids, as the message decoder maps them (agent/message_json.go unmarshalAgentContent).
func treeFactsOfLine(line []byte) *treeFacts {
	if facts, ok := scanTreeFacts(line); ok {
		return facts
	}
	return decodeTreeFacts(line)
}

// decodeTreeFacts is treeFactsOfLine by decoding the line.
func decodeTreeFacts(line []byte) *treeFacts {
	var probe struct {
		Message struct {
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
			Content    []struct {
				Type    string          `json:"type"`
				ID      string          `json:"id"`
				Text    string          `json:"text"`
				Content json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"message"`
	}
	facts := &treeFacts{}
	if json.Unmarshal(line, &probe) != nil || probe.Message.Role != "assistant" {
		return facts
	}
	var text strings.Builder
	for _, block := range probe.Message.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_result":
			var content string
			if json.Unmarshal(block.Content, &content) == nil {
				text.WriteString(content)
			}
		case "toolCall", "tool_use":
			if block.ID != "" {
				facts.callIDs = append(facts.callIDs, block.ID)
			}
		}
	}
	if r := probe.Message.StopReason; r == "" || r == "stop" || r == "toolUse" {
		facts.suppressible = jsTrim(text.String()) == ""
	}
	return facts
}

// treeFactsLocked reads the facts of every assistant message that has none yet, the ones not resident from their lines on several
// goroutines. The caller holds s.mu for writing.
func (s *Session) treeFactsLocked() {
	var missing []int
	for i := range s.slots {
		slot := &s.slots[i]
		if slot.facts != 0 || slot.typ() != typeMessage || slot.role() != "assistant" {
			continue
		}
		if slot.entry != nil {
			slot.facts = s.storeFactsLocked(treeFactsOf(s.messageFor(slot.entry)))
			continue
		}
		missing = append(missing, i)
	}
	if len(missing) == 0 {
		return
	}
	file, opened := s.openRecordsLocked()
	if opened != nil {
		defer func() { _ = opened.Close() }()
	}
	// The goroutines read the facts into found by position; the arena takes them in order once all are read.
	found := make([]*treeFacts, len(s.slots))
	read := func(part []int) {
		reader := recordReader{file: file, blocks: s.blockList}
		for _, i := range part {
			facts := &treeFacts{}
			if record, ok := reader.record(s.coldPart(&s.slots[i])); ok {
				facts = treeFactsOfLine(record)
			}
			found[i] = facts
		}
	}
	parallelOver(missing, read)
	for _, i := range missing {
		s.slots[i].facts = s.storeFactsLocked(found[i])
	}
}

// storeFactsLocked appends facts to the session's facts arena and returns their reference: a flags byte (suppressible), the number of call
// ids and each id after its length, all as uvarints. An arena holds the facts of a long history in about a dozen bytes per assistant
// message, where a treeFacts and its slice of ids took about sixty. The caller holds s.mu for writing.
func (s *Session) storeFactsLocked(facts *treeFacts) uint32 {
	if len(s.factArena) >= math.MaxUint32-1 {
		return 0
	}
	ref := uint32(len(s.factArena)) + 1
	var flags byte
	if facts.suppressible {
		flags = 1
	}
	s.factArena = append(s.factArena, flags)
	s.factArena = binary.AppendUvarint(s.factArena, uint64(len(facts.callIDs)))
	for _, id := range facts.callIDs {
		s.factArena = binary.AppendUvarint(s.factArena, uint64(len(id)))
		s.factArena = append(s.factArena, id...)
	}
	return ref
}

// factsLocked reads the facts at ref, nil for 0. The caller holds s.mu.
func (s *Session) factsLocked(ref uint32) *treeFacts {
	if ref == 0 {
		return nil
	}
	data := s.factArena[ref-1:]
	facts := &treeFacts{suppressible: data[0]&1 != 0}
	n, k := binary.Uvarint(data[1:])
	data = data[1+k:]
	for range n {
		size, k := binary.Uvarint(data)
		facts.callIDs = append(facts.callIDs, string(data[k:k+int(size)]))
		data = data[k+int(size):]
	}
	return facts
}

// parallelOver runs work over contiguous parts of items on up to GOMAXPROCS goroutines, each part at least parallelReadMinLines long, and
// returns when all have finished. A run of more than one part counts the runtime default GOMAXPROCS, not the policy's cap (procpolicy).
func parallelOver(items []int, work func(part []int)) {
	if len(items)/parallelReadMinLines <= 1 {
		work(items)
		return
	}
	// pig additive (D174): the parts run at the runtime default width.
	defer procpolicy.Widen()()
	workers := min(runtime.GOMAXPROCS(0), len(items)/parallelReadMinLines)
	if workers <= 1 {
		work(items)
		return
	}
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() { work(items[w*len(items)/workers : (w+1)*len(items)/workers]) })
	}
	wg.Wait()
}

// lazyTree is a session tree for the /tree selector whose rows carry their entries only once a row needs one: an entry that is not resident
// is a RawEntry of its base members until the row is formatted, copied or searched.
type lazyTree struct {
	s     *Session
	nodes map[*SessionTreeNode]*lazyTreeNode
	// callSlots maps a tool call id to the assistant message slot that holds the call, the last in tree order as Pi's toolCallMap is.
	callSlots map[string]int
	searched  bool
}

type lazyTreeNode struct {
	slot   int
	role   string
	facts  *treeFacts
	loaded bool
	search string
}

// LazyTree builds the tree GetTree builds, with the same nodes, labels, roots and child order, reading no entry that is not resident.
func (s *Session) lazyTree() ([]*SessionTreeNode, *lazyTree) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.treeFactsLocked()
	lt := &lazyTree{s: s, nodes: make(map[*SessionTreeNode]*lazyTreeNode, len(s.slots)), callSlots: map[string]int{}}
	nodes := make(map[string]*SessionTreeNode, len(s.slots))
	for i := range s.slots {
		slot := &s.slots[i]
		entry := slot.entry
		loaded := entry != nil
		if !loaded {
			entry = RawEntry{SessionEntryBase: SessionEntryBase{Type: slot.typ(), ID: slot.id, ParentID: s.parentPtrLocked(i)}}
		}
		n := &SessionTreeNode{Entry: entry}
		nodes[slot.id] = n
		lt.nodes[n] = &lazyTreeNode{slot: i, role: slot.role(), facts: s.factsLocked(slot.facts), loaded: loaded}
	}
	for id, label := range s.labels {
		if n, ok := nodes[id]; ok {
			n.Label, n.LabelTimestamp = label.label, label.timestamp
		}
	}
	var roots []*SessionTreeNode
	for i := range s.slots {
		slot := &s.slots[i]
		n := nodes[slot.id]
		parentID, has := s.parentIDLocked(i)
		if !has || parentID == slot.id {
			roots = append(roots, n)
			continue
		}
		parent, ok := nodes[parentID]
		if !ok {
			roots = append(roots, n)
			continue
		}
		parent.Children = append(parent.Children, n)
	}
	stack := slices.Clone(roots)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		slices.SortStableFunc(n.Children, func(a, b *SessionTreeNode) int {
			return cmpDates(s.slots[lt.nodes[a].slot].ts, s.slots[lt.nodes[b].slot].ts)
		})
		stack = append(stack, n.Children...)
	}
	var walk func([]*SessionTreeNode)
	walk = func(level []*SessionTreeNode) {
		for _, n := range level {
			if facts := lt.nodes[n].facts; facts != nil {
				for _, id := range facts.callIDs {
					lt.callSlots[id] = lt.nodes[n].slot
				}
			}
			walk(n.Children)
		}
	}
	walk(roots)
	return roots, lt
}

// cmpDates orders two Date.getTime values as getTree's sort compares them: a NaN difference keeps the order.
func cmpDates(a, b float64) int {
	switch delta := a - b; {
	case delta < 0:
		return -1
	case delta > 0:
		return 1
	}
	return 0
}

// entry returns the node's entry, reading it on first use.
func (lt *lazyTree) entry(n *SessionTreeNode) SessionEntry {
	ln := lt.nodes[n]
	if ln == nil || ln.loaded {
		return n.Entry
	}
	lt.s.mu.RLock()
	if ln.slot < len(lt.s.slots) && lt.s.slots[ln.slot].id == n.Entry.Base().ID {
		n.Entry = lt.s.entryAtLocked(ln.slot)
	}
	lt.s.mu.RUnlock()
	ln.loaded = true
	return n.Entry
}

// toolCall returns the call a tool result names, reading the assistant message that holds it.
func (lt *lazyTree) toolCall(id string) (ai.ToolCall, bool) {
	slot, ok := lt.callSlots[id]
	if !ok {
		return ai.ToolCall{}, false
	}
	lt.s.mu.RLock()
	var entry SessionEntry
	if slot < len(lt.s.slots) {
		entry = lt.s.entryAtLocked(slot)
	}
	lt.s.mu.RUnlock()
	if entry == nil {
		return ai.ToolCall{}, false
	}
	me, ok := lt.s.messageFor(entry)
	if !ok {
		return ai.ToolCall{}, false
	}
	var found ai.ToolCall
	matched := false
	for _, block := range me.Message.ContentBlocks() {
		if call, isCall := block.(ai.ToolCall); isCall && call.ID == id {
			found, matched = call, true
		}
	}
	return found, matched
}

// searchTexts sets every node's searchable text the first time a search needs one: messages and custom messages from the index, which
// holds their text once indexSearchTexts has read the ones the open left unread, and other entries, which are resident, from the entry.
// The /tree command asks for them after its first paint (WarmSearchTexts), so the read does not delay that paint.
func (lt *lazyTree) searchTexts(text func(n *SessionTreeNode, e SessionEntry, raw json.RawMessage) string) {
	if lt.searched {
		return
	}
	lt.searched = true
	lt.s.indexSearchTexts()
	lt.s.mu.RLock()
	defer lt.s.mu.RUnlock()
	for n, ln := range lt.nodes {
		slot := &lt.s.slots[ln.slot]
		switch entry := slot.entry; {
		case lt.s.coldPart(slot).searchAt != 0:
			ln.search = withSearchLabel(n.Label, searchBody{text: lt.s.searchTexts.text(lt.s.coldPart(slot).searchAt), parts: true})
		case entry == nil && slot.evictable() && !ln.loaded:
			entry = lt.s.entryAtLocked(ln.slot)
			ln.search = text(n, entry, entry.Raw())
		case ln.loaded || entry == nil:
			ln.search = text(n, n.Entry, n.Entry.Raw())
		default:
			ln.search = text(n, entry, entry.Raw())
		}
	}
}
