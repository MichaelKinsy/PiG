package node

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// lateAbort is a context that reports cancellation only from its n-th Err() call on, so the abort arrives while the operation runs.
type lateAbort struct {
	context.Context
	left atomic.Int64
}

func abortingAfterCalls(n int64) *lateAbort {
	c := &lateAbort{Context: context.Background()}
	c.left.Store(n)
	return c
}

func (c *lateAbort) Err() error {
	if c.left.Add(-1) >= 0 {
		return nil
	}
	return context.Canceled
}

// Pi: packages/durable/src/env/node.ts:959-1003 (openBinaryReader): the abort is checked before the open (:965-966) and again once the file is open and known to be regular
// (:989-993); an abort that lands in between closes the handle and fails with `aborted`, and no reader is returned.
// mutation-checked: dropping the after-open check (Go OpenBinaryReader) returns a reader and fails it.
func TestOpenBinaryReaderAbortsWhenCancelledAfterTheFileOpened(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "file"), []byte("abc"), 0o600))
	reader, err := env.OpenBinaryReader(abortingAfterCalls(1), "file", nil)
	if fileErrorCode(t, err) != durableenv.FileErrorAborted || reader != nil {
		t.Fatalf("OpenBinaryReader with a late abort = %v, %v; want aborted and no reader", reader, err)
	}
	// Without the abort the same call opens the reader.
	reader = must(env.OpenBinaryReader(background, "file", nil))
	mustDo(t, reader.Close(background))
}
