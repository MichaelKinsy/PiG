// SPDX-License-Identifier: MIT

// Package rec is the record layer for the records the core rewrites: pi-durable's conversation, task, submission and
// document records (spec §2, §3.2, §5.1, §6), written in pi-durable main da866ada's shapes and read tolerantly from
// 1.0.4 and newer stores (CONTRACT section 2.3).
//
// Entries are immutable bytes and belong to package history (history.AppendEntryRecord, history.EntryRow). The records
// the core rewrites (Conversation, Task, Submission, Document) wrap a jv object (package durable/core/jv): mutators
// return a shallow copy with the field set in place, so unknown fields survive where Pi's spread keeps them (`{...record,
// state}`), and a path where Pi builds a fresh object builds a fresh record with a New* constructor. Task records carry
// main's startedAt/endedAt (StampTimes is src/session/transaction.ts #stampTimes); records written by 1.0.4 lack them.
//
// Every record also exposes the indexed columns pi-durable's SqliteStorage derives from it (kind, status, request ID and
// document address, with the `JSON.stringify` encoding of indexed text, storage.ts encodeIndexedString), so the store
// writes Pi's rows from one place.
//
// Owner: dcore-store (encoders, decoders, key orders). Field semantics are spec-owned; dcore-session and dcore-loop call
// the constructors and mutators and never assemble record JSON themselves.
package rec

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// ErrShape reports a stored record that lacks a field the core indexes. Read-only.
var ErrShape = errors.New("rec: record has an unexpected shape")

// TaskStatus is the persisted task status (spec §5.1), also the tasks.status column.
type TaskStatus uint8

// Task statuses.
const (
	Pending TaskStatus = iota + 1
	Running
	Waiting
	Sleeping
	Completing
	Terminal
)

// taskStatusNames is read-only.
var taskStatusNames = [...]string{"", "pending", "running", "waiting", "sleeping", "completing", "terminal"}

// String returns the persisted name ("pending", ...), or "" for an invalid status.
func (s TaskStatus) String() string {
	if int(s) < len(taskStatusNames) {
		return taskStatusNames[s]
	}
	return ""
}

// ParseTaskStatus is the inverse of String; ok is false for an unknown name.
func ParseTaskStatus(name string) (TaskStatus, bool) {
	for i := 1; i < len(taskStatusNames); i++ {
		if taskStatusNames[i] == name {
			return TaskStatus(i), true
		}
	}
	return 0, false
}

// SubmissionStatus is the persisted submission status (spec §2), also the submissions.status column.
type SubmissionStatus uint8

// Submission statuses.
const (
	Queued SubmissionStatus = iota + 1
	Placed
	Done
	Unanswered
)

// submissionStatusNames is read-only.
var submissionStatusNames = [...]string{"", "queued", "placed", "done", "unanswered"}

// String returns the persisted name ("queued", ...), or "" for an invalid status.
func (s SubmissionStatus) String() string {
	if int(s) < len(submissionStatusNames) {
		return submissionStatusNames[s]
	}
	return ""
}

// ParseSubmissionStatus is the inverse of String; ok is false for an unknown name.
func ParseSubmissionStatus(name string) (SubmissionStatus, bool) {
	for i := 1; i < len(submissionStatusNames); i++ {
		if submissionStatusNames[i] == name {
			return SubmissionStatus(i), true
		}
	}
	return 0, false
}

// JSON is a value of package jv (nil, bool, float64, string, []any or *jv.Object; ECMAScript property order).
type JSON = any

// IndexedString is storage.ts encodeIndexedString: indexed text columns hold JSON.stringify(value), quotes included,
// because some SQLite bindings replace lone UTF-16 surrogates.
func IndexedString(s string) string { return string(jv.AppendString(nil, s)) }

func parseObject(b []byte) (*jv.Object, error) {
	v, err := jv.Parse(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jv.Object)
	if !ok {
		return nil, ErrShape
	}
	return o, nil
}

// idOf reads a safe-integer field.
func idOf(o *jv.Object, key string) (int64, bool) {
	v, _ := o.Get(key)
	f, ok := v.(float64)
	if !ok || f != float64(int64(f)) || f > 9007199254740991 || f < -9007199254740991 {
		return 0, false
	}
	return int64(f), true
}

func stringOf(o *jv.Object, key string) (string, bool) {
	v, _ := o.Get(key)
	s, ok := v.(string)
	return s, ok
}

func objectOf(o *jv.Object, key string) *jv.Object {
	v, _ := o.Get(key)
	r, _ := v.(*jv.Object)
	return r
}

func boolOf(o *jv.Object, key string) bool {
	v, _ := o.Get(key)
	b, _ := v.(bool)
	return b
}

// with returns {...o, key: value}: an existing key keeps its position, a new key is appended.
func with(o *jv.Object, kv ...any) *jv.Object {
	c := o.Shallow()
	for i := 0; i+1 < len(kv); i += 2 {
		c.Set(kv[i].(string), kv[i+1])
	}
	return c
}

func id(v int64) float64 { return float64(v) }
