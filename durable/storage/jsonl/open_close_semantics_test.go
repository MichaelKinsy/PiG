package jsonl_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
)

// failingFileSystem is the node file system with chosen operations replaced.
type failingFileSystem struct {
	*node.NodeFileSystem
	absolute  func(path string) (string, error)
	createDir error
	resolved  []string
}

func (fs *failingFileSystem) AbsolutePath(ctx context.Context, path string) (string, error) {
	if fs.absolute != nil {
		return fs.absolute(path)
	}
	return fs.NodeFileSystem.AbsolutePath(ctx, path)
}

func (fs *failingFileSystem) CreateDir(ctx context.Context, path string, options *env.CreateDirOptions) error {
	fs.resolved = append(fs.resolved, path)
	if fs.createDir != nil {
		return fs.createDir
	}
	return fs.NodeFileSystem.CreateDir(ctx, path, options)
}

// Pi packages/durable/src/storage/jsonl/storage.ts:260-272 open: the directory is resolved through the file system's absolutePath, created from that path, and
// each failing step rejects with its own "JSONL <step> failed: <message>" error.
func TestJsonlOpenResolvesCreatesAndReportsEachFailingStep(t *testing.T) {
	base := t.TempDir()
	fs := &failingFileSystem{NodeFileSystem: &node.NodeFileSystem{Cwd: base}}
	opened, err := jsonl.Open(testContext, "relative/storage", fs, jsonl.JsonlStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "relative", "storage"); len(fs.resolved) != 1 || fs.resolved[0] != want {
		t.Fatalf("CreateDir paths = %v, want the resolved %s", fs.resolved, want)
	}
	if _, statErr := os.Stat(filepath.Join(base, "relative", "storage")); statErr != nil {
		t.Fatalf("the storage directory was not created under the working directory: %v", statErr)
	}
	if err := opened.Close(testContext); err != nil {
		t.Fatal(err)
	}

	resolution := errors.New("no such mount")
	_, err = jsonl.Open(testContext, "x", &failingFileSystem{NodeFileSystem: fs.NodeFileSystem, absolute: func(string) (string, error) { return "", resolution }}, jsonl.JsonlStorageOptions{})
	if err == nil || err.Error() != "JSONL path resolution failed: no such mount" || !errors.Is(err, resolution) {
		t.Fatalf("a failing absolutePath = %v, want the path resolution failure wrapping its cause", err)
	}
	creation := env.NewFileError(env.FileErrorPermissionDenied, "read-only", "x", nil)
	_, err = jsonl.Open(testContext, "x", &failingFileSystem{NodeFileSystem: fs.NodeFileSystem, createDir: creation}, jsonl.JsonlStorageOptions{})
	if err == nil || err.Error() != "JSONL directory creation failed: read-only" || !errors.Is(err, creation) {
		t.Fatalf("a failing createDir = %v, want the directory creation failure naming the file error's message", err)
	}
}

// storage.ts:377-380 close and :842 assertUsable: a closed storage answers every call with the error "JsonlStorage is closed" (checked before the poison), and closing again succeeds.
func TestJsonlCloseMakesEveryLaterCallFailWithTheClosedError(t *testing.T) {
	storage := open(t, t.TempDir())
	if _, ok := any(storage).(*jsonl.JsonlStorage); !ok {
		t.Fatalf("open returned %T, want *jsonl.JsonlStorage", storage)
	}
	createRoot(t, storage)
	if err := storage.Close(testContext); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(testContext); err != nil {
		t.Fatalf("a second Close = %v, want nil", err)
	}
	if _, err := storage.MintId(); err == nil || err.Error() != "JsonlStorage is closed" {
		t.Fatalf("MintId after Close = %v, want the closed error", err)
	}
	if _, err := storage.Commit(testContext, nil); err == nil || err.Error() != "JsonlStorage is closed" {
		t.Fatalf("Commit after Close = %v, want the closed error", err)
	}
}
