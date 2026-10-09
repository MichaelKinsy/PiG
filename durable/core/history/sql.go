// SPDX-License-Identifier: MIT

package history

// The SQL a host runs to answer a Need. Record columns are read as BLOB so rows return bytes without a UTF-16
// round trip (ABI section 4.3). Parameters are always bound.
const (
	// SQLConversation answers NeedConversation. Parameters: conversation id.
	SQLConversation = `SELECT CAST(record AS BLOB) FROM conversations WHERE id = ?`

	// SQLBounds answers NeedBounds with one row: the newest entry id at or below the bound, the newest head
	// marker's id and its head, each NULL when absent. Parameters: conversation id and bound, three times, in that
	// order. entries_by_conversation and entry_heads_by_conversation make each subquery one index probe.
	SQLBounds = `SELECT
  (SELECT id FROM entries WHERE conversation_id = ? AND id <= ? ORDER BY id DESC LIMIT 1),
  (SELECT id FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1),
  (SELECT head FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1)`

	// SQLEntries answers NeedEntries: the rows to give BeginLoad and AddRow, ascending. Parameters: conversation
	// id, lowest id, highest id.
	SQLEntries = `SELECT id, commit_seq, CAST(record AS BLOB) FROM entries WHERE conversation_id = ? AND id >= ? AND id <= ? ORDER BY id`
)

// SQLUnbounded stands for an unbounded upper id in a SQL parameter: the largest integer a JavaScript host binds
// exactly.
const SQLUnbounded = int64(1)<<53 - 1

// SQLBound maps a Need's Max to the value a host binds: unbounded becomes SQLUnbounded.
func SQLBound(max int64) int64 {
	if max == unbounded {
		return SQLUnbounded
	}
	return max
}

// Params returns the SQL parameters of a need, in the order of the statement that answers it.
func (n Need) Params() []int64 {
	switch n.Kind {
	case NeedConversation:
		return []int64{n.Conv}
	case NeedBounds:
		b := SQLBound(n.Max)
		return []int64{n.Conv, b, n.Conv, b, n.Conv, b}
	case NeedEntries:
		return []int64{n.Conv, n.Min, SQLBound(n.Max)}
	}
	return nil
}

// SQL returns the statement that answers a need.
func (n Need) SQL() string {
	switch n.Kind {
	case NeedConversation:
		return SQLConversation
	case NeedBounds:
		return SQLBounds
	case NeedEntries:
		return SQLEntries
	}
	return ""
}
