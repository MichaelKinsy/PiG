package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// A running worker's export (PackRunning, HostDataFacet's body) while both of its stores are written. Pi has one store whose export holds
// its commit queue, so the archive is one point; PiG has two (D462) and the plugins' one is written by the NodeHost, another process, whose
// commit queue the worker cannot hold. The export holds the kit's queue, then takes a snapshot of the plugins' file at one instant, so both
// archives are one point: with a writer that commits tick i to the plugins' store and then to the kit's, the plugins' archive holds the
// kit's count or one more, and each archive holds exactly the ticks its own manifest counts.

var ticks = func() *store.TableDefinition {
	definition, err := store.DefineTable("ticks", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	if err != nil {
		panic(err)
	}
	return definition
}()

// tickOwner is the owner of the tests' table handle: nothing to refuse, nothing live.
type tickOwner struct{}

func (tickOwner) Label() string               { return "ticks" }
func (tickOwner) Scope() any                  { return nil }
func (tickOwner) Queries() *store.LiveQueries { return store.NewLiveQueries() }
func (tickOwner) AssertLive() error           { return nil }
func (tickOwner) Fail(*data.ChordError)       {}

var openFence = store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}

// ticker is a writer of the ticks table of opened.
type ticker struct {
	t      testing.TB
	store  *store.ChordStore
	handle *store.TableHandle
	// base is the store's seq once the table exists: the preload is the next seq, each tick one more.
	base int64
}

func newTicker(t testing.TB, opened *store.ChordStore) *ticker {
	t.Helper()
	if err := store.PrepareTables([]store.DeclaredTable{{Definition: ticks, Table: "test:ticks"}}, opened, openFence, "test", background); err != nil {
		t.Fatal(err)
	}
	return &ticker{t: t, store: opened, handle: store.NewTableHandle(ticks, "test:ticks", opened, tickOwner{}), base: opened.LastSeq()}
}

// put commits the ticks from..to (one commit).
func (w *ticker) put(from, to int) error {
	var staged []store.StagedWrite
	changes := store.NewTableChanges(w.handle, &staged)
	for id := from; id <= to; id++ {
		if err := changes.Put(store.Row{"id": int64(id)}); err != nil {
			return err
		}
	}
	_, err := w.store.Commit(staged, openFence, background)
	return err
}

// archived is what one store archive of an export says: the seq of its manifest and how many ticks it has.
type archived struct{ lastSeq, ticks int64 }

// archivesOf reads the plugins' and the kit's store archives of the export tar at path.
func archivesOf(t *testing.T, path string) (plugins, kit archived) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	found := map[string]archived{}
	err = ReadTar(file, func(entry TarRead) error {
		if entry.Path != "store.archive" && entry.Path != "kit.archive" {
			_, err := io.Copy(io.Discard, entry.Data)
			return err
		}
		var text bytes.Buffer
		if _, err := io.Copy(&text, entry.Data); err != nil {
			return err
		}
		var seen archived
		lines := bufio.NewScanner(&text)
		lines.Buffer(nil, 1<<20)
		for lines.Scan() {
			var part struct {
				Kind    string `json:"kind"`
				Table   string `json:"table"`
				LastSeq int64  `json:"lastSeq"`
			}
			if err := json.Unmarshal(lines.Bytes(), &part); err != nil {
				return err
			}
			switch {
			case part.Kind == "manifest":
				seen.lastSeq = part.LastSeq
			case part.Kind == "put" && part.Table == "test:ticks":
				seen.ticks++
			}
		}
		found[entry.Path] = seen
		return lines.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"store.archive", "kit.archive"} {
		if _, ok := found[name]; !ok {
			t.Fatalf("the export has no %s", name)
		}
	}
	return found["store.archive"], found["kit.archive"]
}

func TestPackRunningExportsBothStoresAtOnePointWhileTheyAreWritten(t *testing.T) {
	const preloaded, exports = 3000, 25
	dir := filepath.Join(t.TempDir(), "host")
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The plugins' store as the NodeHost has it: a store of its own on store.sqlite, written while the worker exports.
	pluginsStore := openStoreAt(t, filepath.Join(dir, "store.sqlite"))
	kitStore := openStoreAt(t, filepath.Join(dir, "kit.sqlite"))
	plugins, kit := newTicker(t, pluginsStore), newTicker(t, kitStore)
	for _, w := range []*ticker{plugins, kit} {
		if err := w.put(1, preloaded); err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan struct{})
	var writer sync.WaitGroup
	var written int
	writer.Go(func() {
		for id := preloaded + 1; ; id++ {
			select {
			case <-stop:
				return
			default:
			}
			// Plugins' tick, then the kit's: the plugins' store is never behind the kit's, and ahead of it by at most the tick in between.
			if err := plugins.put(id, id); err != nil {
				t.Error(err)
				return
			}
			if err := kit.put(id, id); err != nil {
				t.Error(err)
				return
			}
			written = id
		}
	})
	var first, last int64
	for n := range exports {
		out := filepath.Join(t.TempDir(), "out.tar")
		if err := PackRunning(context.Background(), dir, kitStore, archiveManifest, out); err != nil {
			t.Fatalf("export %d: %v", n, err)
		}
		inPlugins, inKit := archivesOf(t, out)
		// Each archive is a point of its store: the ticks its manifest counts, no more (a read past its snapshot would add them).
		if inPlugins.ticks != inPlugins.lastSeq-plugins.base+preloaded-1 {
			t.Fatalf("export %d: the plugins' archive has %d ticks at seq %d (base %d)", n, inPlugins.ticks, inPlugins.lastSeq, plugins.base)
		}
		if inKit.ticks != inKit.lastSeq-kit.base+preloaded-1 {
			t.Fatalf("export %d: the kit's archive has %d ticks at seq %d (base %d)", n, inKit.ticks, inKit.lastSeq, kit.base)
		}
		// And the two are one point: the plugins' is the kit's, or one tick ahead.
		if ahead := inPlugins.ticks - inKit.ticks; ahead != 0 && ahead != 1 {
			t.Fatalf("export %d: the plugins' archive has %d ticks and the kit's %d: not one point", n, inPlugins.ticks, inKit.ticks)
		}
		if n == 0 {
			first = inKit.ticks
		}
		last = inKit.ticks
	}
	close(stop)
	writer.Wait()
	if last <= first || written <= preloaded {
		t.Fatalf("the writer made no progress during the exports: first %d, last %d, written %d", first, last, written)
	}
	if err := pluginsStore.Close(background); err != nil {
		t.Fatal(err)
	}
	if err := kitStore.Close(background); err != nil {
		t.Fatal(err)
	}
}
