package session_test

// pi: packages/durable/test/session-cached-storage.test.ts

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

type anyTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

func taskRecord(id int64, status durable.TaskStatus) anyTask {
	var checkpoint durable.JsonValue = map[string]any{"phase": "run"}
	return anyTask{
		Id:             durable.TaskId(id),
		ConversationId: 1,
		Kind:           "test.task",
		Version:        1,
		State:          durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: status, Checkpoint: &checkpoint},
	}
}

func endedTask(id int64, result durable.JsonValue) anyTask {
	record := taskRecord(id, durable.TaskTerminal)
	record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{
		Status:  durable.TaskTerminal,
		Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result},
	}
	return record
}

func waitingTask(state durable.TaskState[durable.JsonValue, durable.JsonValue]) anyTask {
	record := taskRecord(2, durable.TaskPending)
	record.State = state
	return record
}

func phase(name string) *durable.JsonValue {
	var checkpoint durable.JsonValue = map[string]any{"phase": name}
	return &checkpoint
}

// countingBackend is the Storage under a CachedStorage: it keeps the tasks written, counts the task reads, and can
// hold a task read after it looked its record up, as a read in flight while a commit publishes.
type countingBackend struct {
	durable.Storage

	mu        sync.Mutex
	tasks     map[durable.TaskId]anyTask
	taskReads int
	hold      chan struct{}
	entered   chan struct{}
}

func newCountingBackend() *countingBackend {
	return &countingBackend{tasks: map[durable.TaskId]anyTask{}}
}

func (backend *countingBackend) Commit(_ context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
	backend.mu.Lock()
	for index, batch := range batches {
		for _, write := range batch {
			if write, ok := write.(durable.TaskWrite); ok {
				backend.tasks[write.Value.Id] = write.Value
			}
		}
		receive(index, durable.Seq(index+1))
	}
	backend.mu.Unlock()
	return durable.CommitResult{Stored: len(batches)}, nil
}

func (backend *countingBackend) Task(_ context.Context, id durable.TaskId) (*anyTask, error) {
	backend.mu.Lock()
	backend.taskReads++
	record, ok := backend.tasks[id]
	hold, entered := backend.hold, backend.entered
	backend.mu.Unlock()
	if hold != nil {
		entered <- struct{}{}
		<-hold
	}
	if !ok {
		return nil, nil
	}
	return &record, nil
}

func (backend *countingBackend) reads() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.taskReads
}

// holdReads makes every later task read wait for the returned release after it looked its record up; entered receives
// one value per held read.
func (backend *countingBackend) holdReads() (release func(), entered <-chan struct{}) {
	hold, enteredChan := make(chan struct{}), make(chan struct{}, 8)
	backend.mu.Lock()
	backend.hold, backend.entered = hold, enteredChan
	backend.mu.Unlock()
	return func() { close(hold) }, enteredChan
}

// publishBatch stores writes as one batch, then publishes it, as the Session does.
func publishBatch(t *testing.T, storage *session.CachedStorage, writes ...durable.StorageWrite) {
	t.Helper()
	result, err := storage.Commit(ctx, [][]durable.StorageWrite{writes}, func(int, durable.Seq) {})
	if err != nil || result.Stored != 1 || result.Refused != nil {
		t.Fatalf("commit = %+v, %v", result, err)
	}
	storage.Published(writes)
}

func taskWrite(record anyTask) durable.StorageWrite { return durable.TaskWrite{Value: record} }

func conversationWrite(id durable.ConversationId) durable.StorageWrite {
	return durable.ConversationWrite{Value: durable.ConversationRecord{Id: id}}
}

func statusOf(t *testing.T, storage *session.CachedStorage, id int64) durable.TaskStatus {
	t.Helper()
	record, err := storage.Task(ctx, durable.TaskId(id))
	if err != nil || record == nil {
		t.Fatalf("Task(%d) = %v, %v", id, record, err)
	}
	return record.State.Status
}

func TestCachedStorageServesWrittenRecordWithoutReadAndFollowsLaterWrites(t *testing.T) {
	backend := newCountingBackend()
	storage := session.NewCachedStorage(backend)
	publishBatch(t, storage, conversationWrite(1))
	publishBatch(t, storage, taskWrite(taskRecord(2, durable.TaskPending)))
	if got := statusOf(t, storage, 2); got != durable.TaskPending {
		t.Fatalf("status = %s", got)
	}
	publishBatch(t, storage, taskWrite(taskRecord(2, durable.TaskRunning)))
	if got := statusOf(t, storage, 2); got != durable.TaskRunning {
		t.Fatalf("status = %s", got)
	}
	if backend.reads() != 0 {
		t.Fatalf("task reads = %d", backend.reads())
	}
}

func TestCachedStorageKeepsReadOnlyWhenNoCommitOverlapped(t *testing.T) {
	backend := newCountingBackend()
	backend.tasks[2] = taskRecord(2, durable.TaskPending)
	storage := session.NewCachedStorage(backend)
	release, entered := backend.holdReads()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := storage.Task(ctx, 2); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	publishBatch(t, storage, taskWrite(taskRecord(2, durable.TaskRunning)))
	release()
	<-done
	if got := statusOf(t, storage, 2); got != durable.TaskRunning {
		t.Fatalf("status = %s", got)
	}
	statusOf(t, storage, 2)
	if backend.reads() != 1 {
		t.Fatalf("task reads = %d, want 1", backend.reads())
	}
}

func TestCachedStorageKeepsBoundedEndedTasksAndEveryLiveOneUntilItEnds(t *testing.T) {
	backend := newCountingBackend()
	storage := session.NewCachedStorage(backend)
	publishBatch(t, storage, conversationWrite(1))
	for id := int64(2); id < 600; id++ {
		publishBatch(t, storage, taskWrite(taskRecord(id, durable.TaskPending)))
	}
	// More live tasks than the ended table holds: each one's next change finds it without a read.
	sizes := storage.Sizes()
	if sizes.Tasks != 0 || sizes.LiveTasks != 598 {
		t.Fatalf("sizes = %+v", sizes)
	}
	for id := int64(2); id < 600; id++ {
		statusOf(t, storage, id)
	}
	if backend.reads() != 0 {
		t.Fatalf("task reads = %d", backend.reads())
	}
	for id := int64(2); id < 600; id++ {
		publishBatch(t, storage, taskWrite(endedTask(id, nil)))
	}
	sizes = storage.Sizes()
	if sizes.Tasks != 256 || sizes.LiveTasks != 0 {
		t.Fatalf("sizes = %+v", sizes)
	}
}

func TestCachedStorageAppliesBatchStoredWithCommitWhenPublishedNotAtReceipt(t *testing.T) {
	backend := newCountingBackend()
	storage := session.NewCachedStorage(backend)
	publishBatch(t, storage, conversationWrite(1), taskWrite(taskRecord(2, durable.TaskPending)))
	batch := []durable.StorageWrite{taskWrite(taskRecord(2, durable.TaskRunning))}
	var receipts []int
	result, err := storage.Commit(ctx, [][]durable.StorageWrite{batch}, func(index int, _ durable.Seq) { receipts = append(receipts, index) })
	if err != nil || result != (durable.CommitResult{Stored: 1}) {
		t.Fatalf("commit = %+v, %v", result, err)
	}
	if len(receipts) != 1 || receipts[0] != 0 {
		t.Fatalf("receipts = %v", receipts)
	}
	// Stored, not yet published: the cache still shows what was published.
	if got := statusOf(t, storage, 2); got != durable.TaskPending {
		t.Fatalf("status = %s", got)
	}
	storage.Published(batch)
	if got := statusOf(t, storage, 2); got != durable.TaskRunning {
		t.Fatalf("status = %s", got)
	}
	if backend.reads() != 0 {
		t.Fatalf("task reads = %d", backend.reads())
	}
}

func TestCachedStorageDropsReadOverlappedByPublicationAndKeepsVersionsOnlyForReadsInFlight(t *testing.T) {
	backend := newCountingBackend()
	backend.tasks[2] = taskRecord(2, durable.TaskPending)
	backend.tasks[3] = taskRecord(3, durable.TaskPending)
	storage := session.NewCachedStorage(backend)
	release, entered := backend.holdReads()
	var wg sync.WaitGroup
	for _, id := range []durable.TaskId{2, 3} {
		wg.Go(func() {
			if _, err := storage.Task(ctx, id); err != nil {
				t.Error(err)
			}
		})
	}
	<-entered
	<-entered
	if got := storage.ReadsInFlight(); got != 2 {
		t.Fatalf("reads in flight = %d", got)
	}
	// A publication of another record does not drop the read of task 3.
	storage.Published([]durable.StorageWrite{taskWrite(taskRecord(2, durable.TaskRunning))})
	release()
	wg.Wait()
	if got := storage.ReadsInFlight(); got != 0 {
		t.Fatalf("reads in flight = %d", got)
	}
	if got := statusOf(t, storage, 2); got != durable.TaskRunning {
		t.Fatalf("status = %s", got)
	}
	if got := statusOf(t, storage, 3); got != durable.TaskPending {
		t.Fatalf("status = %s", got)
	}
	if backend.reads() != 2 {
		t.Fatalf("task reads = %d, want 2", backend.reads())
	}
}

func TestCachedStorageKeepsEndedTasksATaskWaitsOnUntilItMovedOnFromItsWait(t *testing.T) {
	backend := newCountingBackend()
	storage := session.NewCachedStorage(backend)
	publishBatch(t, storage, conversationWrite(1))
	on := []durable.TaskId{3, 4}
	waiting := func() anyTask {
		return waitingTask(durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status: durable.TaskWaiting, Checkpoint: phase("read"), On: on, Policy: durable.JoinAllSettled,
		})
	}
	running := func(name string) anyTask {
		return waitingTask(durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: phase(name)})
	}
	publishBatch(t, storage, taskWrite(waiting()))
	publishBatch(t, storage, taskWrite(endedTask(3, int64(3))), taskWrite(endedTask(4, int64(4))))
	// A burst of other ended tasks fills the bounded table.
	for id := int64(10); id < 300; id++ {
		publishBatch(t, storage, taskWrite(endedTask(id, id)))
	}
	sizes := storage.Sizes()
	if sizes.Awaited != 2 || sizes.Waits != 1 {
		t.Fatalf("sizes = %+v", sizes)
	}
	// Resumed: still held for the phase that reads the outcomes.
	publishBatch(t, storage, taskWrite(running("read")))
	for _, id := range on {
		if got := statusOf(t, storage, int64(id)); got != durable.TaskTerminal {
			t.Fatalf("task %d status = %s", id, got)
		}
	}
	if backend.reads() != 0 {
		t.Fatalf("task reads = %d", backend.reads())
	}
	sizes = storage.Sizes()
	if sizes.Awaited != 2 || sizes.Waits != 1 {
		t.Fatalf("sizes = %+v", sizes)
	}
	// Its next change ends the hold: the records go back to the bounded table, and no wait is left.
	publishBatch(t, storage, taskWrite(running("next")))
	sizes = storage.Sizes()
	if sizes.Awaited != 0 || sizes.Waits != 0 || sizes.Tasks != 256 {
		t.Fatalf("sizes = %+v", sizes)
	}
	// A wait that ends with its task leaves nothing either.
	publishBatch(t, storage, taskWrite(waiting()))
	sizes = storage.Sizes()
	if sizes.Awaited != 2 || sizes.Waits != 1 {
		t.Fatalf("sizes = %+v", sizes)
	}
	publishBatch(t, storage, taskWrite(endedTask(2, int64(2))))
	sizes = storage.Sizes()
	if sizes.Awaited != 0 || sizes.Waits != 0 {
		t.Fatalf("sizes = %+v", sizes)
	}
}

// Pi pins `expect(await storage.task(id)).toBe(record)`: the record the write carried is the one the cache serves. A Go write
// carries the record by value, so the cache keeps a copy of the struct and what the record references is shared: the same
// checkpoint and the same memos map, not copies of them. That is as much identity as a value type has.
func TestCachedStorageKeepsWrittenRecordItselfShared(t *testing.T) {
	storage := session.NewCachedStorage(newCountingBackend())
	record := taskRecord(2, durable.TaskPending)
	record.Memos = map[string]durable.JsonValue{"memo": "value"}
	storage.Published([]durable.StorageWrite{conversationWrite(1), taskWrite(record)})
	first, err := storage.Task(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := storage.Task(ctx, 2)
	if first != second {
		t.Fatal("two reads returned different records")
	}
	if first.State.Checkpoint == nil || first.State.Checkpoint != record.State.Checkpoint {
		t.Fatal("the written record's state was copied")
	}
	if reflect.ValueOf(first.Memos).Pointer() != reflect.ValueOf(record.Memos).Pointer() {
		t.Fatal("the written record's memos were copied")
	}
	if first.Id != record.Id || first.Kind != record.Kind || first.State.Status != record.State.Status {
		t.Fatalf("the cache serves %+v, not the record written %+v", first, record)
	}
	conversation, err := storage.Conversation(ctx, 1)
	if err != nil || conversation == nil || conversation.Id != 1 {
		t.Fatalf("conversation = %v, %v", conversation, err)
	}
	again, _ := storage.Conversation(ctx, 1)
	if conversation != again {
		t.Fatal("two reads returned different conversations")
	}
}

func TestCachedStorageReadFromSeveralGoroutinesWhileCommitsPublish(t *testing.T) {
	backend := newCountingBackend()
	for id := int64(2); id < 40; id++ {
		backend.tasks[durable.TaskId(id)] = taskRecord(id, durable.TaskPending)
	}
	storage := session.NewCachedStorage(backend)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				for id := int64(2); id < 40; id++ {
					if _, err := storage.Task(ctx, durable.TaskId(id)); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	for range 50 {
		for id := int64(2); id < 40; id++ {
			storage.Published([]durable.StorageWrite{taskWrite(taskRecord(id, durable.TaskRunning))})
		}
	}
	wg.Wait()
	storage.Sizes()
	if got := storage.ReadsInFlight(); got != 0 {
		t.Fatalf("reads in flight = %d", got)
	}
}

// recordBackend is the Storage under a CachedStorage for the records that are not tasks: it keeps the conversations and
// submissions written, counts their reads, and can hold a conversation read after it looked its record up.
type recordBackend struct {
	recordingStorage

	mu            sync.Mutex
	conversations map[durable.ConversationId]durable.ConversationRecord
	submissions   map[durable.SubmissionId]durable.SubmissionRecord
	conversation  int
	submission    int
	hold          chan struct{}
	entered       chan struct{}
}

func newRecordBackend() *recordBackend {
	return &recordBackend{
		log:           &readLog{},
		conversations: map[durable.ConversationId]durable.ConversationRecord{},
		submissions:   map[durable.SubmissionId]durable.SubmissionRecord{},
	}
}

func (backend *recordBackend) Conversation(_ context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	backend.mu.Lock()
	backend.conversation++
	record, ok := backend.conversations[id]
	hold, entered := backend.hold, backend.entered
	backend.mu.Unlock()
	if hold != nil {
		entered <- struct{}{}
		<-hold
	}
	if !ok {
		return nil, nil
	}
	return &record, nil
}

func (backend *recordBackend) Submission(_ context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.submission++
	record, ok := backend.submissions[id]
	if !ok {
		return nil, nil
	}
	return &record, nil
}

func (backend *recordBackend) counts() (conversations, submissions int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.conversation, backend.submission
}

func submissionWrite(id int64) durable.StorageWrite {
	return durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: durable.SubmissionId(id), ConversationId: 1}}
}

// pi: cached-storage.ts conversation()/submission()/published(): a conversation or a submission written and published is
// served without a read; a conversation read is kept, a submission read is not ("Cached as written only"), an absent record is
// kept for neither; each table is bounded (4096 conversations, 256 submissions), the least recently used leaving first.
// mutation-checked: not keeping a published conversation or submission, not serving either from memory, keeping a submission read, not keeping a conversation read, keeping an absent conversation, or a different bound fails it
func TestCachedStorageServesPublishedConversationsAndSubmissionsAndKeepsOnlyConversationReads(t *testing.T) {
	backend := newRecordBackend()
	backend.conversations[5] = durable.ConversationRecord{Id: 5}
	backend.submissions[9] = durable.SubmissionRecord{Id: 9, ConversationId: 1}
	storage := session.NewCachedStorage(backend)
	storage.Published([]durable.StorageWrite{conversationWrite(1), submissionWrite(4)})

	if conversation, err := storage.Conversation(ctx, 1); err != nil || conversation == nil || conversation.Id != 1 {
		t.Fatalf("Conversation(1) = %+v, %v", conversation, err)
	}
	if submission, err := storage.Submission(ctx, 4); err != nil || submission == nil || submission.Id != 4 {
		t.Fatalf("Submission(4) = %+v, %v", submission, err)
	}
	if conversations, submissions := backend.counts(); conversations != 0 || submissions != 0 {
		t.Fatalf("reads = %d conversations, %d submissions; want none for records published", conversations, submissions)
	}

	// A conversation read is kept, whoever else reads it; a submission read is not.
	for range 2 {
		if conversation, err := storage.Conversation(ctx, 5); err != nil || conversation == nil || conversation.Id != 5 {
			t.Fatalf("Conversation(5) = %+v, %v", conversation, err)
		}
		if submission, err := storage.Submission(ctx, 9); err != nil || submission == nil || submission.Id != 9 {
			t.Fatalf("Submission(9) = %+v, %v", submission, err)
		}
	}
	if conversations, submissions := backend.counts(); conversations != 1 || submissions != 2 {
		t.Fatalf("reads = %d conversations, %d submissions; want 1 and 2", conversations, submissions)
	}
	// An absent conversation is read every time, and nothing is kept for it.
	for range 2 {
		if conversation, err := storage.Conversation(ctx, 77); err != nil || conversation != nil {
			t.Fatalf("Conversation(77) = %+v, %v", conversation, err)
		}
	}
	if conversations, _ := backend.counts(); conversations != 3 {
		t.Fatalf("conversation reads = %d, want 3", conversations)
	}
	if sizes := storage.Sizes(); sizes.Conversations != 2 || sizes.Submissions != 1 {
		t.Fatalf("sizes = %+v, want 2 conversations (1 and 5) and 1 submission", sizes)
	}

	// Bounded: the least recently used record leaves first.
	for id := int64(100); id < 100+4096; id++ {
		storage.Published([]durable.StorageWrite{conversationWrite(durable.ConversationId(id))})
	}
	for id := int64(100); id < 100+256; id++ {
		storage.Published([]durable.StorageWrite{submissionWrite(id)})
	}
	if sizes := storage.Sizes(); sizes.Conversations != 4096 || sizes.Submissions != 256 {
		t.Fatalf("sizes = %+v, want 4096 conversations and 256 submissions", sizes)
	}
	before, beforeSubmissions := backend.counts()
	if _, err := storage.Conversation(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Submission(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if after, afterSubmissions := backend.counts(); after != before+1 || afterSubmissions != beforeSubmissions+1 {
		t.Fatalf("the oldest conversation and submission were not the ones dropped: reads went %d->%d and %d->%d", before, after, beforeSubmissions, afterSubmissions)
	}
	// A newest one is still kept.
	if _, err := storage.Conversation(ctx, 100+4095); err != nil {
		t.Fatal(err)
	}
	if after, _ := backend.counts(); after != before+1 {
		t.Fatalf("the newest conversation was dropped: reads = %d", after)
	}
}

// pi: cached-storage.ts #keep()/#wrote(): the read of a conversation that a commit published meanwhile wrote is not kept, as it
// may not have seen that commit: the cache serves what was published.
// mutation-checked: a publication of a conversation that does not mark its read in flight, or a read kept whatever was published since, fails it
func TestCachedStorageDropsAConversationReadAPublicationOverlapped(t *testing.T) {
	backend := newRecordBackend()
	backend.conversations[2] = durable.ConversationRecord{Id: 2}
	storage := session.NewCachedStorage(backend)
	hold, entered := make(chan struct{}), make(chan struct{}, 1)
	backend.hold, backend.entered = hold, entered
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := storage.Conversation(ctx, 2); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	storage.Published([]durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 2, Parent: &durable.ConversationParent{ConversationId: 1, At: 7}}}})
	close(hold)
	<-done
	conversation, err := storage.Conversation(ctx, 2)
	if err != nil || conversation == nil || conversation.Parent == nil || conversation.Parent.At != 7 {
		t.Fatalf("Conversation(2) = %+v, %v; want the record published while the read was in flight", conversation, err)
	}
	if reads, _ := backend.counts(); reads != 1 {
		t.Fatalf("conversation reads = %d, want 1", reads)
	}
}

// pi: cached-storage.ts #keep(): a read that no publication overlapped is kept, one that one did is not (a read of task 2 and a
// publication of task 2 overlap; a publication of another task does not), and the cache then serves what was published.
// mutation-checked: keeping every read, whatever was published since, fails it (keeping none fails the test above)
func TestCachedStorageKeepsAReadOnlyWhenNoPublicationOfItsRecordOverlappedIt(t *testing.T) {
	backend := newCountingBackend()
	backend.tasks[2] = taskRecord(2, durable.TaskPending)
	storage := session.NewCachedStorage(backend)
	release, entered := backend.holdReads()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := storage.Task(ctx, 2); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	// The write of the record the read is of: the read may have missed it, so it is dropped, and what was published stays.
	storage.Published([]durable.StorageWrite{taskWrite(taskRecord(2, durable.TaskRunning)), taskWrite(taskRecord(8, durable.TaskPending))})
	release()
	<-done
	if got := statusOf(t, storage, 2); got != durable.TaskRunning {
		t.Fatalf("status = %s, want the record published while the read was in flight", got)
	}
	// Task 8 was published, not read: a publication of one record leaves the reads in flight of another alone.
	if backend.reads() != 1 {
		t.Fatalf("task reads = %d, want 1", backend.reads())
	}
}

// pi: cached-storage.ts task(): `liveTasks ?? awaited.record ?? tasks.get(id)`: an ended task in the bounded table is served from
// it; a task that comes back to life leaves the table; an ended task that no one waits on is the table's, and is the table's
// only.
// mutation-checked: not serving the bounded table, keeping a revived task in it as well, or a published ended task not entering it fails it
func TestCachedStorageServesEndedTasksFromTheBoundedTableAndMovesARevivedOneOut(t *testing.T) {
	backend := newCountingBackend()
	storage := session.NewCachedStorage(backend)
	publishBatch(t, storage, conversationWrite(1))
	publishBatch(t, storage, taskWrite(endedTask(3, int64(3))))
	if got := statusOf(t, storage, 3); got != durable.TaskTerminal {
		t.Fatalf("status = %s", got)
	}
	if backend.reads() != 0 {
		t.Fatalf("task reads = %d, want 0 for an ended task the table holds", backend.reads())
	}
	if sizes := storage.Sizes(); sizes.Tasks != 1 || sizes.LiveTasks != 0 {
		t.Fatalf("sizes = %+v", sizes)
	}
	publishBatch(t, storage, taskWrite(taskRecord(3, durable.TaskRunning)))
	if sizes := storage.Sizes(); sizes.Tasks != 0 || sizes.LiveTasks != 1 {
		t.Fatalf("sizes = %+v, want the revived task live only", sizes)
	}
}

// pi: cached-storage.ts #followWait()/#release(): a wait names the tasks it waits on; an ended one the bounded table holds moves
// over to the wait's table (`awaited`), with its record, and goes back when no wait names it any more; a task that waits on
// another set, or waits again, lets go of what it waited on; two waits on one task keep it until both are over.
// mutation-checked: not moving an ended task over, leaving it in the bounded table, not counting a second wait, not releasing the set a task waited on before, or dropping the record on release fails it
func TestCachedStorageFollowsWaitsOnTasksThatEndedBeforeAndThatSeveralTasksWaitOn(t *testing.T) {
	backend := newCountingBackend()
	storage := session.NewCachedStorage(backend)
	publishBatch(t, storage, conversationWrite(1))
	waitingOn := func(id int64, on ...durable.TaskId) anyTask {
		record := taskRecord(id, durable.TaskWaiting)
		record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status: durable.TaskWaiting, Checkpoint: phase("read"), On: on, Policy: durable.JoinAllSettled,
		}
		return record
	}
	burst := func() {
		for id := int64(1000); id < 1300; id++ {
			publishBatch(t, storage, taskWrite(endedTask(id, id)))
		}
	}
	check := func(label string, awaited, waits, tasks int) {
		t.Helper()
		if sizes := storage.Sizes(); sizes.Awaited != awaited || sizes.Waits != waits || sizes.Tasks != tasks {
			t.Fatalf("%s: sizes = %+v, want awaited %d, waits %d, tasks %d", label, sizes, awaited, waits, tasks)
		}
	}

	// Task 3 ended before two tasks wait on it: it moves from the bounded table to the wait table, and stays through a burst.
	publishBatch(t, storage, taskWrite(endedTask(3, int64(3))))
	check("ended", 0, 0, 1)
	publishBatch(t, storage, taskWrite(waitingOn(20, 3)))
	check("one waiter", 1, 1, 0)
	publishBatch(t, storage, taskWrite(waitingOn(21, 3)))
	check("two waiters", 1, 2, 0)
	burst()
	if got := statusOf(t, storage, 3); got != durable.TaskTerminal {
		t.Fatalf("status = %s", got)
	}
	if backend.reads() != 0 {
		t.Fatalf("task reads = %d, want 0: the wait held task 3's record through the burst", backend.reads())
	}
	check("burst", 1, 2, 256)

	// One waiter moves on (its next change after leaving the wait): the other still holds task 3.
	publishBatch(t, storage, taskWrite(taskRecord(20, durable.TaskRunning)))
	check("a waiter left", 1, 2, 256)
	publishBatch(t, storage, taskWrite(taskRecord(20, durable.TaskRunning)))
	check("a waiter moved on", 1, 1, 256)
	burst()
	if got := statusOf(t, storage, 3); got != durable.TaskTerminal || backend.reads() != 0 {
		t.Fatalf("status = %s after %d reads; want task 3's record held for the other waiter", got, backend.reads())
	}

	// The last waits on another set instead: task 3 is let go, its record back in the bounded table.
	publishBatch(t, storage, taskWrite(waitingOn(21, 4)))
	check("waits on another", 1, 1, 256)
	if got := statusOf(t, storage, 3); got != durable.TaskTerminal || backend.reads() != 0 {
		t.Fatalf("status = %s after %d reads; want task 3's record back in the bounded table", got, backend.reads())
	}
	publishBatch(t, storage, taskWrite(endedTask(21, int64(21))))
	check("ended", 0, 0, 256)
}

// cachedScopeStorage is a Storage with a publication scope: Publishing runs publish inside the scope and records the context it got.
type cachedScopeStorage struct {
	recordingStorage
	ran *[]context.Context
}

func (storage cachedScopeStorage) Publishing(ctx context.Context, publish func()) {
	*storage.ran = append(*storage.ran, ctx)
	publish()
}

type cachedScopeKey struct{}

// pi: cached-storage.ts `publishing` and scopeOf(): the cache's publication scope is the backend's own, with the caller's
// context, when it has one; a backend without a scope has none, and the cache runs the publication itself, once.
// mutation-checked: a scope that is ignored, one called with another context, a scope reported for a backend without one, or a publication not run fails it
func TestCachedStoragePublishesInsideItsBackendsScopeWhenItHasOne(t *testing.T) {
	log := &readLog{}
	var ran []context.Context
	scoped := cachedScopeStorage{log: log, ran: &ran}
	if session.ScopeOf(scoped) == nil {
		t.Fatal("ScopeOf a backend with a scope is nil")
	}
	if session.ScopeOf(recordingStorage{log: log}) != nil {
		t.Fatal("ScopeOf a backend without a scope is not nil")
	}
	caller := context.WithValue(ctx, cachedScopeKey{}, "caller")
	published := 0
	session.NewCachedStorage(scoped).Publishing(caller, func() { published++ })
	if published != 1 || len(ran) != 1 || ran[0].Value(cachedScopeKey{}) != "caller" {
		t.Fatalf("published %d times, scope ran %d times; want once inside the scope with the caller's context", published, len(ran))
	}

	plain := session.NewCachedStorage(recordingStorage{log: log})
	plain.Publishing(caller, func() { published++ })
	if published != 2 {
		t.Fatalf("published = %d, want the publication run once without a scope", published)
	}
}

// pi: cached-storage.ts: entries, scans, documents, submission lookups and everything not cached pass straight through to the
// backend with the caller's arguments, and Mint, Close and Commit too.
// mutation-checked: a pass-through method that changes an argument or is not forwarded fails it
func TestCachedStoragePassesEveryOtherCallThroughWithItsArguments(t *testing.T) {
	log := &readLog{}
	storage := session.NewCachedStorage(recordingStorage{log: log})
	name := "member"
	address := durable.DocumentAddress{Kind: "k", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}, Key: &name}
	cutoff := durable.EntryId(12)
	calls := []struct {
		name string
		call func() error
		want string
	}{
		{"ScanConversations", func() error {
			_, err := storage.ScanConversations(ctx, durable.ConversationQuery{}, 11, nil)
			return err
		}, "read scan conversations 11"},
		{"Entry", func() error { _, err := storage.Entry(ctx, 9); return err }, "read entry 9"},
		{"VisibleEntry", func() error { _, err := storage.VisibleEntry(ctx, 2, 9); return err }, "read visible entry 2 9"},
		{"FindLatestHeadMarker", func() error { _, err := storage.FindLatestHeadMarker(ctx, 3, &cutoff); return err }, "read find latest head marker 3 12"},
		{"ScanEntries", func() error {
			_, err := storage.ScanEntries(ctx, durable.EntryQuery{ConversationId: 4}, 12, nil)
			return err
		}, "read scan entries 4 12"},
		{"ScanTasks", func() error { _, err := storage.ScanTasks(ctx, durable.TaskQuery{}, 13, nil); return err }, "read scan tasks 13"},
		{"ScanSubmissions", func() error { _, err := storage.ScanSubmissions(ctx, durable.SubmissionQuery{}, 14, nil); return err }, "read scan submissions 14"},
		{"SubmissionByRequest", func() error { _, err := storage.SubmissionByRequest(ctx, 7, "r-9"); return err }, "read submission by request 7 r-9"},
		{"FindDocument", func() error { _, err := storage.FindDocument(ctx, address, durable.DocumentPoint{}); return err }, "read find document " + durable.AddressId(address)},
		{"Document", func() error { _, err := storage.Document(ctx, 8, durable.DocumentPoint{}); return err }, "read document 8"},
		{"ScanDocuments", func() error {
			_, err := storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: 3}}, 15, nil)
			return err
		}, "read scan documents conversation 15"},
		{"Submission miss", func() error { _, err := storage.Submission(ctx, 5); return err }, "read submission 5"},
		{"Conversation miss", func() error { _, err := storage.Conversation(ctx, 6); return err }, "read conversation 6"},
		{"Task miss", func() error { _, err := storage.Task(ctx, 7); return err }, "read task 7"},
		{"Commit", func() error {
			result, err := storage.Commit(ctx, [][]durable.StorageWrite{nil, nil}, func(int, durable.Seq) {})
			if result.Stored != 2 {
				t.Errorf("Commit stored %d, want 2", result.Stored)
			}
			return err
		}, "read commit 2"},
		{"MintId", func() error {
			if id, err := storage.MintId(); id != 41 {
				t.Errorf("MintId = %d, %v; want 41", id, err)
			}
			return nil
		}, "read mint"},
		{"Close", func() error { return storage.Close(ctx) }, "read close"},
	}
	for _, tc := range calls {
		log.events = nil
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(log.events) != 1 || log.events[0] != tc.want {
			t.Errorf("%s: backend saw %q, want [%q]", tc.name, log.events, tc.want)
		}
	}
}
