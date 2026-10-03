package tools

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/durable/env"
)

// Ports packages/durable/src/tools/file-mutation-queue.ts.

// mutationQueues holds the tail of the mutation chain of each file, keyed by
// file system id and canonical path: a channel closed when the last queued
// mutation of the file finishes.
var mutationQueues = struct {
	mu    sync.Mutex
	tails map[string]chan struct{}
}{tails: map[string]chan struct{}{}}

func mutationKey(ctx context.Context, executionEnv env.FileSystem, path string) (string, error) {
	absolutePath, err := executionEnv.AbsolutePath(ctx, path)
	if err != nil {
		return "", err
	}
	canonicalized, err := canonical(ctx, executionEnv, absolutePath)
	if err != nil {
		return "", err
	}
	return executionEnv.Id() + "\x00" + canonicalized, nil
}

// canonical is the canonical path; for a file that does not exist yet, its
// canonical parent joined with its name, so a write that creates a file and a
// later mutation of it share one key even under a symlinked directory.
func canonical(ctx context.Context, executionEnv env.FileSystem, absolutePath string) (string, error) {
	resolved, err := executionEnv.CanonicalPath(ctx, absolutePath)
	if err == nil {
		return resolved, nil
	}
	fileErr, isFileError := errors.AsType[*env.FileError](err)
	if isFileError && fileErr.Code == env.FileErrorNotSupported {
		return absolutePath, nil
	}
	if !isFileError || fileErr.Code != env.FileErrorNotFound {
		return "", err
	}
	// The file system splits the path, so a name may contain characters that
	// are separators elsewhere.
	parent, err := executionEnv.JoinPath(ctx, []string{absolutePath, ".."})
	if err != nil {
		return "", err
	}
	if parent == absolutePath || !strings.HasPrefix(absolutePath, parent) {
		return absolutePath, nil
	}
	separatorLength := 1
	if strings.HasSuffix(parent, "/") || strings.HasSuffix(parent, `\`) {
		separatorLength = 0
	}
	name := absolutePath[len(parent)+separatorLength:]
	canonicalParent, err := canonical(ctx, executionEnv, parent)
	if err != nil {
		return "", err
	}
	return executionEnv.JoinPath(ctx, []string{canonicalParent, name})
}

// withFileMutationQueue serializes edit and write mutations of one file within
// this process: same file system id and canonical path, whichever environment
// object the call got. Other files, and other file systems, never wait.
// Concurrent calls on one file run in the order their keys resolve. It is not a
// lock against bash or other processes.
func withFileMutationQueue[T any](ctx context.Context, executionEnv env.FileSystem, path string, fn func() (T, error)) (T, error) {
	var zero T
	key, err := mutationKey(ctx, executionEnv, path)
	if err != nil {
		return zero, err
	}
	// Take the slot without waiting, so no other call can take it in between.
	done := make(chan struct{})
	mutationQueues.mu.Lock()
	previous := mutationQueues.tails[key]
	mutationQueues.tails[key] = done
	mutationQueues.mu.Unlock()
	if previous != nil {
		<-previous
	}
	defer func() {
		close(done)
		mutationQueues.mu.Lock()
		if mutationQueues.tails[key] == done {
			delete(mutationQueues.tails, key)
		}
		mutationQueues.mu.Unlock()
	}()
	return fn()
}
