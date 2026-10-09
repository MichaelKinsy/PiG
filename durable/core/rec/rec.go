// SPDX-License-Identifier: MIT

// Package rec is the record layer for the records the core rewrites: pi-durable's conversation, task, submission and
// document records (spec §2, §3.2, §5.1, §6), written in pi-durable main da866ada's shapes and read tolerantly from
// 1.0.4 and newer stores (CONTRACT section 2.3).
//
// Entries are immutable bytes and belong to package history (history.AppendEntryRecord, history.EntryRow). The records
// the core rewrites (Conversation, Task, Submission, Document) wrap a jv object (package durable/core/jv) here: mutators set fields in place,
// so unknown fields survive where Pi's spread keeps them, and a path where Pi builds a fresh object builds a fresh
// record with a New* constructor. Task records carry main's startedAt/endedAt; dcore-session decides when the
// scheduler writes them, from main's source.
//
// Owner: dcore-store (encoders, decoders, key orders). Field semantics are spec-owned; dcore-session and dcore-loop call
// the constructors and mutators and never assemble record JSON themselves.
package rec

// TaskStatus is the persisted task status (spec §5.1), also the tasks.status column.
type TaskStatus uint8

// Task statuses.
const (
	Pending TaskStatus = iota + 1
	Running
	Waiting
	Completing
	Terminal
)

// SubmissionStatus is the persisted submission status (spec §2), also the submissions.status column.
type SubmissionStatus uint8

// Submission statuses.
const (
	Queued SubmissionStatus = iota + 1
	Placed
	Done
	Unanswered
)

// Conversation is a conversation record (spec §2 ConversationRecord).
type Conversation struct{ V JSON }

// Task is a task record (spec §5.1 TaskRecord). Accessors read V; mutators write V in place.
type Task struct{ V JSON }

// Submission is a submission record (spec §2 SubmissionRecord).
type Submission struct{ V JSON }

// Document is a document record (spec §3.2 DocumentRecord).
type Document struct{ V JSON }

// Accessors shared by the rewritten records.
func (t *Task) ID() int64                      { panic("rec.Task.ID: dcore-store") }
func (t *Task) ConversationID() int64          { panic("rec.Task.ConversationID: dcore-store") }
func (t *Task) Kind() string                   { panic("rec.Task.Kind: dcore-store") }
func (t *Task) Status() TaskStatus             { panic("rec.Task.Status: dcore-store") }
func (t *Task) AbortRequested() bool           { panic("rec.Task.AbortRequested: dcore-store") }
func (t *Task) Background() bool               { panic("rec.Task.Background: dcore-store") }
func (t *Task) Checkpoint() JSON               { panic("rec.Task.Checkpoint: dcore-store") }
func (s *Submission) ID() int64                { panic("rec.Submission.ID: dcore-store") }
func (s *Submission) Status() SubmissionStatus { panic("rec.Submission.Status: dcore-store") }
func (c *Conversation) ID() int64              { panic("rec.Conversation.ID: dcore-store") }
func (d *Document) ID() int64                  { panic("rec.Document.ID: dcore-store") }

// JSON is a value of package jv (nil, bool, float64, string, []any or *jv.Object; ECMAScript property order), the
// session lane's ordered JSON model at durable/core/jv.
type JSON = any
