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
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// A running worker's export (PackRunning, HostDataFacet's body) while both of its stores are written. Pi has one store whose export holds
// its commit queue, so the archive is one point; PiG has two (D462) and the plugins' one is written by the NodeHost, another process, whose
// commit queue the worker cannot hold. The export holds the kit's queue, then takes a snapshot of the plugins' file at one instant, so both
// archives are one point: with a writer that commits tick i to the plugins' store and then to the kit's, the plugins' archive holds the
// kit's count or one more, and each archive holds exactly the ticks its own manifest counts.

// tickTable is a table of one int key, `id`.
func tickTable(name string) *store.TableDefinition {
	definition, err := store.DefineTable(name, store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	// ticks is written to both stores: the plugins' tick i, then the kit's. noise is written to the plugins' store alone, by the plugins' own
	// writer, which nothing stops while the worker exports (the NodeHost's plugins write their store whatever the kit does).
	ticks = tickTable("ticks")
	noise = tickTable("noise")
)

// tickOwner is the owner of the tests' table handles: nothing to refuse, nothing live.
type tickOwner struct{}

func (tickOwner) Label() string               { return "ticks" }
func (tickOwner) Scope() any                  { return nil }
func (tickOwner) Queries() *store.LiveQueries { return store.NewLiveQueries() }
func (tickOwner) AssertLive() error           { return nil }
func (tickOwner) Fail(*data.ChordError)       {}

var openFence = store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}

// prepared creates the tables in opened, in one commit; it returns their writers.
func prepared(t testing.TB, opened *store.ChordStore, definitions ...*store.TableDefinition) []*ticker {
	t.Helper()
	declared := make([]store.DeclaredTable, len(definitions))
	for i, definition := range definitions {
		declared[i] = store.DeclaredTable{Definition: definition, Table: "test:" + definition.Name}
	}
	if err := store.PrepareTables(declared, opened, openFence, "test", background); err != nil {
		t.Fatal(err)
	}
	writers := make([]*ticker, len(definitions))
	for i, definition := range definitions {
		writers[i] = &ticker{store: opened, handle: store.NewTableHandle(definition, "test:"+definition.Name, opened, tickOwner{})}
	}
	return writers
}

// ticker is a writer of one table of a store.
type ticker struct {
	store  *store.ChordStore
	handle *store.TableHandle
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

// archived is what one store archive of an export says: the seq of its manifest, how many rows it puts and how many of them are ticks.
type archived struct{ lastSeq, puts, ticks int64 }

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
			switch part.Kind {
			case "manifest":
				seen.lastSeq = part.LastSeq
			case "put":
				seen.puts++
				if part.Table == "test:ticks" {
					seen.ticks++
				}
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
	const preloaded, exports = 1000, 8
	dir := filepath.Join(t.TempDir(), "host")
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The plugins' store as the NodeHost has it: a store of its own on store.sqlite, written while the worker exports.
	pluginsStore := openStoreAt(t, filepath.Join(dir, "store.sqlite"))
	kitStore := openStoreAt(t, filepath.Join(dir, "kit.sqlite"))
	// Each store's seq before its preload: the preload is the next seq, each commit after it one more.
	pluginsTables := prepared(t, pluginsStore, ticks, noise)
	pluginsTicks, pluginsNoise := pluginsTables[0], pluginsTables[1]
	kitTicks := prepared(t, kitStore, ticks)[0]
	pluginsBase, kitBase := pluginsStore.LastSeq(), kitStore.LastSeq()
	for _, w := range []*ticker{pluginsTicks, kitTicks} {
		if err := w.put(1, preloaded); err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan struct{})
	var writers sync.WaitGroup
	var written atomic.Int64
	writers.Go(func() {
		for id := preloaded + 1; ; id++ {
			select {
			case <-stop:
				return
			default:
			}
			// The plugins' tick, then the kit's: the plugins' store is never behind the kit's, and ahead of it by at most the tick in between.
			if err := pluginsTicks.put(id, id); err != nil {
				t.Error(err)
				return
			}
			if err := kitTicks.put(id, id); err != nil {
				t.Error(err)
				return
			}
			written.Store(int64(id))
		}
	})
	// The plugins' own writer, which the kit's hold does not stop: only a snapshot keeps its commits out of the archive.
	writers.Go(func() {
		for id := 1; ; id++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := pluginsNoise.put(id, id); err != nil {
				t.Error(err)
				return
			}
		}
	})
	var first, last int64
	for n := range exports {
		out := filepath.Join(t.TempDir(), "out.tar")
		if err := PackRunning(context.Background(), dir, kitStore, archiveManifest, out); err != nil {
			t.Fatalf("export %d: %v", n, err)
		}
		inPlugins, inKit := archivesOf(t, out)
		// Each archive is a point of its store: exactly the rows its manifest's seq counts (a read past its snapshot would add some).
		if want := inPlugins.lastSeq - pluginsBase + preloaded - 1; inPlugins.puts != want {
			t.Fatalf("export %d: the plugins' archive has %d rows at seq %d (base %d), want %d", n, inPlugins.puts, inPlugins.lastSeq, pluginsBase, want)
		}
		if want := inKit.lastSeq - kitBase + preloaded - 1; inKit.puts != want || inKit.ticks != want {
			t.Fatalf("export %d: the kit's archive has %d rows (%d ticks) at seq %d (base %d), want %d", n, inKit.puts, inKit.ticks, inKit.lastSeq, kitBase, want)
		}
		// And the two are one point: the plugins' ticks are the kit's, or one more.
		if ahead := inPlugins.ticks - inKit.ticks; ahead != 0 && ahead != 1 {
			t.Fatalf("export %d: the plugins' archive has %d ticks and the kit's %d: not one point", n, inPlugins.ticks, inKit.ticks)
		}
		if n == 0 {
			first = inKit.ticks
		}
		last = inKit.ticks
	}
	close(stop)
	writers.Wait()
	if last <= first || written.Load() <= preloaded {
		t.Fatalf("the writers made no progress during the exports: first %d, last %d, written %d", first, last, written.Load())
	}
	if err := pluginsStore.Close(background); err != nil {
		t.Fatal(err)
	}
	if err := kitStore.Close(background); err != nil {
		t.Fatal(err)
	}
}
