package durabletest

// Ports packages/durable/src/testing/storage-conformance.ts

import (
	"context"
	"fmt"
	"slices"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

var conformanceContext = context.Background()

type storedTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

// caseFailure carries an unexpected storage error out of a case, as an upstream rejection ends the case.
type caseFailure struct{ err error }

func must[T any](value T, err error) T {
	if err != nil {
		panic(caseFailure{err: err})
	}
	return value
}

func must3[A, B any](first A, second B, err error) (A, B) {
	if err != nil {
		panic(caseFailure{err: err})
	}
	return first, second
}

// roundTripCursor returns the cursor as a caller holding it across a process boundary sees it.
func roundTripCursor(cursor durable.Cursor) durable.Cursor {
	encoded := must(json.Marshal(cursor))
	var roundTripped durable.Cursor
	check(json.Unmarshal(encoded, &roundTripped))
	return roundTripped
}

func check(err error) {
	if err != nil {
		panic(caseFailure{err: err})
	}
}

func mint[I durable.Id](storage durable.Storage) I {
	return durable.IdFromNumber[I](must(storage.MintId()))
}

func commit(storage durable.Storage, writes ...durable.StorageWrite) durable.Seq {
	return must(storage.Commit(conformanceContext, writes))
}

func commitErr(storage durable.Storage, writes ...durable.StorageWrite) error {
	_, err := storage.Commit(conformanceContext, writes)
	return err
}

func createRoot(storage durable.Storage) durable.ConversationId {
	commit(storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}})
	return durable.ROOT_CONVERSATION_ID
}

func pendingTask(id durable.TaskId, conversationId durable.ConversationId, phase string) storedTask {
	return storedTask{
		Id:             id,
		ConversationId: conversationId,
		Kind:           "test.task",
		Version:        1,
		Input:          map[string]any{"value": int64(id)},
		State:          durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: new(durable.JsonValue(map[string]any{"phase": phase}))},
	}
}

func entry(id durable.EntryId, conversationId durable.ConversationId, kind string) durable.EntryRecord {
	return durable.EntryRecord{Id: id, ConversationId: conversationId, Kind: kind}
}

func entryWith(record durable.EntryRecord, edit func(*durable.EntryRecord)) durable.EntryRecord {
	edit(&record)
	return record
}

func entryWrite(record durable.EntryRecord) durable.StorageWrite {
	return durable.EntryWrite{Value: record}
}
func taskWrite(record storedTask) durable.StorageWrite { return durable.TaskWrite{Value: record} }
func submissionWrite(record durable.SubmissionRecord) durable.StorageWrite {
	return durable.SubmissionWrite{Value: record}
}

func conversationScope(id durable.ConversationId) durable.DocumentRecordScope {
	return durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: id}
}

var sessionScope = durable.DocumentRecordScope{Kind: durable.ScopeSession}

func base(version int, value durable.JsonObject) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentBase, Version: version, Value: value}
}

func delta(version int, ops ...durable.Op) durable.DocumentContent {
	if ops == nil {
		ops = []durable.Op{}
	}
	return durable.DocumentContent{Kind: durable.ContentDelta, Version: version, Ops: ops}
}

func op(values ...any) durable.Op { return durable.Op(values) }

func path(segments ...any) []any { return segments }

// entryView is the upstream `{ entry, commitSeq }` result shape; nil stays undefined.
func entryView(found *durable.EntryAt) any {
	if found == nil {
		return nil
	}
	return map[string]any{"entry": found.Entry, "commitSeq": found.CommitSeq}
}

func entryViewOf(record durable.EntryRecord, commitSeq durable.Seq) any {
	return map[string]any{"entry": record, "commitSeq": commitSeq}
}

// storedView is the upstream StoredDocument shape; nil stays undefined.
func storedView(stored *durable.StoredDocument) any {
	if stored == nil {
		return nil
	}
	return map[string]any{
		"record":          stored.Record,
		"version":         stored.Version,
		"value":           stored.Value,
		"deltasSinceBase": stored.DeltasSinceBase,
	}
}

func ids[T any](items []T, id func(T) int64) []int64 {
	out := make([]int64, len(items))
	for index, item := range items {
		out[index] = id(item)
	}
	return out
}

func conversationIds(items []durable.ConversationRecord) []int64 {
	return ids(items, func(record durable.ConversationRecord) int64 { return int64(record.Id) })
}

func entryIds(items []durable.EntryRecord) []int64 {
	return ids(items, func(record durable.EntryRecord) int64 { return int64(record.Id) })
}

func taskIds(items []storedTask) []int64 {
	return ids(items, func(record storedTask) int64 { return int64(record.Id) })
}

func documentIds(items []durable.DocumentRecord) []int64 {
	return ids(items, func(record durable.DocumentRecord) int64 { return int64(record.Id) })
}

func int64s[I durable.Id](values ...I) []int64 {
	out := make([]int64, len(values))
	for index, value := range values {
		out[index] = int64(value)
	}
	return out
}

func cursorView(cursor *durable.Cursor) any {
	if cursor == nil {
		return nil
	}
	return *cursor
}

// expectation mirrors the subset of Vitest's expect API the conformance cases use.
type expectation struct {
	assertions StorageConformanceAssertions
	actual     any
}

func (e expectation) toBe(expected any)       { e.assertions.StrictEqual(e.actual, expected) }
func (e expectation) toEqual(expected any)    { e.assertions.DeepEqual(e.actual, expected) }
func (e expectation) toMatchObject(value any) { e.assertions.PartialDeepEqual(e.actual, value) }
func (e expectation) toBeUndefined()          { e.assertions.StrictEqual(e.actual, nil) }
func (e expectation) toBeDefined() {
	e.assertions.Ok(Normalize(e.actual) != nil, "Expected value to be defined")
}

func (e expectation) toBeGreaterThan(expected float64) {
	number, _ := Normalize(e.actual).(float64)
	e.assertions.GreaterThan(number, expected)
}

func (e expectation) toHaveLength(expected int) {
	items, _ := Normalize(e.actual).([]any)
	e.assertions.StrictEqual(len(items), expected)
}

type conformanceTest func(storage durable.Storage, expect func(actual any) expectation, rejects func(err error, messageIncludes string))

func createCase(options StorageConformanceOptions, name string, test conformanceTest) StorageConformanceCase {
	return StorageConformanceCase{Name: name, Run: func() error {
		return options.WithStorage(func(storage durable.Storage) (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					failure, ok := recovered.(caseFailure)
					if !ok {
						panic(recovered)
					}
					err = failure.err
				}
			}()
			expect := func(actual any) expectation {
				return expectation{assertions: options.Assertions, actual: actual}
			}
			test(storage, expect, options.Assertions.Rejects)
			return nil
		})
	}}
}

// CreateStorageConformance creates runner-independent cases. WithStorage must call its callback exactly once per
// case.
func CreateStorageConformance(options StorageConformanceOptions) []StorageConformanceCase {
	ctx := conformanceContext
	return []StorageConformanceCase{
		createCase(options, "reserves ID 1 for the immutable root conversation", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			expect(must(storage.MintId())).toBe(2)
			expect(createRoot(storage)).toBe(durable.ROOT_CONVERSATION_ID)
			expect(must(storage.Conversation(ctx, durable.ROOT_CONVERSATION_ID))).toEqual(map[string]any{"id": durable.ROOT_CONVERSATION_ID})
			rejects(
				commitErr(storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}),
				fmt.Sprintf("ID %d already belongs to conversation", durable.ROOT_CONVERSATION_ID),
			)
		}),

		createCase(options, "commits mixed table writes atomically and rolls all of them back on failure", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			rootId := createRoot(storage)
			entryId := mint[durable.EntryId](storage)
			taskId := mint[durable.TaskId](storage)
			submissionId := mint[durable.SubmissionId](storage)
			task := pendingTask(taskId, rootId, "ready")
			input := durable.SubmissionRecord{
				Id: submissionId, ConversationId: rootId, RequestId: new("request-1"),
				Type: durable.SubmissionTypeInput, Status: durable.SubmissionPlaced, Entry: new(entryId),
			}
			userEntry := entryWith(entry(entryId, rootId, "user"), func(record *durable.EntryRecord) {
				record.Data = map[string]any{"text": "hello"}
			})
			initialSeq := commit(storage, entryWrite(userEntry), taskWrite(task), submissionWrite(input))

			expect(entryView(must(storage.Entry(ctx, entryId)))).toEqual(entryViewOf(userEntry, initialSeq))
			expect(must(storage.Task(ctx, taskId))).toEqual(task)
			expect(must(storage.Submission(ctx, submissionId))).toEqual(input)

			transientEntryId := mint[durable.EntryId](storage)
			runningTask := task
			runningTask.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: new(durable.JsonValue(map[string]any{"phase": "effect"}))}
			doneInput := input
			doneInput.Status = durable.SubmissionDone
			doneInput.Answer = new(transientEntryId)
			rejects(
				commitErr(storage,
					taskWrite(runningTask),
					submissionWrite(doneInput),
					entryWrite(entry(transientEntryId, rootId, "assistant")),
					durable.ConversationWrite{Value: durable.ConversationRecord{Id: rootId}},
				),
				fmt.Sprintf("ID %d already belongs to conversation", rootId),
			)

			expect(must(storage.Task(ctx, taskId))).toEqual(task)
			expect(must(storage.Submission(ctx, submissionId))).toEqual(input)
			expect(must(storage.Entry(ctx, transientEntryId))).toBeUndefined()
			afterRollbackSeq := commit(storage, entryWrite(entry(mint[durable.EntryId](storage), rootId, "after-rollback")))
			expect(afterRollbackSeq).toBeGreaterThan(float64(initialSeq))
		}),

		createCase(options, "detaches retained writes and every returned record", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			entryId := mint[durable.EntryId](storage)
			taskId := mint[durable.TaskId](storage)
			submissionId := mint[durable.SubmissionId](storage)
			entryData := map[string]any{"nested": []any{1, 2}}
			nested := map[string]any{"count": 1}
			checkpointValue := map[string]any{"phase": "ready", "nested": nested}
			detail := map[string]any{"codes": []any{"initial"}}
			storedEntry := entryWith(entry(entryId, rootId, "note"), func(record *durable.EntryRecord) { record.Data = entryData })
			stored := pendingTask(taskId, rootId, "ready")
			stored.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: new(durable.JsonValue(checkpointValue))}
			storedInput := durable.SubmissionRecord{
				Id: submissionId, ConversationId: rootId, Type: durable.SubmissionTypeInput,
				Status: durable.SubmissionUnanswered, Reason: new("failed"), Detail: detail,
			}
			commit(storage, entryWrite(storedEntry), taskWrite(stored), submissionWrite(storedInput))

			nestedItems := entryData["nested"].([]any)
			nestedItems[0] = 3
			entryData["nested"] = append(nestedItems, 3)
			nested["count"] = 2
			detail["codes"] = append(detail["codes"].([]any), "mutated")
			expectedState := map[string]any{"status": "pending", "checkpoint": map[string]any{"phase": "ready", "nested": map[string]any{"count": 1}}}
			expect(must(storage.Entry(ctx, entryId)).Entry.Data).toEqual(map[string]any{"nested": []any{1, 2}})
			expect(must(storage.Task(ctx, taskId)).State).toEqual(expectedState)
			expect(must(storage.Submission(ctx, submissionId)).Detail).toEqual(map[string]any{"codes": []any{"initial"}})

			readEntry := must(storage.Entry(ctx, entryId)).Entry
			readItems := member(readEntry.Data, "nested").([]any)
			readItems[0] = 9
			setMember(readEntry.Data, "nested", append(readItems, 9))
			readTask := must(storage.Task(ctx, taskId))
			if readTask.State.Status != durable.TaskTerminal {
				setMember(member(*readTask.State.Checkpoint, "nested"), "count", 9)
			}
			readInput := must(storage.Submission(ctx, submissionId))
			setMember(readInput.Detail, "codes", append(member(readInput.Detail, "codes").([]any), "read mutation"))

			expect(must(storage.Entry(ctx, entryId)).Entry.Data).toEqual(map[string]any{"nested": []any{1, 2}})
			expect(must(storage.Task(ctx, taskId)).State).toEqual(expectedState)
			expect(must(storage.Submission(ctx, submissionId)).Detail).toEqual(map[string]any{"codes": []any{"initial"}})
		}),

		// Go maps have no prototype: the upstream Object.getPrototypeOf and Object.prototype pollution checks have no Go
		// subject. The case keeps every keyed read and the detachment of those keys.
		createCase(options, "detaches prototype-like JSON keys without changing object prototypes", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			entryId := mint[durable.EntryId](storage)
			var data map[string]any
			check(json.Unmarshal([]byte(`{"__proto__":{"polluted":false},"constructor":{"label":"stored"},"toString":"value"}`), &data))
			commit(storage, entryWrite(entryWith(entry(entryId, rootId, "note"), func(record *durable.EntryRecord) { record.Data = data })))

			data["__proto__"].(map[string]any)["polluted"] = true
			data["constructor"].(map[string]any)["label"] = "mutated"
			firstRead := must(storage.Entry(ctx, entryId)).Entry.Data
			expect(hasMember(firstRead, "__proto__")).toBe(true)
			expect(member(firstRead, "__proto__")).toEqual(map[string]any{"polluted": false})
			expect(member(firstRead, "constructor")).toEqual(map[string]any{"label": "stored"})
			expect(member(firstRead, "toString")).toBe("value")

			setMember(member(firstRead, "__proto__"), "polluted", true)
			secondRead := must(storage.Entry(ctx, entryId)).Entry.Data
			expect(member(secondRead, "__proto__")).toEqual(map[string]any{"polluted": false})
			expect(member(secondRead, "constructor")).toEqual(map[string]any{"label": "stored"})
			expect(member(secondRead, "toString")).toBe("value")
		}),

		createCase(options, "indexes entries committed out of ID order", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			commit(storage,
				entryWrite(entry(30, rootId, "message")),
				entryWrite(entry(10, rootId, "message")),
				entryWrite(entryWith(entry(20, rootId, "marker"), func(record *durable.EntryRecord) { record.Head = new(durable.EntryId(10)) })),
			)
			expect(entryIds(must(storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: rootId}, 10, nil)).Items)).toEqual([]int64{30, 20, 10})
			expect(must(storage.FindLatestHeadMarker(ctx, rootId, nil)).Id).toBe(20)
		}),

		createCase(options, "continues an entry cursor below its last item after a newer commit", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			oldestId := mint[durable.EntryId](storage)
			middleId := mint[durable.EntryId](storage)
			newestId := mint[durable.EntryId](storage)
			commit(storage,
				entryWrite(entry(oldestId, rootId, "message")),
				entryWrite(entry(middleId, rootId, "message")),
				entryWrite(entry(newestId, rootId, "message")),
			)
			first := must(storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: rootId}, 2, nil))
			expect(entryIds(first.Items)).toEqual(int64s(newestId, middleId))
			appendedId := mint[durable.EntryId](storage)
			commit(storage, entryWrite(entry(appendedId, rootId, "message")))
			second := must(storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: rootId}, 2, *first.Next))
			expect(entryIds(second.Items)).toEqual(int64s(oldestId))
			expect(cursorView(second.Next)).toBeUndefined()
		}),

		createCase(options, "paginates conversations by opaque cursor in ascending ID order", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			secondId := mint[durable.ConversationId](storage)
			thirdId := mint[durable.ConversationId](storage)
			commit(storage,
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: thirdId}},
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: secondId}},
			)
			first := must(storage.ScanConversations(ctx, durable.ConversationQuery{}, 2, nil))
			expect(conversationIds(first.Items)).toEqual(int64s(rootId, secondId))
			expect(cursorView(first.Next)).toBeDefined()
			encoded := must(json.Marshal(*first.Next))
			var roundTripped durable.Cursor
			check(json.Unmarshal(encoded, &roundTripped))
			second := must(storage.ScanConversations(ctx, durable.ConversationQuery{}, 2, roundTripped))
			expect(conversationIds(second.Items)).toEqual(int64s(thirdId))
			expect(cursorView(second.Next)).toBeUndefined()
		}),

		createCase(options, "filters and pages conversations by durable owner edges", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			otherOwnerId := mint[durable.ConversationId](storage)
			firstTaskId := mint[durable.TaskId](storage)
			secondTaskId := mint[durable.TaskId](storage)
			firstId := mint[durable.ConversationId](storage)
			secondId := mint[durable.ConversationId](storage)
			thirdId := mint[durable.ConversationId](storage)
			owned := func(id, owner durable.ConversationId, task durable.TaskId) durable.StorageWrite {
				return durable.ConversationWrite{Value: durable.ConversationRecord{Id: id, Owner: &durable.ConversationOwner{ConversationId: owner, TaskId: task}}}
			}
			commit(storage,
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: otherOwnerId}},
				owned(firstId, rootId, firstTaskId),
				owned(secondId, rootId, secondTaskId),
				owned(thirdId, otherOwnerId, firstTaskId),
			)
			first := must(storage.ScanConversations(ctx, durable.ConversationQuery{OwnerConversationId: new(rootId)}, 1, nil))
			expect(conversationIds(first.Items)).toEqual(int64s(firstId))
			expect(cursorView(first.Next)).toBeDefined()
			second := must(storage.ScanConversations(ctx, durable.ConversationQuery{OwnerConversationId: new(rootId)}, 1, *first.Next))
			expect(conversationIds(second.Items)).toEqual(int64s(secondId))
			expect(cursorView(second.Next)).toBeUndefined()
			expect(conversationIds(must(storage.ScanConversations(ctx, durable.ConversationQuery{OwnerTaskId: new(firstTaskId)}, 10, nil)).Items)).toEqual(int64s(firstId, thirdId))
			expect(conversationIds(must(storage.ScanConversations(ctx, durable.ConversationQuery{OwnerConversationId: new(rootId), OwnerTaskId: new(firstTaskId)}, 10, nil)).Items)).toEqual(int64s(firstId))
		}),

		createCase(options, "scans deep fork history newest-first through every ancestor cap", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			rootId := createRoot(storage)
			rootFirst := mint[durable.EntryId](storage)
			rootForkPoint := mint[durable.EntryId](storage)
			rootExcludedSameCommit := mint[durable.EntryId](storage)
			rootEntriesSeq := commit(storage,
				entryWrite(entry(rootFirst, rootId, "message")),
				entryWrite(entryWith(entry(rootForkPoint, rootId, "marker"), func(record *durable.EntryRecord) { record.Head = new(rootFirst) })),
				entryWrite(entry(rootExcludedSameCommit, rootId, "message")),
			)
			childId := mint[durable.ConversationId](storage)
			commit(storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: childId, Parent: &durable.ConversationParent{ConversationId: rootId, At: rootForkPoint}}})
			childForkPoint := mint[durable.EntryId](storage)
			childExcluded := mint[durable.EntryId](storage)
			commit(storage, entryWrite(entry(childForkPoint, childId, "note")), entryWrite(entry(childExcluded, childId, "message")))
			rootExcludedLater := mint[durable.EntryId](storage)
			commit(storage, entryWrite(entry(rootExcludedLater, rootId, "message")))
			grandchildId := mint[durable.ConversationId](storage)
			commit(storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: grandchildId, Parent: &durable.ConversationParent{ConversationId: childId, At: childForkPoint}}})
			grandchildHead := mint[durable.EntryId](storage)
			grandchildTail := mint[durable.EntryId](storage)
			grandchildEntriesSeq := commit(storage,
				entryWrite(entryWith(entry(grandchildHead, grandchildId, "marker"), func(record *durable.EntryRecord) { record.Head = new(grandchildHead) })),
				entryWrite(entry(grandchildTail, grandchildId, "message")),
			)
			childExcludedLater := mint[durable.EntryId](storage)
			commit(storage, entryWrite(entry(childExcludedLater, childId, "message")))

			grandchild := durable.EntryQuery{ConversationId: grandchildId}
			first := must(storage.ScanEntries(ctx, grandchild, 2, nil))
			expect(entryIds(first.Items)).toEqual(int64s(grandchildTail, grandchildHead))
			second := must(storage.ScanEntries(ctx, grandchild, 2, *first.Next))
			expect(entryIds(second.Items)).toEqual(int64s(childForkPoint, rootForkPoint))
			third := must(storage.ScanEntries(ctx, grandchild, 2, *second.Next))
			expect(entryIds(third.Items)).toEqual(int64s(rootFirst))
			expect(cursorView(third.Next)).toBeUndefined()

			// Oldest first: the root's segment, then each fork's, each capped at the next fork point.
			ascending := durable.EntryQuery{ConversationId: grandchildId, Order: new(durable.ScanAscending)}
			firstUp := must(storage.ScanEntries(ctx, ascending, 2, nil))
			expect(entryIds(firstUp.Items)).toEqual(int64s(rootFirst, rootForkPoint))
			secondUp := must(storage.ScanEntries(ctx, ascending, 2, *firstUp.Next))
			expect(entryIds(secondUp.Items)).toEqual(int64s(childForkPoint, grandchildHead))
			thirdUp := must(storage.ScanEntries(ctx, grandchild, 2, *secondUp.Next))
			expect(entryIds(thirdUp.Items)).toEqual(int64s(grandchildTail))
			expect(cursorView(thirdUp.Next)).toBeUndefined()
			expect(entryIds(must(storage.ScanEntries(ctx, durable.EntryQuery{
				ConversationId: grandchildId, Order: new(durable.ScanAscending),
				MinEntryId: new(rootForkPoint), MaxEntryId: new(childForkPoint),
			}, 10, nil)).Items)).toEqual(int64s(rootForkPoint, childForkPoint))
			_, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: grandchildId, Order: new(durable.ScanDescending)}, 2, *firstUp.Next)
			rejects(err, "cursor")

			currentMarker := must(storage.FindLatestHeadMarker(ctx, grandchildId, nil))
			expect(currentMarker.Id).toBe(grandchildHead)
			expect(currentMarker.Head).toBe(grandchildHead)
			historicalMarker := must(storage.FindLatestHeadMarker(ctx, grandchildId, new(childForkPoint)))
			expect(historicalMarker.Id).toBe(rootForkPoint)
			expect(historicalMarker.Head).toBe(rootFirst)
			expect(must(storage.FindLatestHeadMarker(ctx, grandchildId, new(rootFirst)))).toBeUndefined()

			active := durable.EntryQuery{ConversationId: grandchildId, MinEntryId: currentMarker.Head}
			activeFirst := must(storage.ScanEntries(ctx, active, 1, nil))
			expect(entryIds(activeFirst.Items)).toEqual(int64s(grandchildTail))
			expect(cursorView(activeFirst.Next)).toBeDefined()
			activeSecond := must(storage.ScanEntries(ctx, active, 1, *activeFirst.Next))
			expect(entryIds(activeSecond.Items)).toEqual(int64s(grandchildHead))
			expect(cursorView(activeSecond.Next)).toBeUndefined()

			expect(entryIds(must(storage.ScanEntries(ctx, durable.EntryQuery{
				ConversationId: grandchildId, MinEntryId: historicalMarker.Head, MaxEntryId: new(childForkPoint),
			}, 10, nil)).Items)).toEqual(int64s(childForkPoint, rootForkPoint, rootFirst))

			expect(entryView(must(storage.Entry(ctx, rootFirst)))).toEqual(entryViewOf(entry(rootFirst, rootId, "message"), rootEntriesSeq))
			expect(must(storage.Entry(ctx, rootForkPoint)).CommitSeq).toBe(rootEntriesSeq)
			expect(must(storage.Entry(ctx, grandchildHead)).CommitSeq).toBe(grandchildEntriesSeq)
			expect(must(storage.Entry(ctx, grandchildTail)).CommitSeq).toBe(grandchildEntriesSeq)
			expect(must(storage.Entry(ctx, 999_999))).toBeUndefined()

			expect(entryView(must(storage.VisibleEntry(ctx, grandchildId, rootFirst)))).toEqual(entryViewOf(entry(rootFirst, rootId, "message"), rootEntriesSeq))
			expect(must(storage.VisibleEntry(ctx, grandchildId, childForkPoint)).Entry.ConversationId).toBe(childId)
			expect(must(storage.VisibleEntry(ctx, grandchildId, grandchildTail)).CommitSeq).toBe(grandchildEntriesSeq)
			expect(must(storage.VisibleEntry(ctx, grandchildId, rootExcludedSameCommit))).toBeUndefined()
			expect(must(storage.VisibleEntry(ctx, grandchildId, rootExcludedLater))).toBeUndefined()
			expect(must(storage.VisibleEntry(ctx, grandchildId, childExcluded))).toBeUndefined()
			expect(must(storage.VisibleEntry(ctx, grandchildId, childExcludedLater))).toBeUndefined()
			expect(must(storage.VisibleEntry(ctx, rootId, grandchildHead))).toBeUndefined()
			expect(must(storage.VisibleEntry(ctx, grandchildId, 999_999))).toBeUndefined()
			_, err = storage.VisibleEntry(ctx, 999_999, rootFirst)
			rejects(err, "Unknown conversation")
			_, err = storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: 999_999}, 10, nil)
			rejects(err, "Unknown conversation")
		}),

		createCase(options, "scans tables in either ID order and continues a cursor in its order", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			rootId := createRoot(storage)
			conversationIdList := []int64{int64(rootId)}
			taskIdList := []int64{}
			submissionIdList := []int64{}
			for range 3 {
				conversationId := mint[durable.ConversationId](storage)
				taskId := mint[durable.TaskId](storage)
				submissionId := mint[durable.SubmissionId](storage)
				commit(storage,
					durable.ConversationWrite{Value: durable.ConversationRecord{Id: conversationId}},
					taskWrite(pendingTask(taskId, rootId, "ready")),
					submissionWrite(durable.SubmissionRecord{Id: submissionId, ConversationId: rootId, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}),
				)
				conversationIdList = append(conversationIdList, int64(conversationId))
				taskIdList = append(taskIdList, int64(taskId))
				submissionIdList = append(submissionIdList, int64(submissionId))
			}
			type scanFn func(order *durable.ScanOrder, cursor durable.Cursor) ([]int64, *durable.Cursor, error)
			scans := []struct {
				scan scanFn
				ids  []int64
			}{
				{func(order *durable.ScanOrder, cursor durable.Cursor) ([]int64, *durable.Cursor, error) {
					found, err := storage.ScanConversations(ctx, durable.ConversationQuery{Order: order}, 2, cursor)
					return conversationIds(found.Items), found.Next, err
				}, conversationIdList},
				{func(order *durable.ScanOrder, cursor durable.Cursor) ([]int64, *durable.Cursor, error) {
					found, err := storage.ScanTasks(ctx, durable.TaskQuery{Order: order}, 2, cursor)
					return taskIds(found.Items), found.Next, err
				}, taskIdList},
				{func(order *durable.ScanOrder, cursor durable.Cursor) ([]int64, *durable.Cursor, error) {
					found, err := storage.ScanSubmissions(ctx, durable.SubmissionQuery{Order: order}, 2, cursor)
					return ids(found.Items, func(record durable.SubmissionRecord) int64 { return int64(record.Id) }), found.Next, err
				}, submissionIdList},
			}
			all := func(scan scanFn, order *durable.ScanOrder) []int64 {
				found, next := must3(scan(order, nil))
				for next != nil {
					// A cursor carries its order; the query may omit it.
					var page []int64
					page, next = must3(scan(nil, roundTripCursor(*next)))
					found = append(found, page...)
				}
				return found
			}
			for _, current := range scans {
				reversed := slices.Clone(current.ids)
				slices.Reverse(reversed)
				expect(all(current.scan, nil)).toEqual(current.ids)
				expect(all(current.scan, new(durable.ScanAscending))).toEqual(current.ids)
				expect(all(current.scan, new(durable.ScanDescending))).toEqual(reversed)
				_, descendingNext := must3(current.scan(new(durable.ScanDescending), nil))
				secondPage, _ := must3(current.scan(new(durable.ScanDescending), *descendingNext))
				expect(secondPage).toEqual(reversed[2:min(4, len(reversed))])
				_, _, err := current.scan(new(durable.ScanAscending), *descendingNext)
				rejects(err, "cursor")
			}
		}),

		createCase(options, "replaces complete task records and pages filtered task scans", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			firstId := mint[durable.TaskId](storage)
			secondId := mint[durable.TaskId](storage)
			thirdId := mint[durable.TaskId](storage)
			first := pendingTask(firstId, rootId, "ready")
			first.Memos = map[string]durable.JsonValue{"winner": "first"}
			second := pendingTask(secondId, rootId, "ready")
			second.Background = true
			third := pendingTask(thirdId, rootId, "ready")
			third.AbortRequested = true
			commit(storage, taskWrite(first), taskWrite(second), taskWrite(third))

			running := first
			running.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: new(durable.JsonValue(map[string]any{"phase": "effect", "attempt": 1}))}
			running.AbortRequested = true
			commit(storage, taskWrite(running))
			expect(must(storage.Task(ctx, firstId))).toEqual(running)
			var result durable.JsonValue = map[string]any{"entryId": 99}
			terminal := storedTask{
				Id: firstId, ConversationId: rootId, Kind: first.Kind, Version: first.Version, Input: first.Input,
				State: durable.TaskState[durable.JsonValue, durable.JsonValue]{
					Status:  durable.TaskTerminal,
					Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result},
				},
				AbortRequested: true,
			}
			commit(storage, taskWrite(terminal))
			expect(must(storage.Task(ctx, firstId))).toEqual(terminal)

			pending := new(durable.TaskPending)
			pendingPage := must(storage.ScanTasks(ctx, durable.TaskQuery{Status: pending}, 1, nil))
			expect(taskIds(pendingPage.Items)).toEqual(int64s(secondId))
			expect(cursorView(pendingPage.Next)).toBeDefined()
			expect(taskIds(must(storage.ScanTasks(ctx, durable.TaskQuery{Status: pending}, 1, *pendingPage.Next)).Items)).toEqual(int64s(thirdId))
			expect(must(storage.ScanTasks(ctx, durable.TaskQuery{Status: new(durable.TaskTerminal), AbortRequested: new(true)}, 10, nil)).Items).toEqual([]storedTask{terminal})
			expect(taskIds(must(storage.ScanTasks(ctx, durable.TaskQuery{Background: new(true)}, 10, nil)).Items)).toEqual(int64s(secondId))
		}),

		createCase(options, "stores owners and scans waiting and completing tasks by status", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			ownerId := mint[durable.TaskId](storage)
			waitingId := mint[durable.TaskId](storage)
			completingId := mint[durable.TaskId](storage)
			owner := pendingTask(ownerId, rootId, "ready")
			waiting := pendingTask(waitingId, rootId, "ready")
			waiting.Owner = new(ownerId)
			waiting.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{
				Status: durable.TaskWaiting, Checkpoint: new(durable.JsonValue(map[string]any{"phase": "next"})),
				On: []durable.TaskId{ownerId}, Policy: durable.JoinAllSettled,
			}
			waiting.Memos = map[string]durable.JsonValue{"kept": true}
			completing := pendingTask(completingId, rootId, "ready")
			completing.Owner = new(ownerId)
			completing.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{
				Status:  durable.TaskCompleting,
				Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "held"}},
			}
			commit(storage, taskWrite(owner), taskWrite(waiting), taskWrite(completing))
			expect(must(storage.Task(ctx, waitingId))).toEqual(waiting)
			expect(must(storage.Task(ctx, completingId))).toEqual(completing)
			scan := func(status durable.TaskStatus) []storedTask {
				return must(storage.ScanTasks(ctx, durable.TaskQuery{Status: &status}, 10, nil)).Items
			}
			expect(scan(durable.TaskWaiting)).toEqual([]storedTask{waiting})
			expect(scan(durable.TaskCompleting)).toEqual([]storedTask{completing})
			expect(taskIds(scan(durable.TaskPending))).toEqual(int64s(ownerId))
			terminal := completing
			terminal.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: completing.State.Outcome}
			commit(storage, taskWrite(terminal))
			expect(scan(durable.TaskCompleting)).toEqual([]storedTask{})
			expect(scan(durable.TaskTerminal)).toEqual([]storedTask{terminal})
		}),

		createCase(options, "indexes request IDs per conversation and replaces complete submission records", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			secondConversationId := mint[durable.ConversationId](storage)
			commit(storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: secondConversationId}})
			firstId := mint[durable.SubmissionId](storage)
			secondId := mint[durable.SubmissionId](storage)
			otherConversationId := mint[durable.SubmissionId](storage)
			queued := func(id durable.SubmissionId, conversationId durable.ConversationId, requestId string) durable.SubmissionRecord {
				return durable.SubmissionRecord{Id: id, ConversationId: conversationId, RequestId: new(requestId), Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}
			}
			first := queued(firstId, rootId, "same")
			second := queued(secondId, rootId, "other")
			otherConversation := queued(otherConversationId, secondConversationId, "same")
			commit(storage, submissionWrite(first), submissionWrite(second), submissionWrite(otherConversation))
			expect(must(storage.SubmissionByRequest(ctx, rootId, "same"))).toEqual(first)
			expect(must(storage.SubmissionByRequest(ctx, secondConversationId, "same"))).toEqual(otherConversation)

			placedSecond := second
			placedSecond.Status = durable.SubmissionPlaced
			placedSecond.Entry = new(mint[durable.EntryId](storage))
			commit(storage, submissionWrite(placedSecond))
			expect(must(storage.Submission(ctx, secondId))).toEqual(placedSecond)
			expect(must(storage.SubmissionByRequest(ctx, rootId, "other"))).toEqual(placedSecond)

			scanIds := func(query durable.SubmissionQuery) []int64 {
				found := []int64{}
				var cursor durable.Cursor
				for {
					page := must(storage.ScanSubmissions(ctx, query, 1, cursor))
					for _, item := range page.Items {
						found = append(found, int64(item.Id))
					}
					if page.Next == nil {
						return found
					}
					cursor = *page.Next
				}
			}
			expect(scanIds(durable.SubmissionQuery{})).toEqual(int64s(firstId, secondId, otherConversationId))
			expect(scanIds(durable.SubmissionQuery{ConversationId: new(rootId)})).toEqual(int64s(firstId, secondId))
			// A status change moves the record between status scans.
			expect(scanIds(durable.SubmissionQuery{Status: new(durable.SubmissionQueued)})).toEqual(int64s(firstId, otherConversationId))
			expect(scanIds(durable.SubmissionQuery{Status: new(durable.SubmissionPlaced)})).toEqual(int64s(secondId))
			expect(scanIds(durable.SubmissionQuery{ConversationId: new(secondConversationId), Status: new(durable.SubmissionQueued)})).toEqual(int64s(otherConversationId))
			expect(scanIds(durable.SubmissionQuery{ConversationId: new(secondConversationId), Status: new(durable.SubmissionPlaced)})).toEqual([]int64{})
			expect(must(storage.ScanSubmissions(ctx, durable.SubmissionQuery{Status: new(durable.SubmissionPlaced)}, 10, nil)).Items).toEqual([]durable.SubmissionRecord{placedSecond})
		}),

		createCase(options, "stores passive write submissions without input-only lifecycle states", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			doneId := mint[durable.SubmissionId](storage)
			failedId := mint[durable.SubmissionId](storage)
			queuedDone := durable.SubmissionRecord{Id: doneId, ConversationId: rootId, RequestId: new("passive-done"), Type: durable.SubmissionTypeWrite, Status: durable.SubmissionQueued}
			queuedFailed := durable.SubmissionRecord{Id: failedId, ConversationId: rootId, RequestId: new("passive-failed"), Type: durable.SubmissionTypeWrite, Status: durable.SubmissionQueued}
			commit(storage, submissionWrite(queuedDone), submissionWrite(queuedFailed))

			done := queuedDone
			done.Status = durable.SubmissionDone
			done.Entry = new(mint[durable.EntryId](storage))
			unanswered := queuedFailed
			unanswered.Status = durable.SubmissionUnanswered
			unanswered.Reason = new("closed")
			unanswered.Detail = map[string]any{"retryable": false}
			commit(storage, submissionWrite(done), submissionWrite(unanswered))
			expect(must(storage.Submission(ctx, doneId))).toEqual(done)
			expect(must(storage.SubmissionByRequest(ctx, rootId, "passive-done"))).toEqual(done)
			expect(must(storage.Submission(ctx, failedId))).toEqual(unanswered)
			expect(must(storage.SubmissionByRequest(ctx, rootId, "passive-failed"))).toEqual(unanswered)
		}),

		createCase(options, "reconstructs rewindable documents and preserves half-open incarnations", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			firstId := mint[durable.DocumentId](storage)
			firstRecord := durable.DocumentCreate{
				Id: firstId, Kind: "conversation.notes", Scope: conversationScope(rootId),
				History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
			}
			items := []any{"a"}
			initial := chorddelta.JsonObjectOf("items", items, "nested", map[string]any{"count": 1})
			createdAt := commit(storage, durable.DocumentCreateWrite{Record: firstRecord, Content: base(1, initial)})
			appended := []any{"b"}
			changedAt := commit(storage, durable.DocumentChangeWrite{Id: firstId, Content: delta(1,
				op("p", path("items"), 1, 0, appended),
				op("s", path("nested", "count"), 2),
			)})

			items[0] = "caller mutation"
			initial.Set("items", append(items, "caller mutation"))
			appended[0] = "caller mutation"
			expect(storedView(must(storage.Document(ctx, firstId, durable.AtSeq(createdAt))))).toMatchObject(map[string]any{
				"version": 1, "value": map[string]any{"items": []any{"a"}, "nested": map[string]any{"count": 1}}, "deltasSinceBase": 0,
			})
			changed := must(storage.Document(ctx, firstId, durable.AtSeq(changedAt)))
			expect(changed.Value).toEqual(map[string]any{"items": []any{"a", "b"}, "nested": map[string]any{"count": 2}})
			expect(changed.DeltasSinceBase).toBe(1)
			changedItems := changed.Value.Value("items").([]any)
			changedItems[0] = "read mutation"
			changed.Value.Set("items", append(changedItems, "read mutation"))
			expect(must(storage.Document(ctx, firstId, durable.CurrentPoint)).Value).toEqual(map[string]any{
				"items": []any{"a", "b"}, "nested": map[string]any{"count": 2},
			})

			checkpointAt := commit(storage, durable.DocumentChangeWrite{Id: firstId, Content: base(2, chorddelta.JsonObjectOf("items", []any{"checkpoint"}, "nested", map[string]any{"count": 3}))})
			replacedAt := commit(storage, durable.DocumentChangeWrite{Id: firstId, Content: delta(2,
				op("r", map[string]any{"items": []any{"replacement"}, "nested": map[string]any{"count": 4}}),
			)})
			expect(storedView(must(storage.Document(ctx, firstId, durable.AtSeq(changedAt))))).toMatchObject(map[string]any{
				"version": 1, "value": map[string]any{"items": []any{"a", "b"}, "nested": map[string]any{"count": 2}},
			})
			expect(storedView(must(storage.Document(ctx, firstId, durable.AtSeq(checkpointAt))))).toMatchObject(map[string]any{
				"version": 2, "value": map[string]any{"items": []any{"checkpoint"}, "nested": map[string]any{"count": 3}}, "deltasSinceBase": 0,
			})
			expect(storedView(must(storage.Document(ctx, firstId, durable.AtSeq(replacedAt))))).toMatchObject(map[string]any{
				"value": map[string]any{"items": []any{"replacement"}, "nested": map[string]any{"count": 4}}, "deltasSinceBase": 1,
			})
			expect(must(storage.Document(ctx, firstId, durable.CurrentPoint)).DeltasSinceBase).toBe(1)

			secondId := mint[durable.DocumentId](storage)
			secondRecord := firstRecord
			secondRecord.Id = secondId
			retiredAt := commit(storage,
				durable.DocumentCreateWrite{Record: secondRecord, Content: base(1, chorddelta.JsonObjectOf("items", []any{"new"}))},
				durable.DocumentRetireWrite{Id: firstId},
				durable.DocumentChangeWrite{Id: firstId, Content: delta(2, op("s", path("retiring"), true))},
			)
			address := durable.DocumentAddress{Kind: firstRecord.Kind, Scope: firstRecord.Scope}
			expect(must(storage.FindDocument(ctx, address, durable.AtSeq(changedAt))).Id).toBe(firstId)
			expect(must(storage.FindDocument(ctx, address, durable.AtSeq(retiredAt)))).toMatchObject(map[string]any{"id": secondId, "createdAt": retiredAt})
			expect(documentIds(must(storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: firstRecord.Scope, At: durable.AtSeq(changedAt)}, 10, nil)).Items)).toEqual(int64s(firstId))
			expect(documentIds(must(storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: firstRecord.Scope, At: durable.AtSeq(retiredAt)}, 10, nil)).Items)).toEqual(int64s(secondId))
			expect(must(storage.Document(ctx, firstId, durable.AtSeq(retiredAt)))).toBeUndefined()
			expect(must(storage.Document(ctx, secondId, durable.CurrentPoint)).Value).toEqual(map[string]any{"items": []any{"new"}})
		}),

		createCase(options, "streams long document tails across root replacement deltas", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			id := mint[durable.DocumentId](storage)
			record := durable.DocumentCreate{
				Id: id, Kind: "conversation.long-tail", Scope: conversationScope(rootId),
				History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
			}
			rows := func(count int, value func(int) int, stable func(int) string) []any {
				out := make([]any, count)
				for index := range count {
					out[index] = chorddelta.JsonObjectOf("value", value(index), "stable", stable(index))
				}
				return out
			}
			initial := chorddelta.JsonObjectOf("revision", 0, "rows", rows(512, func(value int) int { return value }, func(value int) string { return fmt.Sprintf("row-%d", value) }))
			createdAt := commit(storage, durable.DocumentCreateWrite{Record: record, Content: base(1, initial)})
			beforeReplacement := structuredClone(initial)
			beforeReplacementAt := createdAt
			for revision := 1; revision <= 24; revision++ {
				beforeRows := beforeReplacement.Value("rows").([]any)
				index := (revision * 17) % len(beforeRows)
				beforeRows[index].(*chorddelta.JsonObject).Set("value", -revision)
				beforeReplacement.Set("revision", revision)
				beforeReplacementAt = commit(storage, durable.DocumentChangeWrite{Id: id, Content: delta(1,
					op("s", path("rows", index, "value"), -revision),
					op("s", path("revision"), revision),
				)})
			}

			replacement := chorddelta.JsonObjectOf("revision", 100, "rows", rows(512, func(value int) int { return 10_000 + value }, func(value int) string { return fmt.Sprintf("new-%d", value) }))
			replacementSnapshot := structuredClone(replacement)
			replacementAt := commit(storage, durable.DocumentChangeWrite{Id: id, Content: delta(1, op("r", replacement))})
			replacement.Value("rows").([]any)[0].(*chorddelta.JsonObject).Set("value", -999)

			current := structuredClone(replacementSnapshot)
			for revision := 101; revision <= 124; revision++ {
				currentRows := current.Value("rows").([]any)
				index := (revision * 19) % len(currentRows)
				currentRows[index].(*chorddelta.JsonObject).Set("value", -revision)
				current.Set("revision", revision)
				commit(storage, durable.DocumentChangeWrite{Id: id, Content: delta(1,
					op("s", path("rows", index, "value"), -revision),
					op("s", path("revision"), revision),
				)})
			}

			expect(must(storage.Document(ctx, id, durable.AtSeq(createdAt))).Value).toEqual(initial)
			expect(must(storage.Document(ctx, id, durable.AtSeq(beforeReplacementAt))).Value).toEqual(beforeReplacement)
			expect(must(storage.Document(ctx, id, durable.AtSeq(replacementAt))).Value).toEqual(replacementSnapshot)
			read := must(storage.Document(ctx, id, durable.CurrentPoint))
			expect(read.Value).toEqual(current)
			read.Value.Value("rows").([]any)[0].(*chorddelta.JsonObject).Set("value", -1_000)
			expect(must(storage.Document(ctx, id, durable.CurrentPoint)).Value).toEqual(current)
		}),

		createCase(options, "copies stored document bases independently and rejects ambiguous sources", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			childId := mint[durable.ConversationId](storage)
			secondChildId := mint[durable.ConversationId](storage)
			commit(storage,
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: childId}},
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: secondChildId}},
			)
			sourceId := mint[durable.DocumentId](storage)
			sourceRecord := durable.DocumentCreate{
				Id: sourceId, Kind: "copy.source", Scope: conversationScope(rootId),
				History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
			}
			createdAt := commit(storage, durable.DocumentCreateWrite{Record: sourceRecord, Content: base(2, chorddelta.JsonObjectOf("count", 1, "rows", []any{chorddelta.JsonObjectOf("value", "base")}))})
			commit(storage, durable.DocumentChangeWrite{Id: sourceId, Content: delta(2,
				op("s", path("count"), 2),
				op("p", path("rows"), 1, 0, []any{map[string]any{"value": "current"}}),
			)})
			historicalCopyId := mint[durable.DocumentId](storage)
			currentCopyId := mint[durable.DocumentId](storage)
			retiredCopyId := mint[durable.DocumentId](storage)
			childRecord := func(id durable.DocumentId, conversationId durable.ConversationId) durable.DocumentCreate {
				return durable.DocumentCreate{
					Id: id, Kind: sourceRecord.Kind, Scope: conversationScope(conversationId),
					History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
				}
			}
			commit(storage,
				durable.DocumentCopyWrite{Record: childRecord(historicalCopyId, childId), Source: durable.DocumentCopySource{Id: sourceId, At: durable.AtSeq(createdAt)}},
				durable.DocumentCopyWrite{Record: childRecord(currentCopyId, secondChildId), Source: durable.DocumentCopySource{Id: sourceId, At: durable.CurrentPoint}},
				durable.DocumentCopyWrite{Record: childRecord(retiredCopyId, rootId), Source: durable.DocumentCopySource{Id: sourceId, At: durable.CurrentPoint}},
				durable.DocumentRetireWrite{Id: retiredCopyId},
			)
			expect(storedView(must(storage.Document(ctx, historicalCopyId, durable.CurrentPoint)))).toMatchObject(map[string]any{
				"version": 2, "value": map[string]any{"count": 1, "rows": []any{map[string]any{"value": "base"}}},
			})
			currentValue := map[string]any{"count": 2, "rows": []any{map[string]any{"value": "base"}, map[string]any{"value": "current"}}}
			expect(storedView(must(storage.Document(ctx, currentCopyId, durable.CurrentPoint)))).toMatchObject(map[string]any{
				"version": 2, "value": currentValue,
			})
			expect(must(storage.Document(ctx, retiredCopyId, durable.CurrentPoint))).toBeUndefined()

			commit(storage,
				durable.DocumentChangeWrite{Id: sourceId, Content: base(2, chorddelta.JsonObjectOf("count", 99, "rows", []any{}))},
				durable.DocumentRetireWrite{Id: sourceId},
			)
			expect(must(storage.Document(ctx, currentCopyId, durable.CurrentPoint)).Value).toEqual(currentValue)

			latestSourceId := mint[durable.DocumentId](storage)
			latestCopyId := mint[durable.DocumentId](storage)
			latestSource := durable.DocumentCreate{
				Id: latestSourceId, Kind: "copy.latest", Scope: conversationScope(rootId),
				History: durable.HistoryLatest, Fork: durable.ForkCurrent,
			}
			commit(storage, durable.DocumentCreateWrite{Record: latestSource, Content: base(4, chorddelta.JsonObjectOf("retained", "copy"))})
			latestCopy := latestSource
			latestCopy.Id = latestCopyId
			latestCopy.Scope = conversationScope(childId)
			commit(storage, durable.DocumentCopyWrite{Record: latestCopy, Source: durable.DocumentCopySource{Id: latestSourceId, At: durable.CurrentPoint}})
			commit(storage,
				durable.DocumentChangeWrite{Id: latestSourceId, Content: base(4, chorddelta.JsonObjectOf("retained", "source-only"))},
				durable.DocumentRetireWrite{Id: latestSourceId},
			)
			expect(storedView(must(storage.Document(ctx, latestCopyId, durable.CurrentPoint)))).toMatchObject(map[string]any{
				"version": 4, "value": map[string]any{"retained": "copy"},
			})

			conflictId := mint[durable.DocumentId](storage)
			conflictError := commitErr(storage,
				durable.DocumentCopyWrite{Record: childRecord(conflictId, childId), Source: durable.DocumentCopySource{Id: currentCopyId, At: durable.CurrentPoint}},
				durable.DocumentRetireWrite{Id: currentCopyId},
			)
			expect(errorName(conflictError)).toBe("StorageRejected")
			expect(must(storage.Document(ctx, conflictId, durable.CurrentPoint))).toBeUndefined()
			expect(must(storage.Document(ctx, currentCopyId, durable.CurrentPoint)).Value).toEqual(currentValue)

			mismatchId := mint[durable.DocumentId](storage)
			mismatch := childRecord(mismatchId, childId)
			mismatch.Kind = "copy.mismatch"
			mismatchError := commitErr(storage, durable.DocumentCopyWrite{Record: mismatch, Source: durable.DocumentCopySource{Id: currentCopyId, At: durable.CurrentPoint}})
			expect(errorName(mismatchError)).toBe("StorageRejected")
			expect(must(storage.Document(ctx, mismatchId, durable.CurrentPoint))).toBeUndefined()
		}),

		createCase(options, "uses bases for version transitions and rejects historical reads of current-only documents", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			createRoot(storage)
			id := mint[durable.DocumentId](storage)
			record := durable.DocumentCreate{Id: id, Kind: "session.settings", Scope: sessionScope}
			commit(storage, durable.DocumentCreateWrite{Record: record, Content: base(1, chorddelta.JsonObjectOf("count", 1))})
			commit(storage, durable.DocumentChangeWrite{Id: id, Content: delta(1, op("s", path("count"), 2))})
			migratedAt := commit(storage, durable.DocumentChangeWrite{Id: id, Content: base(2, chorddelta.JsonObjectOf("count", 3))})
			expect(storedView(must(storage.Document(ctx, id, durable.CurrentPoint)))).toMatchObject(map[string]any{"version": 2, "value": map[string]any{"count": 3}})
			_, err := storage.Document(ctx, id, durable.AtSeq(migratedAt))
			rejects(err, "does not retain historical content")

			rejects(commitErr(storage, durable.DocumentChangeWrite{Id: id, Content: delta(1, op("s", path("count"), 4))}), "version transition requires a base")
			expect(must(storage.Document(ctx, id, durable.CurrentPoint)).Value).toEqual(map[string]any{"count": 3})
			commit(storage, durable.DocumentRetireWrite{Id: id})
			expect(must(storage.Document(ctx, id, durable.CurrentPoint))).toBeUndefined()
		}),

		createCase(options, "indexes logical addresses and exact-scope scans independently", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			rootId := createRoot(storage)
			firstId := mint[durable.DocumentId](storage)
			secondId := mint[durable.DocumentId](storage)
			conversationDocumentId := mint[durable.DocumentId](storage)
			taskId := mint[durable.TaskId](storage)
			taskSingletonId := mint[durable.DocumentId](storage)
			taskFamilyId := mint[durable.DocumentId](storage)
			taskOtherKindId := mint[durable.DocumentId](storage)
			taskScope := durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskId}
			create := func(record durable.DocumentCreate, owner string) durable.StorageWrite {
				return durable.DocumentCreateWrite{Record: record, Content: base(1, chorddelta.JsonObjectOf("owner", owner))}
			}
			createdAt := commit(storage,
				taskWrite(pendingTask(taskId, rootId, "ready")),
				create(durable.DocumentCreate{Id: firstId, Kind: "cache", Scope: sessionScope, Key: new("__proto__")}, "first"),
				create(durable.DocumentCreate{Id: secondId, Kind: "cache", Scope: sessionScope, Key: new("constructor")}, "second"),
				create(durable.DocumentCreate{
					Id: conversationDocumentId, Kind: "cache", Scope: conversationScope(rootId),
					History: durable.HistoryLatest, Fork: durable.ForkCurrent, Key: new("__proto__"),
				}, "conversation"),
				create(durable.DocumentCreate{Id: taskSingletonId, Kind: "task.cache", Scope: taskScope}, "singleton"),
				create(durable.DocumentCreate{Id: taskFamilyId, Kind: "task.cache", Scope: taskScope, Key: new("member")}, "family"),
				create(durable.DocumentCreate{Id: taskOtherKindId, Kind: "task.other", Scope: taskScope}, "other"),
			)

			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: "cache", Scope: sessionScope, Key: new("__proto__")}, durable.CurrentPoint)).Id).toBe(firstId)
			sessionQuery := durable.DocumentQuery{Scope: sessionScope, At: durable.CurrentPoint}
			expect(must(storage.ScanDocuments(ctx, sessionQuery, 1, nil)).Items).toHaveLength(1)
			first := must(storage.ScanDocuments(ctx, sessionQuery, 1, nil))
			second := must(storage.ScanDocuments(ctx, sessionQuery, 1, *first.Next))
			expect(documentIds(append(first.Items, second.Items...))).toEqual(int64s(firstId, secondId))
			expect(documentIds(must(storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: conversationScope(rootId), At: durable.CurrentPoint}, 10, nil)).Items)).toEqual(int64s(conversationDocumentId))
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: "task.cache", Scope: taskScope}, durable.CurrentPoint)).Id).toBe(taskSingletonId)
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: "task.cache", Scope: taskScope, Key: new("member")}, durable.CurrentPoint)).Id).toBe(taskFamilyId)
			expect(documentIds(must(storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: taskScope, At: durable.CurrentPoint, Kind: new("task.cache")}, 10, nil)).Items)).toEqual(int64s(taskSingletonId, taskFamilyId))
			_, err := storage.Document(ctx, taskSingletonId, durable.AtSeq(createdAt))
			rejects(err, "does not retain historical content")
		}),

		createCase(options, "keeps document lifecycle failures atomic and gives create-plus-retire an empty lifetime", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			rootId := createRoot(storage)
			firstId := mint[durable.DocumentId](storage)
			secondId := mint[durable.DocumentId](storage)
			record := durable.DocumentCreate{Id: firstId, Kind: "singleton", Scope: sessionScope}
			commit(storage, durable.DocumentCreateWrite{Record: record, Content: base(1, chorddelta.JsonObjectOf("value", 1))})
			secondRecord := record
			secondRecord.Id = secondId
			rejects(commitErr(storage,
				durable.DocumentCreateWrite{Record: secondRecord, Content: base(1, chorddelta.JsonObjectOf("value", 2))},
				durable.DocumentChangeWrite{Id: firstId, Content: delta(1)},
			), "already has a current incarnation")
			expect(must(storage.Document(ctx, firstId, durable.CurrentPoint)).Value).toEqual(map[string]any{"value": 1})
			expect(must(storage.Document(ctx, secondId, durable.CurrentPoint))).toBeUndefined()

			emptyId := mint[durable.DocumentId](storage)
			emptyAt := commit(storage,
				durable.DocumentCreateWrite{Record: durable.DocumentCreate{
					Id: emptyId, Kind: record.Kind, Key: new("empty"), Scope: conversationScope(rootId),
					History: durable.HistoryRewindable, Fork: durable.ForkInitial,
				}, Content: base(1, chorddelta.NewJsonObject(0))},
				durable.DocumentRetireWrite{Id: emptyId},
			)
			expect(must(storage.Document(ctx, emptyId, durable.CurrentPoint))).toBeUndefined()
			expect(must(storage.Document(ctx, emptyId, durable.AtSeq(emptyAt)))).toBeUndefined()
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: record.Kind, Scope: conversationScope(rootId), Key: new("empty")}, durable.AtSeq(emptyAt)))).toBeUndefined()
		}),

		createCase(options, "rolls back record tables and secondary indexes when a document command fails", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			rootId := createRoot(storage)
			taskId := mint[durable.TaskId](storage)
			submissionId := mint[durable.SubmissionId](storage)
			documentId := mint[durable.DocumentId](storage)
			task := pendingTask(taskId, rootId, "ready")
			submission := durable.SubmissionRecord{Id: submissionId, ConversationId: rootId, RequestId: new("atomic"), Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}
			record := durable.DocumentCreate{Id: documentId, Kind: "atomic", Scope: sessionScope}
			baselineSeq := commit(storage,
				taskWrite(task),
				submissionWrite(submission),
				durable.DocumentCreateWrite{Record: record, Content: base(1, chorddelta.JsonObjectOf("count", 1))},
			)

			entryId := mint[durable.EntryId](storage)
			conflictingDocumentId := mint[durable.DocumentId](storage)
			runningTask := task
			runningTask.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: new(durable.JsonValue(map[string]any{"phase": "effect"}))}
			failedSubmission := submission
			failedSubmission.Status = durable.SubmissionUnanswered
			failedSubmission.Reason = new("failed")
			conflicting := record
			conflicting.Id = conflictingDocumentId
			rejects(commitErr(storage,
				taskWrite(runningTask),
				submissionWrite(failedSubmission),
				entryWrite(entry(entryId, rootId, "transient")),
				durable.DocumentCreateWrite{Record: conflicting, Content: base(1, chorddelta.JsonObjectOf("count", 2))},
			), "already has a current incarnation")

			expect(must(storage.Task(ctx, taskId))).toEqual(task)
			expect(must(storage.ScanTasks(ctx, durable.TaskQuery{Status: new(durable.TaskPending)}, 10, nil)).Items).toEqual([]storedTask{task})
			expect(must(storage.SubmissionByRequest(ctx, rootId, "atomic"))).toEqual(submission)
			expect(must(storage.Entry(ctx, entryId))).toBeUndefined()
			expect(must(storage.Document(ctx, conflictingDocumentId, durable.CurrentPoint))).toBeUndefined()
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: record.Kind, Scope: record.Scope}, durable.CurrentPoint)).Id).toBe(documentId)
			afterRollbackSeq := commit(storage, durable.DocumentChangeWrite{Id: documentId, Content: delta(1, op("s", path("count"), 3))})
			expect(afterRollbackSeq).toBeGreaterThan(float64(baselineSeq))
		}),

		// Go strings hold lone UTF-16 surrogates as WTF-8, the encoding the JavaScript-semantics JSON codec uses.
		createCase(options, "keeps indexed string identities lossless", func(storage durable.Storage, expect func(any) expectation, _ func(error, string)) {
			rootId := createRoot(storage)
			first := "\xed\xa0\x80"  // "\ud800"
			second := "\xed\xa0\x81" // "\ud801"
			firstTaskId := mint[durable.TaskId](storage)
			secondTaskId := mint[durable.TaskId](storage)
			firstSubmissionId := mint[durable.SubmissionId](storage)
			secondSubmissionId := mint[durable.SubmissionId](storage)
			firstKindDocumentId := mint[durable.DocumentId](storage)
			secondKindDocumentId := mint[durable.DocumentId](storage)
			firstKeyDocumentId := mint[durable.DocumentId](storage)
			secondKeyDocumentId := mint[durable.DocumentId](storage)
			firstTask := pendingTask(firstTaskId, rootId, "ready")
			firstTask.Kind = first
			secondTask := pendingTask(secondTaskId, rootId, "ready")
			secondTask.Kind = second
			identity := func(value string) durable.DocumentContent { return base(1, chorddelta.JsonObjectOf("identity", value)) }
			commit(storage,
				taskWrite(firstTask),
				taskWrite(secondTask),
				submissionWrite(durable.SubmissionRecord{Id: firstSubmissionId, ConversationId: rootId, RequestId: new(first), Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}),
				submissionWrite(durable.SubmissionRecord{Id: secondSubmissionId, ConversationId: rootId, RequestId: new(second), Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued}),
				durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: firstKindDocumentId, Kind: first, Scope: sessionScope}, Content: identity("first kind")},
				durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: secondKindDocumentId, Kind: second, Scope: sessionScope}, Content: identity("second kind")},
				durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: firstKeyDocumentId, Kind: "family", Key: new(first), Scope: sessionScope}, Content: identity("first key")},
				durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: secondKeyDocumentId, Kind: "family", Key: new(second), Scope: sessionScope}, Content: identity("second key")},
			)

			expect(taskIds(must(storage.ScanTasks(ctx, durable.TaskQuery{Kind: new(first)}, 10, nil)).Items)).toEqual(int64s(firstTaskId))
			expect(taskIds(must(storage.ScanTasks(ctx, durable.TaskQuery{Kind: new(second)}, 10, nil)).Items)).toEqual(int64s(secondTaskId))
			expect(must(storage.Task(ctx, firstTaskId)).Kind).toBe(first)
			expect(must(storage.Task(ctx, secondTaskId)).Kind).toBe(second)
			expect(*must(storage.SubmissionByRequest(ctx, rootId, first)).RequestId).toBe(first)
			expect(must(storage.SubmissionByRequest(ctx, rootId, first)).Id).toBe(firstSubmissionId)
			expect(must(storage.SubmissionByRequest(ctx, rootId, second)).Id).toBe(secondSubmissionId)
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: first, Scope: sessionScope}, durable.CurrentPoint)).Id).toBe(firstKindDocumentId)
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: second, Scope: sessionScope}, durable.CurrentPoint)).Id).toBe(secondKindDocumentId)
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: "family", Key: new(first), Scope: sessionScope}, durable.CurrentPoint)).Id).toBe(firstKeyDocumentId)
			expect(must(storage.FindDocument(ctx, durable.DocumentAddress{Kind: "family", Key: new(second), Scope: sessionScope}, durable.CurrentPoint)).Id).toBe(secondKeyDocumentId)
			expect(documentIds(must(storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: sessionScope, At: durable.CurrentPoint, Kind: new(first)}, 10, nil)).Items)).toEqual(int64s(firstKindDocumentId))
		}),

		createCase(options, "keeps one global record ID namespace and rejects exhausted ID minting", func(storage durable.Storage, expect func(any) expectation, rejects func(error, string)) {
			rootId := createRoot(storage)
			const explicitEntryId durable.EntryId = 100
			commit(storage, entryWrite(entry(explicitEntryId, rootId, "message")))
			expect(must(storage.MintId())).toBe(101)
			rejects(
				commitErr(storage, taskWrite(pendingTask(durable.TaskId(explicitEntryId), rootId, "ready"))),
				fmt.Sprintf("ID %d already belongs to entry", explicitEntryId),
			)

			commit(storage, entryWrite(entry(durable.EntryId(maxSafeInteger), rootId, "last-id")))
			_, err := storage.MintId()
			rejects(err, "ID space is exhausted")
			_, err = storage.MintId()
			rejects(err, "ID space is exhausted")
		}),

		createCase(options, "rejects every operation after close", func(storage durable.Storage, _ func(any) expectation, rejects func(error, string)) {
			createRoot(storage)
			check(storage.Close(ctx))
			_, err := storage.Conversation(ctx, durable.ROOT_CONVERSATION_ID)
			rejects(err, "closed")
			rejects(commitErr(storage), "closed")
			_, err = storage.MintId()
			rejects(err, "closed")
		}),
	}
}

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// errorName is the upstream Error.name of a storage error.
func errorName(err error) string {
	//nolint:errorlint // Upstream reads the thrown error's own name, not a cause in its chain.
	if _, ok := err.(*durable.StorageRejected); ok {
		return "StorageRejected"
	}
	if err == nil {
		return ""
	}
	return "Error"
}

// structuredClone deep-copies a JSON object built from ordered objects, maps and slices.
func structuredClone(value durable.JsonObject) durable.JsonObject {
	var clone func(any) any
	clone = func(value any) any {
		switch typed := value.(type) {
		case *chorddelta.JsonObject:
			out := chorddelta.NewJsonObject(typed.Len())
			for key, item := range typed.All() {
				out.Set(key, clone(item))
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, item := range typed {
				out[key] = clone(item)
			}
			return out
		case []any:
			out := make([]any, len(typed))
			for index, item := range typed {
				out[index] = clone(item)
			}
			return out
		default:
			return value
		}
	}
	return clone(value).(*chorddelta.JsonObject)
}

// member reads key of a stored JSON object, which a storage returns as the Go map it was given or, decoded from text, as an ordered
// object.
func member(object any, key string) any {
	if ordered, ok := object.(*chorddelta.JsonObject); ok {
		return ordered.Value(key)
	}
	return object.(map[string]any)[key]
}

// setMember writes key of a stored JSON object of either form.
func setMember(object any, key string, value any) {
	if ordered, ok := object.(*chorddelta.JsonObject); ok {
		ordered.Set(key, value)
		return
	}
	object.(map[string]any)[key] = value
}

// hasMember reports whether a stored JSON object of either form has key.
func hasMember(object any, key string) bool {
	if ordered, ok := object.(*chorddelta.JsonObject); ok {
		return ordered.Has(key)
	}
	_, ok := object.(map[string]any)[key]
	return ok
}
