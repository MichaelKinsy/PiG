package store

// Ports packages/durable/src/storage/store/commit.ts
//
// One Durable commit as one commit of the store's series (slice 6b §2.3): staged before the queue (rows encoded,
// contents serialized, explicit ids raising the store's allocator), then inside the series' transaction what
// `SqliteStorage` does inside its own, with the step's reader: Durable's checks of ids and documents and the copies
// resolved from their sources (its messages, a refusal), then its one composite write: the table puts, and the
// documents in two passes, existing ones first (their content, then their retirement, which frees the addresses this
// commit's creations take), then the creations and copies.

import (
	"context"
	"errors"
	"fmt"

	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/writes"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// RecordType is Durable's record types, as `record_ids` names them.
type RecordType string

// The record types.
const (
	RecordConversation RecordType = "conversation"
	RecordEntry        RecordType = "entry"
	RecordTask         RecordType = "task"
	RecordSubmission   RecordType = "submission"
	RecordDocument     RecordType = "document"
)

// CommitTables are the storage's tables, as a commit writes them.
type CommitTables struct {
	RecordIds     *chordstore.OwnerTable
	Conversations *chordstore.OwnerTable
	Entries       *chordstore.OwnerTable
	EntryHeads    *chordstore.OwnerTable
	Tasks         *chordstore.OwnerTable
	Submissions   *chordstore.OwnerTable
}

// claim is an id a write claims.
type claim struct {
	id  int64
	typ RecordType
}

// rowWrite is a table write of the commit: its record's id and type, its table and its row encoded at staging.
type rowWrite struct {
	id    int64
	typ   RecordType
	table *chordstore.OwnerTable
	row   *chordstore.EncodedRow
}

// stagedBase is a base as the commit stores it: its version and value, and the value serialized.
type stagedBase struct {
	version int64
	value   durable.JsonObject
	text    string
}

// stagedContent is a change's content and its serialization.
type stagedContent struct {
	content durable.DocumentContent
	text    string
}

// documentAction is what one commit does with one document, as `SqliteStorage`'s action map has it.
type documentAction struct {
	create  *durable.DocumentCreate
	copy    *durable.DocumentCopySource
	base    *stagedBase
	content *stagedContent
	retire  bool
}

// checked is what Check found for Apply, in the same step: the stored types of the claimed ids, the changed
// documents in action order.
type checked struct {
	claimed       map[int64]RecordType
	existing      map[durable.DocumentId]chordstore.DocumentRecord
	existingOrder []durable.DocumentId
}

// DurableCommit is a Durable commit staged for the store's series: its checks, then itself as its one write.
type DurableCommit struct {
	owner  string
	tables *CommitTables
	ctx    context.Context
	// claims is every id a write claims, in write order (documents created or copied included), for the id checks.
	claims  []claim
	rows    []rowWrite
	order   []durable.DocumentId
	actions map[durable.DocumentId]*documentAction
	checked *checked
}

// NewDurableCommit stages writes (rule 8: rows and contents encoded here, before the queue); raise makes room above
// each explicit id. A malformed batch (two content commands for one document, a second retirement) returns Durable's
// message.
func NewDurableCommit(batch []durable.StorageWrite, owner string, tables *CommitTables, raise func(id int64), ctx context.Context) (*DurableCommit, error) {
	commit := &DurableCommit{owner: owner, tables: tables, ctx: ctx, actions: map[durable.DocumentId]*documentAction{}}
	for _, write := range batch {
		if err := commit.stage(writes.Value(write), raise); err != nil {
			return nil, err
		}
	}
	return commit, nil
}

// Series is the commit as a commit of the store's series: Check, then itself as its one write.
func (commit *DurableCommit) Series() chordstore.SeriesCommit {
	return chordstore.SeriesCommit{Check: commit.Check, Writes: []chordstore.StagedWrite{commit}}
}

// StageOrder is the write's phase: the commit is one composite write.
func (commit *DurableCommit) StageOrder() chordstore.WriteOrder { return chordstore.OrderWrite }

// Check is Durable's checks against the state the series' earlier commits left, writing nothing: the ids, the
// documents and the copies; an error refuses the commit.
func (commit *DurableCommit) Check(step *chordstore.Step) error {
	claimed, err := commit.checkIds(step)
	if err != nil {
		return err
	}
	existing, order, err := commit.checkDocuments(step)
	if err != nil {
		return err
	}
	if err := commit.resolveCopies(step); err != nil {
		return err
	}
	commit.checked = &checked{claimed: claimed, existing: existing, existingOrder: order}
	return nil
}

// resolveCopies resolves the copies (S11): each source read at its point and checked against the copy's record,
// entered as a creation carrying the source's value as its base.
func (commit *DurableCommit) resolveCopies(step *chordstore.Step) error {
	for _, id := range commit.order {
		action := commit.actions[id]
		source := action.copy
		if source == nil {
			continue
		}
		if _, changed := commit.actions[source.Id]; changed {
			return fmt.Errorf("Document copy %d source is changed in the copy batch", id)
		}
		base, err := commit.resolveCopy(step, action, *source)
		if err != nil {
			return &scan.CausedError{Message: fmt.Sprintf("Document copy %d was rejected", id), Cause: err}
		}
		action.base = base
	}
	return nil
}

func (commit *DurableCommit) resolveCopy(step *chordstore.Step, action *documentAction, source durable.DocumentCopySource) (*stagedBase, error) {
	read, err := ReadAt(step, source.Id, source.At, commit.owner)
	if err != nil {
		return nil, err
	}
	if read.CurrentOnly {
		return nil, durable.NewStorageRequestError(fmt.Sprintf("Document %d does not retain historical content", read.CurrentOnlyID))
	}
	stored := read.Document
	if stored == nil {
		return nil, fmt.Errorf("Fork source document %d cannot be read", source.Id)
	}
	create := action.create
	sameKey := (stored.Record.Key == nil && create.Key == nil) ||
		(stored.Record.Key != nil && create.Key != nil && *stored.Record.Key == *create.Key)
	if stored.Record.Scope.Kind != durable.ScopeConversation || create.Scope.Kind != durable.ScopeConversation ||
		stored.Record.Kind != create.Kind || !sameKey ||
		stored.Record.History != create.History || stored.Record.Fork != create.Fork {
		return nil, fmt.Errorf("Fork source document %d does not match the copied record", source.Id)
	}
	text, err := jsstring.MarshalJSON(stored.Value)
	if err != nil {
		return nil, err
	}
	return &stagedBase{version: int64(stored.Version), value: stored.Value, text: string(text)}, nil
}

// Apply is the commit's writes, after its Check in the same step.
func (commit *DurableCommit) Apply(step *chordstore.Step) ([]chordstore.StoreWrite, error) {
	recorded := []chordstore.StoreWrite{}
	found := commit.checked
	for _, write := range commit.rows {
		if _, stored := found.claimed[write.id]; !stored {
			claimed, err := commit.claim(step, write.id, write.typ)
			if err != nil {
				return nil, err
			}
			recorded = append(recorded, claimed)
		}
		found.claimed[write.id] = write.typ
		stamp := ""
		if write.table == commit.tables.Entries {
			stamp = "commit_seq"
		}
		put, err := write.table.Put(step, write.row, stamp)
		if err != nil {
			return nil, err
		}
		recorded = append(recorded, put)
	}
	// Existing documents first: a retirement frees its address before a creation of this commit takes it.
	for _, id := range found.existingOrder {
		changed, err := commit.change(step, found.existing[id], commit.actions[id])
		if err != nil {
			return nil, err
		}
		recorded = append(recorded, changed...)
	}
	for _, id := range commit.order {
		action := commit.actions[id]
		if action.create == nil {
			continue
		}
		claimed, err := commit.claim(step, int64(id), RecordDocument)
		if err != nil {
			return nil, err
		}
		recorded = append(recorded, claimed)
		created, err := commit.create(step, *action.create, *action.base, action.retire)
		if err != nil {
			return nil, err
		}
		recorded = append(recorded, created)
	}
	return recorded, nil
}

// stage stages one write: a table row encoded, a document command into its action.
func (commit *DurableCommit) stage(write durable.StorageWrite, raise func(id int64)) error {
	tables := commit.tables
	switch typed := write.(type) {
	case durable.ConversationWrite:
		row, err := ConversationRow(typed.Value)
		if err != nil {
			return err
		}
		return commit.row(int64(typed.Value.Id), RecordConversation, tables.Conversations, row, raise)
	case durable.EntryWrite:
		value := typed.Value
		row, err := EntryRow(value)
		if err != nil {
			return err
		}
		if err := commit.row(int64(value.Id), RecordEntry, tables.Entries, row, raise); err != nil {
			return err
		}
		// Its claim is the entry's: Apply claims an id once.
		if value.Head != nil {
			encoded, err := tables.EntryHeads.Encode(chordstore.Row{"id": int64(value.Id), "conversation_id": int64(value.ConversationId)})
			if err != nil {
				return err
			}
			commit.rows = append(commit.rows, rowWrite{id: int64(value.Id), typ: RecordEntry, table: tables.EntryHeads, row: encoded})
		}
		return nil
	case durable.TaskWrite:
		row, err := TaskRow(typed.Value)
		if err != nil {
			return err
		}
		return commit.row(int64(typed.Value.Id), RecordTask, tables.Tasks, row, raise)
	case durable.SubmissionWrite:
		row, err := SubmissionRow(typed.Value)
		if err != nil {
			return err
		}
		return commit.row(int64(typed.Value.Id), RecordSubmission, tables.Submissions, row, raise)
	case durable.DocumentCreateWrite:
		action, err := commit.contentAction(typed.Record.Id)
		if err != nil {
			return err
		}
		record := typed.Record
		action.create = &record
		text, err := jsstring.MarshalJSON(typed.Content.Value)
		if err != nil {
			return err
		}
		action.base = &stagedBase{version: int64(typed.Content.Version), value: typed.Content.Value, text: string(text)}
		commit.claims = append(commit.claims, claim{id: int64(record.Id), typ: RecordDocument})
		raise(int64(record.Id))
		return nil
	case durable.DocumentCopyWrite:
		action, err := commit.contentAction(typed.Record.Id)
		if err != nil {
			return err
		}
		record, source := typed.Record, typed.Source
		action.create = &record
		action.copy = &source
		commit.claims = append(commit.claims, claim{id: int64(record.Id), typ: RecordDocument})
		raise(int64(record.Id))
		return nil
	case durable.DocumentChangeWrite:
		// A creation's base is its content command (`SqliteStorage` counts it so too), in either order.
		action, err := commit.contentAction(typed.Id)
		if err != nil {
			return err
		}
		content := typed.Content
		var text []byte
		if content.Kind == durable.ContentBase {
			text, err = jsstring.MarshalJSON(content.Value)
		} else {
			text, err = jsstring.MarshalJSON(content.Ops)
		}
		if err != nil {
			return err
		}
		action.content = &stagedContent{content: content, text: string(text)}
		return nil
	case durable.DocumentRetireWrite:
		action := commit.action(typed.Id)
		if action.retire {
			return fmt.Errorf("Document %d is retired more than once", typed.Id)
		}
		action.retire = true
		return nil
	}
	return nil
}

// contentAction is the action of document id for a content command: refused when it has one already.
func (commit *DurableCommit) contentAction(id durable.DocumentId) (*documentAction, error) {
	action := commit.action(id)
	if action.create != nil || action.content != nil || action.copy != nil {
		return nil, fmt.Errorf("Document %d has more than one content command", id)
	}
	return action, nil
}

func (commit *DurableCommit) row(id int64, typ RecordType, table *chordstore.OwnerTable, row chordstore.Row, raise func(id int64)) error {
	commit.claims = append(commit.claims, claim{id: id, typ: typ})
	encoded, err := table.Encode(row)
	if err != nil {
		return err
	}
	commit.rows = append(commit.rows, rowWrite{id: id, typ: typ, table: table, row: encoded})
	raise(id)
	return nil
}

func (commit *DurableCommit) action(id durable.DocumentId) *documentAction {
	action := commit.actions[id]
	if action == nil {
		action = &documentAction{}
		commit.actions[id] = action
		commit.order = append(commit.order, id)
	}
	return action
}

// checkIds is Durable's global id checks (`SqliteStorage.checkGlobalIds`): a conversation, entry or document id
// claimed by anything, or written twice; a task or submission id claimed by another type. It returns the stored
// types.
func (commit *DurableCommit) checkIds(step *chordstore.Step) (map[int64]RecordType, error) {
	stored := map[int64]RecordType{}
	read := map[int64]bool{}
	claimed := map[int64]RecordType{}
	for _, each := range commit.claims {
		if !read[each.id] {
			row, err := commit.tables.RecordIds.Get(commit.ctx, step, []sqlite.SqliteValue{each.id})
			if err != nil {
				return nil, err
			}
			if row != nil {
				text, _ := row["record_type"].(string)
				stored[each.id] = RecordType(text)
			}
			read[each.id] = true
		}
		existing := stored[each.id]
		earlier := claimed[each.id]
		if each.typ == RecordConversation || each.typ == RecordEntry || each.typ == RecordDocument {
			if existing != "" {
				return nil, fmt.Errorf("ID %d already belongs to %s", each.id, existing)
			}
			if earlier != "" {
				return nil, fmt.Errorf("ID %d is written more than once", each.id)
			}
		} else {
			if existing != "" && existing != each.typ {
				return nil, fmt.Errorf("ID %d already belongs to %s", each.id, existing)
			}
			if earlier != "" && earlier != each.typ {
				return nil, fmt.Errorf("ID %d is written as two record types", each.id)
			}
		}
		claimed[each.id] = each.typ
	}
	return stored, nil
}

// liveKey is a document address as the live counts of checkDocuments take it.
type liveKey struct {
	namespace, kind string
	key             string
	hasKey          bool
}

// checkDocuments is Durable's document checks (`SqliteStorage.checkDocumentActions`): a change or retirement of an
// unknown or retired document, a delta on another version, more than one live incarnation per address after the
// commit. It returns the existing records the commit changes or retires, with their ids in action order.
func (commit *DurableCommit) checkDocuments(step *chordstore.Step) (map[durable.DocumentId]chordstore.DocumentRecord, []durable.DocumentId, error) {
	existing := map[durable.DocumentId]chordstore.DocumentRecord{}
	var existingOrder []durable.DocumentId
	for _, id := range commit.order {
		action := commit.actions[id]
		if action.create != nil {
			continue
		}
		stored, err := chordstore.ReadDocument(step, int64(id))
		if err != nil {
			return nil, nil, err
		}
		var found *durable.DocumentRecord
		if stored != nil {
			if found, err = DurableRecord(*stored, commit.owner); err != nil {
				return nil, nil, err
			}
		}
		if found == nil {
			return nil, nil, fmt.Errorf("Unknown document: %d", id)
		}
		if found.RetiredAt != nil {
			return nil, nil, fmt.Errorf("Document %d is retired", id)
		}
		if action.content != nil && action.content.content.Kind == durable.ContentDelta && int64(action.content.content.Version) != stored.Version {
			return nil, nil, fmt.Errorf("Document %d version transition requires a base", id)
		}
		existing[id] = *stored
		existingOrder = append(existingOrder, id)
	}
	// Per address a live creation takes: its live incarnations after the commit so far.
	live := map[liveKey]int{}
	for _, id := range commit.order {
		action := commit.actions[id]
		if action.create == nil || action.retire {
			continue
		}
		address := AddressOf(action.create.Kind, action.create.Scope, action.create.Key, commit.owner)
		key := liveKey{namespace: address.Namespace, kind: address.Kind}
		if address.Key != nil {
			key.key, key.hasKey = *address.Key, true
		}
		count, counted := live[key]
		if !counted {
			// The incarnation live there now counts unless this commit retires it.
			current, err := chordstore.FindDocument(step, address, chordstore.CurrentPoint)
			if err != nil {
				return nil, nil, err
			}
			if current != nil {
				if retiring := commit.actions[durable.DocumentId(current.ID)]; retiring == nil || !retiring.retire {
					count = 1
				}
			}
		}
		count++
		if count > 1 {
			return nil, nil, errors.New("Document address already has a current incarnation")
		}
		live[key] = count
	}
	return existing, existingOrder, nil
}

// claim puts id's `record_ids` row: its record type.
func (commit *DurableCommit) claim(step *chordstore.Step, id int64, typ RecordType) (chordstore.StoreWrite, error) {
	table := commit.tables.RecordIds
	encoded, err := table.Encode(chordstore.Row{"id": id, "record_type": string(typ)})
	if err != nil {
		return nil, err
	}
	return table.Put(step, encoded, "")
}

// change is an existing document's content, then its retirement, as their writes.
func (commit *DurableCommit) change(step *chordstore.Step, record chordstore.DocumentRecord, action *documentAction) ([]chordstore.StoreWrite, error) {
	var written []chordstore.StoreWrite
	revision := record.Revision
	if action.content != nil {
		stored := engineContent(action.content.content)
		if err := chordstore.WriteContent(step, record.ID, record.History, revision, stored, action.content.text); err != nil {
			return nil, err
		}
		written = append(written, &chordstore.DocumentChange{ID: record.ID, Revision: revision, Content: stored})
		revision++
	}
	if action.retire {
		if err := chordstore.RetireRecord(step, record); err != nil {
			return nil, err
		}
		written = append(written, &chordstore.DocumentRetire{ID: record.ID, Revision: revision})
	}
	return written, nil
}

// create is a creation or a resolved copy (retired at once for a create-plus-retire, an empty lifetime), as its write.
func (commit *DurableCommit) create(step *chordstore.Step, create durable.DocumentCreate, base stagedBase, retired bool) (chordstore.StoreWrite, error) {
	record, err := EngineRecord(create, commit.owner, base.version, step.Seq, retired)
	if err != nil {
		return nil, err
	}
	if err := chordstore.InsertDocument(step, record, &base.text); err != nil {
		return nil, err
	}
	return &chordstore.DocumentCreate{Record: record, Base: &chordstore.DocumentBase{Version: base.version, Fingerprint: "", Value: base.value}}, nil
}

// engineContent is a Durable content as the engine stores it: its documents have no schema fingerprint.
func engineContent(content durable.DocumentContent) chordstore.DocumentContent {
	if content.Kind == durable.ContentBase {
		return chordstore.DocumentContent{Kind: chordstore.ContentBase, Version: int64(content.Version), Fingerprint: "", Value: content.Value}
	}
	return chordstore.DocumentContent{Kind: chordstore.ContentDelta, Version: int64(content.Version), Ops: content.Ops}
}
