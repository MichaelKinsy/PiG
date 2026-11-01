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
