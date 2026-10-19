// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store

// The texts of the context reads. A host that answers them from the context it derived compares a statement's text with these,
// as it reads the table the core published (a probe core publishes its own).
const (
	TextContextSummary  = "SELECT pig_context_summary(?, ?, ?, ?, ?)"
	TextContextMessages = "SELECT pig_context_messages(?, ?, ?, ?)"
	TextContextRelease  = "SELECT pig_context_release(?)"
)

// The statements a core with a synchronous host read (ABI section 4.2, pig.read) runs when a step needs a record it did
// not load at open. Each reads the columns its dump statement reads, so one row decoder serves both.
const (
	// SQLTaskByID reads one task's record. Parameters: id.
	SQLTaskByID = stmtTaskByID
	// SQLSubmissionByID reads one submission's record. Parameters: id.
	SQLSubmissionByID = stmtSubmissionByID
	// SQLSubmissionByRequest reads a conversation's submission with a request ID. Parameters: conversation id,
	// JSON.stringify(requestId).
	SQLSubmissionByRequest = stmtSubmissionByRequest
	// SQLTasksPage and SQLSubmissionsPage read every task or submission a page at a time, as the dump does. Parameters: the
	// id the page follows.
	SQLTasksPage       = stmtDumpTasks
	SQLSubmissionsPage = stmtDumpSubmissions
	// SQLTasksDesc reads a page of tasks newest first, with the columns of SQLTasksPage. Parameters: the id the page precedes.
	SQLTasksDesc = stmtTasksDesc
	// SQLRecordType reads the record_ids row of an id. Parameters: id.
	SQLRecordType = stmtRecordType
	// SQLEntryByID reads one entry: conversation_id, commit_seq, record. Parameters: id.
	SQLEntryByID = stmtEntryByID
	// SQLEntryIDsDesc reads a page of a conversation's entry ids in a range, newest first. Parameters: conversation id,
	// lowest id, highest id, page size.
	SQLEntryIDsDesc = stmtEntryIDsDesc
	// SQLEntryBounds is history.SQLBounds: a conversation's newest entry, newest head marker and its head at or below a
	// bound.
	SQLEntryBounds = stmtBounds
	// SQLEntriesPage reads a page of a conversation's entries in an id range: id, commit_seq, record. Parameters:
	// conversation id, the id the page follows, highest id.
	SQLEntriesPage = stmtEntriesPage
	// SQLEntryCount counts a conversation's entries in an id range, so a load reserves its index once. Parameters:
	// conversation id, lowest id, highest id.
	SQLEntryCount = stmtEntryCount
	// SQLEntriesDesc reads a page of a conversation's entries in a range, newest first: id, commit_seq, record. Parameters:
	// conversation id, lowest id, highest id, page size.
	SQLEntriesDesc = stmtEntriesDesc
	// SQLContextSummary is the host's summary of a conversation's context through an entry: what a request preparation reads
	// of it. Parameters: conversation id, the newest entry (tail), the newest head marker at or below it (0: none) and that
	// marker's head, 1 when the estimate of the compaction check is wanted else 0. One row, one BLOB column: the summary JSON
	// `{tail, head, entries?, system, tokens}`.
	SQLContextSummary = stmtContextSummary
	// SQLContextMessages is the host's model messages of the same context. Parameters: conversation id, tail, marker,
	// marker's head. One row, one BLOB column: the JSON array of the messages.
	SQLContextMessages = stmtContextMessages
	// SQLContextRelease tells the host the conversation's kept context is dropped. Parameter: conversation id. No rows.
	SQLContextRelease = stmtContextRelease
	// PageRows is how many rows one page read returns at most.
	PageRows = dumpPage
)
