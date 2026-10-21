// Ports packages/durable/src/harness/submissions.ts.

package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/telemetry"
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
	telemetry telemetry.TelemetryContext
	line      *session.SessionImpl
	storage   durable.Storage
	now       func() float64
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
// telemetry is the Harness's root adapter, the parent of input admissions; nil is the no-op context.
func NewSubmissions(line *session.SessionImpl, storage durable.Storage, now func() float64, queueModes func() QueueModes, resume func(), root telemetry.TelemetryContext) *Submissions {
	if root == nil {
		root = telemetry.NoopTelemetryContext
	}
	submissions := &Submissions{telemetry: root, line: line, storage: storage, now: now, queueModes: queueModes, resume: resume}
	line.ObserveCommits(func(ctx context.Context, publication durable.CommitPublication) {
		submissions.observe(publication)
	})
	line.SubscribeClose(func() {
		submissions.closeMu.Lock()
		submissions.closed = true
		submissions.closeMu.Unlock()
		submissions.waiters.RejectAll(closedError(line))
	})
	return submissions
}

// Submit admits a submission in one commit; see AdmitSubmission.
func (submissions *Submissions) Submit(ctx context.Context, conversationId durable.ConversationId, draft durable.SubmissionDraft) (durable.Submission, error) {
	submissions.resume()
	parent := submissions.telemetry
	if draft.Type != durable.SubmissionTypeInput {
		parent = chordctx.Telemetry(ctx)
	}
	submit := func(span telemetry.TelemetrySpan) (durable.Submission, error) {
		traced := span != telemetry.NoopTelemetryContext
		if traced && draft.Type == durable.SubmissionTypeInput {
			span.AddLink(telemetry.SpanLink{Context: chordctx.Telemetry(ctx)})
		}
		admission, err := commitOnLine(chordctx.WithTelemetry(ctx, span), submissions.line, session.TransactionScope{}, func(tx *session.Transaction) (Admission, error) {
			return AdmitSubmission(tx, conversationId, draft, submissions.now(), submissions.queueModes(), span)
		})
		if err != nil {
			// Error text may contain caller content. Record only the status.
			span.SetStatus(telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError})
			return nil, err
		}
		if traced {
			span.SetAttributes(telemetry.SpanAttributes{telemetry.Attr("submission.id", float64(admission.Id)), telemetry.Attr("result", admission.Status)})
		}
		return &submissionHandle{id: admission.Id, submissions: submissions}, nil
	}
	// The no-op parent starts the no-op span and drops the attributes, so none are built for it.
	if parent == telemetry.NoopTelemetryContext {
		return submit(telemetry.NoopTelemetryContext)
	}
	return telemetry.StartTypedSpan(telemetry.CreateTypedSpanStarter(parent, DurableTelemetrySchema), "pi.durable.submit", telemetry.SpanAttributes{
		telemetry.Attr("submission.type", string(draft.Type)),
		telemetry.Attr("conversation.id", float64(conversationId)),
	}, func(span telemetry.TelemetrySpan, _ telemetry.TypedSpanStarter) (durable.Submission, error) {
		return submit(span)
	})
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

// Wait returns the settled record of a submission; cancelling ctx cancels only this wait, including its wait for the
// listeners of the batches committed before it to be entered, which it resumes its caller after as upstream's drain
// microtask runs before the code that awaited the promise.
func (submissions *Submissions) Wait(ctx context.Context, id durable.SubmissionId) (durable.SettledSubmissionRecord, error) {
	record, err := submissions.waitSettled(ctx, id)
	if err != nil {
		return record, err
	}
	if err := submissions.line.AwaitDeliveriesEntered(ctx); err != nil {
		return durable.SubmissionRecord{}, err
	}
	return record, nil
}

func (submissions *Submissions) waitSettled(ctx context.Context, id durable.SubmissionId) (durable.SettledSubmissionRecord, error) {
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
			return found{}, closedError(submissions.line)
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
		submissions.line.DeferUntilPublished(func() { submissions.waiters.Resolve(write.Value.Id, write.Value) })
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

// checkInputEntry rejects an input entry whose kind is not a non-empty string outside the pi. prefix with a TypeError
// (submissions.ts checkInputEntry).
func checkInputEntry(entry durable.InputEntry) error {
	if entry.Kind == "" || strings.HasPrefix(entry.Kind, "pi.") {
		return &TypeError{Message: "An input entry's kind must be a non-empty string not starting with \"pi.\", got " + entry.Kind}
	}
	return nil
}

// Admission is what AdmitSubmission reports: the submission and how it was admitted ("queued", "placed", or "done"; a
// stale write reports done).
type Admission struct {
	Id     durable.SubmissionId
	Status string
}

// injectTrace is `span?.propagation?.inject(span)`: the carrier of the admission span, nil without propagation.
func injectTrace(span telemetry.TelemetrySpan) telemetry.TelemetryCarrier {
	if span == nil {
		return nil
	}
	propagation := span.Propagation()
	if propagation == nil {
		return nil
	}
	return propagation.Inject(span)
}

// AdmitSubmission admits a submission inside a commit (spec §6, submissions.ts:136-207); Conversation.Submit and
// conversation-owned compactions share it. A known request ID returns its existing submission without writing. A
// display-only write (only kind and data) is appended and settled done at once, busy or not, as it changes nothing the
// model sees; only behind a queued reset it queues, so it lands after the reset instead of before it in the context the
// reset ends. A busy conversation queues anything else in pi.inbox, or rejects whenBusy reject input with
// ConversationBusy. An idle conversation with queued items queues it behind them and runs a final boundary. Otherwise
// idle input places its entry (pi.user, or the input's own) and starts a run, and an idle write appends its entry and
// settles done, or stale when its head reaches before the active range.
func AdmitSubmission(tx durable.Tx, conversationId durable.ConversationId, draft durable.SubmissionDraft, now float64, queueModes QueueModes, span telemetry.TelemetrySpan) (Admission, error) {
	if draft.Type == durable.SubmissionTypeInput && draft.InputEntry != nil {
		if err := checkInputEntry(*draft.InputEntry); err != nil {
			return Admission{}, err
		}
	}
	if draft.RequestId != nil {
		existing, err := tx.SubmissionByRequest(conversationId, *draft.RequestId)
		if err != nil {
			return Admission{}, err
		}
		if existing != nil {
			if existing.Type != draft.Type {
				return Admission{}, fmt.Errorf("Request %s already identifies a submission of type %s", *draft.RequestId, existing.Type)
			}
			status := string(existing.Status)
			if existing.Status == durable.SubmissionUnanswered {
				status = string(durable.SubmissionDone)
			}
			return Admission{Id: existing.Id, Status: status}, nil
		}
	}
	trace := injectTrace(span)
	if draft.Type == durable.SubmissionTypeWrite && IsDisplayOnly(*draft.Entry) {
		draftInbox, err := docDraft(tx, InboxDoc, conversationId)
		if err != nil {
			return Admission{}, err
		}
		inbox, err := decodeDraft[InboxState](draftInbox)
		if err != nil {
			return Admission{}, err
		}
		if !HasQueuedReset(inbox.Items) {
			entry, err := tx.AppendEntry(conversationId, *draft.Entry)
			if err != nil {
				return Admission{}, err
			}
			id := entry.Id
			record, err := tx.CreateSubmission(durable.SubmissionCreate{Trace: trace, ConversationId: conversationId, RequestId: draft.RequestId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &id})
			if err != nil {
				return Admission{}, err
			}
			return Admission{Id: record.Id, Status: string(durable.SubmissionDone)}, nil
		}
	}
	live, err := docDraft(tx, LiveDoc, conversationId)
	if err != nil {
		return Admission{}, err
	}
	busy := live.Has("run")
	if draft.Type == durable.SubmissionTypeInput && draft.WhenBusy == durable.WhenBusySteer && busy && span != nil {
		if propagation := span.Propagation(); propagation != nil {
			if owner, ok := runTaskOf(live); ok {
				run, err := tx.Task(owner)
				if err != nil {
					return Admission{}, err
				}
				if run != nil && run.Trace != nil {
					span.AddLink(telemetry.SpanLink{Context: propagation.Extract(run.Trace)})
				}
			}
		}
	}
	if busy && draft.Type == durable.SubmissionTypeInput && draft.WhenBusy == durable.WhenBusyReject {
		return Admission{}, durable.NewConversationBusy(conversationId)
	}
	// A boundary reads the table, so it is prepared before the first table write; a busy one needs none.
	var boundary *Boundary
	if !busy {
		boundary, err = PrepareBoundary(tx, conversationId, queueModes)
		if err != nil {
			return Admission{}, err
		}
	}
	queuedItems := false
	if boundary != nil {
		items := boundary.Inbox.Array("items")
		queuedItems = items != nil && items.Len() > 0
	}
	if boundary == nil || queuedItems {
		record, err := tx.CreateSubmission(durable.SubmissionCreate{Trace: trace, ConversationId: conversationId, RequestId: draft.RequestId, Type: draft.Type, Status: durable.SubmissionQueued})
		if err != nil {
			return Admission{}, err
		}
		id := record.Id
		var item InboxItem
		if draft.Type == durable.SubmissionTypeWrite {
			entry, err := durable.ToJsonObject(*draft.Entry)
			if err != nil {
				return Admission{}, err
			}
			item = InboxItem{Id: id, Mode: InboxWrite, Entry: entry}
		} else {
			content, err := durable.ToJsonValue(draft.Content)
			if err != nil {
				return Admission{}, err
			}
			mode := InboxFollowUp
			if draft.WhenBusy == durable.WhenBusySteer {
				mode = InboxSteer
			}
			item = InboxItem{Id: id, Mode: mode, Content: content}
			if draft.InputEntry != nil {
				if item.Entry, err = pickInputEntry(*draft.InputEntry); err != nil {
					return Admission{}, err
				}
			}
		}
		inbox := live
		if boundary != nil {
			inbox = boundary.Inbox
		} else if inbox, err = docDraft(tx, InboxDoc, conversationId); err != nil {
			return Admission{}, err
		}
		if err := pushJSON(inbox.Array("items"), item); err != nil {
			return Admission{}, err
		}
		if boundary == nil {
			return Admission{Id: id, Status: "queued"}, nil
		}
		placed, err := ApplyBoundary(tx, boundary, BoundaryFinal, now)
		if err != nil {
			return Admission{}, err
		}
		if len(placed.Users) > 0 {
			if err := StartRun(tx, conversationId, live, placed.Users); err != nil {
				return Admission{}, err
			}
		}
		if draft.Type == durable.SubmissionTypeWrite {
			return Admission{Id: id, Status: string(durable.SubmissionDone)}, nil
		}
		if len(placed.Users) > 0 && placed.Users[len(placed.Users)-1] == id {
			return Admission{Id: id, Status: string(durable.SubmissionPlaced)}, nil
		}
		return Admission{Id: id, Status: "queued"}, nil
	}
	if draft.Type == durable.SubmissionTypeWrite {
		if IsStale(boundary, *draft.Entry) {
			reason := "stale"
			record, err := tx.CreateSubmission(durable.SubmissionCreate{Trace: trace, ConversationId: conversationId, RequestId: draft.RequestId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionUnanswered, Reason: &reason})
			if err != nil {
				return Admission{}, err
			}
			return Admission{Id: record.Id, Status: string(durable.SubmissionDone)}, nil
		}
		entry, err := tx.AppendEntry(conversationId, *draft.Entry)
		if err != nil {
			return Admission{}, err
		}
		id := entry.Id
		record, err := tx.CreateSubmission(durable.SubmissionCreate{Trace: trace, ConversationId: conversationId, RequestId: draft.RequestId, Type: durable.SubmissionTypeWrite, Status: durable.SubmissionDone, Entry: &id})
		if err != nil {
			return Admission{}, err
		}
		return Admission{Id: record.Id, Status: string(durable.SubmissionDone)}, nil
	}
	entry, err := appendInput(tx, conversationId, draft.Content, draft.InputEntry, now)
	if err != nil {
		return Admission{}, err
	}
	entryId := entry.Id
	record, err := tx.CreateSubmission(durable.SubmissionCreate{Trace: trace, ConversationId: conversationId, RequestId: draft.RequestId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionPlaced, Entry: &entryId})
	if err != nil {
		return Admission{}, err
	}
	if err := StartRun(tx, conversationId, live, []durable.SubmissionId{record.Id}); err != nil {
		return Admission{}, err
	}
	return Admission{Id: record.Id, Status: string(durable.SubmissionPlaced)}, nil
}
