// Package jsonl is the portable JSONL implementation of the durable Storage contract: one main commit log and
// per-document and per-task sidecar logs in one directory, accessed through a file system capability.
package jsonl

// Ports packages/durable/src/storage/jsonl/storage.ts
// Ports packages/durable/src/storage/jsonl/index.ts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/internal/ordered"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

const (
	formatVersion = 1
	mainFile      = "main.jsonl"
	reclaimSuffix = ".reclaim"
)

type storedTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

// FileSystem is the part of env.FileSystem that JSONL storage uses. Every env.FileSystem satisfies it.
type FileSystem interface {
	AbsolutePath(ctx context.Context, path string) (string, error)
	JoinPath(ctx context.Context, parts []string) (string, error)
	ReadBinaryFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, content any) error
	AppendFile(ctx context.Context, path string, content any) error
	TruncateFile(ctx context.Context, path string, size int64) error
	FlushFile(ctx context.Context, path string) error
	RenameFile(ctx context.Context, sourcePath, destinationPath string) error
	ListDir(ctx context.Context, path string) ([]env.FileInfo, error)
	CreateDir(ctx context.Context, path string, options *env.CreateDirOptions) error
	Remove(ctx context.Context, path string, options *env.RemoveOptions) error
}

var _ FileSystem = env.FileSystem(nil)

// JsonlStorageOptions configures JsonlStorage.
type JsonlStorageOptions struct {
	// Fsync flushes every affected sidecar before appending the main marker. Defaults to false.
	Fsync bool
}

// JsonlCorruptionError reports complete persisted data that cannot be recovered.
type JsonlCorruptionError struct {
	Message string
	Cause   error
}

// NewJsonlCorruptionError is `new JsonlCorruptionError(message, cause)`; cause may be nil.
func NewJsonlCorruptionError(message string, cause error) *JsonlCorruptionError {
	e := &JsonlCorruptionError{Message: message, Cause: cause}
	return e
}

// Name is the error class name, Pi's `name` property.
func (*JsonlCorruptionError) Name() string { return "JsonlCorruptionError" }

func (e *JsonlCorruptionError) Error() string { return e.Message }

// Unwrap returns the cause.
func (e *JsonlCorruptionError) Unwrap() error { return e.Cause }

// JsonlStoragePoisonedError reports storage whose files may hold a partial publication; it must be reopened.
type JsonlStoragePoisonedError struct {
	// Message is the `message` property the constructor sets.
	Message string
	Cause   error
}

// NewJsonlStoragePoisonedError is `new JsonlStoragePoisonedError(cause)`: it sets the fixed message
// "JSONL storage is poisoned and must be reopened" (storage.ts:90).
func NewJsonlStoragePoisonedError(cause error) *JsonlStoragePoisonedError {
	return &JsonlStoragePoisonedError{Message: "JSONL storage is poisoned and must be reopened", Cause: cause}
}

// Name is the `name` property, "JsonlStoragePoisonedError".
func (e *JsonlStoragePoisonedError) Name() string { return "JsonlStoragePoisonedError" }

func (e *JsonlStoragePoisonedError) Error() string { return e.Message }

// Unwrap returns the cause.
func (e *JsonlStoragePoisonedError) Unwrap() error { return e.Cause }

// fileFailure is the upstream `JSONL <action> failed: <message>` wrapper of a file system failure.
type fileFailure struct {
	message string
	cause   error
}

func (e *fileFailure) Error() string { return e.message }
func (e *fileFailure) Unwrap() error { return e.cause }

func errorFromFile(action string, err error) error {
	message := err.Error()
	if fileError, ok := errors.AsType[*env.FileError](err); ok {
		message = fileError.Message
	}
	return &fileFailure{message: fmt.Sprintf("JSONL %s failed: %s", action, message), cause: err}
}

func isNotFound(err error) bool {
	var fileError *env.FileError
	return errors.As(err, &fileError) && fileError.Code == env.FileErrorNotFound
}

func sidecarFileName(kind string, id int64) string { return fmt.Sprintf("%s-%d.jsonl", kind, id) }

var (
	sidecarFilePattern = regexp.MustCompile(`^(?:doc|task)-(?:0|[1-9]\d*)\.jsonl$`)
	reclaimFilePattern = regexp.MustCompile(`^(?:doc|task)-(?:0|[1-9]\d*)\.jsonl\.reclaim$`)
)

func isCurrentOnly(record durable.DocumentCreate) bool {
	return record.Scope.Kind != durable.ScopeConversation || record.History == durable.HistoryLatest
}

type sidecarKey struct {
	file    string
	seq     int64
	ordinal int64
}

// orderedFiles is an insertion-ordered file-to-content map.
type orderedFiles struct {
	names    []string
	contents map[string]string
}

func newOrderedFiles() *orderedFiles { return &orderedFiles{contents: map[string]string{}} }

func (files *orderedFiles) set(name, content string) {
	if _, ok := files.contents[name]; !ok {
		files.names = append(files.names, name)
	}
	files.contents[name] = content
}

func (files *orderedFiles) get(name string) (string, bool) {
	content, ok := files.contents[name]
	return content, ok
}

// Wire shapes. Field order matches upstream object literals, so persisted lines keep upstream key order.

type contentWire struct {
	Kind    durable.DocumentContentKind `json:"kind"`
	Version int                         `json:"version"`
	Value   *durable.JsonObject         `json:"value,omitempty"`
	Ops     *[]durable.Op               `json:"ops,omitempty"`
}

func contentToWire(content durable.DocumentContent) contentWire {
	wire := contentWire{Kind: content.Kind, Version: content.Version}
	if content.Kind == durable.ContentBase {
		value := content.Value
		if value == nil {
			value = delta.NewJsonObject(0)
		}
		wire.Value = &value
	} else {
		ops := content.Ops
		if ops == nil {
			ops = []durable.Op{}
		}
		wire.Ops = &ops
	}
	return wire
}

type recordWrite struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type idWrite struct {
	Type string `json:"type"`
	Id   int64  `json:"id"`
}

type sidecarWrite struct {
	Type    string `json:"type"`
	Id      int64  `json:"id"`
	Ordinal int64  `json:"ordinal"`
}

type createWrite struct {
	Type    string                 `json:"type"`
	Record  durable.DocumentCreate `json:"record"`
	Ordinal int64                  `json:"ordinal"`
}

type markerWire struct {
	Format int    `json:"format"`
	Type   string `json:"type"`
	Seq    int64  `json:"seq"`
	Writes []any  `json:"writes"`
}

type taskPayload struct {
	Type  string     `json:"type"`
	Value storedTask `json:"value"`
}

type documentPayload struct {
	Type    string      `json:"type"`
	Id      int64       `json:"id"`
	Content contentWire `json:"content"`
}

type sidecarRecordWire struct {
	Format  int    `json:"format"`
	Type    string `json:"type"`
	Seq     int64  `json:"seq"`
	Ordinal int64  `json:"ordinal"`
	Payload any    `json:"payload"`
}

// mainOperation is one decoded marker write.
type mainOperation struct {
	kind    string
	write   durable.StorageWrite // conversation, entry, submission, terminal task, document.retire
	id      int64
	ordinal int64
	record  durable.DocumentCreate
}

type mainMarker struct {
	seq    int64
	writes []mainOperation
}

// sidecarRecord is one decoded sidecar line: a live task record or document content.
type sidecarRecord struct {
	seq     int64
	ordinal int64
	task    *storedTask
	docId   int64
	content *durable.DocumentContent
	raw     []byte
}

type parsedLine[T any] struct {
	value T
	start int64
}

type parsedFile[T any] struct {
	path  string
	lines []parsedLine[T]
}

type encodedCommit struct {
	marker   string
	sidecars *orderedFiles
}

func jsonLine(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}

// JsonlStorage is the portable JSONL implementation of durable.Storage. An in-memory MemoryStorage holds the
// recovered state; every commit is published to the files before it is applied there.
type JsonlStorage struct {
	fs        FileSystem
	directory string
	mainPath  string
	fsync     bool
	memory    *storage.MemoryStorage

	// commitMu serializes commits and owns the sidecar bookkeeping below.
	commitMu             sync.Mutex
	currentOnlyDocuments map[durable.DocumentId]bool
	liveTaskSidecars     map[durable.TaskId]bool

	stateMu     sync.Mutex
	closed      bool
	poisonError *JsonlStoragePoisonedError
}

var _ durable.Storage = (*JsonlStorage)(nil)

// Open opens or creates a JSONL storage directory using the supplied file system and recovers its state.
func Open(ctx context.Context, directory string, fs FileSystem, options JsonlStorageOptions) (*JsonlStorage, error) {
	absolute, err := fs.AbsolutePath(ctx, directory)
	if err != nil {
		return nil, errorFromFile("path resolution", err)
	}
	if err := fs.CreateDir(ctx, absolute, &env.CreateDirOptions{Recursive: new(true)}); err != nil {
		return nil, errorFromFile("directory creation", err)
	}
	mainPath, err := fs.JoinPath(ctx, []string{absolute, mainFile})
	if err != nil {
		return nil, errorFromFile("path join", err)
	}
	jsonl := &JsonlStorage{
		fs:                   fs,
		directory:            absolute,
		mainPath:             mainPath,
		fsync:                options.Fsync,
		memory:               storage.NewMemoryStorage(),
		currentOnlyDocuments: map[durable.DocumentId]bool{},
		liveTaskSidecars:     map[durable.TaskId]bool{},
	}
	if err := jsonl.recover(ctx); err != nil {
		return nil, err
	}
	return jsonl, nil
}

type resolvedSidecar struct {
	file    string
	content string
	path    string
}

// Commit publishes every sidecar record, then the main marker, then applies the batch in memory and reclaims sidecars
// whose content the batch superseded.
func (jsonl *JsonlStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	jsonl.commitMu.Lock()
	defer jsonl.commitMu.Unlock()
	if err := jsonl.assertUsable(); err != nil {
		return 0, err
	}
	prepared, err := jsonl.memory.PrepareCommit(writes)
	if err != nil {
		return 0, err
	}
	resolved := prepared.Writes()
	encoded, err := encodeCommit(prepared.Seq(), resolved)
	if err != nil {
		return 0, err
	}
	reclamations := jsonl.planReclamations(resolved, encoded)
	sidecars := make([]resolvedSidecar, 0, len(encoded.sidecars.names))
	for _, file := range encoded.sidecars.names {
		path, err := jsonl.resolveFile(ctx, file)
		if err != nil {
			return 0, err
		}
		content, _ := encoded.sidecars.get(file)
		sidecars = append(sidecars, resolvedSidecar{file: file, content: content, path: path})
	}

	for _, sidecar := range sidecars {
		if err := jsonl.fs.AppendFile(ctx, sidecar.path, sidecar.content); err != nil {
			return 0, jsonl.poison(errorFromFile("append to "+sidecar.file, err))
		}
	}
	if jsonl.fsync {
		for _, sidecar := range sidecars {
			if err := jsonl.fs.FlushFile(ctx, sidecar.path); err != nil {
				return 0, jsonl.poison(errorFromFile("flush of "+sidecar.file, err))
			}
		}
	}
	if err := jsonl.fs.AppendFile(ctx, jsonl.mainPath, encoded.marker); err != nil {
		return 0, jsonl.poison(errorFromFile("append to "+mainFile, err))
	}
	seq := prepared.Apply()
	jsonl.adoptSidecarState(resolved)
	jsonl.reclaimSidecars(ctx, reclamations)
	return seq, nil
}

// MintId returns a fresh candidate from the Session-global numeric ID namespace.
func (jsonl *JsonlStorage) MintId() (int64, error) {
	store, err := jsonl.store()
	if err != nil {
		return 0, err
	}
	return store.MintId()
}

// Conversation looks up one conversation by exact ID.
func (jsonl *JsonlStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.Conversation(ctx, id)
}

// ScanConversations scans conversations in ascending ID order.
func (jsonl *JsonlStorage) ScanConversations(
	ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	store, err := jsonl.store()
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	return store.ScanConversations(ctx, query, limit, cursor)
}

// Entry looks up one global entry and the sequence of the commit that persisted it.
func (jsonl *JsonlStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.Entry(ctx, id)
}

// VisibleEntry looks up one entry only when it is visible through the requested conversation's ancestry.
func (jsonl *JsonlStorage) VisibleEntry(
	ctx context.Context, conversationId durable.ConversationId, id durable.EntryId,
) (*durable.EntryAt, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.VisibleEntry(ctx, conversationId, id)
}

// FindLatestHeadMarker returns the newest visible entry with a head at or below the optional inclusive cutoff.
func (jsonl *JsonlStorage) FindLatestHeadMarker(
	ctx context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
}

// ScanEntries scans the inclusive visible range newest-first.
func (jsonl *JsonlStorage) ScanEntries(
	ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	store, err := jsonl.store()
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	return store.ScanEntries(ctx, query, limit, cursor)
}

// Task looks up the latest complete record for one task.
func (jsonl *JsonlStorage) Task(ctx context.Context, id durable.TaskId) (*storedTask, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.Task(ctx, id)
}

// ScanTasks scans task records matching every supplied filter.
func (jsonl *JsonlStorage) ScanTasks(
	ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor,
) (durable.Page[storedTask, durable.Cursor], error) {
	store, err := jsonl.store()
	if err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	return store.ScanTasks(ctx, query, limit, cursor)
}

// Submission looks up the latest complete record for one admitted submission.
func (jsonl *JsonlStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.Submission(ctx, id)
}

// ScanSubmissions scans submissions matching every supplied filter in ascending ID order.
func (jsonl *JsonlStorage) ScanSubmissions(
	ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	store, err := jsonl.store()
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	return store.ScanSubmissions(ctx, query, limit, cursor)
}

// SubmissionByRequest finds a submission by its conversation-scoped host deduplication key.
func (jsonl *JsonlStorage) SubmissionByRequest(
	ctx context.Context, conversationId durable.ConversationId, requestId string,
) (*durable.SubmissionRecord, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.SubmissionByRequest(ctx, conversationId, requestId)
}

// FindDocument resolves the incarnation occupying one exact logical address at the selected point.
func (jsonl *JsonlStorage) FindDocument(
	ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint,
) (*durable.DocumentRecord, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.FindDocument(ctx, address, at)
}

// Document materializes one specific incarnation by ID at the selected point.
func (jsonl *JsonlStorage) Document(
	ctx context.Context, id durable.DocumentId, at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	store, err := jsonl.store()
	if err != nil {
		return nil, err
	}
	return store.Document(ctx, id, at)
}

// ScanDocuments scans incarnations alive in one exact scope at the selected point.
func (jsonl *JsonlStorage) ScanDocuments(
	ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	store, err := jsonl.store()
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	return store.ScanDocuments(ctx, query, limit, cursor)
}

// Close releases the storage; later operations fail. Repeated calls do nothing.
func (jsonl *JsonlStorage) Close(ctx context.Context) error {
	jsonl.stateMu.Lock()
	if jsonl.closed {
		jsonl.stateMu.Unlock()
		return nil
	}
	jsonl.closed = true
	jsonl.stateMu.Unlock()
	return jsonl.memory.Close(ctx)
}

func encodeCommit(seq durable.Seq, writes []durable.StorageWrite) (encodedCommit, error) {
	mainWrites := make([]any, 0, len(writes))
	records := newOrderedFiles()
	fileRecords := map[string][]string{}
	var nextOrdinal int64
	addSidecar := func(file string, payload any) (int64, error) {
		ordinal := nextOrdinal
		nextOrdinal++
		line, err := jsonLine(sidecarRecordWire{
			Format: formatVersion, Type: "record", Seq: int64(seq), Ordinal: ordinal, Payload: payload,
		})
		if err != nil {
			return 0, err
		}
		records.set(file, "")
		fileRecords[file] = append(fileRecords[file], line)
		return ordinal, nil
	}

	for _, write := range writes {
		switch typed := write.(type) {
		case durable.ConversationWrite:
			mainWrites = append(mainWrites, recordWrite{Type: "conversation", Value: typed.Value})
		case durable.EntryWrite:
			mainWrites = append(mainWrites, recordWrite{Type: "entry", Value: typed.Value})
		case durable.SubmissionWrite:
			mainWrites = append(mainWrites, recordWrite{Type: "submission", Value: typed.Value})
		case durable.DocumentRetireWrite:
			mainWrites = append(mainWrites, idWrite{Type: "document.retire", Id: int64(typed.Id)})
		case durable.TaskWrite:
			if typed.Value.State.Status == durable.TaskTerminal {
				mainWrites = append(mainWrites, recordWrite{Type: "task", Value: typed.Value})
				continue
			}
			ordinal, err := addSidecar(sidecarFileName("task", int64(typed.Value.Id)), taskPayload{Type: "task", Value: typed.Value})
			if err != nil {
				return encodedCommit{}, err
			}
			mainWrites = append(mainWrites, sidecarWrite{Type: "task.sidecar", Id: int64(typed.Value.Id), Ordinal: ordinal})
		case durable.DocumentCreateWrite:
			ordinal, err := addSidecar(sidecarFileName("doc", int64(typed.Record.Id)), documentPayload{
				Type: "document", Id: int64(typed.Record.Id), Content: contentToWire(typed.Content),
			})
			if err != nil {
				return encodedCommit{}, err
			}
			mainWrites = append(mainWrites, createWrite{Type: "document.create", Record: typed.Record, Ordinal: ordinal})
		case durable.DocumentChangeWrite:
			ordinal, err := addSidecar(sidecarFileName("doc", int64(typed.Id)), documentPayload{
				Type: "document", Id: int64(typed.Id), Content: contentToWire(typed.Content),
			})
			if err != nil {
				return encodedCommit{}, err
			}
			mainWrites = append(mainWrites, sidecarWrite{Type: "document.change", Id: int64(typed.Id), Ordinal: ordinal})
		}
	}

	for _, file := range records.names {
		records.set(file, strings.Join(fileRecords[file], ""))
	}
	marker, err := jsonLine(markerWire{Format: formatVersion, Type: "commit", Seq: int64(seq), Writes: mainWrites})
	if err != nil {
		return encodedCommit{}, err
	}
	return encodedCommit{marker: marker, sidecars: records}, nil
}

func (jsonl *JsonlStorage) planReclamations(writes []durable.StorageWrite, encoded encodedCommit) *orderedFiles {
	createdCurrentOnly := map[durable.DocumentId]bool{}
	var retired []durable.DocumentId
	retiredSet := map[durable.DocumentId]bool{}
	var bases []durable.DocumentId
	baseSet := map[durable.DocumentId]bool{}
	var taskOrder []durable.TaskId
	finalTasks := map[durable.TaskId]storedTask{}
	for _, write := range writes {
		switch typed := write.(type) {
		case durable.DocumentCreateWrite:
			if isCurrentOnly(typed.Record) {
				createdCurrentOnly[typed.Record.Id] = true
			}
		case durable.DocumentChangeWrite:
			if typed.Content.Kind == durable.ContentBase && !baseSet[typed.Id] {
				baseSet[typed.Id] = true
				bases = append(bases, typed.Id)
			}
		case durable.DocumentRetireWrite:
			if !retiredSet[typed.Id] {
				retiredSet[typed.Id] = true
				retired = append(retired, typed.Id)
			}
		case durable.TaskWrite:
			if _, seen := finalTasks[typed.Value.Id]; !seen {
				taskOrder = append(taskOrder, typed.Value.Id)
			}
			finalTasks[typed.Value.Id] = typed.Value
		}
	}

	replacements := newOrderedFiles()
	isCurrentOnlyDocument := func(id durable.DocumentId) bool {
		return jsonl.currentOnlyDocuments[id] || createdCurrentOnly[id]
	}
	for _, id := range retired {
		if isCurrentOnlyDocument(id) {
			replacements.set(sidecarFileName("doc", int64(id)), "")
		}
	}
	for _, id := range bases {
		if !isCurrentOnlyDocument(id) || retiredSet[id] {
			continue
		}
		file := sidecarFileName("doc", int64(id))
		if content, ok := encoded.sidecars.get(file); ok {
			replacements.set(file, content)
		}
	}
	for _, id := range taskOrder {
		task := finalTasks[id]
		file := sidecarFileName("task", int64(id))
		_, encodedTask := encoded.sidecars.get(file)
		if task.State.Status == durable.TaskTerminal && (jsonl.liveTaskSidecars[id] || encodedTask) {
			replacements.set(file, "")
		}
	}
	return replacements
}

func (jsonl *JsonlStorage) adoptSidecarState(writes []durable.StorageWrite) {
	for _, write := range writes {
		switch typed := write.(type) {
		case durable.DocumentCreateWrite:
			if isCurrentOnly(typed.Record) {
				jsonl.currentOnlyDocuments[typed.Record.Id] = true
			}
		case durable.TaskWrite:
			if typed.Value.State.Status == durable.TaskTerminal {
				delete(jsonl.liveTaskSidecars, typed.Value.Id)
			} else {
				jsonl.liveTaskSidecars[typed.Value.Id] = true
			}
		}
	}
}

// reclaimSidecars is retryable best-effort maintenance: the main marker already published this state, so a failure
// leaves superseded records for the next recovery to reclaim.
func (jsonl *JsonlStorage) reclaimSidecars(ctx context.Context, replacements *orderedFiles) {
	if len(replacements.names) == 0 {
		return
	}
	if jsonl.fsync {
		if err := jsonl.fs.FlushFile(ctx, jsonl.mainPath); err != nil {
			return
		}
	}
	for _, file := range replacements.names {
		content, _ := replacements.get(file)
		jsonl.replaceSidecar(ctx, file, content)
	}
}

func (jsonl *JsonlStorage) replaceSidecar(ctx context.Context, file, content string) {
	path, err := jsonl.fs.JoinPath(ctx, []string{jsonl.directory, file})
	if err != nil {
		return
	}
	if content == "" {
		_ = jsonl.fs.Remove(ctx, path, &env.RemoveOptions{Force: true})
		return
	}
	temporaryPath, err := jsonl.fs.JoinPath(ctx, []string{jsonl.directory, file + reclaimSuffix})
	if err != nil {
		return
	}
	if err := jsonl.fs.WriteFile(ctx, temporaryPath, content); err != nil {
		return
	}
	if jsonl.fsync {
		if err := jsonl.fs.FlushFile(ctx, temporaryPath); err != nil {
			return
		}
	}
	_ = jsonl.fs.RenameFile(ctx, temporaryPath, path)
}

func (jsonl *JsonlStorage) recover(ctx context.Context) error {
	fs := jsonl.fs
	main, err := readLines(ctx, fs, jsonl.mainPath, mainFile, parseMainMarker)
	if err != nil {
		return err
	}
	var previousSeq int64
	for _, marker := range main.lines {
		if marker.value.seq <= previousSeq {
			return NewJsonlCorruptionError("Commit sequence does not strictly increase in "+mainFile, nil)
		}
		previousSeq = marker.value.seq
	}

	listed, err := fs.ListDir(ctx, jsonl.directory)
	if err != nil {
		return errorFromFile("directory listing", err)
	}
	for _, info := range listed {
		if info.Kind == env.FileKindFile && reclaimFilePattern.MatchString(info.Name) {
			_ = fs.Remove(ctx, info.Path, &env.RemoveOptions{Force: true})
		}
	}
	var sidecarFiles []string
	for _, info := range listed {
		if info.Kind == env.FileKindFile && sidecarFilePattern.MatchString(info.Name) {
			sidecarFiles = append(sidecarFiles, info.Name)
		}
	}
	slices.Sort(sidecarFiles)
	parsedFiles := make([]parsedFile[sidecarRecord], 0, len(sidecarFiles))
	recordByKey := map[sidecarKey]sidecarRecord{}
	for _, file := range sidecarFiles {
		path, err := fs.JoinPath(ctx, []string{jsonl.directory, file})
		if err != nil {
			return errorFromFile("path join", err)
		}
		parsed, err := readLines(ctx, fs, path, file, func(text []byte, line int) (sidecarRecord, error) {
			return parseSidecarRecord(text, file, line)
		})
		if err != nil {
			return err
		}
		parsedFiles = append(parsedFiles, parsed)
		var previous *sidecarRecord
		for index := range parsed.lines {
			line := parsed.lines[index].value
			if previous != nil && (line.seq < previous.seq || (line.seq == previous.seq && line.ordinal <= previous.ordinal)) {
				return NewJsonlCorruptionError("Sidecar records are out of order in "+file, nil)
			}
			previous = &parsed.lines[index].value
			recordByKey[sidecarKey{file: file, seq: line.seq, ordinal: line.ordinal}] = line
		}
	}

	currentOnlyDocuments := map[durable.DocumentId]bool{}
	retiredDocuments := map[durable.DocumentId]bool{}
	var taskOrder []durable.TaskId
	finalTaskIsLive := map[durable.TaskId]bool{}
	setTask := func(id durable.TaskId, live bool) {
		if _, seen := finalTaskIsLive[id]; !seen {
			taskOrder = append(taskOrder, id)
		}
		finalTaskIsLive[id] = live
	}
	for _, line := range main.lines {
		for _, operation := range line.value.writes {
			switch operation.kind {
			case "document.create":
				if isCurrentOnly(operation.record) {
					currentOnlyDocuments[operation.record.Id] = true
				}
			case "document.retire":
				retiredDocuments[durable.DocumentId(operation.id)] = true
			case "task":
				setTask(durable.TaskId(operation.id), false)
			case "task.sidecar":
				setTask(durable.TaskId(operation.id), true)
			}
		}
	}
	retiredCurrentOnly := map[durable.DocumentId]bool{}
	for id := range retiredDocuments {
		if currentOnlyDocuments[id] {
			retiredCurrentOnly[id] = true
		}
	}

	latestBases := map[durable.DocumentId]sidecarRecord{}
	for _, line := range main.lines {
		marker := line.value
		for _, operation := range marker.writes {
			if operation.kind != "document.create" && operation.kind != "document.change" {
				continue
			}
			id := durable.DocumentId(operation.id)
			if !currentOnlyDocuments[id] {
				continue
			}
			record, ok := recordByKey[sidecarKey{file: sidecarFileName("doc", int64(id)), seq: marker.seq, ordinal: operation.ordinal}]
			if !ok || record.content == nil || record.docId != int64(id) || record.content.Kind != durable.ContentBase {
				continue
			}
			previous, seen := latestBases[id]
			if !seen || record.seq > previous.seq || (record.seq == previous.seq && record.ordinal > previous.ordinal) {
				latestBases[id] = record
			}
		}
	}
	isBeforeLatestBase := func(id durable.DocumentId, seq, ordinal int64) bool {
		latest, ok := latestBases[id]
		return ok && (seq < latest.seq || (seq == latest.seq && ordinal < latest.ordinal))
	}
	terminalTasks := map[durable.TaskId]bool{}
	for id, live := range finalTaskIsLive {
		if !live {
			terminalTasks[id] = true
		}
	}

	confirmed := map[sidecarKey]bool{}
	for _, line := range main.lines {
		marker := line.value
		writes := make([]durable.StorageWrite, 0, len(marker.writes))
		for _, operation := range marker.writes {
			switch operation.kind {
			case "conversation", "entry", "submission", "task", "document.retire":
				writes = append(writes, operation.write)
			case "task.sidecar":
				id := durable.TaskId(operation.id)
				optional := terminalTasks[id]
				record, err := confirmRecord(marker, operation.ordinal, sidecarFileName("task", operation.id), recordByKey, confirmed, optional)
				if err != nil {
					return err
				}
				if record != nil {
					if record.task == nil || int64(record.task.Id) != operation.id {
						return NewJsonlCorruptionError(fmt.Sprintf("Confirmed task sidecar data does not match commit %d", marker.seq), nil)
					}
					if !optional {
						writes = append(writes, durable.TaskWrite{Value: *record.task})
					}
				}
			case "document.create", "document.change":
				id := durable.DocumentId(operation.id)
				reclaimed := retiredCurrentOnly[id] || isBeforeLatestBase(id, marker.seq, operation.ordinal)
				record, err := confirmRecord(marker, operation.ordinal, sidecarFileName("doc", operation.id), recordByKey, confirmed, reclaimed)
				if err != nil {
					return err
				}
				var content *durable.DocumentContent
				if record != nil {
					if record.content == nil || record.docId != operation.id {
						return NewJsonlCorruptionError(fmt.Sprintf("Confirmed document sidecar data does not match commit %d", marker.seq), nil)
					}
					content = record.content
				}
				if operation.kind == "document.create" {
					if content != nil && content.Kind != durable.ContentBase {
						return NewJsonlCorruptionError(fmt.Sprintf("Document creation lacks a confirmed base in commit %d", marker.seq), nil)
					}
					created := durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.NewJsonObject(0)}
					if !reclaimed && content != nil {
						created = *content
					}
					writes = append(writes, durable.DocumentCreateWrite{Record: operation.record, Content: created})
				} else if !reclaimed && content != nil {
					writes = append(writes, durable.DocumentChangeWrite{Id: id, Content: *content})
				}
			}
		}
		prepared, err := jsonl.memory.PrepareCommitAt(writes, durable.Seq(marker.seq))
		if err != nil {
			return NewJsonlCorruptionError(fmt.Sprintf("Invalid committed state at sequence %d", marker.seq), err)
		}
		prepared.Apply()
	}

	reclamations := newOrderedFiles()
	for index, file := range sidecarFiles {
		parsed := parsedFiles[index]
		unconfirmedAt := int64(-1)
		var confirmedLines []parsedLine[sidecarRecord]
		for _, line := range parsed.lines {
			if confirmed[sidecarKey{file: file, seq: line.value.seq, ordinal: line.value.ordinal}] {
				if unconfirmedAt >= 0 {
					return NewJsonlCorruptionError("Confirmed record follows an unconfirmed tail in "+file, nil)
				}
				confirmedLines = append(confirmedLines, line)
			} else if unconfirmedAt < 0 {
				unconfirmedAt = line.start
			}
		}
		if unconfirmedAt >= 0 {
			if err := fs.TruncateFile(ctx, parsed.path, unconfirmedAt); err != nil {
				return errorFromFile("tail truncation of "+file, err)
			}
		}

		numericId, _ := strconv.ParseInt(file[strings.IndexByte(file, '-')+1:len(file)-len(".jsonl")], 10, 64)
		var retained []parsedLine[sidecarRecord]
		decided := false
		if strings.HasPrefix(file, "task-") && terminalTasks[durable.TaskId(numericId)] {
			decided = true
		} else if strings.HasPrefix(file, "doc-") {
			documentId := durable.DocumentId(numericId)
			if retiredCurrentOnly[documentId] {
				decided = true
			} else if _, ok := latestBases[documentId]; ok {
				decided = true
				for _, line := range confirmedLines {
					if !isBeforeLatestBase(documentId, line.value.seq, line.value.ordinal) {
						retained = append(retained, line)
					}
				}
			}
		}
		if decided && (len(retained) < len(confirmedLines) || len(retained) == 0) {
			var content strings.Builder
			for _, line := range retained {
				content.Write(line.value.raw)
				content.WriteByte('\n')
			}
			reclamations.set(file, content.String())
		}
	}
	jsonl.reclaimSidecars(ctx, reclamations)

	for id := range currentOnlyDocuments {
		jsonl.currentOnlyDocuments[id] = true
	}
	for _, id := range taskOrder {
		if finalTaskIsLive[id] {
			jsonl.liveTaskSidecars[id] = true
		}
	}
	return nil
}

func confirmRecord(
	marker mainMarker, ordinal int64, file string, recordByKey map[sidecarKey]sidecarRecord,
	confirmed map[sidecarKey]bool, optional bool,
) (*sidecarRecord, error) {
	key := sidecarKey{file: file, seq: marker.seq, ordinal: ordinal}
	if confirmed[key] {
		return nil, NewJsonlCorruptionError("Sidecar record is confirmed more than once", nil)
	}
	record, ok := recordByKey[key]
	if !ok {
		if optional {
			return nil, nil
		}
		return nil, NewJsonlCorruptionError(fmt.Sprintf("Missing confirmed sidecar record %s at sequence %d", file, marker.seq), nil)
	}
	confirmed[key] = true
	return &record, nil
}

// readLines parses every complete line of a file. A torn final line is truncated away first; a missing file has no
// lines.
func readLines[T any](
	ctx context.Context, fs FileSystem, path, name string, parse func(text []byte, line int) (T, error),
) (parsedFile[T], error) {
	content, err := fs.ReadBinaryFile(ctx, path)
	if err != nil {
		if isNotFound(err) {
			return parsedFile[T]{path: path}, nil
		}
		return parsedFile[T]{}, errorFromFile("read of "+name, err)
	}
	completeSize := len(content)
	if completeSize > 0 && content[completeSize-1] != '\n' {
		completeSize = bytes.LastIndexByte(content, '\n') + 1
		if err := fs.TruncateFile(ctx, path, int64(completeSize)); err != nil {
			return parsedFile[T]{}, errorFromFile("torn-line truncation of "+name, err)
		}
	}
	var lines []parsedLine[T]
	start := 0
	lineNumber := 1
	for end := range completeSize {
		if content[end] != '\n' {
			continue
		}
		text := content[start:end]
		if !utf8.Valid(text) {
			return parsedFile[T]{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid UTF-8 in complete %s line %d", name, lineNumber), nil)
		}
		value, err := parse(text, lineNumber)
		if err != nil {
			return parsedFile[T]{}, err
		}
		lines = append(lines, parsedLine[T]{value: value, start: int64(start)})
		start = end + 1
		lineNumber++
	}
	return parsedFile[T]{path: path, lines: lines}, nil
}

func parseJSON(text []byte, description string) (any, error) {
	// Objects decode in document order, so stored values keep their key order, and strings keep lone surrogates.
	value, err := chordjson.DecodeUnquoting(text, unquoteJS)
	if err != nil {
		return nil, NewJsonlCorruptionError("Malformed complete "+description, err)
	}
	return value, nil
}

// safeInteger reports whether a decoded JSON value is a JavaScript safe integer.
func safeInteger(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number != math.Trunc(number) || math.Abs(number) > 1<<53-1 {
		return 0, false
	}
	return int64(number), true
}

// unquoteJS decodes a JSON string with JavaScript string semantics: a lone surrogate escape becomes its WTF-8 form.
func unquoteJS(raw []byte, text *string) error { return json.Unmarshal(raw, text) }

func objectOf(value any) (*delta.JsonObject, bool) {
	object, ok := value.(*delta.JsonObject)
	return object, ok
}

// decodeAs converts a validated decoded JSON value to a typed record.
func decodeAs[T any](value any, description string) (T, error) {
	var typed T
	encoded, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(encoded, &typed)
	}
	if err == nil && ordered.HasDynamic(reflect.TypeFor[T]()) {
		// A JsonValue member holds the line's objects in order, as JSON.parse gives Pi; the tree is decoded afresh so the record
		// shares nothing with value.
		var tree any
		if tree, err = chordjson.DecodeUnquoting(encoded, unquoteJS); err == nil {
			ordered.Restore(&typed, tree)
		}
	}
	if err != nil {
		return typed, NewJsonlCorruptionError("Invalid write in "+description, err)
	}
	return typed, nil
}

func validateMainOperation(value any, description string) (mainOperation, error) {
	object, ok := objectOf(value)
	operationType, isString := object.Value("type").(string)
	if !ok || !isString {
		return mainOperation{}, NewJsonlCorruptionError("Invalid write in "+description, nil)
	}
	switch operationType {
	case "conversation", "entry", "submission":
		record, ok := objectOf(object.Value("value"))
		id, safe := safeInteger(record.Value("id"))
		if !ok || !safe {
			return mainOperation{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid %s write in %s", operationType, description), nil)
		}
		operation := mainOperation{kind: operationType, id: id}
		var err error
		switch operationType {
		case "conversation":
			var decoded durable.ConversationRecord
			decoded, err = decodeAs[durable.ConversationRecord](record, description)
			operation.write = durable.ConversationWrite{Value: decoded}
		case "entry":
			var decoded durable.EntryRecord
			decoded, err = decodeAs[durable.EntryRecord](record, description)
			operation.write = durable.EntryWrite{Value: decoded}
		default:
			var decoded durable.SubmissionRecord
			decoded, err = decodeAs[durable.SubmissionRecord](record, description)
			operation.write = durable.SubmissionWrite{Value: decoded}
		}
		return operation, err
	case "task":
		record, ok := objectOf(object.Value("value"))
		id, safe := safeInteger(record.Value("id"))
		state, stateOk := objectOf(record.Value("state"))
		if !ok || !safe || !stateOk || state.Value("status") != string(durable.TaskTerminal) {
			return mainOperation{}, NewJsonlCorruptionError("Invalid terminal task write in "+description, nil)
		}
		decoded, err := decodeAs[storedTask](record, description)
		return mainOperation{kind: operationType, id: id, write: durable.TaskWrite{Value: decoded}}, err
	case "document.retire":
		id, safe := safeInteger(object.Value("id"))
		if !safe {
			return mainOperation{}, NewJsonlCorruptionError("Invalid document retirement in "+description, nil)
		}
		return mainOperation{kind: operationType, id: id, write: durable.DocumentRetireWrite{Id: durable.DocumentId(id)}}, nil
	case "task.sidecar":
		id, safe := safeInteger(object.Value("id"))
		ordinal, safeOrdinal := safeInteger(object.Value("ordinal"))
		if !safe || !safeOrdinal || ordinal < 0 {
			return mainOperation{}, NewJsonlCorruptionError("Invalid task sidecar write in "+description, nil)
		}
		return mainOperation{kind: operationType, id: id, ordinal: ordinal}, nil
	case "document.create":
		record, ok := objectOf(object.Value("record"))
		id, safe := safeInteger(record.Value("id"))
		ordinal, safeOrdinal := safeInteger(object.Value("ordinal"))
		if !ok || !safe || !safeOrdinal || ordinal < 0 {
			return mainOperation{}, NewJsonlCorruptionError("Invalid document creation in "+description, nil)
		}
		decoded, err := decodeAs[durable.DocumentCreate](record, description)
		return mainOperation{kind: operationType, id: id, ordinal: ordinal, record: decoded}, err
	case "document.change":
		id, safe := safeInteger(object.Value("id"))
		ordinal, safeOrdinal := safeInteger(object.Value("ordinal"))
		if !safe || !safeOrdinal || ordinal < 0 {
			return mainOperation{}, NewJsonlCorruptionError("Invalid document change in "+description, nil)
		}
		return mainOperation{kind: operationType, id: id, ordinal: ordinal}, nil
	default:
		return mainOperation{}, NewJsonlCorruptionError("Unknown write type in "+description, nil)
	}
}

func parseMainMarker(text []byte, line int) (mainMarker, error) {
	description := fmt.Sprintf("%s line %d", mainFile, line)
	value, err := parseJSON(text, description)
	if err != nil {
		return mainMarker{}, err
	}
	object, ok := objectOf(value)
	format, formatOk := safeInteger(object.Value("format"))
	seq, seqOk := safeInteger(object.Value("seq"))
	writes, writesOk := object.Value("writes").([]any)
	if !ok || !formatOk || format != formatVersion || object.Value("type") != "commit" || !seqOk || seq < 1 || !writesOk {
		return mainMarker{}, NewJsonlCorruptionError("Invalid commit marker in "+description, nil)
	}
	marker := mainMarker{seq: seq, writes: make([]mainOperation, 0, len(writes))}
	for _, write := range writes {
		operation, err := validateMainOperation(write, description)
		if err != nil {
			return mainMarker{}, err
		}
		marker.writes = append(marker.writes, operation)
	}
	return marker, nil
}

func validateDocumentContent(value any, description string) (durable.DocumentContent, error) {
	invalid := NewJsonlCorruptionError("Invalid document content in "+description, nil)
	object, ok := objectOf(value)
	version, safe := safeInteger(object.Value("version"))
	if !ok || !safe || version < 1 {
		return durable.DocumentContent{}, invalid
	}
	switch object.Value("kind") {
	case string(durable.ContentBase):
		base, ok := objectOf(object.Value("value"))
		if !ok {
			return durable.DocumentContent{}, invalid
		}
		return durable.DocumentContent{Kind: durable.ContentBase, Version: int(version), Value: base}, nil
	case string(durable.ContentDelta):
		rawOps, ok := object.Value("ops").([]any)
		if !ok {
			return durable.DocumentContent{}, invalid
		}
		ops := make([]durable.Op, len(rawOps))
		for index, rawOp := range rawOps {
			opValues, _ := rawOp.([]any)
			ops[index] = durable.Op(opValues)
		}
		return durable.DocumentContent{Kind: durable.ContentDelta, Version: int(version), Ops: ops}, nil
	default:
		return durable.DocumentContent{}, invalid
	}
}

func parseSidecarRecord(text []byte, file string, line int) (sidecarRecord, error) {
	description := fmt.Sprintf("%s line %d", file, line)
	value, err := parseJSON(text, description)
	if err != nil {
		return sidecarRecord{}, err
	}
	object, ok := objectOf(value)
	format, formatOk := safeInteger(object.Value("format"))
	seq, seqOk := safeInteger(object.Value("seq"))
	ordinal, ordinalOk := safeInteger(object.Value("ordinal"))
	payload, payloadOk := objectOf(object.Value("payload"))
	payloadType, typeOk := payload.Value("type").(string)
	if !ok || !formatOk || format != formatVersion || object.Value("type") != "record" || !seqOk || seq < 1 ||
		!ordinalOk || ordinal < 0 || !payloadOk || !typeOk {
		return sidecarRecord{}, NewJsonlCorruptionError("Invalid sidecar record in "+description, nil)
	}
	record := sidecarRecord{seq: seq, ordinal: ordinal, raw: slices.Clone(text)}
	switch payloadType {
	case "task":
		task, ok := objectOf(payload.Value("value"))
		_, safe := safeInteger(task.Value("id"))
		state, stateOk := objectOf(task.Value("state"))
		if !ok || !safe || !stateOk || state.Value("status") == string(durable.TaskTerminal) {
			return sidecarRecord{}, NewJsonlCorruptionError("Invalid live task record in "+description, nil)
		}
		decoded, err := decodeAs[storedTask](task, description)
		if err != nil {
			return sidecarRecord{}, err
		}
		record.task = &decoded
	case "document":
		id, safe := safeInteger(payload.Value("id"))
		if !safe {
			return sidecarRecord{}, NewJsonlCorruptionError("Invalid document record in "+description, nil)
		}
		content, err := validateDocumentContent(payload.Value("content"), description)
		if err != nil {
			return sidecarRecord{}, err
		}
		record.docId = id
		record.content = &content
	default:
		return sidecarRecord{}, NewJsonlCorruptionError("Unknown sidecar record type in "+description, nil)
	}
	return record, nil
}

func (jsonl *JsonlStorage) resolveFile(ctx context.Context, file string) (string, error) {
	path, err := jsonl.fs.JoinPath(ctx, []string{jsonl.directory, file})
	if err != nil {
		return "", errorFromFile("path join", err)
	}
	return path, nil
}

func (jsonl *JsonlStorage) store() (*storage.MemoryStorage, error) {
	if err := jsonl.assertUsable(); err != nil {
		return nil, err
	}
	return jsonl.memory, nil
}

func (jsonl *JsonlStorage) poison(cause error) error {
	jsonl.stateMu.Lock()
	defer jsonl.stateMu.Unlock()
	if jsonl.poisonError == nil {
		jsonl.poisonError = NewJsonlStoragePoisonedError(cause)
	}
	return jsonl.poisonError
}

func (jsonl *JsonlStorage) assertUsable() error {
	jsonl.stateMu.Lock()
	defer jsonl.stateMu.Unlock()
	if jsonl.closed {
		return errors.New("JsonlStorage is closed")
	}
	if jsonl.poisonError != nil {
		return jsonl.poisonError
	}
	return nil
}
