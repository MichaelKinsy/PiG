package session

import "strconv"

// The keys of the Session line (session/keys.ts): what a commit writes and a job reads, so a read of an unpublished write waits for
// its publication. A key names a record or a set of records; the writer keeps, per key, the newest unpublished part that writes it.

// TaskKey is a task record.
func TaskKey(id ID) string { return "task:" + strconv.FormatInt(id, 10) }

// ConversationKey is a conversation record.
func ConversationKey(id ID) string { return "conv:" + strconv.FormatInt(id, 10) }

// EntryKey is an entry record.
func EntryKey(id ID) string { return "entry:" + strconv.FormatInt(id, 10) }

// AppendKey is the newest entries of a conversation: appended blind, read by scans, the newest entry and head markers.
func AppendKey(id ID) string { return "entry-append:" + strconv.FormatInt(id, 10) }

// SubmissionKey is a submission record.
func SubmissionKey(id ID) string { return "sub:" + strconv.FormatInt(id, 10) }

// RequestKey is the submission a conversation's request ID names.
func RequestKey(conversationID ID, requestID string) string {
	return "req:" + strconv.FormatInt(conversationID, 10) + ":" + requestID
}

// DocumentKey is a document address (addressID): its current incarnation, value and held changes.
func DocumentKey(addressID string) string { return "doc:" + addressID }

// IncarnationKey is a document incarnation, for reads by its ID.
func IncarnationKey(id ID) string { return "docid:" + strconv.FormatInt(id, 10) }

// ScopeKey is the incarnations alive in a scope: created and retired blind, read by scans and the task-document index.
func ScopeKey(scope Scope) string {
	switch scope.Kind {
	case ScopeConversation:
		return "docscope:c" + strconv.FormatInt(scope.Owner, 10)
	case ScopeTask:
		return "docscope:t" + strconv.FormatInt(scope.Owner, 10)
	}
	return "docscope:session"
}

// OwnedKey is a node of the scheduler's ownership tree (a task ID, or "c" and a conversation ID): the live work listed below it.
// Written by task creation and a task becoming terminal for every node from the task's parent up to and including the nearest live
// task or the root conversation; read by every walk that expands the node.
func OwnedKey(node string) string { return "owned:" + node }

const (
	// RootsKey is the ownerless conversations with live work below them, which global idle walks from.
	RootsKey = "roots"
	// IntentKey is the live tasks with cancellation intent: abort marks, and held outcomes other than completed.
	IntentKey = "intent"
	// AllKeys is every unpublished write: what a read that cannot be keyed, such as a scan, touches.
	AllKeys = "*"
)

// tableKeys appends the keys of the table records in writes; document writes are keyed by the transaction, which knows addresses.
func tableKeys(writes []Write, keys []string) []string {
	for i := range writes {
		w := &writes[i]
		switch w.Kind {
		case WriteConversation:
			keys = append(keys, ConversationKey(mustInt(w.Record, "id")))
		case WriteEntry:
			keys = append(keys, EntryKey(mustInt(w.Record, "id")), AppendKey(mustInt(w.Record, "conversationId")))
		case WriteTask:
			keys = append(keys, TaskKey(mustInt(w.Record, "id")))
		case WriteSubmission:
			keys = append(keys, SubmissionKey(mustInt(w.Record, "id")))
			if req, ok := getStr(w.Record, "requestId"); ok {
				keys = append(keys, RequestKey(mustInt(w.Record, "conversationId"), req))
			}
		}
	}
	return keys
}
