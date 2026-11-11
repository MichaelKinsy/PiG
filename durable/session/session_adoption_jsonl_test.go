package session_test

// Ports the case "publishes a JSONL commit once the storage reclaimed its sidecar and resolved" of
// packages/durable/test/session-adoption.test.ts. A base at the second count makes JSONL rewrite the document's sidecar after
// it took the Session's receipt; the Session publishes only once the storage resolved, so the host's observer follows the
// sidecar's rename, the last step of a reclaim.

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
)

// renameLog is the local file system that records each rename, the last step of a reclaim.
type renameLog struct {
	*node.NodeFileSystem
	mu    sync.Mutex
	order []string
}

func (fileSystem *renameLog) record(step string) {
	fileSystem.mu.Lock()
	fileSystem.order = append(fileSystem.order, step)
	fileSystem.mu.Unlock()
}

func (fileSystem *renameLog) RenameFile(ctx context.Context, source, destination string) error {
	fileSystem.record("rename " + filepath.Base(destination))
	return fileSystem.NodeFileSystem.RenameFile(ctx, source, destination)
}

func TestPublishesAJsonlCommitOnceTheStorageReclaimedItsSidecarAndResolved(t *testing.T) {
	rebased := defineDoc("adoption.rebased", 1, sessionScope, func() obj { return delta.JsonObjectOf("count", 0) },
		withCheckpoint(func(value obj, _ []durable.Op, _ durable.CheckpointInfo) bool { return num(value.Value("count")) == 2 }))
	directory := t.TempDir()
	files := &renameLog{NodeFileSystem: &node.NodeFileSystem{Cwd: directory}}
	storage, err := jsonl.Open(ctx, directory, files, jsonl.JsonlStorageOptions{})
	must(t, err)
	kernel := session.NewSessionImpl(storage, session.Hooks{}, nil, nil)
	count := func() {
		commit(t, kernel, func(tx durable.Tx) error {
			draft := mustDoc(t, tx, rebased)
			return draft.Set("count", int(num(draft.Get("count")))+1)
		})
	}
	defer func() { must(t, kernel.Close(ctx)) }()
	count()
	sessiontest.ObserveCommitsFunc(kernel, func(context.Context, durable.CommitPublication) { files.record("host") })
	count()
	files.record("resolved")
	files.mu.Lock()
	order := slices.Clone(files.order)
	files.mu.Unlock()
	if len(order) != 3 || !regexp.MustCompile(`^rename doc-\d+\.jsonl$`).MatchString(order[0]) || order[1] != "host" || order[2] != "resolved" {
		t.Fatalf("order %v, want [rename doc-N.jsonl host resolved]", order)
	}
}
