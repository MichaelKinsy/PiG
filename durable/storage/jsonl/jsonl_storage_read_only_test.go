package jsonl_test

// pi: packages/durable/src/storage/jsonl/storage.ts

// Ports packages/durable/test/jsonl-storage.test.ts "JsonlStorage read-only open" (Pi f5f4a71af, #11939): a viewer opens a
// directory another process drives without repairing or reclaiming anything.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
)

// directoryBytes is every file of directory with its bytes, to show an open wrote nothing.
func directoryBytes(t *testing.T, directory string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, entry := range must(os.ReadDir(directory)) {
		files[entry.Name()] = string(must(os.ReadFile(filepath.Join(directory, entry.Name()))))
	}
	return files
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	file := must(os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644))
	defer func() { mustDo(t, file.Close()) }()
	must(file.WriteString(text))
}

// unconfirmedRecord is the last record of a task sidecar as an append in flight would leave the next one: the next
// sequence, a different phase, never confirmed by a main marker.
func unconfirmedRecord(t *testing.T, sidecar string, seq int, phase string) string {
	t.Helper()
	lines := readLines(t, sidecar)
	var record map[string]any
	mustDo(t, json.Unmarshal([]byte(lines[len(lines)-1]), &record))
	record["seq"] = seq
	checkpoint := record["payload"].(map[string]any)["value"].(map[string]any)["state"].(map[string]any)["checkpoint"].(map[string]any)
	checkpoint["phase"] = phase
	return string(must(json.Marshal(record)))
}

func openReadOnly(t *testing.T, directory string, fileSystem jsonl.FileSystem) *jsonl.JsonlStorage {
	t.Helper()
	return openStorage(t, directory, fileSystem, jsonl.JsonlStorageOptions{ReadOnly: true})
}

// racingEnv runs race once, right before the first read of file, and counts reads of main.jsonl (Pi's RacingEnv).
type racingEnv struct {
	*node.NodeFileSystem
	mainReads int
	file      string
	race      func()
}

func newRacingEnv(directory, file string, race func()) *racingEnv {
	return &racingEnv{NodeFileSystem: &node.NodeFileSystem{Cwd: directory}, file: file, race: race}
}

func (fileSystem *racingEnv) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	switch filepath.Base(path) {
	case "main.jsonl":
		fileSystem.mainReads++
	case fileSystem.file:
		if race := fileSystem.race; race != nil {
			fileSystem.race = nil
			race()
		}
	}
	return fileSystem.NodeFileSystem.ReadBinaryFile(ctx, path)
}

func expectTask(t *testing.T, storage *jsonl.JsonlStorage, id durable.TaskId, want storedTask) {
	t.Helper()
	got := must(storage.Task(testContext, id))
	if got == nil {
		t.Fatalf("task %d is missing, want %s", id, durabletest.Describe(want))
	}
	expectEqual(t, *got, want)
}

func TestJsonlStorageReadOnlyOpen(t *testing.T) {
	t.Run("skips torn lines, unconfirmed tails, and due reclamation in memory without writing", func(t *testing.T) {
		directory := tempDirectory(t)
		instrumented := newInstrumentedEnv(directory)
		storage := openStorage(t, directory, instrumented, jsonl.JsonlStorageOptions{})
		createRoot(t, storage)
		liveId := mint[durable.TaskId](t, storage)
		endedId := mint[durable.TaskId](t, storage)
		commit(t, storage,
			durable.TaskWrite{Value: pendingTask(liveId, "ready")},
			durable.TaskWrite{Value: pendingTask(endedId, "ready")},
		)
		// The terminal task's sidecar stays behind: its reclamation is due at the next writable open.
		instrumented.fail(failure{operation: "remove", call: 1, mode: "before"})
		commit(t, storage, durable.TaskWrite{Value: terminalTask(endedId)})
		instrumented.clear()
		endedSidecar := filepath.Join(directory, fmt.Sprintf("task-%d.jsonl", endedId))
		liveSidecar := filepath.Join(directory, fmt.Sprintf("task-%d.jsonl", liveId))
		if !fileExists(t, endedSidecar) {
			t.Fatal("the terminal task's sidecar is gone before the viewer opens")
		}
		appendTo(t, liveSidecar, unconfirmedRecord(t, liveSidecar, 4, "unconfirmed")+"\n{\"format\":1")
		appendTo(t, filepath.Join(directory, "main.jsonl"), `{"format":1,"type":"commit"`)
		appendTo(t, liveSidecar+".reclaim", "leftover")
		before := directoryBytes(t, directory)

		viewer := openReadOnly(t, directory, &node.NodeFileSystem{Cwd: directory})
		expectTask(t, viewer, liveId, pendingTask(liveId, "ready"))
		expectTask(t, viewer, endedId, terminalTask(endedId))
		_, err := durabletest.CommitWrites(testContext, viewer, []durable.StorageWrite{durable.TaskWrite{Value: pendingTask(liveId, "next")}})
		if _, ok := errors.AsType[*jsonl.JsonlStorageReadOnlyError](err); !ok {
			t.Fatalf("commit error = %v, want *JsonlStorageReadOnlyError", err)
		}
		if after := directoryBytes(t, directory); !reflect.DeepEqual(after, before) {
			t.Fatalf("a read-only open wrote: before %v, after %v", before, after)
		}

		// A writable open still repairs the same files.
		open(t, directory)
		if after := directoryBytes(t, directory); reflect.DeepEqual(after, before) {
			t.Fatal("a writable open repaired nothing")
		}
		if fileExists(t, endedSidecar) {
			t.Fatal("the writable open left the terminal task's sidecar")
		}
		if fileExists(t, liveSidecar+".reclaim") {
			t.Fatal("the writable open left the reclaim file")
		}
	})

	t.Run("keeps a torn-only main file", func(t *testing.T) {
		directory := tempDirectory(t)
		mustDo(t, os.WriteFile(filepath.Join(directory, "main.jsonl"), []byte(`{"format":1,"type":"commit"`), 0o644))
		viewer := openReadOnly(t, directory, &node.NodeFileSystem{Cwd: directory})
		conversation, err := viewer.Conversation(testContext, durable.ROOT_CONVERSATION_ID)
		expectNil(t, conversation, err)
		if size := must(os.Stat(filepath.Join(directory, "main.jsonl"))).Size(); size != 27 {
			t.Fatalf("main.jsonl size = %d, want 27", size)
		}
	})

	t.Run("creates no missing directory", func(t *testing.T) {
		directory := filepath.Join(tempDirectory(t), "missing")
		_, err := jsonl.Open(testContext, directory, &node.NodeFileSystem{Cwd: directory}, jsonl.JsonlStorageOptions{ReadOnly: true})
		expectError(t, err, "directory listing")
		if fileExists(t, directory) {
			t.Fatal("the read-only open created the directory")
		}
	})

	t.Run("retries a recovery that raced a writer's commit, and fails on corruption at once", func(t *testing.T) {
		directory := tempDirectory(t)
		writer := open(t, directory)
		createRoot(t, writer)
		taskId := mint[durable.TaskId](t, writer)
		commit(t, writer, durable.TaskWrite{Value: pendingTask(taskId, "ready")})

		// The writer ends the task between the viewer's read of main.jsonl and of the task's sidecar, so the sidecar the
		// viewer's markers need is reclaimed when it reads it.
		racing := newRacingEnv(directory, fmt.Sprintf("task-%d.jsonl", taskId), func() {
			commit(t, writer, durable.TaskWrite{Value: terminalTask(taskId)})
		})
		viewer := openReadOnly(t, directory, racing)
		expectTask(t, viewer, taskId, terminalTask(taskId))
		if racing.mainReads != 2 {
			t.Fatalf("main.jsonl reads = %d, want 2", racing.mainReads)
		}

		// Missing confirmed data while main.jsonl stays the same is corruption: no retry.
		brokenId := mint[durable.TaskId](t, writer)
		commit(t, writer, durable.TaskWrite{Value: pendingTask(brokenId, "ready")})
		mustDo(t, os.WriteFile(filepath.Join(directory, fmt.Sprintf("task-%d.jsonl", brokenId)), nil, 0o644))
		counting := newRacingEnv(directory, "none", nil)
		_, err := jsonl.Open(testContext, directory, counting, jsonl.JsonlStorageOptions{ReadOnly: true})
		expectError(t, err, "Missing confirmed sidecar record")
		if counting.mainReads != 1 {
			t.Fatalf("main.jsonl reads = %d, want 1: corruption is not retried", counting.mainReads)
		}
	})
}
