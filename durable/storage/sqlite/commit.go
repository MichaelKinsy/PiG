package sqlite

// Ports packages/durable/src/storage/sqlite/commit.ts and rows.ts
//
// One SqliteStorage.Commit call in a few statements whatever its number of batches: the check state of all its batches
// read up front (commitState), each batch checked in memory against what the batches before it left, and the accepted
// batches written per table (pendingWrites, through statements.go). Over a remote SQLite every statement is a round trip
// while the write lock is held; a group of commits must not cost one round trip per write.

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/durable"
)

// documentRow is a document record's documents row: id, kind, family, key, scope, owner, created and retired seqs, record.
func documentRow(record durable.DocumentRecord) ([]cell, error) {
	parts := addressPartsOf(record.Kind, record.Scope, record.Key)
	var retired cell
	if record.RetiredAt != nil {
		retired = int64(*record.RetiredAt)
	}
	encoded, err := encodeJSON(record)
	if err != nil {
		return nil, err
	}
	return []cell{int64(record.Id), parts.kind, parts.family, parts.keyValue, parts.scopeKind, parts.ownerId, int64(record.CreatedAt), retired, encoded}, nil
}

// commitState is what the checks of one call's batches read: the stored state of every ID, document and address the
// batches touch, read in a few statements when the call starts, then advanced in memory by each accepted batch.
type commitState struct {
	// recordIds is the table of each claimed ID; absent: unclaimed.
	recordIds map[int64]tableName
	// documents is each touched document's record; absent: no such document.
	documents map[int64]durable.DocumentRecord
	// versions is the version of each delta-changed document's latest revision; absent: no revision.
	versions map[int64]int64
	// current is the current incarnation of each created address; absent: none.
	current map[addressParts]durable.DocumentId
}

// readCommitState reads the stored state the checks of batches need: one statement per kind of state (split only by size).
func (storage *SqliteStorage) readCommitState(executor SqliteExecutor, batches [][]durable.StorageWrite) (*commitState, error) {
	tables := storage.schema.Tables
	var ids, documentIds, deltaIds []cell
	seenIds, seenDocuments, seenDeltas := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	addresses := []addressParts{}
	seenAddresses := map[addressParts]bool{}
	add := func(seen map[int64]bool, list *[]cell, id int64) {
		if !seen[id] {
			seen[id] = true
			*list = append(*list, id)
		}
	}
	for _, batch := range batches {
		for _, write := range batch {
			if id, _, ok := writeTable(write); ok {
				add(seenIds, &ids, id)
			}
			switch typed := write.(type) {
			case durable.DocumentCreateWrite:
				add(seenDocuments, &documentIds, int64(typed.Record.Id))
				parts := addressPartsOf(typed.Record.Kind, typed.Record.Scope, typed.Record.Key)
				if !seenAddresses[parts] {
					seenAddresses[parts] = true
					addresses = append(addresses, parts)
				}
			case durable.DocumentCopyWrite:
				add(seenDocuments, &documentIds, int64(typed.Record.Id))
				parts := addressPartsOf(typed.Record.Kind, typed.Record.Scope, typed.Record.Key)
				if !seenAddresses[parts] {
					seenAddresses[parts] = true
					addresses = append(addresses, parts)
				}
			case durable.DocumentChangeWrite:
				add(seenDocuments, &documentIds, int64(typed.Id))
				if typed.Content.Kind == durable.ContentDelta {
					add(seenDeltas, &deltaIds, int64(typed.Id))
				}
			case durable.DocumentRetireWrite:
				add(seenDocuments, &documentIds, int64(typed.Id))
			}
		}
	}
	state := &commitState{
		recordIds: map[int64]tableName{},
		documents: map[int64]durable.DocumentRecord{},
		versions:  map[int64]int64{},
		current:   map[addressParts]durable.DocumentId{},
	}
	rows, err := readRows(executor, "SELECT id, record_type FROM "+tables.RecordIds+" WHERE id IN (SELECT value FROM json_each(?))", ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id, err := rowInt(row, "id")
		if err != nil {
			return nil, err
		}
		table, err := rowText(row, "record_type")
		if err != nil {
			return nil, err
		}
		state.recordIds[id] = tableName(table)
	}
	if rows, err = readRows(executor, "SELECT record FROM "+tables.Documents+" WHERE id IN (SELECT value FROM json_each(?))", documentIds); err != nil {
		return nil, err
	}
	for _, row := range rows {
		text, err := rowText(row, "record")
		if err != nil {
			return nil, err
		}
		record, err := parseJSON[durable.DocumentRecord](text)
		if err != nil {
			return nil, err
		}
		state.documents[int64(record.Id)] = record
	}
	// Driven by the requested IDs: each latest revision is one descending probe of the primary key.
	if rows, err = readRows(executor, `SELECT requested.value AS document_id, (
				SELECT version FROM `+tables.DocumentRevisions+` WHERE document_id = requested.value ORDER BY seq DESC LIMIT 1
			) AS version FROM json_each(?) AS requested`, deltaIds); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row["version"] == nil {
			continue
		}
		id, err := rowInt(row, "document_id")
		if err != nil {
			return nil, err
		}
		version, err := rowInt(row, "version")
		if err != nil {
			return nil, err
		}
		state.versions[id] = version
	}
	if err := storage.readCurrent(executor, state, addresses); err != nil {
		return nil, err
	}
	return state, nil
}

// readCurrent reads the current incarnation of each of addresses into state.current.
func (storage *SqliteStorage) readCurrent(executor SqliteExecutor, state *commitState, addresses []addressParts) error {
	values := make([]cell, len(addresses))
	for index, parts := range addresses {
		values[index] = []cell{parts.kind, parts.scopeKind, parts.ownerId, parts.family, parts.keyValue}
	}
	documents := storage.schema.Tables.Documents
	rows, err := readRows(executor, `SELECT document.id, document.kind, document.scope_kind, document.owner_id, document.family, document.key_value
			FROM json_each(?) AS address JOIN `+documents+` AS document
			ON document.kind = json_extract(address.value, '$[0]') AND document.scope_kind = json_extract(address.value, '$[1]')
			AND document.owner_id = json_extract(address.value, '$[2]') AND document.family = json_extract(address.value, '$[3]')
			AND document.key_value = json_extract(address.value, '$[4]')
			WHERE document.retired_at IS NULL`, values)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var parts addressParts
		var id int64
		for _, column := range []struct {
			name string
			text *string
			num  *int64
		}{{"id", nil, &id}, {"scope_kind", &parts.scopeKind, nil}, {"owner_id", nil, &parts.ownerId}, {"family", nil, &parts.family}, {"kind", &parts.kind, nil}, {"key_value", &parts.keyValue, nil}} {
			if column.text != nil {
				if *column.text, err = rowText(row, column.name); err != nil {
					return err
				}
			} else if *column.num, err = rowInt(row, column.name); err != nil {
				return err
			}
		}
		state.current[parts] = durable.DocumentId(id)
	}
	return nil
}

// check is Durable's own checks of one batch that need no copy source: the refusal.
func (state *commitState) check(batch []durable.StorageWrite, actions *documentActions) error {
	if err := state.checkIds(batch); err != nil {
		return err
	}
	return state.checkDocuments(actions)
}

// apply advances the state past an accepted batch stored at seq, its copies resolved to their content.
func (state *commitState) apply(batch []durable.StorageWrite, actions *documentActions, seq durable.Seq) {
	for _, write := range batch {
		// An accepted batch claims an ID only for its stored table, if it has one (checkIds).
		if id, table, ok := writeTable(write); ok {
			state.recordIds[id] = table
		}
	}
	for _, id := range actions.order {
		action := actions.actions[id]
		if action.create != nil {
			record := newDocumentRecord(*action.create, seq, action.retire)
			state.documents[int64(id)] = record
			if !action.retire {
				state.current[addressPartsOf(record.Kind, record.Scope, record.Key)] = id
			}
		} else if action.retire {
			record := state.documents[int64(id)]
			retired := seq
			record.RetiredAt = &retired
			state.documents[int64(id)] = record
			// The batch may have created the address's next incarnation already: only this one leaves.
			parts := addressPartsOf(record.Kind, record.Scope, record.Key)
			if state.current[parts] == id {
				delete(state.current, parts)
			}
		}
		// A retired document's versions are never read again: every later action on it is refused first.
		if action.content != nil {
			state.versions[int64(id)] = int64(action.content.Version)
		}
	}
}

// newDocumentRecord is the stored record of a created document.
func newDocumentRecord(create durable.DocumentCreate, seq durable.Seq, retire bool) durable.DocumentRecord {
	record := durable.DocumentRecord{
		Id:        create.Id,
		Kind:      create.Kind,
		Key:       create.Key,
		CreatedAt: seq,
		Scope:     create.Scope,
		History:   create.History,
		Fork:      create.Fork,
	}
	if retire {
		retired := seq
		record.RetiredAt = &retired
	}
	return record
}

func (state *commitState) checkIds(batch []durable.StorageWrite) error {
	claimed := map[int64]tableName{}
	for _, write := range batch {
		id, table, ok := writeTable(write)
		if !ok {
			continue
		}
		existing := state.recordIds[id]
		earlier := claimed[id]
		if table == tableConversation || table == tableEntry || table == tableDocument {
			if existing != "" {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" {
				return fmt.Errorf("ID %d is written more than once", id)
			}
		} else {
			if existing != "" && existing != table {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" && earlier != table {
				return fmt.Errorf("ID %d is written as two record types", id)
			}
		}
		claimed[id] = table
	}
	return nil
}

func (state *commitState) checkDocuments(actions *documentActions) error {
	liveCounts := map[addressParts]int{}
	var liveOrder []addressParts
	for _, id := range actions.order {
		action := actions.actions[id]
		if action.copy != nil && actions.has(action.copy.Id) {
			return fmt.Errorf("Document copy %d source is changed in the copy batch", id)
		}
		existing, exists := state.documents[int64(id)]
		if action.create == nil && !exists {
			return fmt.Errorf("Unknown document: %d", id)
		}
		if action.create != nil && exists {
			return fmt.Errorf("Document %d already exists", id)
		}
		if exists && existing.RetiredAt != nil {
			return fmt.Errorf("Document %d is retired", id)
		}
		if action.content != nil && action.content.Kind == durable.ContentDelta {
			previous, hasBase := state.versions[int64(id)]
			if !hasBase {
				return fmt.Errorf("Document %d delta has no base", id)
			}
			if previous != int64(action.content.Version) {
				return fmt.Errorf("Document %d version transition requires a base", id)
			}
		}
		// A plain change keeps its address's live count: only creations and retirements are counted.
		if action.create == nil && !action.retire {
			continue
		}
		var parts addressParts
		if action.create != nil {
			parts = addressPartsOf(action.create.Kind, action.create.Scope, action.create.Key)
		} else {
			parts = addressPartsOf(existing.Kind, existing.Scope, existing.Key)
		}
		live, counted := liveCounts[parts]
		if !counted {
			liveOrder = append(liveOrder, parts)
			// Only created addresses were read: an address no batch creates only loses incarnations, never exceeds one.
			if _, has := state.current[parts]; has {
				live = 1
			}
		}
		if action.retire && exists {
			live--
		}
		if action.create != nil && !action.retire {
			live++
		}
		liveCounts[parts] = live
	}
	for _, parts := range liveOrder {
		if liveCounts[parts] > 1 {
			return errors.New("Document address already has a current incarnation")
		}
	}
	return nil
}

// pendingWrites are the writes of the accepted batches not yet written, per table, as their net effect: a task or
// submission written twice is written once, as last written; a current-only document's revisions before its latest base
// or retirement are dropped (and its stored ones deleted).
type pendingWrites struct {
	recordIds     orderedCells
	conversations [][]cell
	entries       [][]cell
	tasks         orderedCells
	submissions   orderedCells
	// documents holds each created or retired document's record.
	documents orderedCells
	// cleared are the documents whose stored revisions go.
	cleared []cell
	// revisions are each document's new revisions, in seq order.
	revisions orderedRevisions
	stored    []committedEntry
}

// orderedCells is a map of rows by ID that keeps the position of an ID's first write.
type orderedCells struct {
	ids  []int64
	rows map[int64][]cell
}

func (set *orderedCells) put(id int64, row []cell) {
	if set.rows == nil {
		set.rows = map[int64][]cell{}
	}
	if _, ok := set.rows[id]; !ok {
		set.ids = append(set.ids, id)
	}
	set.rows[id] = row
}

func (set *orderedCells) all() [][]cell {
	rows := make([][]cell, 0, len(set.ids))
	for _, id := range set.ids {
		rows = append(rows, set.rows[id])
	}
	return rows
}

func (set *orderedCells) clear() { *set = orderedCells{} }

// orderedRevisions is each document's revision rows, in the order the documents were first given one.
type orderedRevisions struct {
	ids  []int64
	rows map[int64][][]cell
}

func (set *orderedRevisions) add(id int64, row []cell) {
	if set.rows == nil {
		set.rows = map[int64][][]cell{}
	}
	if _, ok := set.rows[id]; !ok {
		set.ids = append(set.ids, id)
	}
	set.rows[id] = append(set.rows[id], row)
}

func (set *orderedRevisions) drop(id int64) {
	if _, ok := set.rows[id]; !ok {
		return
	}
	delete(set.rows, id)
	for index, other := range set.ids {
		if other == id {
			set.ids = append(set.ids[:index], set.ids[index+1:]...)
			break
		}
	}
}

func (set *orderedRevisions) all() [][]cell {
	var rows [][]cell
	for _, id := range set.ids {
		rows = append(rows, set.rows[id]...)
	}
	return rows
}

// add adds a batch stored at seq, after state applied it.
func (pending *pendingWrites) add(batch []durable.StorageWrite, actions *documentActions, seq durable.Seq, state *commitState) error {
	for _, write := range batch {
		if err := pending.addWrite(write, seq); err != nil {
			return err
		}
	}
	for _, id := range actions.order {
		if err := pending.addDocument(id, actions.actions[id], seq, state.documents[int64(id)]); err != nil {
			return err
		}
	}
	return nil
}

func (pending *pendingWrites) addWrite(write durable.StorageWrite, seq durable.Seq) error {
	if id, table, ok := writeTable(write); ok {
		pending.recordIds.put(id, []cell{id, string(table)})
	}
	switch typed := write.(type) {
	case durable.ConversationWrite:
		value := typed.Value
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		var ownerConversation, ownerTask cell
		if value.Owner != nil {
			ownerConversation = int64(value.Owner.ConversationId)
			ownerTask = int64(value.Owner.TaskId)
		}
		pending.conversations = append(pending.conversations, []cell{int64(value.Id), ownerConversation, ownerTask, record})
	case durable.EntryWrite:
		value := typed.Value
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		var head cell
		if value.Head != nil {
			head = int64(*value.Head)
		}
		pending.entries = append(pending.entries, []cell{int64(value.Id), int64(value.ConversationId), head, int64(seq), record})
		pending.stored = append(pending.stored, committedEntry{conversationId: value.ConversationId, text: record})
	case durable.TaskWrite:
		value := typed.Value
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		pending.tasks.put(int64(value.Id), []cell{
			int64(value.Id), int64(value.ConversationId), encodeIndexedString(value.Kind), string(value.State.Status),
			boolInt(value.AbortRequested), boolInt(value.Background), record,
		})
	case durable.SubmissionWrite:
		value := typed.Value
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		var requestId cell
		if value.RequestId != nil {
			requestId = encodeIndexedString(*value.RequestId)
		}
		pending.submissions.put(int64(value.Id), []cell{int64(value.Id), int64(value.ConversationId), requestId, string(value.Status), record})
	}
	return nil
}

func (pending *pendingWrites) addDocument(id durable.DocumentId, action *documentAction, seq durable.Seq, record durable.DocumentRecord) error {
	if action.create != nil || action.retire {
		row, err := documentRow(record)
		if err != nil {
			return err
		}
		pending.documents.put(int64(id), row)
	}
	if content := action.content; content != nil {
		if content.Kind == durable.ContentBase && isCurrentOnly(record) {
			pending.clear(int64(id))
		}
		var encoded string
		var err error
		if content.Kind == durable.ContentBase {
			encoded, err = encodeJSON(content.Value)
		} else {
			encoded, err = encodeJSON(content.Ops)
		}
		if err != nil {
			return err
		}
		pending.revisions.add(int64(id), []cell{int64(id), int64(seq), string(content.Kind), int64(content.Version), encoded})
	}
	if action.retire && isCurrentOnly(record) {
		pending.clear(int64(id))
	}
	return nil
}

// clear drops a document's revisions: the pending ones, and the stored ones when written.
func (pending *pendingWrites) clear(id int64) {
	for _, other := range pending.cleared {
		if other == id {
			pending.revisions.drop(id)
			return
		}
	}
	pending.cleared = append(pending.cleared, id)
	pending.revisions.drop(id)
}

// commitStatements are the SQL texts of a commit call's writes, built once per schema.
type commitStatements struct {
	recordIds       insertStatement
	conversations   insertStatement
	entries         insertStatement
	tasks           insertStatement
	submissions     insertStatement
	documents       insertStatement
	revisions       insertStatement
	deleteRevisions string
}

func newCommitStatements(tables SqliteTables) *commitStatements {
	return &commitStatements{
		recordIds:     newInsertStatement(tables.RecordIds, []string{"id", "record_type"}, "ON CONFLICT(id) DO NOTHING"),
		conversations: newInsertStatement(tables.Conversations, []string{"id", "owner_conversation_id", "owner_task_id", "record"}, ""),
		entries:       newInsertStatement(tables.Entries, []string{"id", "conversation_id", "head", "commit_seq", "record"}, ""),
		tasks: newInsertStatement(tables.Tasks, []string{"id", "conversation_id", "kind", "status", "abort_requested", "background", "record"},
			`ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id, kind = excluded.kind,
				status = excluded.status, abort_requested = excluded.abort_requested,
				background = excluded.background, record = excluded.record`),
		submissions: newInsertStatement(tables.Submissions, []string{"id", "conversation_id", "request_id", "status", "record"},
			`ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id,
				request_id = excluded.request_id, status = excluded.status, record = excluded.record`),
		// A created document is inserted, a stored one retired: one upsert writes both.
		documents: newInsertStatement(tables.Documents, []string{"id", "kind", "family", "key_value", "scope_kind", "owner_id", "created_at", "retired_at", "record"},
			"ON CONFLICT(id) DO UPDATE SET retired_at = excluded.retired_at, record = excluded.record"),
		revisions:       newInsertStatement(tables.DocumentRevisions, []string{"document_id", "seq", "kind", "version", "content"}, ""),
		deleteRevisions: "DELETE FROM " + tables.DocumentRevisions + " WHERE document_id IN (SELECT value FROM json_each(?))",
	}
}

// flush writes everything pending, a few statements per table (insertRows), and forgets it.
func (pending *pendingWrites) flush(executor SqliteExecutor, statements *commitStatements) error {
	steps := [...]struct {
		statement *insertStatement
		rows      [][]cell
	}{
		{&statements.recordIds, pending.recordIds.all()},
		{&statements.conversations, pending.conversations},
		{&statements.entries, pending.entries},
		{&statements.tasks, pending.tasks.all()},
		{&statements.submissions, pending.submissions.all()},
		{&statements.documents, pending.documents.all()},
	}
	for _, step := range steps {
		if err := insertRows(executor, step.statement, step.rows); err != nil {
			return err
		}
	}
	if err := runForEach(executor, statements.deleteRevisions, pending.cleared); err != nil {
		return err
	}
	if err := insertRows(executor, &statements.revisions, pending.revisions.all()); err != nil {
		return err
	}
	pending.recordIds.clear()
	pending.conversations = nil
	pending.entries = nil
	pending.tasks.clear()
	pending.submissions.clear()
	pending.documents.clear()
	pending.cleared = nil
	pending.revisions = orderedRevisions{}
	return nil
}
