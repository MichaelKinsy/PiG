package jsonl_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi storage/jsonl/storage.ts:570-577: a sidecar whose (seq, ordinal) does not strictly increase is corrupt, and that includes a record repeated at the same
// (seq, ordinal) (`ordinal <= previous.ordinal`); the out-of-order test above covers the decreasing ordinal, this covers the equal one.
// mutation-checked: `ordinal < previous.ordinal` (an equal ordinal accepted) fails it.
func TestJsonlRecoveryRejectsASidecarRecordRepeatedAtTheSameSeqAndOrdinal(t *testing.T) {
	directory := tempDirectory(t)
	storage := open(t, directory)
	createRoot(t, storage)
	taskId := mint[durable.TaskId](t, storage)
	commit(t, storage, durable.TaskWrite{Value: pendingTask(taskId, "only")})
	path := file(directory, "task", int64(taskId))
	lines := readLines(t, path)
	expectLength(t, lines, 1)
	mustDo(t, os.WriteFile(path, []byte(lines[0]+"\n"+lines[0]+"\n"), 0o600))
	_, err := openJsonl(directory)
	expectError(t, err, fmt.Sprintf("Sidecar records are out of order in task-%d.jsonl", taskId))
}

// Pi storage.ts:104 and :551-555: opening a directory removes every `doc-N.jsonl.reclaim` and `task-N.jsonl.reclaim` file a crashed reclamation left (isReclaimFileName),
// and no other file (a name that is not that exact pattern stays).
// mutation-checked: not removing the reclaim files at recovery fails it.
func TestJsonlRecoveryRemovesLeftoverReclaimFilesOnly(t *testing.T) {
	directory := tempDirectory(t)
	createRoot(t, open(t, directory))
	stale := []string{"doc-5.jsonl.reclaim", "task-12.jsonl.reclaim"}
	kept := []string{"doc-05.jsonl.reclaim", "note.jsonl.reclaim", "doc-5.jsonl.reclaim.bak"}
	for _, name := range append(append([]string{}, stale...), kept...) {
		mustDo(t, os.WriteFile(filepath.Join(directory, name), []byte("leftover"), 0o600))
	}
	open(t, directory)
	for _, name := range stale {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived recovery (stat error %v)", name, err)
		}
	}
	for _, name := range kept {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Errorf("%s is not a reclaim file and must stay: %v", name, err)
		}
	}
}
