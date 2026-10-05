// Ports packages/durable/src/harness/submissions.ts.

package harness

import (
	"context"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// commitOnLine runs change on the Session line and returns its typed result.
func commitOnLine[T any](ctx context.Context, line *session.SessionImpl, scope session.TransactionScope, change func(tx *session.Transaction) (T, error)) (T, error) {
	var zero T
	value, err := line.CommitWith(ctx, func(tx *session.Transaction) (any, error) { return change(tx) }, scope)
	if err != nil {
		return zero, err
	}
	typed, _ := value.(T)
	return typed, nil
}

// readOnLine runs read on the Session line and returns its typed result.
func readOnLine[T any](line *session.SessionImpl, read func() (T, error)) (T, error) {
	var zero T
	value, err := line.ReadOnLine(func() (any, error) { return read() })
	if err != nil {
		return zero, err
	}
	typed, _ := value.(T)
	return typed, nil
}

// Submissions is admission, waits, and withdrawal of the durable submissions of one Harness (submissions.ts:21-118).
type Submissions struct {
	line    *session.SessionImpl
	storage durable.Storage
	now     func() float64
	// queueModes is read at each admission, on the Session line.
	queueModes func() QueueModes
	// resume enables task scheduling; submitting or waiting asks for progress.
	resume  func()
	waiters Waiters[durable.SubmissionId, durable.SettledSubmissionRecord]
	// closeMu makes Wait's closed check and waiter registration one step with respect to close, which upstream's single thread gives for free: a waiter registered after RejectAll would never settle.
	closeMu sync.Mutex
	closed  bool
}

// NewSubmissions subscribes to the Session's commits and close.
func NewSubmissions(line *session.SessionImpl, storage durable.Storage, now func() float64, queueModes func() QueueModes, resume func()) *Submissions {
	submissions := &Submissions{line: line, storage: storage, now: now, queueModes: queueModes, resume: resume}
	line.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) { submissions.observe(publication) })
	line.SubscribeClose(func() {
		submissions.closeMu.Lock()
		submissions.closed = true
		submissions.closeMu.Unlock()
		submissions.waiters.RejectAll(ErrClosed)
	})
	return submissions
}

// Submit admits a submission in one commit; see AdmitSubmission.
func (submissions *Submissions) Submit(ctx context.Context, conversationId durable.ConversationId, draft durable.SubmissionDraft) (durable.Submission, error) {
	submissions.resume()
	id, err := commitOnLine(ctx, submissions.line, session.TransactionScope{}, func(tx *session.Transaction) (durable.SubmissionId, error) {
		return AdmitSubmission(tx, conversationId, draft, submissions.now(), submissions.queueModes())
	})
	if err != nil {
		return nil, err
	}
	return &submissionHandle{id: id, submissions: submissions}, nil
}

// Get returns a handle for an existing submission; nil when absent.
func (submissions *Submissions) Get(ctx context.Context, id durable.SubmissionId) (durable.Submission, error) {
	record, err := readOnLine(submissions.line, func() (*durable.SubmissionRecord, error) {
		return submissions.storage.Submission(ctx, id)
	})
	if err != nil || record == nil {
		return nil, err
	}
	return &submissionHandle{id: record.Id, submissions: submissions}, nil
}

// Status returns the committed record of a submission.
func (submissions *Submissions) Status(ctx context.Context, id durable.SubmissionId) (durable.SubmissionRecord, error) {
	record, err := readOnLine(submissions.line, func() (*durable.SubmissionRecord, error) {
		return submissions.storage.Submission(ctx, id)
	})
	if err != nil {
		return durable.SubmissionRecord{}, err
	}
	if record == nil {
		return durable.SubmissionRecord{}, fmt.Errorf("Submission %d does not exist", id)
	}
	return *record, nil
}

// Wait returns the settled record of a submission; cancelling ctx cancels only this wait.
func (submissions *Submissions) Wait(ctx context.Context, id durable.SubmissionId) (durable.SettledSubmissionRecord, error) {
	submissions.resume()
	// Check and register on the line so no settling publication falls between them.
	type found struct {
		record *durable.SubmissionRecord
		waiter *Waiter[durable.SettledSubmissionRecord]
	}
	result, err := readOnLine(submissions.line, func() (found, error) {
		record, err := submissions.storage.Submission(ctx, id)
		if err != nil {
			return found{}, err
		}
		if record == nil {
			return found{}, fmt.Errorf("Submission %d does not exist", id)
		}
		if isSettledSubmission(*record) {
			return found{record: record}, nil
		}
		// Close rejects registered waiters synchronously and may begin during the read.
		submissions.closeMu.Lock()
		defer submissions.closeMu.Unlock()
		if submissions.closed {
			return found{}, ErrClosed
		}
		return found{waiter: submissions.waiters.Add(ctx, id)}, nil
	})
	if err != nil {
		return durable.SubmissionRecord{}, err
	}
	if result.record != nil {
		return *result.record, nil
	}
	return result.waiter.Wait()
}

// Abort withdraws a queued submission and removes its inbox item; placed inputs and settled submissions are reported.
// conversationId, when given, must own the submission.
func (submissions *Submissions) Abort(ctx context.Context, id durable.SubmissionId, conversationId *durable.ConversationId) (durable.SubmissionAbortResult, error) {
	return commitOnLine(ctx, submissions.line, session.TransactionScope{}, func(tx *session.Transaction) (durable.SubmissionAbortResult, error) {
		record, err := tx.Submission(id)
		if err != nil {
			return "", err
		}
		if record == nil || (conversationId != nil && record.ConversationId != *conversationId) {
			return durable.SubmissionNotFound, nil
		}
		if record.Status == durable.SubmissionQueued {
			if err := tx.SettleSubmission(id, durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "aborted"}); err != nil {
				return "", err
			}
			if err := RemoveInboxItem(tx, record.ConversationId, id); err != nil {
				return "", err
			}
			return durable.SubmissionAborted, nil
		}
		if record.Status == durable.SubmissionPlaced {
			return durable.SubmissionAlreadyPlaced, nil
		}
		return durable.SubmissionSettled, nil
	})
}

func (submissions *Submissions) observe(publication durable.CommitPublication) {
	for _, change := range publication.Changes {
		write, ok := change.(durable.SubmissionWrite)
		if !ok || !isSettledSubmission(write.Value) {
			continue
		}
		submissions.waiters.Resolve(write.Value.Id, write.Value)
	}
}

type submissionHandle struct {
	id          durable.SubmissionId
	submissions *Submissions
}

func (handle *submissionHandle) Id() durable.SubmissionId { return handle.id }

func (handle *submissionHandle) Status(ctx context.Context) (durable.SubmissionRecord, error) {
	return handle.submissions.Status(ctx, handle.id)
}

func (handle *submissionHandle) Wait(ctx context.Context) (durable.SettledSubmissionRecord, error) {
	return handle.submissions.Wait(ctx, handle.id)
}

func (handle *submissionHandle) Abort(ctx context.Context) (durable.SubmissionAbortResult, error) {
	result, err := handle.submissions.Abort(ctx, handle.id, nil)
	if err != nil {
		return "", err
	}
	if result == durable.SubmissionNotFound {
		return "", fmt.Errorf("Submission %d does not exist", handle.id)
	}
	return result, nil
}

func isSettledSubmission(record durable.SubmissionRecord) bool {
	return record.Status == durable.SubmissionDone || record.Status == durable.SubmissionUnanswered
}

// AdmitSubmission admits a submission inside a commit (spec §6, submissions.ts:136-207); Conversation.Submit and
// conversation-owned compactions share it. A known request ID returns its existing submission without writing. A busy
// conversation queues it in pi.inbox, or rejects whenBusy reject input with ConversationBusy. An idle conversation with
// queued items queues it behind them and runs a final boundary. Otherwise idle input places a user entry and starts a
// run, and an idle write appends its entry and settles done, or stale when its head reaches before the active range.
func AdmitSubmission(tx durable.Tx, conversationId durable.ConversationId, draft durable.SubmissionDraft, now float64, queueModes QueueModes) (durable.SubmissionId, error) {
	if draft.RequestId != nil {
		existing, err := tx.SubmissionByRequest(conversationId, *draft.RequestId)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			if existing.Type != draft.Type {
				return 0, fmt.Errorf("Request %s already identifies a submission of type %s", *draft.RequestId, existing.Type)
			}
			return existing.Id, nil
		}
	}
	live, err := docDraft(tx, LiveDoc, conversationId)
	if err != nil {
		return 0, err
	}
	busy := live.Has("run")
	if busy && draft.Type == durable.SubmissionTypeInput && draft.WhenBusy == durable.WhenBusyReject {
		return 0, durable.NewConversationBusy(conversationId)
	}
	// A boundary reads the table, so it is prepared before the first table write; a busy one needs none.
	var boundary *Boundary
	if !busy {
		boundary, err = PrepareBoundary(tx, conversationId, queueModes)
		if err != nil {
			return 0, err
		}
	}
	queuedItems := false
	if boundary != nil {
		items := boundary.Inbox.Array("items")
		queuedItems = items != nil && items.Len() > 0
	}
	if boundary == nil || queuedItems {
		record, err := tx.CreateSubmission(durable.SubmissionCreate{ConversationId: conversationId, RequestId: draft.RequestId, Type: draft.Type, Status: durable.SubmissionQueued})
		if err != nil {
			return 0, err
		}
		id := record.Id
		var item InboxItem
		if draft.Type == durable.SubmissionTypeWrite {
			entry, err := durable.ToJsonObject(*draft.Entry)
			if err != nil {
				return 0, err
			}
			item = InboxItem{Id: id, Mode: InboxWrite, Entry: entry}
		} else {
			content, err := durable.ToJsonValue(draft.Content)
			if err != nil {
				return 0, err
			}
			mode := InboxFollowUp
			if draft.WhenBusy == durable.WhenBusySteer {
				mode = InboxSteer
			}
			item = InboxItem{Id: id, Mode: mode, Content: content}
		}
		inbox := live
		if boundary != nil {
			inbox = boundary.Inbox
		} else if inbox, err = docDraft(tx, InboxDoc, conversationId); err != nil {
			return 0, err
		}
		if err := pushJSON(inbox.Array("items"), item); err != nil {
			return 0, err
		}
		if boundary == nil {
			return id, nil
		}
		placed, err := ApplyBoundary(tx, boundary, BoundaryFinal, now)
		if err != nil {
			return 0, err
		}
		if len(placed.Users) > 0 {
			if err := StartRun(tx, conversationId, live, placed.Users); err != nil {
				return 0, err
			}
		}
		return id, nil
	}
	if draft.Type == durable.SubmissionTypeWrite {
		if IsStale(boundary, *draft.Entry) {
			reason := "stale"
			record, err := tx.CreateSubmission(durable.SubmissionCreate{ConversationId: conversationId, RequestId: draft.RequestId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionUnanswered, Reason: &reason})
			if err != nil {
				return 0, err
			}
			return record.Id, nil
		}
		entry, err := tx.AppendEntry(conversationId, *draft.Entry)
		if err != nil {
			return 0, err
		}
		id := entry.Id
		record, err := tx.CreateSubmission(durable.SubmissionCreate{ConversationId: conversationId, RequestId: draft.RequestId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &id})
		if err != nil {
			return 0, err
		}
		return record.Id, nil
	}
	message := ai.UserMessage{Content: draft.Content, Timestamp: int64(now)}
	entry, err := durable.TxAppendEntry(tx, durable.UserEntry, conversationId, durable.TypedEntryDraft[durable.Never]{Model: []ai.Message{message}})
	if err != nil {
		return 0, err
	}
	entryId := entry.Id
	record, err := tx.CreateSubmission(durable.SubmissionCreate{ConversationId: conversationId, RequestId: draft.RequestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionPlaced, Entry: &entryId})
	if err != nil {
		return 0, err
	}
	if err := StartRun(tx, conversationId, live, []durable.SubmissionId{record.Id}); err != nil {
		return 0, err
	}
	return record.Id, nil
}
