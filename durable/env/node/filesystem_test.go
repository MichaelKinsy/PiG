package node

// Ported from packages/durable/test/env-node.test.ts at v1.0.0 (the filesystem
// and text line reader describes; shell cases are in shell_test.go). Each test
// name is the upstream case title.

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func fileError(t *testing.T, err error) *durableenv.FileError {
	t.Helper()
	var fileErr *durableenv.FileError
	if !errors.As(err, &fileErr) {
		t.Fatalf("error %v (%T) is not a FileError", err, err)
	}
	return fileErr
}

// expectFileError asserts that err is a FileError with code and, when path is
// not empty, that path.
func expectFileError(t *testing.T, err error, code durableenv.FileErrorCode, path string) {
	t.Helper()
	fileErr := fileError(t, err)
	if fileErr.Code != code {
		t.Fatalf("code = %q (%v), want %q", fileErr.Code, fileErr, code)
	}
	if path != "" && fileErr.Path != path {
		t.Fatalf("path = %q, want %q", fileErr.Path, path)
	}
}

func TestFilesystemReadsWritesListsAndRemovesFilesAndDirectories(t *testing.T) {
	env, root := newTestEnv(t)
	if got := must(env.AbsolutePath(background, "nested/child")); got != filepath.Join(root, "nested/child") {
		t.Fatalf("absolute = %q", got)
	}
	if got := must(env.JoinPath(background, []string{root, "nested", "child"})); got != filepath.Join(root, "nested", "child") {
		t.Fatalf("join = %q", got)
	}
	mustDo(t, env.CreateDir(background, "nested/child", nil))
	mustDo(t, env.WriteFile(background, "nested/child/file.txt", []byte("hel")))
	mustDo(t, env.AppendFile(background, "nested/child/file.txt", []byte("lo")))
	if got := must(env.ReadTextFile(background, "nested/child/file.txt")); got != "hello" {
		t.Fatalf("read = %q", got)
	}
	if got := must(env.ReadTextLines(background, "nested/child/file.txt", &durableenv.ReadTextLinesOptions{MaxLines: new(1)})); !slices.Equal(got, []string{"hello"}) {
		t.Fatalf("lines = %q", got)
	}
	if got := must(env.ReadBinaryFile(background, "nested/child/file.txt")); string(got) != "hello" {
		t.Fatalf("binary = %q", got)
	}
	entries := must(env.ListDir(background, "nested/child"))
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	want := durableenv.FileInfo{Name: "file.txt", Path: filepath.Join(root, "nested/child/file.txt"), Kind: durableenv.FileKindFile, Size: 5}
	got := entries[0]
	if got.Name != want.Name || got.Path != want.Path || got.Kind != want.Kind || got.Size != want.Size || got.MtimeMs <= 0 {
		t.Fatalf("entry = %+v, want %+v with a modification time", got, want)
	}
	if !must(env.Exists(background, "nested/child/file.txt")) {
		t.Fatal("file missing")
	}
	mustDo(t, env.Remove(background, "nested/child/file.txt", nil))
	if must(env.Exists(background, "nested/child/file.txt")) {
		t.Fatal("file still exists")
	}
}

func TestFilesystemExpandsHomeRelativePathsAndFileURLs(t *testing.T) {
	env, root := newTestEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := must(env.AbsolutePath(background, "~/pi-node-env-test")); got != filepath.Join(home, "pi-node-env-test") {
		t.Fatalf("home = %q", got)
	}
	filePath := filepath.Join(root, "file with spaces.txt")
	urlPath := filePath
	if runtime.GOOS == "windows" {
		// Node's pathToFileURL: file:///C:/dir/file%20with%20spaces.txt.
		urlPath = "/" + filepath.ToSlash(filePath)
	}
	fileURL := (&url.URL{Scheme: "file", Path: urlPath}).String()
	if got := must(env.AbsolutePath(background, fileURL)); got != filePath {
		t.Fatalf("file URL %q = %q", fileURL, got)
	}
}

func TestFilesystemReturnsFileInfoForFilesDirectoriesAndSymlinksWithoutFollowingSymlinks(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.CreateDir(background, "dir", &durableenv.CreateDirOptions{Recursive: new(true)}))
	mustDo(t, env.WriteFile(background, "dir/file.txt", []byte("hello")))
	testenv.Symlink(t, filepath.Join(root, "dir/file.txt"), filepath.Join(root, "file-link"))
	testenv.RequireDirectoryLink(t, filepath.Join(root, "dir"), filepath.Join(root, "dir-link"))

	check := func(path, name string, kind durableenv.FileKind, size int64) {
		t.Helper()
		info := must(env.FileInfo(background, path))
		if info.Name != name || info.Path != filepath.Join(root, path) || info.Kind != kind || (size >= 0 && info.Size != size) {
			t.Fatalf("FileInfo(%q) = %+v", path, info)
		}
	}
	check("dir", "dir", durableenv.FileKindDirectory, -1)
	check("dir/file.txt", "file.txt", durableenv.FileKindFile, 5)
	check("file-link", "file-link", durableenv.FileKindSymlink, -1)
	check("dir-link", "dir-link", durableenv.FileKindSymlink, -1)
	want, err := filepath.EvalSymlinks(filepath.Join(root, "dir/file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := must(env.CanonicalPath(background, "file-link")); got != want {
		t.Fatalf("canonical = %q, want %q", got, want)
	}
}

func TestFilesystemListsSymlinksAsSymlinks(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "target.txt", []byte("hello")))
	testenv.Symlink(t, filepath.Join(root, "target.txt"), filepath.Join(root, "link.txt"))
	type entry struct {
		Name string
		Kind durableenv.FileKind
	}
	var got []entry
	for _, info := range must(env.ListDir(background, ".")) {
		got = append(got, entry{info.Name, info.Kind})
	}
	slices.SortFunc(got, func(a, b entry) int { return strings.Compare(a.Name, b.Name) })
	want := []entry{{"link.txt", durableenv.FileKindSymlink}, {"target.txt", durableenv.FileKindFile}}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func TestFilesystemStopsReadingTextLinesAtTheRequestedLimit(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("one\ntwo\nthree")))
	if got := must(env.ReadTextLines(background, "file.txt", &durableenv.ReadTextLinesOptions{MaxLines: new(1)})); !slices.Equal(got, []string{"one"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestFilesystemReturnsFileErrorForMissingPathsAndKeepsExistsFalseForMissingPaths(t *testing.T) {
	env, root := newTestEnv(t)
	_, err := env.FileInfo(background, "missing.txt")
	expectFileError(t, err, durableenv.FileErrorNotFound, filepath.Join(root, "missing.txt"))
	if must(env.Exists(background, "missing.txt")) {
		t.Fatal("missing path exists")
	}
}

func TestFilesystemReturnsFileErrorForListingNonDirectories(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("hello")))
	_, err := env.ListDir(background, "file.txt")
	expectFileError(t, err, durableenv.FileErrorNotDirectory, "")
}

func TestFilesystemAppendsToNewFilesAndCreatesParentDirectories(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.AppendFile(background, "new/nested/file.txt", []byte("a")))
	mustDo(t, env.AppendFile(background, "new/nested/file.txt", []byte("b")))
	if got := must(env.ReadTextFile(background, "new/nested/file.txt")); got != "ab" {
		t.Fatalf("read = %q", got)
	}
}

func TestFilesystemAtomicallyRenamesAFileAndReplacesTheDestination(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "source.txt", []byte("new")))
	mustDo(t, env.WriteFile(background, "destination.txt", []byte("old")))
	mustDo(t, env.RenameFile(background, "source.txt", "destination.txt"))
	if must(env.Exists(background, "source.txt")) {
		t.Fatal("source still exists")
	}
	if got := must(env.ReadTextFile(background, "destination.txt")); got != "new" {
		t.Fatalf("destination = %q", got)
	}
}

func TestFilesystemReportsTheSourcePathWhenRenameFailsBecauseTheSourceIsMissing(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "destination.txt", []byte("unchanged")))
	err := env.RenameFile(background, "missing-source.txt", "destination.txt")
	expectFileError(t, err, durableenv.FileErrorNotFound, filepath.Join(root, "missing-source.txt"))
	if got := must(env.ReadTextFile(background, "destination.txt")); got != "unchanged" {
		t.Fatalf("destination = %q", got)
	}
}

func TestFilesystemCreatesTemporaryDirectoriesAndFiles(t *testing.T) {
	env, _ := newTestEnv(t)
	tempDir := must(env.CreateTempDir(background, new("node-env-test-")))
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatal(err)
	}
	tempFile := must(env.CreateTempFile(background, &durableenv.CreateTempFileOptions{Prefix: "prefix-", Suffix: ".txt"}))
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(tempFile)) })
	if _, err := os.Stat(tempFile); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(tempFile, ".txt") || !strings.HasPrefix(filepath.Base(tempFile), "prefix-") {
		t.Fatalf("temp file = %q", tempFile)
	}
	if got := must(env.ReadTextFile(background, tempFile)); got != "" {
		t.Fatalf("temp file content = %q", got)
	}
}

func TestFilesystemHonorsCreateDirRecursiveFalseAndRemoveRecursiveForceOptions(t *testing.T) {
	env, _ := newTestEnv(t)
	expectFileError(t, env.CreateDir(background, "missing/child", &durableenv.CreateDirOptions{Recursive: new(false)}), durableenv.FileErrorNotFound, "")

	mustDo(t, env.WriteFile(background, "dir/child/file.txt", []byte("hello")))
	if err := env.Remove(background, "dir", &durableenv.RemoveOptions{Recursive: false}); err == nil {
		t.Fatal("removing a directory without recursive succeeded")
	}
	mustDo(t, env.Remove(background, "dir", &durableenv.RemoveOptions{Recursive: true}))
	if must(env.Exists(background, "dir")) {
		t.Fatal("dir still exists")
	}

	if err := env.Remove(background, "missing", &durableenv.RemoveOptions{Force: false}); err == nil {
		t.Fatal("removing a missing path without force succeeded")
	}
	mustDo(t, env.Remove(background, "missing", &durableenv.RemoveOptions{Force: true}))
}

func TestFilesystemReturnsAbortedResultsWithoutSideEffectsForPreAbortedFileOperations(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("hello")))
	ctx := abortedContext()
	failures := map[string]error{}
	record := func(name string, err error) { failures[name] = err }
	_, err := env.ReadTextFile(ctx, "file.txt")
	record("readTextFile", err)
	_, err = env.ReadTextLines(ctx, "file.txt", nil)
	record("readTextLines", err)
	_, err = env.ReadBinaryFile(ctx, "file.txt")
	record("readBinaryFile", err)
	_, err = env.OpenTextLineReader(ctx, "file.txt")
	record("openTextLineReader", err)
	record("writeFile", env.WriteFile(ctx, "other.txt", []byte("hello")))
	record("appendFile", env.AppendFile(ctx, "file.txt", []byte(" world")))
	record("truncateFile", env.TruncateFile(ctx, "file.txt", 1))
	record("flushFile", env.FlushFile(ctx, "file.txt"))
	record("renameFile", env.RenameFile(ctx, "file.txt", "renamed.txt"))
	_, err = env.FileInfo(ctx, "file.txt")
	record("fileInfo", err)
	_, err = env.ListDir(ctx, ".")
	record("listDir", err)
	_, err = env.CanonicalPath(ctx, "file.txt")
	record("canonicalPath", err)
	_, err = env.Exists(ctx, "file.txt")
	record("exists", err)
	record("createDir", env.CreateDir(ctx, "dir", nil))
	record("remove", env.Remove(ctx, "file.txt", nil))
	_, err = env.CreateTempDir(ctx, nil)
	record("createTempDir", err)
	_, err = env.CreateTempFile(ctx, nil)
	record("createTempFile", err)
	if len(failures) != 17 {
		t.Fatalf("recorded %d operations, want 17", len(failures))
	}
	for name, err := range failures {
		if err == nil || fileError(t, err).Code != durableenv.FileErrorAborted {
			t.Errorf("%s = %v, want an aborted FileError", name, err)
		}
	}
	if got := must(env.ReadTextFile(background, "file.txt")); got != "hello" {
		t.Fatalf("file = %q", got)
	}
	var names []string
	for _, info := range must(env.ListDir(background, ".")) {
		names = append(names, info.Name)
	}
	if !slices.Equal(names, []string{"file.txt"}) {
		t.Fatalf("directory = %v", names)
	}
}

func TestFilesystemTruncatesAndExtendsFilesToExactByteSizes(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.bin", []byte{0x61, 0xc3, 0xa9, 0x0a, 0x62, 0x0a}))

	// Truncation is byte-exact even when the boundary splits a UTF-8 sequence.
	mustDo(t, env.TruncateFile(background, "file.bin", 2))
	if got := must(env.ReadBinaryFile(background, "file.bin")); !bytes.Equal(got, []byte{0x61, 0xc3}) {
		t.Fatalf("truncated = %x", got)
	}
	mustDo(t, env.TruncateFile(background, "file.bin", 4))
	if got := must(env.ReadBinaryFile(background, "file.bin")); !bytes.Equal(got, []byte{0x61, 0xc3, 0, 0}) {
		t.Fatalf("extended = %x", got)
	}
	mustDo(t, env.TruncateFile(background, "file.bin", 0))
	if got := must(env.FileInfo(background, "file.bin")).Size; got != 0 {
		t.Fatalf("size = %d", got)
	}
}

// The upstream sizes 1.5, NaN and Infinity cannot be passed as an int64; the
// type rejects them.
func TestFilesystemRejectsInvalidTruncationSizesAndNeverCreatesMissingFiles(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("hello")))
	for _, size := range []int64{-1, maxSafeInteger + 1} {
		expectFileError(t, env.TruncateFile(background, "file.txt", size), durableenv.FileErrorInvalid, filepath.Join(root, "file.txt"))
	}
	if got := must(env.ReadTextFile(background, "file.txt")); got != "hello" {
		t.Fatalf("file = %q", got)
	}
	expectFileError(t, env.TruncateFile(background, "missing.txt", 0), durableenv.FileErrorNotFound, filepath.Join(root, "missing.txt"))
	if must(env.Exists(background, "missing.txt")) {
		t.Fatal("truncate created a missing file")
	}
}

func TestFilesystemFlushesExistingFilesWithoutChangingContentAndReportsMissingPaths(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("durable")))
	mustDo(t, env.FlushFile(background, "file.txt"))
	if got := must(env.ReadTextFile(background, "file.txt")); got != "durable" {
		t.Fatalf("file = %q", got)
	}
	expectFileError(t, env.FlushFile(background, "missing.txt"), durableenv.FileErrorNotFound, filepath.Join(root, "missing.txt"))
	if must(env.Exists(background, "missing.txt")) {
		t.Fatal("flush created a missing file")
	}
	mustDo(t, env.CreateDir(background, "dir", nil))
	// Windows cannot open a directory for writing; it reports permission denied.
	want := durableenv.FileErrorIsDirectory
	if runtime.GOOS == "windows" {
		want = durableenv.FileErrorPermissionDenied
	}
	expectFileError(t, env.FlushFile(background, "dir"), want, "")
}

func TestFilesystemSyncsThroughAnOpenedHandleReturnsSyncFailuresAndAlwaysClosesTheHandle(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("durable")))
	original := syncFile
	t.Cleanup(func() { syncFile = original })
	var synced []*os.File
	syncFile = func(file *os.File) error {
		synced = append(synced, file)
		return original(file)
	}
	mustDo(t, env.FlushFile(background, "file.txt"))
	if len(synced) != 1 {
		t.Fatalf("sync called %d times, want 1", len(synced))
	}
	if _, err := synced[0].Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("handle after flush: %v, want closed", err)
	}

	syncFile = func(file *os.File) error {
		synced = append(synced, file)
		return syscall.EIO
	}
	err := env.FlushFile(background, "file.txt")
	fileErr := fileError(t, err)
	if fileErr.Code != durableenv.FileErrorUnknown || fileErr.Path != filepath.Join(root, "file.txt") || fileErr.Message != "EIO: i/o error, fsync" {
		t.Fatalf("flush failure = %+v", fileErr)
	}
	if len(synced) != 2 {
		t.Fatalf("sync called %d times, want 2", len(synced))
	}
	if _, err := synced[1].Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("handle after failed flush: %v, want closed", err)
	}
}

func TestFilesystemCleanupIsBestEffort(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.Cleanup(background))
}

func readAllLines(t *testing.T, reader durableenv.TextLineReader) []durableenv.TextLine {
	t.Helper()
	var lines []durableenv.TextLine
	for {
		line := must(reader.ReadLine(background))
		if line == nil {
			return lines
		}
		lines = append(lines, *line)
	}
}

func TestTextLineReaderReportsWhetherEachLineWasNewlineTerminatedAndPreservesCarriageReturns(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "lines.txt", []byte("one\r\n\ntwo\npartial")))
	reader := must(env.OpenTextLineReader(background, "lines.txt"))
	defer func() { _ = reader.Close(background) }()
	want := []durableenv.TextLine{{Text: "one\r", Terminated: true}, {Text: "", Terminated: true}, {Text: "two", Terminated: true}, {Text: "partial", Terminated: false}}
	if got := readAllLines(t, reader); !slices.Equal(got, want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
	if line := must(reader.ReadLine(background)); line != nil {
		t.Fatalf("line after the end = %+v", line)
	}
}

func TestTextLineReaderReturnsNoLinesForAnEmptyFileAndOneTerminatedEmptyLineForALoneNewline(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "empty.txt", ""))
	mustDo(t, env.WriteFile(background, "newline.txt", []byte("\n")))

	empty := must(env.OpenTextLineReader(background, "empty.txt"))
	if line := must(empty.ReadLine(background)); line != nil {
		t.Fatalf("empty file line = %+v", line)
	}
	mustDo(t, empty.Close(background))

	newline := must(env.OpenTextLineReader(background, "newline.txt"))
	if line := must(newline.ReadLine(background)); line == nil || *line != (durableenv.TextLine{Text: "", Terminated: true}) {
		t.Fatalf("lone newline line = %+v", line)
	}
	if line := must(newline.ReadLine(background)); line != nil {
		t.Fatalf("line after the lone newline = %+v", line)
	}
	mustDo(t, newline.Close(background))
}

func TestTextLineReaderDecodesMultiByteCharactersSplitAcrossReadChunksAndLinesLongerThanOneChunk(t *testing.T) {
	env, _ := newTestEnv(t)
	// The reader uses 64 KiB chunks; place a four-byte character across the first boundary.
	first := strings.Repeat("a", 64*1024-2) + "😀tail"
	second := strings.Repeat("é", 100_000)
	mustDo(t, env.WriteFile(background, "large.txt", []byte(first+"\n"+second)))
	reader := must(env.OpenTextLineReader(background, "large.txt"))
	defer func() { _ = reader.Close(background) }()
	if line := must(reader.ReadLine(background)); line == nil || *line != (durableenv.TextLine{Text: first, Terminated: true}) {
		t.Fatal("first line differs")
	}
	if line := must(reader.ReadLine(background)); line == nil || *line != (durableenv.TextLine{Text: second, Terminated: false}) {
		t.Fatal("second line differs")
	}
	if line := must(reader.ReadLine(background)); line != nil {
		t.Fatalf("line after the end = %+v", line)
	}
}

func TestTextLineReaderRejectsAReadCancelledWhilePendingWithoutConsumingItsBytes(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "lines.txt", []byte("one\ntwo\n")))
	original := readFileAt
	t.Cleanup(func() { readFileAt = original })
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	readFileAt = func(file *os.File, chunk []byte, offset int64) (int, error) {
		once.Do(func() {
			close(started)
			<-release
		})
		return original(file, chunk, offset)
	}

	reader := must(env.OpenTextLineReader(background, "lines.txt"))
	defer func() { _ = reader.Close(background) }()
	ctx, cancel := cancellable()
	pending := make(chan error, 1)
	go func() {
		_, err := reader.ReadLine(ctx)
		pending <- err
	}()
	<-started
	cancel()
	close(release)
	expectFileError(t, <-pending, durableenv.FileErrorAborted, "")
	if line := must(reader.ReadLine(background)); line == nil || *line != (durableenv.TextLine{Text: "one", Terminated: true}) {
		t.Fatalf("first line = %+v", line)
	}
	if line := must(reader.ReadLine(background)); line == nil || *line != (durableenv.TextLine{Text: "two", Terminated: true}) {
		t.Fatalf("second line = %+v", line)
	}
	if line := must(reader.ReadLine(background)); line != nil {
		t.Fatalf("line after the end = %+v", line)
	}
}

func TestTextLineReaderRejectsReadsAfterCloseAndClosesIdempotently(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "lines.txt", []byte("one\n")))
	reader := must(env.OpenTextLineReader(background, "lines.txt"))
	mustDo(t, reader.Close(background))
	mustDo(t, reader.Close(background))
	_, err := reader.ReadLine(background)
	expectFileError(t, err, durableenv.FileErrorInvalid, filepath.Join(root, "lines.txt"))
}

func TestTextLineReaderReportsMissingFilesWhenOpeningAReader(t *testing.T) {
	env, root := newTestEnv(t)
	_, err := env.OpenTextLineReader(background, "missing.txt")
	expectFileError(t, err, durableenv.FileErrorNotFound, filepath.Join(root, "missing.txt"))
}

// Not an upstream case: the reader decodes with a TextDecoder, which drops a
// byte order mark at the start of the file and nowhere else.
func TestTextLineReaderDropsAByteOrderMarkAtTheStartOfTheFileOnly(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "bom.txt", []byte("\xef\xbb\xbfone\n\xef\xbb\xbftwo")))
	reader := must(env.OpenTextLineReader(background, "bom.txt"))
	defer func() { _ = reader.Close(background) }()
	want := []durableenv.TextLine{{Text: "one", Terminated: true}, {Text: "\ufefftwo", Terminated: false}}
	if got := readAllLines(t, reader); !slices.Equal(got, want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
	if got := must(env.ReadTextFile(background, "bom.txt")); !strings.HasPrefix(got, "\ufeff") {
		t.Fatalf("ReadTextFile dropped the byte order mark: %q", got)
	}
}
