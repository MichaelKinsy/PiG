package app

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// pi: packages/chord/src/node/app/app.ts
// Ports `workersOf`'s `store` (Pi main f57cd9484): "The server's: a worker's exit is published after the commits its calls queued
// there". The store the workers settle on is the server's own: a commit queued in the server's store (here one whose admission is
// held, as the commit of a worker's call that is still waiting for its turn is) holds a stopped worker's exit, so the stop does not
// resolve until that commit has landed. With the server's store not given to the workers, the exit is published at once: the stop
// resolves while the commit is held. No other store has such a commit, so the test passes only with this store.
func TestAppGivesItsWorkersTheServersStoreAWorkersExitIsPublishedAfterTheCommitsQueuedThere(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, projectOptions(t))
	notes := holdNotes(t, s, "p1")
	defer notes.Release()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	// A test that fails while the commit is held must not leave the app closing behind it.
	t.Cleanup(letGo)
	committed := make(chan error, 1)
	go func() {
		_, err := s.app.store.(*store.ChordStore).Commit(nil, store.CommitFence{
			Admit: func() bool {
				close(entered)
				<-release
				return true
			},
			Adopt: func(int64, []int64, int) error { return nil },
		}, background)
		committed <- err
	}()
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- s.app.Workers["project"].Stop(projectAddress("p1"), background) }()
	// The worker exits and its link closes with it (before its exit is told): the holder sees that.
	eventually(t, "the worker's link closed", func() bool { return notes.State().Value().Kind != host.RefStateLive })
	// Pi's own wait, in its case of the same decision (packages/chord/test/node/workers/workers.test.ts:859 at f57cd9484: `await new
	// Promise((resolve) => setTimeout(resolve, 200))` after the holder sees the link closed, before the state is asserted unchanged):
	// the component has no seam, from the app, to wait on for its having handled the exit. A component that waits for the commit
	// never resolves the stop in it; one that does not does at once. Once the commit is released, what follows is awaited by event.
	select {
	case err := <-stopped:
		t.Fatalf("the stop resolved (%v) while a commit queued in the server's store was held", err)
	case <-time.After(200 * time.Millisecond):
	}
	letGo()
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}
