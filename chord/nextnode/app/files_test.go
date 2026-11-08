package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/files.test.ts: `Files` and `FilesFolder` (slice 13 §4, decision "Files v2"), on a
// host with the files facet (the kit's server host is not ported: see files_support_test.go): spaces, paths and folders,
// versions and blob references, keys, metadata queries, live folder listings, chunked uploads, the caps and the summary
// per space. Copying a host's files is in files_copy_test.go.

func equalNames(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func TestSpacesKeepTheSamePathApartInTwoSpacesRefuseANameOutsideTheRule(t *testing.T) {
	f := filesOf(t)
	shared := f.put("a.txt", "shared")
	other := f.putIn("artifacts", "a.txt", "artifacts")
	if num(shared, "version") != 1 || num(other, "version") != 1 {
		t.Fatalf("versions = %v, %v", shared.Value("version"), other.Value("version"))
	}
	got := f.must("get", "space", "artifacts", "path", "a.txt").(*delta.JsonObject)
	if f.text(str(got, "id")) != "artifacts" {
		t.Fatal("the artifacts space holds other bytes")
	}
	for _, space := range []string{"", "Upper", "a/b", strings.Repeat("x", 65)} {
		_, err := f.putErr(space, "a.txt", []byte("x"))
		_ = wantCode(t, err, data.CodeInvalid)
	}
	f.close()
}

func TestSpacesRemoveASpaceItsFilesFoldersSummaryAndTheBlobsOnlyItUsed(t *testing.T) {
	f := filesOf(t)
	f.putIn("scratch", "docs/a.txt", "a")
	kept := f.put("b.txt", "b")
	f.must("copy", "id", str(kept, "id"), "space", "scratch", "path", "b.txt")
	if len(f.blobFiles()) != 2 {
		t.Fatalf("blobs = %v", f.blobFiles())
	}
	f.mustRaw("removeSpace", "scratch")
	equalNames(t, "scratch", f.names("", "scratch"))
	if f.summary("scratch") != nil {
		t.Fatal("the removed space still has a summary")
	}
	if len(f.blobFiles()) != 1 {
		t.Fatalf("blobs = %v", f.blobFiles())
	}
	if f.text(str(kept, "id")) != "b" {
		t.Fatal("the kept file changed")
	}
	f.close()
}

func TestPathsMakeTheFoldersOfAPathListFoldersFirstThenFilesEachByPath(t *testing.T) {
	f := filesOf(t)
	f.put("docs/a/report.md", "r")
	f.put("docs/b.md", "b")
	f.put("readme.md", "x")
	f.must("mkdir", "space", "shared", "path", "empty/inner")
	equalNames(t, "root", f.names(""), "docs/", "empty/", "readme.md")
	equalNames(t, "docs", f.names("docs"), "docs/a/", "docs/b.md")
	equalNames(t, "empty", f.names("empty"), "empty/inner/")
	if path := str(f.must("mkdir", "space", "shared", "path", "docs").(*delta.JsonObject), "path"); path != "docs" {
		t.Fatalf("mkdir docs = %q", path)
	}
	f.close()
}

func TestPathsPageAListingAcrossItsFoldersAndFilesWithCursors(t *testing.T) {
	f := filesOf(t)
	for _, name := range []string{"f1", "f2", "f3"} {
		f.must("mkdir", "space", "shared", "path", name)
	}
	for _, name := range []string{"a", "b", "c"} {
		f.put(name+".txt", name)
	}
	var seen []string
	var after any
	for {
		args := []any{"space", "shared", "folder", "", "limit", float64(2)}
		if after != nil {
			args = append(args, "after", after)
		}
		page := f.must("list", args...).(*delta.JsonObject)
		seen = append(seen, entryNames(page.Value("items"), false)...)
		next, ok := page.Get("next")
		if !ok {
			break
		}
		after = next
	}
	equalNames(t, "listing", seen, "f1", "f2", "f3", "a.txt", "b.txt", "c.txt")
	// A page the folders fill exactly continues only when files follow.
	folders := filesOf(t)
	folders.must("mkdir", "space", "shared", "path", "only")
	page := folders.must("list", "space", "shared", "folder", "", "limit", float64(1)).(*delta.JsonObject)
	if _, has := page.Get("next"); has || len(page.Value("items").([]any)) != 1 {
		t.Fatalf("page = %s", jsonOf(t, page))
	}
	entry := page.Value("items").([]any)[0].(*delta.JsonObject).Value("folder").(*delta.JsonObject)
	if str(entry, "space") != "shared" || str(entry, "path") != "only" {
		t.Fatalf("entry = %s", jsonOf(t, entry))
	}
	_, err := f.call("list", "space", "shared", "folder", "", "after", "nope")
	_ = wantCode(t, err, data.CodeInvalid)
	folders.close()
	f.close()
}

func TestPathsRefuseBadPathsAFileWhereAFolderIsAndAFolderWhereAFileIs(t *testing.T) {
	f := filesOf(t)
	for _, path := range []string{"", "/a", "a/", "a//b", "a/../b", ".", "a/./b", "a\x00b", strings.Repeat("x", 1025)} {
		_, err := f.putErr("shared", path, []byte("x"))
		_ = wantCode(t, err, data.CodeInvalid)
	}
	f.put("docs/a.txt", "a")
	_, err := f.putErr("shared", "docs", []byte("x"))
	if got := wantCode(t, err, data.CodeInvalid); jsonOf(t, got.Details) != `{"conflict":"docs"}` {
		t.Fatalf("details = %s", jsonOf(t, got.Details))
	}
	_, err = f.putErr("shared", "docs/a.txt/b", []byte("x"))
	if got := wantCode(t, err, data.CodeInvalid); jsonOf(t, got.Details) != `{"conflict":"docs/a.txt"}` {
		t.Fatalf("details = %s", jsonOf(t, got.Details))
	}
	_, err = f.call("mkdir", "space", "shared", "path", "docs/a.txt")
	_ = wantCode(t, err, data.CodeInvalid)
	_, err = f.call("begin", "space", "shared", "path", "docs", "mime", "x", "source", "t")
	_ = wantCode(t, err, data.CodeInvalid)
	f.close()
}

func TestPathsMoveAFileWithAllItsVersionsChangingOnlyTheIndex(t *testing.T) {
	f := filesOf(t)
	first := f.put("a.txt", "1")
	f.put("a.txt", "2")
	blobs := f.blobFiles()
	f.must("move", "space", "shared", "from", "a.txt", "to", "archive/b.txt")
	equalNames(t, "root", f.names(""), "archive/")
	equalNames(t, "archive", f.names("archive"), "archive/b.txt")
	versions := f.must("versions", "space", "shared", "path", "archive/b.txt").(*delta.JsonObject)
	var numbers []float64
	for _, item := range versions.Value("items").([]any) {
		numbers = append(numbers, num(item.(*delta.JsonObject), "version"))
	}
	if !reflect.DeepEqual(numbers, []float64{2, 1}) {
		t.Fatalf("versions = %v", numbers)
	}
	if f.text(str(first, "id")) != "1" {
		t.Fatal("version 1 changed")
	}
	if !reflect.DeepEqual(f.blobFiles(), blobs) {
		t.Fatalf("blobs = %v, want %v", f.blobFiles(), blobs)
	}
	f.close()
}

func TestPathsMoveAFolderWithEverythingUnderItRefuseATargetThatExistsOrLiesInside(t *testing.T) {
	f := filesOf(t)
	f.put("docs/a.txt", "a")
	f.put("docs/sub/b.txt", "b")
	f.put("docsx/c.txt", "c")
	f.put("taken.txt", "t")
	f.must("move", "space", "shared", "from", "docs", "to", "old/docs")
	equalNames(t, "root", f.names(""), "docsx/", "old/", "taken.txt")
	equalNames(t, "old/docs", f.names("old/docs"), "old/docs/sub/", "old/docs/a.txt")
	equalNames(t, "old/docs/sub", f.names("old/docs/sub"), "old/docs/sub/b.txt")
	equalNames(t, "docsx", f.names("docsx"), "docsx/c.txt")
	got := f.must("get", "space", "shared", "path", "old/docs/sub/b.txt").(*delta.JsonObject)
	if num(got, "version") != 1 {
		t.Fatalf("version = %v", got.Value("version"))
	}
	_, err := f.call("move", "space", "shared", "from", "old", "to", "old/inner")
	wantCode(t, err, data.CodeInvalid)
	_, err = f.call("move", "space", "shared", "from", "old", "to", "taken.txt")
	if e := wantCode(t, err, data.CodeInvalid); jsonOf(t, e.Details) != `{"conflict":"taken.txt"}` {
		t.Fatalf("details = %s", jsonOf(t, e.Details))
	}
	_, err = f.call("move", "space", "shared", "from", "missing", "to", "x")
	wantCode(t, err, data.CodeInvalid)
	f.close()
}

func TestPathsRemoveAFolderWithEverythingUnderItAndAFileWithEveryVersion(t *testing.T) {
	f := filesOf(t)
	f.put("docs/a.txt", "a")
	f.put("docs/sub/b.txt", "b")
	f.put("c.txt", "1")
	f.put("c.txt", "2")
	f.must("removePath", "space", "shared", "path", "docs")
	equalNames(t, "root", f.names(""), "c.txt")
	f.must("removePath", "space", "shared", "path", "c.txt")
	equalNames(t, "root", f.names(""))
	equalNames(t, "blobs", f.blobFiles())
	f.close()
}

func TestVersionsAddAVersionPerWriteThePathNamesTheNewestOlderOnesStayReadable(t *testing.T) {
	f := filesOf(t)
	v1 := f.put("a.txt", "one")
	v2 := f.put("a.txt", "two")
	if num(v1, "version") != 1 || num(v2, "version") != 2 {
		t.Fatalf("versions = %v, %v", v1.Value("version"), v2.Value("version"))
	}
	if got := f.must("get", "space", "shared", "path", "a.txt").(*delta.JsonObject); str(got, "id") != str(v2, "id") {
		t.Fatal("the path does not name the newest version")
	}
	if got := f.must("get", "space", "shared", "path", "a.txt", "version", float64(1)).(*delta.JsonObject); str(got, "id") != str(v1, "id") {
		t.Fatal("version 1 is not the first")
	}
	if got := f.must("get", "space", "shared", "path", "a.txt", "version", float64(3)); got != nil {
		t.Fatalf("version 3 = %v, want null", got)
	}
	if f.text(str(v1, "id")) != "one" {
		t.Fatal("version 1 changed")
	}
	listed := f.must("list", "space", "shared", "folder", "").(*delta.JsonObject).Value("items").([]any)
	want := `[{"kind":"file","file":` + jsonOf(t, v2) + `}]`
	if jsonOf(t, listed) != want {
		t.Fatalf("listed = %s, want %s", jsonOf(t, listed), want)
	}
	f.close()
}

func TestVersionsRemovingTheNewestMakesTheNextOlderOneTheNewest(t *testing.T) {
	f := filesOf(t)
	v1 := f.put("a.txt", "one")
	v2 := f.put("a.txt", "two")
	f.mustRaw("remove", str(v2, "id"))
	f.mustRaw("remove", str(v2, "id"))
	if got := f.must("get", "space", "shared", "path", "a.txt").(*delta.JsonObject); str(got, "id") != str(v1, "id") {
		t.Fatal("version 1 is not the newest again")
	}
	// Version 2's number is never used again: a link to it must not show other bytes.
	if got := f.put("a.txt", "three"); num(got, "version") != 3 {
		t.Fatalf("version = %v, want 3", got.Value("version"))
	}
	_, err := f.call("read", "id", str(v2, "id"), "offset", float64(0), "length", float64(1))
	wantCode(t, err, data.CodeInvalid)
	f.close()
}

func TestVersionsNeverUseANumberTwiceForAPath(t *testing.T) {
	f := filesOf(t)
	version := func(path, text string) float64 { return num(f.put(path, text), "version") }
	version("a.txt", "1")
	version("a.txt", "2")
	f.must("removePath", "space", "shared", "path", "a.txt")
	if got := version("a.txt", "3"); got != 3 {
		t.Fatalf("after removePath: %v, want 3", got)
	}
	f.mustRaw("removeSpace", "shared")
	if got := version("a.txt", "4"); got != 4 {
		t.Fatalf("after removeSpace: %v, want 4", got)
	}
	// Moved away: the path left keeps its mark; moved onto a removed path: the higher mark of the two.
	f.must("move", "space", "shared", "from", "a.txt", "to", "b.txt")
	if got := version("a.txt", "5"); got != 5 {
		t.Fatalf("after move away: %v, want 5", got)
	}
	f.put("docs/c.txt", "c1")
	f.must("removePath", "space", "shared", "path", "docs")
	f.put("old/c.txt", "o1")
	f.must("move", "space", "shared", "from", "old", "to", "docs")
	if got := version("docs/c.txt", "next"); got != 2 {
		t.Fatalf("onto a removed path: %v, want 2", got)
	}
	if got := version("b.txt", "b"); got != 5 {
		t.Fatalf("b.txt: %v, want 5", got)
	}
	f.close()
}

func TestVersionsCopiesShareABlobDeletedFromDiskOnceNoVersionReferencesIt(t *testing.T) {
	f := filesOf(t)
	original := f.put("a.txt", "shared bytes", "ref", "m1", "meta", delta.JsonObjectOf("n", float64(1)))
	copied := f.must("copy", "id", str(original, "id"), "space", "other", "path", "b/c.txt").(*delta.JsonObject)
	if str(copied, "space") != "other" || str(copied, "path") != "b/c.txt" || num(copied, "version") != 1 || str(copied, "ref") != "m1" ||
		jsonOf(t, copied.Value("meta")) != `{"n":1}` {
		t.Fatalf("copy = %s", jsonOf(t, copied))
	}
	if len(f.blobFiles()) != 1 {
		t.Fatalf("blobs = %v", f.blobFiles())
	}
	f.mustRaw("remove", str(original, "id"))
	if f.text(str(copied, "id")) != "shared bytes" || len(f.blobFiles()) != 1 {
		t.Fatalf("the copy lost its bytes: blobs = %v", f.blobFiles())
	}
	f.mustRaw("remove", str(copied, "id"))
	equalNames(t, "blobs", f.blobFiles())
	f.close()
}

func TestVersionsSweepBlobsAWriteLeftWithoutItsCommitAtTheNextStart(t *testing.T) {
	first := filesOf(t)
	kept := first.put("a.txt", "kept")
	blobs := first.blobDir()
	if err := os.WriteFile(filepath.Join(blobs, "stray"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	first.close()
	second := filesOf(t, fixtureOptions{Dir: first.dir})
	eventually(t, "the stray blob to be swept", func() bool {
		_, err := os.Stat(filepath.Join(blobs, "stray"))
		return os.IsNotExist(err)
	})
	if second.text(str(kept, "id")) != "kept" {
		t.Fatal("the kept blob changed")
	}
	second.close()
}

func TestKeysStoreAWriteWithAKeyOnceTheSecondAnswersTheFirstInItsSpaceOnly(t *testing.T) {
	f := filesOf(t)
	first := f.put("a.txt", "one", "key", "discord:1:0")
	again := f.put("b.txt", "two", "key", "discord:1:0")
	if jsonOf(t, again) != jsonOf(t, first) {
		t.Fatalf("again = %s, want %s", jsonOf(t, again), jsonOf(t, first))
	}
	if got := f.must("byKey", "space", "shared", "key", "discord:1:0"); jsonOf(t, got) != jsonOf(t, first) {
		t.Fatalf("byKey = %s", jsonOf(t, got))
	}
	if other := f.put("a.txt", "x", "key", "discord:1:0", "space", "other"); str(other, "space") != "other" {
		t.Fatalf("other = %s", jsonOf(t, other))
	}
	equalNames(t, "root", f.names(""), "a.txt")
	// Removing the version frees its key.
	f.mustRaw("remove", str(first, "id"))
	if got := f.must("byKey", "space", "shared", "key", "discord:1:0"); got != nil {
		t.Fatalf("byKey after remove = %v", got)
	}
	f.close()
}

func TestKeysBeginWithAKeyTheFileWrittenWithItOrTheUploadUnderWayWithIt(t *testing.T) {
	f := filesOf(t)
	write := []any{"space", "shared", "path", "big.bin", "mime", "x", "source", "test", "key", "k1"}
	begun := f.must("begin", write...).(*delta.JsonObject)
	if str(begun, "kind") != "upload" {
		t.Fatalf("begun = %s", jsonOf(t, begun))
	}
	if again := f.must("begin", write...); jsonOf(t, again) != jsonOf(t, begun) {
		t.Fatalf("begin again = %s, want %s", jsonOf(t, again), jsonOf(t, begun))
	}
	upload := str(begun, "upload")
	f.must("append", "upload", upload, "offset", float64(0), "bytes", []byte("abc"))
	file := f.mustRaw("finish", upload)
	want := `{"kind":"exists","file":` + jsonOf(t, file) + `}`
	if got := f.must("begin", write...); jsonOf(t, got) != want {
		t.Fatalf("begin after finish = %s, want %s", jsonOf(t, got), want)
	}
	f.close()
}

func TestMetadataQueriesKeepSourceRefKeyMimeAndMetaFindVersionsByIDRefAndKey(t *testing.T) {
	f := filesOf(t)
	a := f.put("a.png", "a", "mime", "image/png", "source", "guest:g1", "ref", "msg-1", "meta", delta.JsonObjectOf("w", float64(2)))
	b := f.put("b.png", "b", "source", "owner", "ref", "msg-1", "key", "k")
	f.put("c.png", "c", "ref", "msg-2")
	if got := jsonOf(t, a); !strings.HasPrefix(got, `{"id":"`+str(a, "id")+`","space":"shared","path":"a.png","version":1,"size":1,"mime":"image/png","source":"guest:g1","ref":"msg-1","created":`) ||
		!strings.HasSuffix(got, `,"meta":{"w":2}}`) {
		t.Fatalf("a = %s", got)
	}
	if got := f.mustRaw("file", str(a, "id")); jsonOf(t, got) != jsonOf(t, a) {
		t.Fatalf("file = %s", jsonOf(t, got))
	}
	if got := f.mustRaw("file", "missing"); got != nil {
		t.Fatalf("file(missing) = %v", got)
	}
	page := f.must("byRef", "space", "shared", "ref", "msg-1", "limit", float64(1)).(*delta.JsonObject)
	if jsonOf(t, page.Value("items")) != "["+jsonOf(t, a)+"]" {
		t.Fatalf("page 1 = %s", jsonOf(t, page))
	}
	next := f.must("byRef", "space", "shared", "ref", "msg-1", "after", page.Value("next")).(*delta.JsonObject)
	if jsonOf(t, next.Value("items")) != "["+jsonOf(t, b)+"]" {
		t.Fatalf("page 2 = %s", jsonOf(t, next))
	}
	if got := f.must("byKey", "space", "shared", "key", "k"); jsonOf(t, got) != jsonOf(t, b) {
		t.Fatalf("byKey = %s", jsonOf(t, got))
	}
	f.close()
}

func TestImportedTimesKeepAnImportsCreatedTimeTheSummarysNewestStayInCreatedOrder(t *testing.T) {
	f := filesOf(t)
	fresh := f.put("fresh.txt", "now")
	old := f.put("old.txt", "then", "created", float64(1000))
	if num(old, "created") != 1000 {
		t.Fatalf("created = %v", old.Value("created"))
	}
	if got := f.mustRaw("file", str(old, "id")).(*delta.JsonObject); num(got, "created") != 1000 {
		t.Fatalf("file created = %v", got.Value("created"))
	}
	begun := f.must("begin", "space", "shared", "path", "old.bin", "mime", "x", "source", "t", "created", float64(2000)).(*delta.JsonObject)
	upload := str(begun, "upload")
	f.must("append", "upload", upload, "offset", float64(0), "bytes", []byte("x"))
	if got := f.mustRaw("finish", upload).(*delta.JsonObject); num(got, "created") != 2000 {
		t.Fatalf("finished created = %v", got.Value("created"))
	}
	var paths []string
	for _, file := range f.summary("shared").Value("newest").([]any) {
		paths = append(paths, str(file.(*delta.JsonObject), "path"))
	}
	equalNames(t, "newest", paths, str(fresh, "path"), "old.bin", "old.txt")
	f.close()
}

func TestLiveFolderQueriesListAFolderLiveThroughWritesMovesAndRemovals(t *testing.T) {
	f := filesOf(t)
	f.put("docs/a.txt", "a")
	folder, err := f.host.Get(background, FilesFolder, delta.JsonObjectOf("space", "shared", "folder", "docs"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	paths := func() []string {
		value, err := folder.Member("listing").Value()
		if err != nil {
			t.Fatal(err)
		}
		return entryNames(value.(*delta.JsonObject).Value("entries"), false)
	}
	equalNames(t, "listing", paths(), "docs/a.txt")
	f.put("docs/b.txt", "b")
	equalNames(t, "listing", paths(), "docs/a.txt", "docs/b.txt")
	f.must("mkdir", "space", "shared", "path", "docs/sub")
	equalNames(t, "listing", paths(), "docs/sub", "docs/a.txt", "docs/b.txt")
	f.must("move", "space", "shared", "from", "docs/a.txt", "to", "elsewhere.txt")
	equalNames(t, "listing", paths(), "docs/sub", "docs/b.txt")
	f.must("removePath", "space", "shared", "path", "docs")
	if value, _ := folder.Member("listing").Value(); jsonOf(t, value) != `{"entries":[],"more":false}` {
		t.Fatalf("listing = %s", jsonOf(t, value))
	}
	// Another space's change leaves it as it is.
	f.putIn("other", "docs/c.txt", "c")
	equalNames(t, "listing", paths())
	folder.Release()
	f.close()
}

func TestLiveFolderQueriesShowTheFirst500EntriesAndWhetherMoreFollow(t *testing.T) {
	f := filesOf(t)
	for index := range 501 {
		f.must("mkdir", "space", "shared", "path", fmt.Sprintf("d%03d", index))
	}
	folder, err := f.host.Get(background, FilesFolder, delta.JsonObjectOf("space", "shared", "folder", ""), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	value, _ := folder.Member("listing").Value()
	listing := value.(*delta.JsonObject)
	if len(listing.Value("entries").([]any)) != 500 || listing.Value("more") != true {
		t.Fatalf("listing has %d entries, more = %v", len(listing.Value("entries").([]any)), listing.Value("more"))
	}
	folder.Release()
	f.close()
}

func beginUpload(t *testing.T, f *fixture, path string, extra ...any) string {
	t.Helper()
	args := append([]any{"space", "shared", "path", path, "mime", "x", "source", "test"}, extra...)
	begun := f.must("begin", args...).(*delta.JsonObject)
	if str(begun, "kind") != "upload" {
		t.Fatalf("begin = %s", jsonOf(t, begun))
	}
	return str(begun, "upload")
}

func TestChunkedUploadsWriteALargeFileInChunksBeginAppendAtTheStoredOffsetFinish(t *testing.T) {
	f := filesOf(t)
	upload := beginUpload(t, f, "big.bin")
	chunk := bytes.Repeat([]byte{7}, 1024*1024)
	for index := range 4 {
		f.must("append", "upload", upload, "offset", float64(index*len(chunk)), "bytes", chunk)
	}
	// A writer after a drop asks where to resume; an append elsewhere is refused with the stored offset.
	offset := f.mustRaw("uploaded", upload).(float64)
	if offset != float64(4*len(chunk)) {
		t.Fatalf("uploaded = %v", offset)
	}
	_, err := f.call("append", "upload", upload, "offset", float64(3*len(chunk)), "bytes", chunk)
	if got := wantCode(t, err, data.CodeInvalid); jsonOf(t, got.Details) != fmt.Sprintf(`{"offset":%d}`, int64(offset)) {
		t.Fatalf("details = %s", jsonOf(t, got.Details))
	}
	f.must("append", "upload", upload, "offset", offset, "bytes", chunk)
	equalNames(t, "root", f.names(""))
	file := f.mustRaw("finish", upload).(*delta.JsonObject)
	if str(file, "path") != "big.bin" || num(file, "size") != 5*1024*1024 || num(file, "version") != 1 {
		t.Fatalf("file = %s", jsonOf(t, file))
	}
	tail := f.must("read", "id", str(file, "id"), "offset", float64(5*1024*1024-2), "length", float64(10)).([]byte)
	if !bytes.Equal(tail, []byte{7, 7}) {
		t.Fatalf("tail = %v", tail)
	}
	_, err = f.call("append", "upload", upload, "offset", float64(0), "bytes", chunk)
	wantCode(t, err, data.CodeInvalid)
	_, err = f.callRaw("uploaded", upload)
	wantCode(t, err, data.CodeInvalid)
	f.close()
	// The routine sweeping expired uploads ends with the object, quietly.
	if errors := f.errors.all(); len(errors) != 0 {
		t.Fatalf("background errors = %v", errors)
	}
}

func TestChunkedUploadsWriteAppendsSentWithoutWaitingAtTheirOwnOffsetsByteForByte(t *testing.T) {
	f := filesOf(t)
	upload := beginUpload(t, f, "big.bin")
	size := 32 * 1024
	calls := make([]*pending, 250)
	for index := range calls {
		calls[index] = f.start("append", "upload", upload, "offset", float64(index*size), "bytes", bytes.Repeat([]byte{byte(index)}, size))
	}
	for _, call := range calls {
		if _, err := call.wait(); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.mustRaw("uploaded", upload); got != float64(250*size) {
		t.Fatalf("uploaded = %v", got)
	}
	id := str(f.mustRaw("finish", upload).(*delta.JsonObject), "id")
	for index := range 250 {
		piece := f.must("read", "id", id, "offset", float64(index*size), "length", float64(size)).([]byte)
		if !bytes.Equal(piece, bytes.Repeat([]byte{byte(index)}, size)) {
			t.Fatalf("piece %d differs", index)
		}
	}
	f.close()
}

func TestChunkedUploadsFinishAnUploadOnlyOnceTheAppendsSentBeforeItLanded(t *testing.T) {
	f := filesOf(t)
	upload := beginUpload(t, f, "x")
	size := 512 * 1024
	appends := make([]*pending, 8)
	for index := range appends {
		appends[index] = f.start("append", "upload", upload, "offset", float64(index*size), "bytes", bytes.Repeat([]byte{byte(index + 1)}, size))
	}
	file := f.mustRaw("finish", upload).(*delta.JsonObject)
	for _, call := range appends {
		if _, err := call.wait(); err != nil {
			t.Fatal(err)
		}
	}
	if num(file, "size") != float64(8*size) {
		t.Fatalf("size = %v", file.Value("size"))
	}
	if tail := f.must("read", "id", str(file, "id"), "offset", float64(8*size-1), "length", float64(10)).([]byte); !bytes.Equal(tail, []byte{8}) {
		t.Fatalf("tail = %v", tail)
	}
	f.close()
}

// openDescriptors is the number of open file descriptors of this process.
func openDescriptors(t *testing.T) (int, bool) {
	t.Helper()
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		if entries, err := os.ReadDir(dir); err == nil {
			return len(entries), true
		}
	}
	return 0, false
}

func TestChunkedUploadsCapTheUploadsUnderWayPerObjectPastItBeginIsRefusedBusyAnEndingOneFreesItsSlot(t *testing.T) {
	f := filesOf(t, fixtureOptions{Uploads: 3})
	before, counted := openDescriptors(t)
	var begun []string
	write := func(index int) []any {
		return []any{"space", "shared", "path", fmt.Sprintf("u%d", index), "mime", "x", "source", "test"}
	}
	for index := range 3 {
		result := f.must("begin", write(index)...).(*delta.JsonObject)
		if str(result, "kind") == "upload" {
			begun = append(begun, str(result, "upload"))
		}
	}
	_, err := f.call("begin", write(3)...)
	wantCode(t, err, data.CodeUnavailable, data.ReasonBusy)
	if after, ok := openDescriptors(t); counted && ok && after-before > 3 {
		t.Fatalf("descriptors grew by %d, want at most 3", after-before)
	}
	f.mustRaw("finish", begun[0])
	if str(f.must("begin", write(4)...).(*delta.JsonObject), "kind") != "upload" {
		t.Fatal("an ending upload did not free its slot")
	}
	f.close()
}

func TestChunkedUploadsCancelAnUploadItsBytesNoLongerCountItsSlotIsFree(t *testing.T) {
	f := filesOf(t, fixtureOptions{Caps: FilesCaps{MaxTotal: 100}, Uploads: 1})
	upload := beginUpload(t, f, "u")
	f.must("append", "upload", upload, "offset", float64(0), "bytes", make([]byte, 90))
	f.mustRaw("cancel", upload)
	_, err := f.callRaw("uploaded", upload)
	wantCode(t, err, data.CodeInvalid)
	if _, err := f.putErr("shared", "p", make([]byte, 90)); err != nil {
		t.Fatal(err)
	}
	beginUpload(t, f, "v")
	f.close()
}

func TestChunkedUploadsDropTheUploadsAPreviousRunLeft(t *testing.T) {
	first := filesOf(t)
	first.must("begin", "space", "shared", "path", "left", "mime", "x", "source", "test")
	first.close()
	uploads := filepath.Join(first.dir, "files", ".uploads")
	entries, err := os.ReadDir(uploads)
	if err != nil || len(entries) != 1 {
		t.Fatalf("uploads after the first run = %v, %v", entries, err)
	}
	left := entries[0].Name()
	if err := os.WriteFile(filepath.Join(uploads, "stray"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filesOf(t, fixtureOptions{Dir: first.dir})
	upload := beginUpload(t, second, "new")
	// The routine dropping them runs once the object is live; this run's upload stays.
	eventually(t, "the earlier run's uploads to be dropped", func() bool {
		for _, name := range names(t, uploads) {
			if name == left || name == "stray" {
				return false
			}
		}
		return true
	})
	if got := names(t, uploads); len(got) != 1 {
		t.Fatalf("uploads = %v, want this run's only", got)
	}
	second.must("append", "upload", upload, "offset", float64(0), "bytes", []byte("kept"))
	if second.text(str(second.mustRaw("finish", upload).(*delta.JsonObject), "id")) != "kept" {
		t.Fatal("the upload lost its bytes")
	}
	second.close()
}

func TestCapsRefuseAFileOverThePerFileCapAndWritesOverTheTotalACopyAddsNoBytes(t *testing.T) {
	f := filesOf(t, fixtureOptions{Caps: FilesCaps{MaxFile: 1000, MaxTotal: 1500}})
	putSized := func(path string, size int) (delta.JsonValue, error) {
		return f.putErr("shared", path, make([]byte, size))
	}
	_, err := putSized("a", 1001)
	wantCode(t, err, data.CodeTooLarge)
	big, err := putSized("a", 1000)
	if err != nil {
		t.Fatal(err)
	}
	_, err = putSized("b", 501)
	wantCode(t, err, data.CodeTooLarge)
	f.must("copy", "id", str(big.(*delta.JsonObject), "id"), "space", "shared", "path", "a-copy")
	// An upload under way counts against the total.
	upload := beginUpload(t, f, "u")
	f.must("append", "upload", upload, "offset", float64(0), "bytes", make([]byte, 400))
	_, err = putSized("c", 101)
	wantCode(t, err, data.CodeTooLarge)
	if _, err := putSized("c", 100); err != nil {
		t.Fatal(err)
	}
	_, err = f.call("append", "upload", upload, "offset", float64(400), "bytes", make([]byte, 1))
	wantCode(t, err, data.CodeTooLarge)
	// Removing frees room.
	f.must("removePath", "space", "shared", "path", "c")
	if _, err := putSized("d", 100); err != nil {
		t.Fatal(err)
	}
	f.close()
}

func TestCapsRefuseAPutAndAReadOfMoreThan4MiB(t *testing.T) {
	f := filesOf(t)
	limit := 4 * 1024 * 1024
	id := str(f.put("f", "0123456789"), "id")
	_, err := f.call("read", "id", id, "offset", float64(0), "length", float64(limit+1))
	wantCode(t, err, data.CodeTooLarge)
	_, err = f.putErr("shared", "g", make([]byte, limit+1))
	wantCode(t, err, data.CodeTooLarge)
	f.close()
}

func TestSummaryCountsASpacesFilesVersionsAndBytesAndKeepsItsNewestVersions(t *testing.T) {
	f := filesOf(t)
	a1 := f.put("docs/a.txt", "1")
	a2 := f.put("docs/a.txt", "22")
	b := f.put("b.txt", "333")
	want := `{"files":2,"versions":3,"bytes":6,"newest":[` + jsonOf(t, b) + `,` + jsonOf(t, a2) + `,` + jsonOf(t, a1) + `]}`
	if got := jsonOf(t, f.summary("shared")); got != want {
		t.Fatalf("summary = %s, want %s", got, want)
	}
	f.must("move", "space", "shared", "from", "docs", "to", "old")
	var paths []string
	for _, file := range f.summary("shared").Value("newest").([]any) {
		paths = append(paths, str(file.(*delta.JsonObject), "path"))
	}
	equalNames(t, "newest after move", paths, "b.txt", "old/a.txt", "old/a.txt")
	f.mustRaw("remove", str(b, "id"))
	moved := f.summary("shared")
	if num(moved, "files") != 1 || num(moved, "versions") != 2 || num(moved, "bytes") != 3 {
		t.Fatalf("summary = %s", jsonOf(t, moved))
	}
	f.must("removePath", "space", "shared", "path", "old")
	if got := jsonOf(t, f.summary("shared")); got != `{"files":0,"versions":0,"bytes":0,"newest":[]}` {
		t.Fatalf("summary = %s", got)
	}
	f.close()
}

func TestSummaryKeepsTheNewest20AndStaysAcrossARestart(t *testing.T) {
	first := filesOf(t)
	for index := range 25 {
		first.put(fmt.Sprintf("f%d", index), "x")
	}
	summary := jsonOf(t, first.summary("shared"))
	newest := first.summary("shared").Value("newest").([]any)
	if len(newest) != 20 || str(newest[0].(*delta.JsonObject), "path") != "f24" {
		t.Fatalf("newest = %d entries, first %v", len(newest), newest[0])
	}
	first.close()
	second := filesOf(t, fixtureOptions{Dir: first.dir})
	if got := jsonOf(t, second.summary("shared")); got != summary {
		t.Fatalf("summary after restart = %s, want %s", got, summary)
	}
	second.close()
}
