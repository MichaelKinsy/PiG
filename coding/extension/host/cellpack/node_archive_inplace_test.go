package cellpack

import (
	"io"
	"io/fs"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

// The embedded archive of a Node cell is read where it lies: positioned reads of the Binary's read-only data. A start copies none
// of it to the heap (the digest, the comparison with the extracted entry and an extraction all stream from it), so the memory a
// start allocates does not grow with the archive.

// embed.FS, which holds the Binary's cells, serves a file as an io.ReaderAt: the start's reads of the archive depend on it.
func TestEmbeddedFilesAreReadInPlace(t *testing.T) {
	for name, fsys := range map[string]fs.FS{"embed.FS": cells, "fstest.MapFS": fstest.MapFS{"cells/manifest.json": {Data: []byte(stockManifest)}}} {
		file, err := fsys.Open("cells/manifest.json")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := file.(io.ReaderAt); !ok {
			t.Errorf("%s: an opened file is a %T, which is not an io.ReaderAt", name, file)
		}
		_ = file.Close()
	}
}

func TestNodeCellStartAllocatesLessThanTheArchive(t *testing.T) {
	data, digest := nodeArchive(t, nodeBenchEntries(200, 30<<10)...)
	m := nodeManifest(digest)
	fsys := fstest.MapFS{"cells/" + m.Cells[0].Binary: {Data: data}}
	dest := t.TempDir()
	if err := extract(fsys, m, dest, io.Discard); err != nil {
		t.Fatalf("first start: %v", err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := extract(fsys, m, dest, io.Discard); err != nil {
		t.Fatalf("warm start: %v", err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > uint64(len(data))/4 {
		t.Errorf("a warm start of a %d byte archive allocated %d bytes: it copies the archive", len(data), allocated)
	}
}

// onlyRead hides every method of a file but the ones fs.File needs, as a file system whose files cannot be read at an offset does.
type onlyRead struct{ file fs.File }

func (o onlyRead) Stat() (fs.FileInfo, error) { return o.file.Stat() }
func (o onlyRead) Read(p []byte) (int, error) { return o.file.Read(p) }
func (o onlyRead) Close() error               { return o.file.Close() }

type onlyReadFS struct{ fstest.MapFS }

func (o onlyReadFS) Open(name string) (fs.File, error) {
	file, err := o.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return onlyRead{file}, nil
}

func TestNodeCellStartRefusesAnArchiveItCannotReadInPlace(t *testing.T) {
	data, digest := nodeArchive(t, nodeFixtureEntries(false)...)
	m := nodeManifest(digest)
	fsys := onlyReadFS{fstest.MapFS{"cells/" + m.Cells[0].Binary: {Data: data}}}
	err := extract(fsys, m, t.TempDir(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cannot be read in place") {
		t.Fatalf("extract = %v, want an error that the archive cannot be read in place", err)
	}
}
