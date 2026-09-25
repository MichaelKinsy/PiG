package pico3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// jsonlRecord is one line of any file. Refs (main only) lists the sidecar
// files the same commit also wrote.
type jsonlRecord struct {
	Seq    Seq      `json:"seq"`
	MaxId  Id       `json:"maxId"`
	Writes []Write  `json:"writes"`
	Refs   []string `json:"refs,omitempty"`
}

// JsonlStorage persists a session directory:
//
//	main.jsonl                  conversations, entries, inputs, rewindable and
//	                            session document ops, each task's create and
//	                            terminal records, and one marker per commit
//	sticky-<conversation>.jsonl sticky document ops, rewritten from the last base
//	task-<id>.jsonl             a live task's intermediate patches, unlinked
//	                            after its terminal record is published
//
// Sidecar records are appended first, then exactly one main record naming the
// sidecars it expects: the publication point. Replay applies a sidecar record
// only when main confirms it; unconfirmed sidecar tails are discarded. Bytes
// after the last newline of any file are a torn write and are truncated at
// open. With fsync false, recovery covers process termination only. One
// process owns a directory at a time.
type JsonlStorage struct {
	*MemoryStorage
	dir          string
	fsync        bool
	mainFile     *os.File
	sidecars     map[Id]*os.File
	taskSidecars map[Id]*os.File
	closedOnce   bool
	// appendRecord writes one record; tests replace it to inject failures.
	appendRecord func(file *os.File, record jsonlRecord) error
}

// JsonlOptions configures JsonlStorage; Fsync defaults to true when nil.
type JsonlOptions struct {
	Fsync *bool
}

// OpenJsonlStorage opens or creates a session directory and replays it.
func OpenJsonlStorage(ctx context.Context, dir string, options JsonlOptions) (*JsonlStorage, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if strings.HasSuffix(name.Name(), ".jsonl") {
			if err := truncateTornTail(filepath.Join(dir, name.Name())); err != nil {
				return nil, err
			}
		}
	}
	mainFile, err := os.OpenFile(filepath.Join(dir, "main.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	storage := &JsonlStorage{
		MemoryStorage: NewMemoryStorage(),
		dir:           dir,
		fsync:         options.Fsync == nil || *options.Fsync,
		mainFile:      mainFile,
		sidecars:      map[Id]*os.File{},
		taskSidecars:  map[Id]*os.File{},
	}
	storage.appendRecord = storage.append
	if err := storage.replay(ctx); err != nil {
		_ = storage.Close(ctx)
		return nil, err
	}
	return storage, nil
}

// Sizes reports every file's size, for tests and tooling.
func (storage *JsonlStorage) Sizes() (map[string]int64, error) {
	entries, err := os.ReadDir(storage.dir)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, entry := range entries {
		// Stat by path, as upstream statSync does: on Windows a directory
		// entry's size lags a file still open for appending.
		info, err := os.Stat(filepath.Join(storage.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out[entry.Name()] = info.Size()
	}
	return out, nil
}

type readRecord struct {
	record jsonlRecord
	end    int64
}

func readRecords(file string) ([]readRecord, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []readRecord
	start := 0
	var last Seq
	for start < len(data) {
		newline := bytes.IndexByte(data[start:], '\n')
		if newline < 0 {
			break
		}
		line := data[start : start+newline]
		start += newline + 1
		if len(line) == 0 {
			continue
		}
		record, err := parseRecord(file, line)
		if err != nil {
			return nil, err
		}
		if record.Seq <= last {
			return nil, fmt.Errorf("%s: sequence not increasing at %d", file, record.Seq)
		}
		last = record.Seq
		out = append(out, readRecord{record: record, end: int64(start)})
	}
	return out, nil
}

func parseRecord(file string, line []byte) (jsonlRecord, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		return jsonlRecord{}, fmt.Errorf("%s: malformed record: %w", file, err)
	}
	var seq, maxId *float64
	var writes []json.RawMessage
	if json.Unmarshal(probe["seq"], &seq) != nil || seq == nil || json.Unmarshal(probe["maxId"], &maxId) != nil || maxId == nil ||
		json.Unmarshal(probe["writes"], &writes) != nil || probe["writes"] == nil || writes == nil {
		return jsonlRecord{}, fmt.Errorf("%s: record lacks seq/maxId/writes", file)
	}
	var record jsonlRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return jsonlRecord{}, fmt.Errorf("%s: malformed record: %w", file, err)
	}
	return record, nil
}

type replayState struct {
	dir               string
	bySeq             map[Seq][]Write
	confirmed         map[Seq]map[string]bool
	found             map[Seq]map[string]bool
	retainedStickyAt  map[string]Seq
	retiredTasks      map[Id]Seq
	maxId             Id
	main              []jsonlRecord
	sidecarFileNames  []string
	unconfirmedToTrim map[string]int64
}

func (storage *JsonlStorage) replay(ctx context.Context) error {
	state := &replayState{
		dir: storage.dir, bySeq: map[Seq][]Write{}, confirmed: map[Seq]map[string]bool{},
		found: map[Seq]map[string]bool{}, retainedStickyAt: map[string]Seq{}, retiredTasks: map[Id]Seq{},
		unconfirmedToTrim: map[string]int64{},
	}
	if err := state.readMain(); err != nil {
		return err
	}
	if err := state.readSidecars(); err != nil {
		return err
	}
	if err := state.verifyRefs(); err != nil {
		return err
	}
	for _, seq := range slices.Sorted(mapKeys(state.bySeq)) {
		storage.seq = seq - 1
		if _, err := storage.MemoryStorage.Commit(ctx, state.bySeq[seq]); err != nil {
			return err
		}
	}
	storage.mu.Lock()
	storage.setNextIdLocked(state.maxId + 1)
	storage.mu.Unlock()
	return storage.unlinkStaleTaskSidecars(ctx, state.sidecarFileNames)
}

func mapKeys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

func (state *replayState) readMain() error {
	records, err := readRecords(filepath.Join(state.dir, "main.jsonl"))
	if err != nil {
		return err
	}
	for _, read := range records {
		record := read.record
		state.main = append(state.main, record)
		refs := map[string]bool{}
		for _, ref := range record.Refs {
			refs[ref] = true
		}
		state.confirmed[record.Seq] = refs
		for _, write := range record.Writes {
			if write.Type == WriteTaskPatch && write.Patch.Status != nil && *write.Patch.Status == TaskTerminal {
				state.retiredTasks[write.Patch.Id] = record.Seq
			}
		}
		state.bySeq[record.Seq] = append([]Write{}, record.Writes...)
		state.maxId = max(state.maxId, record.MaxId)
	}
	return nil
}

func (state *replayState) readSidecars() error {
	names, err := os.ReadDir(state.dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		file := name.Name()
		if !strings.HasSuffix(file, ".jsonl") || (!strings.HasPrefix(file, "sticky-") && !strings.HasPrefix(file, "task-")) {
			continue
		}
		state.sidecarFileNames = append(state.sidecarFileNames, file)
		if err := state.readSidecar(file); err != nil {
			return err
		}
	}
	return nil
}

func (state *replayState) readSidecar(file string) error {
	path := filepath.Join(state.dir, file)
	records, err := readRecords(path)
	if err != nil {
		return err
	}
	if len(records) > 0 && strings.HasPrefix(file, "sticky-") && IsBase(docOps(records[0].record.Writes)) {
		state.retainedStickyAt[file] = records[0].record.Seq
	}
	var confirmedEnd int64
	sawUnconfirmed := false
	for _, read := range records {
		record := read.record
		if !state.confirmed[record.Seq][file] {
			sawUnconfirmed = true
			continue
		}
		if sawUnconfirmed {
			return fmt.Errorf("%s: confirmed record follows an unconfirmed tail at %d", path, record.Seq)
		}
		confirmedEnd = read.end
		if state.found[record.Seq] == nil {
			state.found[record.Seq] = map[string]bool{}
		}
		state.found[record.Seq][file] = true
		state.bySeq[record.Seq] = append(state.bySeq[record.Seq], record.Writes...)
		state.maxId = max(state.maxId, record.MaxId)
	}
	if sawUnconfirmed {
		return os.Truncate(path, confirmedEnd)
	}
	return nil
}

func docOps(writes []Write) []Op {
	var ops []Op
	for _, write := range writes {
		if write.Type == WriteDoc {
			ops = append(ops, write.Ops...)
		}
	}
	return ops
}

func (state *replayState) verifyRefs() error {
	for _, record := range state.main {
		for _, file := range record.Refs {
			if state.found[record.Seq][file] {
				continue
			}
			if retainedFrom, ok := state.retainedStickyAt[file]; ok && record.Seq < retainedFrom {
				continue
			}
			if id, ok := taskSidecarId(file); ok {
				if retiredAt, retired := state.retiredTasks[id]; retired && record.Seq < retiredAt {
					continue
				}
			}
			return fmt.Errorf("%s: missing record for published sequence %d", filepath.Join(state.dir, file), record.Seq)
		}
	}
	return nil
}

func taskSidecarId(file string) (Id, bool) {
	if !strings.HasPrefix(file, "task-") || !strings.HasSuffix(file, ".jsonl") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(file, "task-"), ".jsonl"), 10, 64)
	return id, err == nil
}

// unlinkStaleTaskSidecars removes sidecars of terminal or unknown tasks: a
// leftover cannot resurrect anything.
func (storage *JsonlStorage) unlinkStaleTaskSidecars(ctx context.Context, files []string) error {
	for _, file := range files {
		id, ok := taskSidecarId(file)
		if !ok {
			continue
		}
		task, err := storage.Task(ctx, id)
		if err != nil {
			return err
		}
		if task == nil || task.Status == TaskTerminal {
			if err := os.Remove(filepath.Join(storage.dir, file)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// Commit appends sidecar records, then the main publication record, then
// applies the batch in memory.
func (storage *JsonlStorage) Commit(_ context.Context, writes []Write) (Seq, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closedOnce || storage.closed {
		return 0, errors.New("storage closed")
	}
	seq := storage.seq + 1
	apply, err := storage.stageLocked(writes, seq)
	if err != nil {
		return 0, err
	}
	maxId := storage.nextIdAfterLocked(writes)
	plan := planCommit(writes)
	refs, err := storage.appendSidecars(seq, maxId, plan)
	if err != nil {
		return 0, err
	}
	main := jsonlRecord{Seq: seq, MaxId: maxId, Writes: plan.main, Refs: refs}
	if err := storage.appendRecord(storage.mainFile, main); err != nil {
		return 0, err
	}
	if err := apply(); err != nil {
		return 0, err
	}
	storage.seq = seq
	for _, id := range plan.retired {
		storage.retireTaskSidecar(id)
	}
	return seq, nil
}

type commitPlan struct {
	main        []Write
	sticky      map[Id][]Write
	stickyOrder []Id
	tasks       map[Id][]Write
	taskOrder   []Id
	retired     []Id
}

func planCommit(writes []Write) commitPlan {
	plan := commitPlan{main: []Write{}, sticky: map[Id][]Write{}, tasks: map[Id][]Write{}}
	for _, write := range writes {
		switch {
		case write.Type == WriteDoc && write.Ref.Doc == DocSticky:
			id := write.Ref.ConversationId
			if _, seen := plan.sticky[id]; !seen {
				plan.stickyOrder = append(plan.stickyOrder, id)
			}
			plan.sticky[id] = append(plan.sticky[id], write)
		case write.Type == WriteTaskPatch && !patchIsTerminal(write.Patch):
			id := write.Patch.Id
			if _, seen := plan.tasks[id]; !seen {
				plan.taskOrder = append(plan.taskOrder, id)
			}
			plan.tasks[id] = append(plan.tasks[id], write)
		default:
			plan.main = append(plan.main, write)
			if write.Type == WriteTaskPatch {
				plan.retired = append(plan.retired, write.Patch.Id)
			}
		}
	}
	return plan
}

func patchIsTerminal(patch *TaskPatch) bool {
	return patch.Status != nil && *patch.Status == TaskTerminal
}

func (storage *JsonlStorage) appendSidecars(seq Seq, maxId Id, plan commitPlan) ([]string, error) {
	var refs []string
	for _, id := range plan.taskOrder {
		file, err := storage.sidecarFile(storage.taskSidecars, id, fmt.Sprintf("task-%d.jsonl", id))
		if err != nil {
			return nil, err
		}
		if err := storage.appendRecord(file, jsonlRecord{Seq: seq, MaxId: maxId, Writes: plan.tasks[id]}); err != nil {
			return nil, err
		}
		refs = append(refs, fmt.Sprintf("task-%d.jsonl", id))
	}
	for _, id := range plan.stickyOrder {
		file, err := storage.sidecarFile(storage.sidecars, id, fmt.Sprintf("sticky-%d.jsonl", id))
		if err != nil {
			return nil, err
		}
		if err := storage.appendRecord(file, jsonlRecord{Seq: seq, MaxId: maxId, Writes: plan.sticky[id]}); err != nil {
			return nil, err
		}
		refs = append(refs, fmt.Sprintf("sticky-%d.jsonl", id))
	}
	return refs, nil
}

func (storage *JsonlStorage) nextIdAfterLocked(writes []Write) Id {
	maxId := storage.nextIdValue - 1
	for _, write := range writes {
		switch write.Type {
		case WriteConversation:
			maxId = max(maxId, write.Conversation.Id)
		case WriteEntry:
			maxId = max(maxId, write.Entry.Id)
		case WriteTask:
			maxId = max(maxId, write.Task.Id)
		case WriteInput:
			maxId = max(maxId, write.Input.Id)
		}
	}
	return maxId
}

func (storage *JsonlStorage) append(file *os.File, record jsonlRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if storage.fsync {
		return file.Sync()
	}
	return nil
}

func (storage *JsonlStorage) sidecarFile(files map[Id]*os.File, id Id, name string) (*os.File, error) {
	if file, ok := files[id]; ok {
		return file, nil
	}
	file, err := os.OpenFile(filepath.Join(storage.dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	files[id] = file
	return file, nil
}

func (storage *JsonlStorage) retireTaskSidecar(id Id) {
	if file, ok := storage.taskSidecars[id]; ok {
		_ = file.Close()
	}
	delete(storage.taskSidecars, id)
	_ = os.Remove(filepath.Join(storage.dir, fmt.Sprintf("task-%d.jsonl", id)))
}

// Truncate rewrites the sticky sidecar from its last base: temp file,
// optional fsync, rename.
func (storage *JsonlStorage) Truncate(_ context.Context, ref DocRef) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.truncateLocked(ref)
	if ref.Doc != DocSticky {
		return nil
	}
	name := filepath.Join(storage.dir, fmt.Sprintf("sticky-%d.jsonl", ref.ConversationId))
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := nonEmptyLines(string(data))
	start, err := lastBaseLine(lines)
	if err != nil || start == 0 {
		return err
	}
	return storage.rewriteSticky(ref.ConversationId, name, lines[start:])
}

func nonEmptyLines(text string) []string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func lastBaseLine(lines []string) (int, error) {
	for index, line := range slices.Backward(lines) {
		var record jsonlRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return 0, err
		}
		if IsBase(docOps(record.Writes)) {
			return index, nil
		}
	}
	return 0, nil
}

func (storage *JsonlStorage) rewriteSticky(conversationId Id, name string, lines []string) error {
	temporary := name + ".tmp"
	var content strings.Builder
	for _, line := range lines {
		content.WriteString(line)
		content.WriteByte('\n')
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content.String()); err != nil {
		_ = file.Close()
		return err
	}
	if storage.fsync {
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	if old, ok := storage.sidecars[conversationId]; ok {
		_ = old.Close()
		delete(storage.sidecars, conversationId)
	}
	if err := os.Rename(temporary, name); err != nil {
		return err
	}
	_, err = storage.sidecarFile(storage.sidecars, conversationId, filepath.Base(name))
	return err
}

// Close closes every file; it is idempotent.
func (storage *JsonlStorage) Close(_ context.Context) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closedOnce {
		return nil
	}
	storage.closedOnce = true
	storage.closed = true
	var errs []error
	errs = append(errs, storage.mainFile.Close())
	for _, file := range storage.sidecars {
		errs = append(errs, file.Close())
	}
	for _, file := range storage.taskSidecars {
		errs = append(errs, file.Close())
	}
	return errors.Join(errs...)
}

// truncateTornTail cuts bytes after the last newline so a later append cannot
// extend a corrupt line.
func truncateTornTail(file string) error {
	data, err := os.ReadFile(file)
	if err != nil || len(data) == 0 {
		return err
	}
	end := bytes.LastIndexByte(data, '\n')
	if end+1 != len(data) {
		return os.Truncate(file, int64(end+1))
	}
	return nil
}
