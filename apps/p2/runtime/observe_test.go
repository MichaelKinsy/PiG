package runtime

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	nodeenv "github.com/MichaelKinsy/PiG/durable/env/node"
)

func observed(dir string) []ObservedFiles {
	return []ObservedFiles{{
		Env: nodeenv.NewNodeExecutionEnv(nodeenv.NodeExecutionEnvOptions{Cwd: dir}),
		Targets: func(context.Context) ([]durableenv.WatchTarget, error) {
			return []durableenv.WatchTarget{{Path: filepath.Join(dir, "settings.json")}}, nil
		},
	}}
}

func TestObserverLoadsOnStartAndOnChange(t *testing.T) {
	dir := t.TempDir()
	loads := make(chan bool, 16)
	observer := NewObserver(observed(dir), func(_ context.Context, retrying bool) bool {
		loads <- retrying
		return false
	}, 50*time.Millisecond)
	defer observer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := observer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if retrying := <-loads; retrying {
		t.Fatal("the first load is not a retry")
	}
	// A creation is a change: the watcher reloads without a call.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-loads:
	case <-ctx.Done():
		t.Fatal("no load after the file was created")
	}
	// An explicit Reload returns once loads have caught up.
	before := len(loads)
	if err := observer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if len(loads) <= before {
		t.Fatal("Reload did not load")
	}
}

func TestObserverReadsHalfWrittenContentOnceMore(t *testing.T) {
	var calls atomic.Int32
	retried := make(chan bool, 1)
	observer := NewObserver(observed(t.TempDir()), func(_ context.Context, retrying bool) bool {
		if calls.Add(1) == 1 {
			return true // an editor's save caught half-way
		}
		select {
		case retried <- retrying:
		default:
		}
		return false
	}, time.Hour)
	defer observer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := observer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case retrying := <-retried:
		if !retrying {
			t.Fatal("the second read is flagged as a retry")
		}
	case <-ctx.Done():
		t.Fatal("no second read")
	}
}

func TestObserverCloseReleasesWaitingReloads(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	observer := NewObserver(observed(t.TempDir()), func(ctx context.Context, _ bool) bool {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return false
	}, time.Hour)
	errs := make(chan error, 1)
	go func() { errs <- observer.Start(context.Background()) }()
	<-started
	observer.Close() // cancels the load's context
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := observer.Reload(context.Background()); err != nil {
		t.Fatal("Reload after Close returns at once:", err)
	}
	close(release)
}
