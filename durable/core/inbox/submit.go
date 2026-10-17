// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package inbox

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// Submission types and settlement statuses (src/harness/types.ts SubmissionDraft).
const (
	TypeInput = "input"
	TypeWrite = "write"
)

// ErrConversationBusy matches a *BusyError with errors.Is.
// Read-only: assigned once at initialization.
var ErrConversationBusy = errors.New("ConversationBusy")

// BusyError is Pi's ConversationBusy: a submission reached a busy conversation and was not admitted.
type BusyError struct{ ConversationID session.ID }

func (e *BusyError) Error() string {
	return "Conversation " + jv.FormatNumber(float64(e.ConversationID)) + " is busy"
}

// Is matches ErrConversationBusy.
func (e *BusyError) Is(target error) bool { return target == ErrConversationBusy }

// Draft is a SubmissionDraft: input carries Content, WhenBusy and optionally the InputEntry it is placed as (Entry), a
// write carries Entry, the entry it appends.
type Draft struct {
	RequestID *string
	Type      string
	Content   any
	Entry     *jv.Object
	// WhenBusy is "steer", "followUp" or "reject"; empty queues as followUp.
	WhenBusy string
}

// Runs is what admission needs of the generation task: the conversation's pi.live document and the start of a run for
// placed inputs (startRun in src/harness/generation.ts). The turn package, which depends on this one, implements it.
type Runs interface {
	Live(tx *session.Tx, conversationID session.ID) (*delta.Obj, error)
	Start(tx *session.Tx, conversationID session.ID, live *delta.Obj, inputs []session.ID) error
}

func idOf(record *jv.Object) session.ID {
	id, _ := record.Get("id")
	n, _ := id.(float64)
	return session.ID(n)
}

func submissionRecord(conversationID session.ID, requestID *string, typ, status string) *jv.Object {
	record := jv.NewObject()
	record.Set("conversationId", float64(conversationID))
	if requestID != nil {
		record.Set("requestId", *requestID)
	}
	record.Set("type", typ)
	record.Set("status", status)
	return record
}

// Admit is admitSubmission: it admits a submission inside a commit (spec section 6); Conversation.submit() and
// conversation-owned compactions share it. A known request ID returns its existing submission without writing. A busy
// display-only write (only kind and data) is appended and settled done at once, busy or not, as it changes nothing the
// model sees; only behind a queued reset it queues, so it lands after the reset instead of before it in the context the
// reset ends. A busy conversation queues anything else in pi.inbox, or rejects whenBusy "reject" input with
// ConversationBusy. An idle conversation with queued items queues it behind them and runs a final boundary. Otherwise idle
// input places its entry (pi.user, or the input's own) and starts a run, and an idle write appends its entry and settles
// done, or stale when its head reaches before the active range.
func Admit(tx *session.Tx, runs Runs, conversationID session.ID, draft Draft, now float64, modes QueueModes) (session.ID, error) {
	if draft.Type == TypeInput && draft.Entry != nil {
		if err := checkInputEntry(draft.Entry); err != nil {
			return 0, err
		}
	}
	if draft.RequestID != nil {
		existing, err := tx.SubmissionByRequest(conversationID, *draft.RequestID)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			if typ, _ := existing.Get("type"); typ != draft.Type {
				return 0, errors.New("Request " + *draft.RequestID + " already identifies a submission of type " + asString(typ))
			}
			return idOf(existing), nil
		}
	}
	if draft.Type == TypeWrite && draft.Entry != nil && IsDisplayOnly(draft.Entry) {
		inbox, err := tx.Doc(Def, session.DocArgs{Owner: conversationID})
		if err != nil {
			return 0, err
		}
		if !HasQueuedReset(queued(inbox)) {
			entry, err := tx.AppendEntry(conversationID, draft.Entry)
			if err != nil {
				return 0, err
			}
			record := submissionRecord(conversationID, draft.RequestID, TypeWrite, "done")
			record.Set("entry", float64(idOf(entry)))
			created, err := tx.CreateSubmission(record)
			if err != nil {
				return 0, err
			}
			return idOf(created), nil
		}
	}
	live, err := runs.Live(tx, conversationID)
	if err != nil {
		return 0, err
	}
	busy := live.Has("run")
	if busy && draft.Type == TypeInput && draft.WhenBusy == "reject" {
		return 0, &BusyError{conversationID}
	}
	// A boundary reads the table, so it is prepared before the first table write; a busy conversation needs none.
	var boundary *Boundary
	if !busy {
		if boundary, err = Prepare(tx, conversationID, modes); err != nil {
			return 0, err
		}
		// submissions.ts :169: `await prepareBoundary(...)` resumes one tick after the async function returns.
		tx.Await(1)
	}
	if boundary == nil || queued(boundary.Inbox).Len() > 0 {
		return queueAndPlace(tx, runs, conversationID, draft, now, boundary, live)
	}
	if draft.Type == TypeWrite {
		if boundary.IsStale(draft.Entry) {
			record := submissionRecord(conversationID, draft.RequestID, TypeWrite, "unanswered")
			record.Set("reason", "stale")
			created, err := tx.CreateSubmission(record)
			if err != nil {
				return 0, err
			}
			return idOf(created), nil
		}
		entry, err := tx.AppendEntry(conversationID, draft.Entry)
		if err != nil {
			return 0, err
		}
		record := submissionRecord(conversationID, draft.RequestID, TypeWrite, "done")
		record.Set("entry", float64(idOf(entry)))
		created, err := tx.CreateSubmission(record)
		if err != nil {
			return 0, err
		}
		return idOf(created), nil
	}
	entry, err := tx.AppendEntry(conversationID, inputDraft(draft, now))
	if err != nil {
		return 0, err
	}
	record := submissionRecord(conversationID, draft.RequestID, TypeInput, "placed")
	record.Set("entry", float64(idOf(entry)))
	created, err := tx.CreateSubmission(record)
	if err != nil {
		return 0, err
	}
	id := idOf(created)
	return id, start(tx, runs, conversationID, live, []session.ID{id})
}

// start is `await startRun(...)` (submissions.ts :189, :205): the caller resumes one tick after startRun returns.
func start(tx *session.Tx, runs Runs, conversationID session.ID, live *delta.Obj, inputs []session.ID) error {
	if err := runs.Start(tx, conversationID, live, inputs); err != nil {
		return err
	}
	tx.Await(1)
	return nil
}

// inputDraft is appendInput's draft for an idle input: its own entry's kind and data, or pi.user.
func inputDraft(draft Draft, now float64) *jv.Object {
	if draft.Entry == nil {
		return UserDraft(draft.Content, now)
	}
	return withInputModel(PickInputEntry(draft.Entry), UserDraft(draft.Content, now))
}

// checkInputEntry rejects an input entry whose kind is not a non-empty string outside the pi. prefix, as a TypeError
// (submissions.ts checkInputEntry).
func checkInputEntry(entry *jv.Object) error {
	kind, has := entry.Get("kind")
	if s, ok := kind.(string); !ok || s == "" || strings.HasPrefix(s, "pi.") {
		if !has {
			return session.NewTypeError("An input entry's kind must be a non-empty string not starting with \"pi.\", got undefined")
		}
		return session.NewTypeError("An input entry's kind must be a non-empty string not starting with \"pi.\", got " + jsString(kind))
	}
	return nil
}

// jsString is String(value) for a JSON value.
func jsString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return jv.FormatNumber(v)
	case []any:
		parts := make([]string, len(v))
		for i, element := range v {
			if element != nil {
				parts[i] = jsString(element)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// queueAndPlace queues the submission in pi.inbox; an idle conversation (boundary set) then runs a final boundary.
func queueAndPlace(tx *session.Tx, runs Runs, conversationID session.ID, draft Draft, now float64, boundary *Boundary, live *delta.Obj) (session.ID, error) {
	created, err := tx.CreateSubmission(submissionRecord(conversationID, draft.RequestID, draft.Type, "queued"))
	if err != nil {
		return 0, err
	}
	id := idOf(created)
	var inbox *delta.Obj
	if boundary != nil {
		inbox = boundary.Inbox
	} else if inbox, err = tx.Doc(Def, session.DocArgs{Owner: conversationID}); err != nil {
		return 0, err
	}
	item := jv.NewObject()
	item.Set("id", float64(id))
	if draft.Type == TypeWrite {
		item.Set("mode", "write")
		item.Set("entry", jv.Clone(draft.Entry))
	} else {
		mode := "followUp"
		if draft.WhenBusy == "steer" {
			mode = "steer"
		}
		item.Set("mode", mode)
		item.Set("content", jv.Clone(draft.Content))
		if draft.Entry != nil {
			item.Set("entry", PickInputEntry(draft.Entry))
		}
	}
	if _, err := queued(inbox).Push(item); err != nil {
		return 0, err
	}
	if boundary == nil {
		return id, nil
	}
	result, err := Apply(tx, boundary, true, now)
	if err != nil {
		return 0, err
	}
	tx.Await(1) // submissions.ts :188: `await applyBoundary(...)`
	if len(result.Users) > 0 {
		return id, start(tx, runs, conversationID, live, result.Users)
	}
	return id, nil
}

// AbortResult is the outcome of Abort.
type AbortResult string

// Abort outcomes (submissions.ts AbortResult, plus not_found).
const (
	Aborted       AbortResult = "aborted"
	AlreadyPlaced AbortResult = "already_placed"
	Settled       AbortResult = "settled"
	NotFound      AbortResult = "not_found"
)

// Abort is Submissions.abort: it withdraws a queued submission and removes its inbox item; placed inputs and settled
// submissions are reported. conversationID zero matches any conversation.
func Abort(tx *session.Tx, id, conversationID session.ID) (AbortResult, error) {
	record, err := tx.Submission(id)
	if err != nil {
		return "", err
	}
	if record == nil {
		return NotFound, nil
	}
	owner, _ := record.Get("conversationId")
	ownerID := session.ID(owner.(float64))
	if conversationID != 0 && ownerID != conversationID {
		return NotFound, nil
	}
	switch status, _ := record.Get("status"); status {
	case "queued":
		if err := tx.SettleSubmission(id, Unanswered("aborted")); err != nil {
			return "", err
		}
		return Aborted, RemoveItem(tx, ownerID, id)
	case "placed":
		return AlreadyPlaced, nil
	}
	return Settled, nil
}
