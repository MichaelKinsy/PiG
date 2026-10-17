// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package rec

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// Submission is a submission record (spec §2 SubmissionRecord): `{id, conversationId, requestId?, type, status, entry?,
// answer?, reason?, detail?}` with the admission code's key order. Mutators return a modified shallow copy.
type Submission struct{ V *jv.Object }

// ParseSubmission reads a stored submission record.
func ParseSubmission(b []byte) (*Submission, error) {
	o, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	return SubmissionOf(o)
}

// SubmissionOf reads a submission record that is already a value: ParseSubmission without the text, with the same checks.
func SubmissionOf(o *jv.Object) (*Submission, error) {
	if o == nil {
		return nil, ErrShape
	}
	s := &Submission{V: o}
	if _, ok := idOf(o, "id"); !ok {
		return nil, ErrShape
	}
	if _, ok := idOf(o, "conversationId"); !ok {
		return nil, ErrShape
	}
	if s.Status() == 0 {
		return nil, ErrShape
	}
	return s, nil
}

// Append appends the record's JSON.stringify.
func (s *Submission) Append(dst []byte) []byte { return jv.Append(dst, s.V) }

// ID is the submission ID.
func (s *Submission) ID() int64 { n, _ := idOf(s.V, "id"); return n }

// ConversationID is the conversation the submission was admitted to.
func (s *Submission) ConversationID() int64 { n, _ := idOf(s.V, "conversationId"); return n }

// Type is "input" or "write".
func (s *Submission) Type() string { t, _ := stringOf(s.V, "type"); return t }

// Status is the persisted status; 0 when the record has none.
func (s *Submission) Status() SubmissionStatus {
	name, _ := stringOf(s.V, "status")
	st, _ := ParseSubmissionStatus(name)
	return st
}

// RequestID is the host deduplication key; ok is false when absent.
func (s *Submission) RequestID() (string, bool) { return stringOf(s.V, "requestId") }

// RequestColumn is the submissions.request_id column: JSON.stringify(requestId), ok false for NULL.
func (s *Submission) RequestColumn() (string, bool) {
	r, ok := s.RequestID()
	if !ok {
		return "", false
	}
	return IndexedString(r), true
}

// Entry is the placed or answered-input entry; ok is false when absent.
func (s *Submission) Entry() (int64, bool) { return idOf(s.V, "entry") }

// Answer is the answering entry of a done input; ok is false when absent.
func (s *Submission) Answer() (int64, bool) { return idOf(s.V, "answer") }

// Reason is the reason of an unanswered submission.
func (s *Submission) Reason() (string, bool) { return stringOf(s.V, "reason") }
