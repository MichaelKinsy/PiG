package jsonl_test

// pi: packages/durable/src/storage/jsonl/storage.ts

// Ports packages/durable/test/jsonl-storage.test.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
)

var testContext = context.Background()

type storedTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

// must returns value or fails the test by panicking with err; Go cannot pass a multi-value call next to t.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expectError(t *testing.T, err error, messageIncludes string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), messageIncludes) {
		t.Fatalf("error = %v, want one including %q", err, messageIncludes)
	}
}

func expectEqual(t *testing.T, actual, expected any) {
	t.Helper()
	if !reflect.DeepEqual(durabletest.Normalize(actual), durabletest.Normalize(expected)) {
		t.Fatalf("got %s, want %s", durabletest.Describe(actual), durabletest.Describe(expected))
	}
}

func expectNil[T any](t *testing.T, value *T, err error) {
	t.Helper()
	mustDo(t, err)
	if value != nil {
		t.Fatalf("got %s, want undefined", durabletest.Describe(value))
	}
}

func tempDirectory(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func openStorage(t *testing.T, directory string, fileSystem jsonl.FileSystem, options jsonl.JsonlStorageOptions) *jsonl.JsonlStorage {
	t.Helper()
	if fileSystem == nil {
		fileSystem = &node.NodeFileSystem{Cwd: directory}
	}
	storage := must(jsonl.Open(testContext, directory, fileSystem, options))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	return storage
}

func open(t *testing.T, directory string) *jsonl.JsonlStorage {
	t.Helper()
	return openStorage(t, directory, nil, jsonl.JsonlStorageOptions{})
}

// reopeningStorage closes and reopens the directory after every commit, so every read observes recovered state.
type reopeningStorage struct {
	current   *jsonl.JsonlStorage
	directory string
	closed    bool
}

func openJsonl(directory string) (*jsonl.JsonlStorage, error) {
	return jsonl.Open(testContext, directory, &node.NodeFileSystem{Cwd: directory}, jsonl.JsonlStorageOptions{})
}

func (storage *reopeningStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	if storage.closed {
		return storage.current.Commit(ctx, writes)
	}
	seq, err := storage.current.Commit(ctx, writes)
	if closeErr := storage.current.Close(testContext); closeErr != nil && err == nil {
		err = closeErr
	}
	reopened, openErr := openJsonl(storage.directory)
	if openErr != nil {
		return 0, openErr
	}
	storage.current = reopened
	return seq, err
}

func (storage *reopeningStorage) MintId() (int64, error) { return storage.current.MintId() }

func (storage *reopeningStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	return storage.current.Conversation(ctx, id)
}

func (storage *reopeningStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	return storage.current.ScanConversations(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	return storage.current.Entry(ctx, id)
}

func (storage *reopeningStorage) VisibleEntry(ctx context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	return storage.current.VisibleEntry(ctx, conversationId, id)
}

func (storage *reopeningStorage) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, at *durable.EntryId) (*durable.EntryRecord, error) {
	return storage.current.FindLatestHeadMarker(ctx, conversationId, at)
}

func (storage *reopeningStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return storage.current.ScanEntries(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Task(ctx context.Context, id durable.TaskId) (*storedTask, error) {
	return storage.current.Task(ctx, id)
}

func (storage *reopeningStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[storedTask, durable.Cursor], error) {
	return storage.current.ScanTasks(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	return storage.current.Submission(ctx, id)
}

func (storage *reopeningStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	return storage.current.ScanSubmissions(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) SubmissionByRequest(ctx context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	return storage.current.SubmissionByRequest(ctx, conversationId, requestId)
}

func (storage *reopeningStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	return storage.current.FindDocument(ctx, address, at)
}

func (storage *reopeningStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	return storage.current.Document(ctx, id, at)
}

func (storage *reopeningStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	return storage.current.ScanDocuments(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Close(ctx context.Context) error {
	if storage.closed {
		return nil
	}
	storage.closed = true
	return storage.current.Close(ctx)
}

func TestJsonlStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "JsonlStorage", func(use func(durable.Storage) error) error {
		storage, err := openJsonl(t.TempDir())
		if err != nil {
			return err
		}
		defer func() { _ = storage.Close(testContext) }()
		return use(storage)
	})

	durabletest.RegisterStorageConformance(t, "JsonlStorage across reopen", func(use func(durable.Storage) error) error {
		directory := t.TempDir()
		current, err := openJsonl(directory)
		if err != nil {
			return err
		}
		storage := &reopeningStorage{current: current, directory: directory}
		defer func() { _ = storage.Close(testContext) }()
		return use(storage)
	})
}

func pendingTask(id durable.TaskId, phase string) storedTask {
	var checkpoint durable.JsonValue = map[string]any{"phase": phase}
	return storedTask{
		Id:             id,
		ConversationId: durable.ROOT_CONVERSATION_ID,
		Kind:           "test.task",
		Version:        1,
		Input:          nil,
		State:          durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
	}
}

func terminalTask(id durable.TaskId) storedTask {
	var result durable.JsonValue
	return storedTask{
		Id:             id,
		ConversationId: durable.ROOT_CONVERSATION_ID,
		Kind:           "test.task",
		Version:        1,
		Input:          nil,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status:  durable.TaskTerminal,
			Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result},
		},
	}
}

func commit(t *testing.T, storage durable.Storage, writes ...durable.StorageWrite) durable.Seq {
	t.Helper()
	return must(storage.Commit(testContext, writes))
}

func createRoot(t *testing.T, storage durable.Storage) {
	t.Helper()
	commit(t, storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}})
}

func mint[I durable.Id](t *testing.T, storage durable.Storage) I {
	t.Helper()
	return durable.IdFromNumber[I](must(storage.MintId()))
}

func sessionDocument(id durable.DocumentId, kind string) durable.DocumentCreate {
	return durable.DocumentCreate{Id: id, Kind: kind, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}
}

func base(value durable.JsonObject) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: value}
}

func delta(ops ...durable.Op) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentDelta, Version: 1, Ops: ops}
}

type failure struct {
	operation string // append, flush, write, rename, remove
	call      int
	mode      string // before, after, short
}

// instrumentedEnv records file operations and injects one failure; it plays the upstream NodeExecutionEnv subclass.
type instrumentedEnv struct {
	*node.NodeFileSystem
	operations []string
	failure    *failure
	calls      map[string]int
}

func newInstrumentedEnv(directory string) *instrumentedEnv {
	return &instrumentedEnv{NodeFileSystem: &node.NodeFileSystem{Cwd: directory}, calls: map[string]int{}}
}

func (instrumented *instrumentedEnv) fail(injected failure) {
	instrumented.failure = &injected
	instrumented.resetObservations()
}

func (instrumented *instrumentedEnv) clear() {
	instrumented.failure = nil
	instrumented.resetObservations()
}

func (instrumented *instrumentedEnv) resetObservations() {
	instrumented.calls = map[string]int{}
	instrumented.operations = []string{}
}

// observe records one operation and returns the failure injected at this call, if any.
func (instrumented *instrumentedEnv) observe(operation, description string) *failure {
	instrumented.calls[operation]++
	instrumented.operations = append(instrumented.operations, operation+":"+description)
	injected := instrumented.failure
	if injected == nil || injected.operation != operation || injected.call != instrumented.calls[operation] {
		return nil
	}
	return injected
}

func injected(message, path string) error {
	return env.NewFileError(env.FileErrorUnknown, message, path, nil)
}

func half(content any) []byte {
	var data []byte
	if text, ok := content.(string); ok {
		data = []byte(text)
	} else {
		data = content.([]byte)
	}
	return data[:max(1, len(data)/2)]
}

func (instrumented *instrumentedEnv) AppendFile(ctx context.Context, path string, content any) error {
	injectedFailure := instrumented.observe("append", filepath.Base(path))
	if injectedFailure == nil {
		return instrumented.NodeFileSystem.AppendFile(ctx, path, content)
	}
	switch injectedFailure.mode {
	case "before":
		return injected("injected append failure", path)
	case "short":
		if err := instrumented.NodeFileSystem.AppendFile(ctx, path, half(content)); err != nil {
			return err
		}
		return injected("injected short append", path)
	}
	if err := instrumented.NodeFileSystem.AppendFile(ctx, path, content); err != nil {
		return err
	}
	return injected("injected post-append failure", path)
}

func (instrumented *instrumentedEnv) FlushFile(ctx context.Context, path string) error {
	injectedFailure := instrumented.observe("flush", filepath.Base(path))
	if injectedFailure == nil {
		return instrumented.NodeFileSystem.FlushFile(ctx, path)
	}
	if injectedFailure.mode == "after" {
		if err := instrumented.NodeFileSystem.FlushFile(ctx, path); err != nil {
			return err
		}
	}
	return injected("injected flush failure", path)
}

func (instrumented *instrumentedEnv) WriteFile(ctx context.Context, path string, content any) error {
	injectedFailure := instrumented.observe("write", filepath.Base(path))
	if injectedFailure == nil {
		return instrumented.NodeFileSystem.WriteFile(ctx, path, content)
	}
	switch injectedFailure.mode {
	case "before":
		return injected("injected write failure", path)
	case "short":
		if err := instrumented.NodeFileSystem.WriteFile(ctx, path, half(content)); err != nil {
			return err
		}
		return injected("injected short write", path)
	}
	if err := instrumented.NodeFileSystem.WriteFile(ctx, path, content); err != nil {
		return err
	}
	return injected("injected post-write failure", path)
}

func (instrumented *instrumentedEnv) RenameFile(ctx context.Context, sourcePath, destinationPath string) error {
	injectedFailure := instrumented.observe("rename", filepath.Base(sourcePath)+"->"+filepath.Base(destinationPath))
	if injectedFailure == nil {
		return instrumented.NodeFileSystem.RenameFile(ctx, sourcePath, destinationPath)
	}
	if injectedFailure.mode == "after" {
		if err := instrumented.NodeFileSystem.RenameFile(ctx, sourcePath, destinationPath); err != nil {
			return err
		}
	}
	return injected("injected rename failure", sourcePath)
}

func (instrumented *instrumentedEnv) Remove(ctx context.Context, path string, options *env.RemoveOptions) error {
	injectedFailure := instrumented.observe("remove", filepath.Base(path))
	if injectedFailure == nil {
		return instrumented.NodeFileSystem.Remove(ctx, path, options)
	}
	if injectedFailure.mode == "after" {
		if err := instrumented.NodeFileSystem.Remove(ctx, path, options); err != nil {
			return err
		}
	}
	return injected("injected remove failure", path)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	text := string(must(os.ReadFile(path)))
	if text == "" {
		return []string{}
	}
	return strings.Split(strings.TrimRight(text, " \t\r\n"), "\n")
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	mustDo(t, err)
	return true
}

func expectLength(t *testing.T, lines []string, length int) {
	t.Helper()
	if len(lines) != length {
		t.Fatalf("got %d lines %q, want %d", len(lines), lines, length)
	}
}

func file(directory, kind string, id int64) string {
	return filepath.Join(directory, fmt.Sprintf("%s-%d.jsonl", kind, id))
}

func documentValue(t *testing.T, storage durable.Storage, id durable.DocumentId) durable.JsonValue {
	t.Helper()
	document := must(storage.Document(testContext, id, durable.CurrentPoint))
	if document == nil {
		t.Fatalf("document %d is missing", id)
	}
	return document.Value
}

func appendBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	appended := must(os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600))
	_, err := appended.Write(content)
	mustDo(t, errors.Join(err, appended.Close()))
}

func jsonLine(t *testing.T, value any) []byte {
	t.Helper()
	return append(must(json.Marshal(value)), '\n')
}

func reclaimFiles(t *testing.T, directory string) []string {
	t.Helper()
	names := []string{}
	for _, entry := range must(os.ReadDir(directory)) {
		if strings.HasSuffix(entry.Name(), ".reclaim") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// Pi: packages/durable/src/storage/jsonl/storage.ts:277 (commit).
// Pi: packages/durable/src/storage/jsonl/storage.ts:36 (conversation, document, entry).
// Pi: packages/durable/src/storage/jsonl/storage.ts:365 (findDocument).
// Pi: packages/durable/src/storage/jsonl/storage.ts:345 (task).
func TestPicoJsonlStoragePublicationAndRecovery(t *testing.T) {
	t.Run("opens through the Node adapter", func(t *testing.T) {
		directory := tempDirectory(t)
		t.Chdir(directory)
		storage := must(node.OpenNodeJsonlStorage(testContext, "store", jsonl.JsonlStorageOptions{}))
		t.Cleanup(func() { _ = storage.Close(testContext) })
		createRoot(t, storage)
		expectEqual(t, must(storage.Conversation(testContext, durable.ROOT_CONVERSATION_ID)), durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID})
	})

	t.Run("publishes every commit before reclaiming current-only document and terminal-task sidecars", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		taskId := mint[durable.TaskId](t, storage)
		documentId := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.TaskWrite{Value: pendingTask(taskId, "ready")})
		commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(documentId, "test.document"), Content: base(chorddelta.JsonObjectOf("count", 0))})
		commit(t, storage, durable.DocumentChangeWrite{Id: documentId, Content: delta()})
		commit(t, storage, durable.DocumentChangeWrite{Id: documentId, Content: base(chorddelta.JsonObjectOf("count", 1))})
		expectLength(t, readLines(t, file(directory, "task", int64(taskId))), 1)
		expectLength(t, readLines(t, file(directory, "doc", int64(documentId))), 1)

		commit(t, storage, durable.DocumentRetireWrite{Id: documentId})
		commit(t, storage, durable.TaskWrite{Value: terminalTask(taskId)})

		mainLines := readLines(t, filepath.Join(directory, "main.jsonl"))
		expectLength(t, mainLines, 7)
		if fileExists(t, file(directory, "task", int64(taskId))) || fileExists(t, file(directory, "doc", int64(documentId))) {
			t.Fatal("reclaimed sidecars still exist")
		}
		markerTypes := make([]string, 0, len(mainLines))
		for _, line := range mainLines {
			var marker struct{ Type string }
			mustDo(t, json.Unmarshal([]byte(line), &marker))
			markerTypes = append(markerTypes, marker.Type)
		}
		expectEqual(t, markerTypes, []string{"commit", "commit", "commit", "commit", "commit", "commit", "commit"})
	})

	t.Run("orders multiple live-task replacements in one sidecar by commit ordinal", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		taskId := mint[durable.TaskId](t, storage)
		commit(t, storage,
			durable.TaskWrite{Value: pendingTask(taskId, "first")},
			durable.TaskWrite{Value: pendingTask(taskId, "second")},
		)
		expectLength(t, readLines(t, file(directory, "task", int64(taskId))), 2)

		reopened := open(t, directory)
		expectEqual(t, must(reopened.Task(testContext, taskId)), pendingTask(taskId, "second"))
	})

	t.Run("removes a complete-plus-torn unconfirmed multi-record sidecar append", func(t *testing.T) {
		directory := tempDirectory(t)
		instrumented := newInstrumentedEnv(directory)
		storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
		createRoot(t, storage)
		taskId := mint[durable.TaskId](t, storage)
		instrumented.fail(failure{operation: "append", call: 1, mode: "short"})
		_, err := storage.Commit(testContext, []durable.StorageWrite{
			durable.TaskWrite{Value: pendingTask(taskId, "first")},
			durable.TaskWrite{Value: pendingTask(taskId, "second-"+strings.Repeat("x", 512))},
		})
		expectError(t, err, "poisoned")
		partial := must(os.ReadFile(file(directory, "task", int64(taskId))))
		if partial[len(partial)-1] == '\n' {
			t.Fatal("partial sidecar ends with a newline")
		}
		if count := bytes.Count(partial, []byte{'\n'}); count != 1 {
			t.Fatalf("partial sidecar has %d newlines, want 1", count)
		}

		reopened := open(t, directory)
		task, err := reopened.Task(testContext, taskId)
		expectNil(t, task, err)
		if size := must(os.Stat(file(directory, "task", int64(taskId)))).Size(); size != 0 {
			t.Fatalf("sidecar size = %d, want 0", size)
		}
	})

	t.Run("serializes the complete candidate before I/O and leaves preparation failures usable", func(t *testing.T) {
		directory := tempDirectory(t)
		instrumented := newInstrumentedEnv(directory)
		storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
		createRoot(t, storage)
		instrumented.clear()
		id := mint[durable.EntryId](t, storage)
		// Go has no BigInt; a channel is the JSON-unencodable value standing in for upstream's 1n.
		_, err := storage.Commit(testContext, []durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{
			Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "bad", Data: make(chan int),
		}}})
		if err == nil {
			t.Fatal("commit of unencodable data succeeded")
		}
		expectEqual(t, instrumented.operations, []string{})
		found, err := storage.Entry(testContext, id)
		expectNil(t, found, err)
		seq := commit(t, storage, durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "good"}})
		if seq != 2 {
			t.Fatalf("seq = %d, want 2", seq)
		}
	})

	for _, injectedFailure := range []failure{
		{"append", 1, "before"}, {"append", 1, "after"}, {"append", 1, "short"},
		{"append", 2, "before"}, {"append", 2, "after"},
		{"append", 3, "before"}, {"append", 3, "short"}, {"append", 3, "after"},
	} {
		name := fmt.Sprintf("poisons after %s failure at append %d and recovers only confirmed state", injectedFailure.mode, injectedFailure.call)
		t.Run(name, func(t *testing.T) {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
			createRoot(t, storage)
			firstId := mint[durable.DocumentId](t, storage)
			secondId := mint[durable.DocumentId](t, storage)
			instrumented.fail(injectedFailure)
			_, err := storage.Commit(testContext, []durable.StorageWrite{
				durable.DocumentCreateWrite{Record: sessionDocument(firstId, "first"), Content: base(chorddelta.JsonObjectOf("text", "α"))},
				durable.DocumentCreateWrite{Record: sessionDocument(secondId, "second"), Content: base(chorddelta.JsonObjectOf("text", "β"))},
			})
			expectError(t, err, "poisoned")
			_, err = storage.Document(testContext, firstId, durable.CurrentPoint)
			expectError(t, err, "poisoned")

			reopened := open(t, directory)
			markerSurvived := injectedFailure.call == 3 && injectedFailure.mode == "after"
			for id, text := range map[durable.DocumentId]string{firstId: "α", secondId: "β"} {
				document := must(reopened.Document(testContext, id, durable.CurrentPoint))
				if !markerSurvived {
					expectNil(t, document, nil)
					continue
				}
				if document == nil {
					t.Fatalf("document %d is missing", id)
				}
				expectEqual(t, document.Value, chorddelta.JsonObjectOf("text", text))
			}
		})
	}

	for _, injectedFailure := range []failure{
		{"flush", 1, "before"}, {"flush", 1, "after"}, {"flush", 2, "before"}, {"flush", 2, "after"},
	} {
		name := fmt.Sprintf("poisons after %s failure at flush %d and never writes a marker", injectedFailure.mode, injectedFailure.call)
		t.Run(name, func(t *testing.T) {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{Fsync: true})
			createRoot(t, storage)
			firstId := mint[durable.DocumentId](t, storage)
			secondId := mint[durable.DocumentId](t, storage)
			instrumented.fail(injectedFailure)
			_, err := storage.Commit(testContext, []durable.StorageWrite{
				durable.DocumentCreateWrite{Record: sessionDocument(firstId, "flush.first"), Content: base(chorddelta.NewJsonObject(0))},
				durable.DocumentCreateWrite{Record: sessionDocument(secondId, "flush.second"), Content: base(chorddelta.NewJsonObject(0))},
			})
			expectError(t, err, "poisoned")
			reopened := open(t, directory)
			for _, id := range []durable.DocumentId{firstId, secondId} {
				document, err := reopened.Document(testContext, id, durable.CurrentPoint)
				expectNil(t, document, err)
			}
		})
	}

	t.Run("orders publication flushes exactly and flushes main only to authorize reclamation", func(t *testing.T) {
		for _, fsync := range []bool{false, true} {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{Fsync: fsync})
			createRoot(t, storage)
			firstId := mint[durable.DocumentId](t, storage)
			secondId := mint[durable.DocumentId](t, storage)
			instrumented.clear()
			commit(t, storage,
				durable.DocumentCreateWrite{Record: sessionDocument(secondId, "second"), Content: base(chorddelta.NewJsonObject(0))},
				durable.DocumentCreateWrite{Record: sessionDocument(firstId, "first"), Content: base(chorddelta.NewJsonObject(0))},
			)
			first := fmt.Sprintf("doc-%d.jsonl", firstId)
			second := fmt.Sprintf("doc-%d.jsonl", secondId)
			expected := []string{"append:" + second, "append:" + first}
			if fsync {
				expected = append(expected, "flush:"+second, "flush:"+first)
			}
			expected = append(expected, "append:main.jsonl")
			expectEqual(t, instrumented.operations, expected)

			instrumented.clear()
			commit(t, storage, durable.DocumentChangeWrite{Id: firstId, Content: base(chorddelta.JsonObjectOf("checkpoint", true))})
			expected = []string{"append:" + first}
			if fsync {
				expected = append(expected, "flush:"+first)
			}
			expected = append(expected, "append:main.jsonl")
			if fsync {
				expected = append(expected, "flush:main.jsonl")
			}
			expected = append(expected, "write:"+first+".reclaim")
			if fsync {
				expected = append(expected, "flush:"+first+".reclaim")
			}
			expected = append(expected, "rename:"+first+".reclaim->"+first)
			expectEqual(t, instrumented.operations, expected)

			instrumented.clear()
			commit(t, storage, durable.EntryWrite{Value: durable.EntryRecord{
				Id: mint[durable.EntryId](t, storage), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "main-only",
			}})
			expectEqual(t, instrumented.operations, []string{"append:main.jsonl"})

			taskId := mint[durable.TaskId](t, storage)
			commit(t, storage, durable.TaskWrite{Value: pendingTask(taskId, "ready")})
			instrumented.clear()
			commit(t, storage, durable.TaskWrite{Value: terminalTask(taskId)})
			expected = []string{"append:main.jsonl"}
			if fsync {
				expected = append(expected, "flush:main.jsonl")
			}
			expected = append(expected, fmt.Sprintf("remove:task-%d.jsonl", taskId))
			expectEqual(t, instrumented.operations, expected)
		}
	})

	for _, injectedFailure := range []failure{
		{"write", 1, "before"}, {"write", 1, "short"}, {"write", 1, "after"}, {"rename", 1, "before"}, {"rename", 1, "after"},
	} {
		t.Run(fmt.Sprintf("recovers a committed base across reclaim %s %s", injectedFailure.operation, injectedFailure.mode), func(t *testing.T) {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
			createRoot(t, storage)
			id := mint[durable.DocumentId](t, storage)
			commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(id, "test.document"), Content: base(chorddelta.JsonObjectOf("count", 0))})
			commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"count"}, 1})})

			instrumented.fail(injectedFailure)
			if seq := commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(chorddelta.JsonObjectOf("count", 2))}); seq != 4 {
				t.Fatalf("seq = %d, want 4", seq)
			}
			expectEqual(t, documentValue(t, storage, id), chorddelta.JsonObjectOf("count", 2))
			mustDo(t, storage.Close(testContext))

			reopened := open(t, directory)
			expectEqual(t, documentValue(t, reopened, id), chorddelta.JsonObjectOf("count", 2))
			expectLength(t, readLines(t, file(directory, "doc", int64(id))), 1)
			expectEqual(t, reclaimFiles(t, directory), []string{})
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("recovers document-retirement reclamation across remove "+mode, func(t *testing.T) {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
			createRoot(t, storage)
			taskId := mint[durable.TaskId](t, storage)
			id := mint[durable.DocumentId](t, storage)
			commit(t, storage,
				durable.TaskWrite{Value: pendingTask(taskId, "ready")},
				durable.DocumentCreateWrite{
					Record:  durable.DocumentCreate{Id: id, Kind: "task.document", Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskId}},
					Content: base(chorddelta.JsonObjectOf("count", 1)),
				},
			)
			instrumented.fail(failure{"remove", 1, mode})
			if seq := commit(t, storage, durable.DocumentRetireWrite{Id: id}); seq != 3 {
				t.Fatalf("seq = %d, want 3", seq)
			}
			document, err := storage.Document(testContext, id, durable.CurrentPoint)
			expectNil(t, document, err)
			mustDo(t, storage.Close(testContext))

			reopened := open(t, directory)
			document, err = reopened.Document(testContext, id, durable.CurrentPoint)
			expectNil(t, document, err)
			expectEqual(t, must(reopened.Task(testContext, taskId)), pendingTask(taskId, "ready"))
			if fileExists(t, file(directory, "doc", int64(id))) {
				t.Fatal("retired document sidecar still exists")
			}
			expectEqual(t, reclaimFiles(t, directory), []string{})
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("defers reclamation after authorizing-main flush "+mode+" failure", func(t *testing.T) {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{Fsync: true})
			createRoot(t, storage)
			id := mint[durable.DocumentId](t, storage)
			commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(id, "test.document"), Content: base(chorddelta.JsonObjectOf("count", 0))})
			instrumented.fail(failure{"flush", 2, mode})
			if seq := commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(chorddelta.JsonObjectOf("count", 2))}); seq != 3 {
				t.Fatalf("seq = %d, want 3", seq)
			}
			sidecar := fmt.Sprintf("doc-%d.jsonl", id)
			expectEqual(t, instrumented.operations, []string{"append:" + sidecar, "flush:" + sidecar, "append:main.jsonl", "flush:main.jsonl"})
			expectEqual(t, documentValue(t, storage, id), chorddelta.JsonObjectOf("count", 2))
			expectLength(t, readLines(t, file(directory, "doc", int64(id))), 2)
			mustDo(t, storage.Close(testContext))

			recoveryEnv := newInstrumentedEnv(directory)
			recoveryEnv.fail(failure{"flush", 1, mode})
			deferred := openStorage(t, directory, recoveryEnv, jsonl.JsonlStorageOptions{Fsync: true})
			expectEqual(t, documentValue(t, deferred, id), chorddelta.JsonObjectOf("count", 2))
			expectEqual(t, recoveryEnv.operations, []string{"flush:main.jsonl"})
			expectLength(t, readLines(t, file(directory, "doc", int64(id))), 2)
			mustDo(t, deferred.Close(testContext))

			reclaimed := openStorage(t, directory, nil, jsonl.JsonlStorageOptions{Fsync: true})
			expectEqual(t, documentValue(t, reclaimed, id), chorddelta.JsonObjectOf("count", 2))
			expectLength(t, readLines(t, file(directory, "doc", int64(id))), 1)
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("keeps a committed base usable after reclaim-temp flush "+mode+" failure", func(t *testing.T) {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{Fsync: true})
			createRoot(t, storage)
			id := mint[durable.DocumentId](t, storage)
			commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(id, "test.document"), Content: base(chorddelta.JsonObjectOf("count", 0))})
			instrumented.fail(failure{"flush", 3, mode})
			if seq := commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(chorddelta.JsonObjectOf("count", 2))}); seq != 3 {
				t.Fatalf("seq = %d, want 3", seq)
			}
			instrumented.clear()
			commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"count"}, 3})})
			mustDo(t, storage.Close(testContext))

			reopened := openStorage(t, directory, nil, jsonl.JsonlStorageOptions{Fsync: true})
			expectEqual(t, documentValue(t, reopened, id), chorddelta.JsonObjectOf("count", 3))
			expectLength(t, readLines(t, file(directory, "doc", int64(id))), 2)
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("recovers terminal-task reclamation across remove "+mode, func(t *testing.T) {
			directory := tempDirectory(t)
			instrumented := newInstrumentedEnv(directory)
			storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
			createRoot(t, storage)
			id := mint[durable.TaskId](t, storage)
			commit(t, storage, durable.TaskWrite{Value: pendingTask(id, "ready")})
			instrumented.fail(failure{"remove", 1, mode})
			if seq := commit(t, storage, durable.TaskWrite{Value: terminalTask(id)}); seq != 3 {
				t.Fatalf("seq = %d, want 3", seq)
			}
			expectEqual(t, must(storage.Task(testContext, id)), terminalTask(id))
			mustDo(t, storage.Close(testContext))

			reopened := open(t, directory)
			expectEqual(t, must(reopened.Task(testContext, id)), terminalTask(id))
			if fileExists(t, file(directory, "task", int64(id))) {
				t.Fatal("terminal task sidecar still exists")
			}
			expectEqual(t, reclaimFiles(t, directory), []string{})
		})
	}

	t.Run("appends later deltas to the replacement sidecar after a current-only base", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(id, "test.document"), Content: base(chorddelta.JsonObjectOf("count", 0))})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(chorddelta.JsonObjectOf("count", 10))})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"count"}, 11})})
		expectLength(t, readLines(t, file(directory, "doc", int64(id))), 2)
		reopened := open(t, directory)
		expectEqual(t, documentValue(t, reopened, id), chorddelta.JsonObjectOf("count", 11))
	})

	t.Run("never reclaims rewindable document history, including after a base and retirement", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		record := durable.DocumentCreate{
			Id: id, Kind: "rewindable",
			Scope:   durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID},
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		}
		createdAt := commit(t, storage, durable.DocumentCreateWrite{Record: record, Content: base(chorddelta.JsonObjectOf("count", 0))})
		changedAt := commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"count"}, 1})})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(chorddelta.JsonObjectOf("count", 2))})
		commit(t, storage, durable.DocumentRetireWrite{Id: id})

		expectLength(t, readLines(t, file(directory, "doc", int64(id))), 3)
		reopened := open(t, directory)
		expectEqual(t, must(reopened.Document(testContext, id, durable.AtSeq(createdAt))).Value, chorddelta.JsonObjectOf("count", 0))
		expectEqual(t, must(reopened.Document(testContext, id, durable.AtSeq(changedAt))).Value, chorddelta.JsonObjectOf("count", 1))
		document, err := reopened.Document(testContext, id, durable.CurrentPoint)
		expectNil(t, document, err)
		expectLength(t, readLines(t, file(directory, "doc", int64(id))), 3)
	})

	t.Run("reclaims retired task-, session-, and latest-conversation document sidecars", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		taskId := mint[durable.TaskId](t, storage)
		sessionId := mint[durable.DocumentId](t, storage)
		latestId := mint[durable.DocumentId](t, storage)
		taskDocumentId := mint[durable.DocumentId](t, storage)
		createdAt := commit(t, storage,
			durable.TaskWrite{Value: pendingTask(taskId, "ready")},
			durable.DocumentCreateWrite{Record: sessionDocument(sessionId, "session"), Content: base(chorddelta.NewJsonObject(0))},
			durable.DocumentCreateWrite{
				Record: durable.DocumentCreate{
					Id: latestId, Kind: "latest",
					Scope:   durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID},
					History: durable.HistoryLatest, Fork: durable.ForkCurrent,
				},
				Content: base(chorddelta.NewJsonObject(0)),
			},
			durable.DocumentCreateWrite{
				Record:  durable.DocumentCreate{Id: taskDocumentId, Kind: "task", Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskId}},
				Content: base(chorddelta.NewJsonObject(0)),
			},
		)
		retiredAt := commit(t, storage,
			durable.DocumentRetireWrite{Id: sessionId},
			durable.DocumentRetireWrite{Id: latestId},
			durable.DocumentRetireWrite{Id: taskDocumentId},
			durable.TaskWrite{Value: terminalTask(taskId)},
		)
		for _, path := range []string{
			file(directory, "doc", int64(sessionId)), file(directory, "doc", int64(latestId)),
			file(directory, "doc", int64(taskDocumentId)), file(directory, "task", int64(taskId)),
		} {
			if fileExists(t, path) {
				t.Fatalf("%s still exists", filepath.Base(path))
			}
		}

		reopened := open(t, directory)
		expectEqual(t, must(reopened.Task(testContext, taskId)), terminalTask(taskId))
		for _, id := range []durable.DocumentId{sessionId, latestId, taskDocumentId} {
			document, err := reopened.Document(testContext, id, durable.CurrentPoint)
			expectNil(t, document, err)
		}
		address := durable.DocumentAddress{Kind: "session", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}
		found := must(reopened.FindDocument(testContext, address, durable.AtSeq(createdAt)))
		if !durabletest.MatchObject(durabletest.Normalize(found), durabletest.Normalize(map[string]any{
			"id": sessionId, "createdAt": createdAt, "retiredAt": retiredAt,
		})) {
			t.Fatalf("findDocument at creation = %s", durabletest.Describe(found))
		}
		missing, err := reopened.FindDocument(testContext, address, durable.AtSeq(retiredAt))
		expectNil(t, missing, err)
	})

	t.Run("truncates torn UTF-8 tails at exact byte offsets and reuses the unconfirmed sequence", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		documentId := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(documentId, "test.document"), Content: base(chorddelta.JsonObjectOf("text", "kept"))})
		sidecarPath := file(directory, "doc", int64(documentId))
		mainPath := filepath.Join(directory, "main.jsonl")
		sidecarSize := must(os.Stat(sidecarPath)).Size()
		mainSize := must(os.Stat(mainPath)).Size()
		torn := []byte(`{"text":"€`)
		appendBytes(t, sidecarPath, torn[:len(torn)-1])
		appendBytes(t, mainPath, torn[:len(torn)-1])

		reopened := open(t, directory)
		if size := must(os.Stat(sidecarPath)).Size(); size != sidecarSize {
			t.Fatalf("sidecar size = %d, want %d", size, sidecarSize)
		}
		if size := must(os.Stat(mainPath)).Size(); size != mainSize {
			t.Fatalf("main size = %d, want %d", size, mainSize)
		}
		if seq := commit(t, reopened, durable.DocumentChangeWrite{Id: documentId, Content: delta()}); seq != 3 {
			t.Fatalf("seq = %d, want 3", seq)
		}
	})

	t.Run("removes complete unconfirmed sidecar tails without resurrecting terminal tasks", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		taskId := mint[durable.TaskId](t, storage)
		commit(t, storage, durable.TaskWrite{Value: pendingTask(taskId, "ready")})
		commit(t, storage, durable.TaskWrite{Value: terminalTask(taskId)})
		sidecarPath := file(directory, "task", int64(taskId))
		if fileExists(t, sidecarPath) {
			t.Fatal("terminal task sidecar still exists")
		}
		appendBytes(t, sidecarPath, jsonLine(t, map[string]any{
			"format": 1, "type": "record", "seq": 4, "ordinal": 0,
			"payload": map[string]any{"type": "task", "value": pendingTask(taskId, "stale")},
		}))

		reopened := open(t, directory)
		if fileExists(t, sidecarPath) {
			t.Fatal("unconfirmed terminal task sidecar was not removed")
		}
		expectEqual(t, must(reopened.Task(testContext, taskId)), terminalTask(taskId))
		if seq := commit(t, reopened); seq != 4 {
			t.Fatalf("seq = %d, want 4", seq)
		}
	})

	t.Run("fails open when confirmed sidecar data is missing", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		documentId := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(documentId, "test.document"), Content: base(chorddelta.NewJsonObject(0))})
		mustDo(t, os.WriteFile(file(directory, "doc", int64(documentId)), nil, 0o600))
		_, err := openJsonl(directory)
		expectError(t, err, "Missing confirmed sidecar record")
	})

	t.Run("rejects a confirmed record after an unconfirmed sidecar record", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		documentId := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(documentId, "test.document"), Content: base(chorddelta.JsonObjectOf("count", 0))})
		commit(t, storage, durable.DocumentChangeWrite{Id: documentId, Content: delta(durable.Op{"s", []any{"count"}, 1})})
		path := file(directory, "doc", int64(documentId))
		lines := readLines(t, path)
		unconfirmed := jsonLine(t, map[string]any{
			"format": 1, "type": "record", "seq": 2, "ordinal": 999,
			"payload": map[string]any{"type": "document", "id": documentId, "content": map[string]any{"kind": "delta", "version": 1, "ops": []any{}}},
		})
		mustDo(t, os.WriteFile(path, []byte(lines[0]+"\n"+string(unconfirmed)+lines[1]+"\n"), 0o600))
		_, err := openJsonl(directory)
		expectError(t, err, "Confirmed record follows an unconfirmed tail")
	})

	t.Run("rejects non-increasing main commit sequences", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		path := filepath.Join(directory, "main.jsonl")
		appendBytes(t, path, must(os.ReadFile(path)))
		_, err := openJsonl(directory)
		expectError(t, err, "Commit sequence does not strictly increase")
	})

	t.Run("rejects structurally invalid confirmed document content", func(t *testing.T) {
		directory := tempDirectory(t)
		storage := open(t, directory)
		createRoot(t, storage)
		documentId := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{Record: sessionDocument(documentId, "test.document"), Content: base(chorddelta.NewJsonObject(0))})
		path := file(directory, "doc", int64(documentId))
		var record map[string]any
		mustDo(t, json.Unmarshal(bytes.TrimSpace(must(os.ReadFile(path))), &record))
		delete(record["payload"].(map[string]any)["content"].(map[string]any), "value")
		mustDo(t, os.WriteFile(path, jsonLine(t, record), 0o600))
		_, err := openJsonl(directory)
		expectError(t, err, "Invalid document content")
	})

	t.Run("rejects malformed complete main and sidecar lines", func(t *testing.T) {
		mainDirectory := tempDirectory(t)
		createRoot(t, open(t, mainDirectory))
		appendBytes(t, filepath.Join(mainDirectory, "main.jsonl"), []byte("{bad}\n"))
		_, err := openJsonl(mainDirectory)
		expectError(t, err, "Malformed complete main.jsonl")

		sidecarDirectory := tempDirectory(t)
		createRoot(t, open(t, sidecarDirectory))
		mustDo(t, os.WriteFile(filepath.Join(sidecarDirectory, "doc-99.jsonl"), []byte("{bad}\n"), 0o600))
		_, err = openJsonl(sidecarDirectory)
		expectError(t, err, "Malformed complete doc-99.jsonl")
	})
}

// Recovery rejects sidecar records whose (seq, ordinal) order does not strictly increase, including two records of
// one commit (packages/durable/src/storage/jsonl/storage.ts:570-577). No upstream case reorders records of one
// commit, so this guards the ordinal half of the check.
func TestJsonlStorageRejectsSameCommitSidecarRecordsOutOfOrdinalOrder(t *testing.T) {
	directory := tempDirectory(t)
	storage := open(t, directory)
	createRoot(t, storage)
	taskId := mint[durable.TaskId](t, storage)
	commit(t, storage,
		durable.TaskWrite{Value: pendingTask(taskId, "first")},
		durable.TaskWrite{Value: pendingTask(taskId, "second")},
	)
	path := file(directory, "task", int64(taskId))
	lines := readLines(t, path)
	expectLength(t, lines, 2)
	mustDo(t, os.WriteFile(path, []byte(lines[1]+"\n"+lines[0]+"\n"), 0o600))
	_, err := openJsonl(directory)
	expectError(t, err, fmt.Sprintf("Sidecar records are out of order in task-%d.jsonl", taskId))
}

// Records read back from the JSONL files keep their dynamic members' key order, as JSON.parse gives Pi; the reopening storage reads
// every record from its file.
func TestJsonlStorageKeepsRecordKeyOrder(t *testing.T) {
	directory := t.TempDir()
	current, err := openJsonl(directory)
	if err != nil {
		t.Fatal(err)
	}
	storage := &reopeningStorage{current: current, directory: directory}
	defer func() { _ = storage.Close(testContext) }()
	if err := durabletest.CheckRecordKeyOrder(storage); err != nil {
		t.Fatal(err)
	}
}
