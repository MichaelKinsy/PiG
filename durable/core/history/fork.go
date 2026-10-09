// SPDX-License-Identifier: MIT

package history

// VisibleEntry looks up entry at as conversation conv sees it: the conversation that owns it and the commit
// sequence of its commit (Storage.entry(conversationId, id), src/storage/sqlite/storage.ts readEntry). found is
// false when the entry is not visible. A Need means rows must be read first.
func (st *Store) VisibleEntry(conv, at int64) (owner, seq int64, found bool, need *Need) {
	cur, upper := conv, unbounded
	for {
		cs := st.convs[cur]
		if cs == nil || !cs.known {
			return 0, 0, false, &Need{Kind: NeedConversation, Conv: cur}
		}
		if at <= upper {
			ch := st.chainOf(cur, true)
			if ch.From == unloadedFrom || (ch.From != 0 && ch.From > at) || ch.To < at {
				return 0, 0, false, &Need{Kind: NeedEntries, Conv: cur, Min: at, Max: at}
			}
			if i := ch.find(at); i >= 0 {
				return cur, ch.ents[i].seq, true, nil
			}
		}
		if !cs.rec.HasParent {
			return 0, 0, false, nil
		}
		if cs.rec.ParentAt < upper {
			upper = cs.rec.ParentAt
		}
		if at > upper {
			return 0, 0, false, nil
		}
		cur = cs.rec.ParentConv
	}
}

// DocPoint selects a document state: the committed state after commit sequence Seq, or the current state.
type DocPoint struct {
	Current bool
	Seq     int64
}

// ForkPolicy is a document definition's fork rule.
type ForkPolicy uint8

// Fork policies of conversation documents (src/types.ts DocumentDefinition.fork).
const (
	ForkInitial ForkPolicy = iota
	ForkCurrent
	ForkAsOf
)

// DocSource is a document alive in a conversation scope, as the document scan reports it.
type DocSource struct {
	ID int64
	// Kind is the document kind as a JavaScript string; Key is its family member key when HasKey.
	Kind   []byte
	Key    []byte
	HasKey bool
	// Rewindable is the document's history mode: rewindable (true) or latest.
	Rewindable bool
	Fork       ForkPolicy
}

// DocScanner scans the documents alive in one conversation scope at a point, in the storage's scan order.
type DocScanner interface {
	ScanConversationDocs(conv int64, at DocPoint) []DocSource
}

// ForkCopy is one conversation document a fork copies: the new incarnation's id, and the source document and point
// it copies (the `document.copy` write of src/session/transaction.ts #stageConversation).
type ForkCopy struct {
	NewID  int64
	Source DocSource
	At     DocPoint
}

// DuplicateForkCopyError reports a fork that selects two source documents for one address.
type DuplicateForkCopyError struct{ Member []byte }

func (e *DuplicateForkCopyError) Error() string {
	return "Fork selects multiple source documents for " + string(e.Member)
}

// AddressID is the stable string identity of a conversation document address, JSON.stringify([kind, "conversation",
// owner, key ?? null]) (src/documents.ts addressId).
func AddressID(kind []byte, owner int64, key []byte, hasKey bool) []byte {
	b := append([]byte(nil), '[')
	b = appendString(b, kind)
	b = append(b, `,"conversation",`...)
	b = appendInt(b, owner)
	b = append(b, ',')
	if hasKey {
		b = appendString(b, key)
	} else {
		b = append(b, "null"...)
	}
	return append(b, ']')
}

// PrepareForkDocumentCopies selects every conversation document a fork of parent at entry at copies into child
// (src/session/forks.ts prepareForkDocumentCopies). The documents of the conversation that owns the fork point are
// copied as of that entry's commit when their policy is asOf; the parent's own documents are copied as current when
// their policy is current. Each copy mints one id, in scan order, from mint. entrySeq and entryOwner come from
// VisibleEntry.
func PrepareForkDocumentCopies(scan DocScanner, parent, entryOwner, entrySeq, child int64, mint func() int64) ([]ForkCopy, error) {
	var copies []ForkCopy
	var seen [][]byte
	collect := func(scope int64, at DocPoint, policy ForkPolicy) error {
		for _, src := range scan.ScanConversationDocs(scope, at) {
			if src.Fork != policy {
				continue
			}
			id := mint()
			addr := AddressID(src.Kind, child, src.Key, src.HasKey)
			for _, s := range seen {
				if string(s) == string(addr) {
					member := append([]byte(nil), src.Kind...)
					if src.HasKey {
						member = append(append(member, '/'), src.Key...)
					}
					return &DuplicateForkCopyError{Member: member}
				}
			}
			seen = append(seen, addr)
			copies = append(copies, ForkCopy{NewID: id, Source: src, At: at})
		}
		return nil
	}
	if err := collect(entryOwner, DocPoint{Seq: entrySeq}, ForkAsOf); err != nil {
		return nil, err
	}
	if err := collect(parent, DocPoint{Current: true}, ForkCurrent); err != nil {
		return nil, err
	}
	return copies, nil
}

// NewFork registers the conversation forked from parent at entry at, which must be visible from parent
// (`#stageConversation`, src/session/transaction.ts), and returns its record. The caller mints id in the commit that
// writes the record, before it mints the document copies' ids. A task-owned fork passes its owner.
func (st *Store) NewFork(id, parent, at int64, owner *Owner) ConvRecord {
	rec := ConvRecord{ID: id, HasParent: true, ParentConv: parent, ParentAt: at}
	if owner != nil {
		rec.HasOwner, rec.OwnerConv, rec.OwnerTask = true, owner.Conv, owner.Task
	}
	st.AddConversation(rec, true)
	return rec
}

// Owner is the task and conversation that created a conversation.
type Owner struct{ Conv, Task int64 }
