package app

// pi: packages/chord/src/node/app/data.ts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// A running worker's export (PackRunning, HostDataFacet's body) while both of its stores are written. Pi has one store whose export holds
// its commit queue, so the archive is one point; PiG has two (D462) and the plugins' one is written by the NodeHost, another process, whose
// commit queue the worker cannot hold. The export holds the kit's queue, then takes a snapshot of the plugins' file at one instant, so both
// exports are one point, and writes them as the one `store.archive` Pi has: with a writer that commits tick i to the plugins' store and then
// to the kit's, the plugins' ticks in the archive are the kit's count or one more, and the plugins' rows are exactly those its manifest's
// seq counts (the archive's seq is the larger of the two stores', the plugins').

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

func (tickOwner) Label() string         { return "ticks" }
func (tickOwner) Scope() any            { return nil }
func (tickOwner) AssertLive() error     { return nil }
func (tickOwner) Fail(*data.ChordError) {}

var openFence = store.CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}

// prepared creates the tables in opened (named `<owner>:<name>`), in one commit; it returns their writers.
func prepared(t testing.TB, opened *store.ChordStore, owner string, definitions ...*store.TableDefinition) []*ticker {
	t.Helper()
	declared := make([]store.DeclaredTable, len(definitions))
	for i, definition := range definitions {
		declared[i] = store.DeclaredTable{Definition: definition, Table: owner + ":" + definition.Name}
	}
	if err := store.PrepareTables(declared, opened, openFence, "test", background); err != nil {
		t.Fatal(err)
	}
	writers := make([]*ticker, len(definitions))
	for i, definition := range definitions {
		writers[i] = &ticker{store: opened, handle: store.NewTableHandle(definition, owner+":"+definition.Name, opened, tickOwner{})}
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

// archived is what the export's `store.archive` says: the seq of its manifest, how many rows each table puts, and what else the export has.
type archived struct {
	lastSeq int64
	puts    map[string]int64
}

// The tables of the test: the plugins' ticks and noise (owner `test`) and the kit's ticks (owner of the kit's Files, which the kit's store
// is the only one to hold).
const (
	pluginTicksTable = "test:ticks"
	pluginNoiseTable = "test:noise"
	kitTicksTable    = filesName + ":ticks"
)

// archiveOf reads the `store.archive` of the export tar at path; the tar holds Pi's entries only.
func storeArchiveOf(t *testing.T, path string) archived {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	seen := archived{puts: map[string]int64{}}
	var entries []string
	err = ReadTar(file, func(entry TarRead) error {
		entries = append(entries, entry.Path)
		if entry.Path != "store.archive" {
			_, err := io.Copy(io.Discard, entry.Data)
			return err
		}
		var text bytes.Buffer
		if _, err := io.Copy(&text, entry.Data); err != nil {
			return err
		}
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
				seen.puts[part.Table]++
			}
		}
		return lines.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"manifest.json", "store.archive", "server.archive"}; !slices.Equal(entries, want) {
		t.Fatalf("the export holds %v, want %v", entries, want)
	}
	return seen
}

func TestPackRunningExportsBothStoresAtOnePointWhileTheyAreWritten(t *testing.T) {
	const preloaded, pads, exports = 1000, 3, 8
	dir := filepath.Join(t.TempDir(), "host")
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The plugins' store as the NodeHost has it: a store of its own on store.sqlite, written while the worker exports.
	pluginsStore := openStoreAt(t, filepath.Join(dir, "store.sqlite"))
	kitStore := openStoreAt(t, filepath.Join(dir, "kit.sqlite"))
	// Each store's seq before its preload: the preload is the next seq, each commit after it one more.
	pluginsTables := prepared(t, pluginsStore, "test", ticks, noise)
	pluginsTicks, pluginsNoise := pluginsTables[0], pluginsTables[1]
	kitTicks := prepared(t, kitStore, filesName, ticks)[0]
	// The plugins' seq is kept ahead of the kit's by a few commits (single rows of a key the noise writer does not use), so the archive's
	// seq, the larger of the two, is the plugins' whatever the writers do.
	for id := 1; id <= pads; id++ {
		if err := pluginsNoise.put(-id, -id); err != nil {
			t.Fatal(err)
		}
	}
	pluginsBase, kitBase := pluginsStore.LastSeq(), kitStore.LastSeq()
	if pluginsBase <= kitBase {
		t.Fatalf("the plugins' store is at seq %d, the kit's at %d: the test needs the plugins' ahead", pluginsBase, kitBase)
	}
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
		if err := PackRunning(context.Background(), dir, kitStore, archiveManifest, serverPart(t, filepath.Dir(dir)), out); err != nil {
			t.Fatalf("export %d: %v", n, err)
		}
		in := storeArchiveOf(t, out)
		// The plugins' part is a point of its store: exactly the rows its seq counts (a read past its snapshot would add some).
		pluginRows := in.puts[pluginTicksTable] + in.puts[pluginNoiseTable]
		if want := in.lastSeq - pluginsBase + preloaded - 1 + pads; pluginRows != want {
			t.Fatalf("export %d: the archive has %d rows of the plugins' at seq %d (base %d), want %d", n, pluginRows, in.lastSeq, pluginsBase, want)
		}
		inKit := in.puts[kitTicksTable]
		if inKit < preloaded {
			t.Fatalf("export %d: the archive has %d ticks of the kit's: its table is missing or short", n, inKit)
		}
		// And the two are one point: the plugins' ticks are the kit's, or one more.
		if ahead := in.puts[pluginTicksTable] - inKit; ahead != 0 && ahead != 1 {
			t.Fatalf("export %d: the archive has %d ticks of the plugins' and %d of the kit's: not one point", n, in.puts[pluginTicksTable], inKit)
		}
		if n == 0 {
			first = inKit
		}
		last = inKit
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
