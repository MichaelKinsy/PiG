// SPDX-License-Identifier: MIT

package history

// The row writes of conversations and entries, as pi-durable's SqliteStorage makes them (src/storage/sqlite/
// storage.ts, the "conversation" and "entry" cases of the write switch). Each is preceded by a record id claim.
const (
	// SQLClaimID claims an id for a record type. Parameters: id, type (RecordTypeConversation or RecordTypeEntry).
	SQLClaimID = `INSERT OR IGNORE INTO record_ids (id, record_type) VALUES (?, ?)`
	// SQLInsertConversation writes a conversation. Parameters: id, owner conversation id or NULL, owner task id or
	// NULL, record.
	SQLInsertConversation = `INSERT INTO conversations (id, owner_conversation_id, owner_task_id, record) VALUES (?, ?, ?, ?)`
	// SQLInsertEntry writes an entry. Parameters: id, conversation id, head or NULL, commit sequence, record.
	SQLInsertEntry = `INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)`

	// RecordTypeConversation and RecordTypeEntry are the record_ids types of the two tables.
	RecordTypeConversation = "conversation"
	RecordTypeEntry        = "entry"
)

// EntryRow is the column values of an entries insert.
type EntryRow struct {
	ID, Conv int64
	Head     int64
	HasHead  bool
	Seq      int64
	Record   []byte
}

// NewEntryRow reads the indexed columns of a stored entry record: the id, the conversation id, and the head column,
// which is the record's head when it is a number and NULL otherwise (`write.value.head ?? null`).
func NewEntryRow(rec []byte, seq int64) (EntryRow, error) {
	sc, err := scanRecord(rec)
	if err != nil {
		return EntryRow{}, err
	}
	if !sc.hasID || !sc.hasConv {
		return EntryRow{}, ErrShape
	}
	return EntryRow{ID: sc.id, Conv: sc.conv, Head: sc.head, HasHead: sc.flags&flagHead != 0, Seq: seq, Record: rec}, nil
}

// ConversationRow is the column values of a conversations insert.
type ConversationRow struct {
	ID                   int64
	HasOwner             bool
	OwnerConv, OwnerTask int64
	Record               []byte
}

// NewConversationRow reads the indexed columns of a stored conversation record.
func NewConversationRow(rec []byte) (ConversationRow, error) {
	c, err := ParseConversation(rec)
	if err != nil {
		return ConversationRow{}, err
	}
	return ConversationRow{ID: c.ID, HasOwner: c.HasOwner, OwnerConv: c.OwnerConv, OwnerTask: c.OwnerTask, Record: rec}, nil
}
