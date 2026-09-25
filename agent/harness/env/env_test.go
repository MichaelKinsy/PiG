package env

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var background = context.Background()

func TestNodeEnvReadsWritesListsAndRemovesFilesAndDirectories(t *testing.T) {
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
	if got := must(env.ReadTextLines(background, "nested/child/file.txt", &harness.ReadTextLinesOptions{MaxLines: new(1)})); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("lines = %q", got)
	}
	if got := must(env.ReadBinaryFile(background, "nested/child/file.txt")); string(got) != "hello" {
		t.Fatalf("binary = %q", got)
	}
	entries := must(env.ListDir(background, "nested/child"))
	if len(entries) != 1 || entries[0].Name != "file.txt" || entries[0].Path != filepath.Join(root, "nested/child/file.txt") || entries[0].Kind != harness.FileKindFile || entries[0].Size != 5 || entries[0].MtimeMs <= 0 {
		t.Fatalf("entries = %+v", entries)
	}
	if !must(env.Exists(background, "nested/child/file.txt")) {
		t.Fatal("file missing")
	}
	mustDo(t, env.Remove(background, "nested/child/file.txt", nil))
	if must(env.Exists(background, "nested/child/file.txt")) {
		t.Fatal("file still exists")
	}
}

func TestNodeEnvExpandsHomeRelativePathsAndFileURLs(t *testing.T) {
	env, root := newTestEnv(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := must(env.AbsolutePath(background, "~/pi-node-env-test")); got != filepath.Join(home, "pi-node-env-test") {
		t.Fatalf("home = %q", got)
	}
	if got := must(env.AbsolutePath(background, "~")); got != home {
		t.Fatalf("bare home = %q", got)
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

func TestNodeEnvKeepsMalformedFileURLsAsOrdinaryPaths(t *testing.T) {
	env, root := newTestEnv(t)
	cases := map[string]string{
		"file://host/x":          filepath.Join(root, "file:/host/x"),
		"file:///a%2Fb":          filepath.Join(root, "file:/a%2Fb"),
		"file:///a%20b/c":        "/a b/c",
		"file://localhost/q":     "/q",
		"file:/x":                filepath.Join(root, "file:/x"),
		"/abs/../other/./path/":  "/other/path",
		"relative/../sibling.go": filepath.Join(root, "sibling.go"),
	}
	trailingJoin := "b/"
	if runtime.GOOS == "windows" {
		// Node's win32 fileURLToPath, isAbsolute, resolve, and join, observed
		// with upstream's resolvePath on Windows: a host URL is a UNC share
		// root, drive-less file URLs stay ordinary relative paths, and a
		// drive-less rooted path lands on the process's current drive.
		processDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		drive := filepath.VolumeName(processDir)
		cases = map[string]string{
			"file://host/x":                   `\\host\x\`,
			"file:///a%2Fb":                   filepath.Join(root, `file:\a%2Fb`),
			"file:///a%20b/c":                 filepath.Join(root, `file:\a%20b\c`),
			"file://localhost/q":              filepath.Join(root, `file:\localhost\q`),
			"file:/x":                         filepath.Join(root, `file:\x`),
			"/abs/../other/./path/":           filepath.Join(drive+`\`, "other", "path"),
			`\abs\x`:                          drive + `\abs\x`,
			filepath.VolumeName(root) + "rel": filepath.Join(root, "rel"),
			"relative/../sibling.go":          filepath.Join(root, "sibling.go"),
		}
		trailingJoin = `b\`
	}
	for input, want := range cases {
		if got := must(env.AbsolutePath(background, input)); got != want {
			t.Errorf("AbsolutePath(%q) = %q, want %q", input, got, want)
		}
	}
	if got := must(env.JoinPath(background, nil)); got != "." {
		t.Fatalf("empty join = %q", got)
	}
	if got := must(env.JoinPath(background, []string{"a", "", "../b/"})); got != trailingJoin {
		t.Fatalf("trailing join = %q, want %q", got, trailingJoin)
	}
}

func TestNodeEnvFileInfoDoesNotFollowSymlinks(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.CreateDir(background, "dir", &harness.CreateDirOptions{Recursive: new(true)}))
	mustDo(t, env.WriteFile(background, "dir/file.txt", []byte("hello")))
	testenv.Symlink(t, filepath.Join(root, "dir/file.txt"), filepath.Join(root, "file-link"))
	testenv.Symlink(t, filepath.Join(root, "dir"), filepath.Join(root, "dir-link"))
	for path, kind := range map[string]harness.FileKind{"dir": harness.FileKindDirectory, "dir/file.txt": harness.FileKindFile, "file-link": harness.FileKindSymlink, "dir-link": harness.FileKindSymlink} {
		info := must(env.FileInfo(background, path))
		if info.Name != filepath.Base(path) || info.Path != filepath.Join(root, path) || info.Kind != kind {
			t.Errorf("FileInfo(%q) = %+v", path, info)
		}
	}
	if info := must(env.FileInfo(background, "dir/file.txt")); info.Size != 5 {
		t.Fatalf("size = %d", info.Size)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, "dir/file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := must(env.CanonicalPath(background, "file-link")); got != want {
		t.Fatalf("canonical = %q, want %q", got, want)
	}
}

func TestNodeEnvListsSymlinksAsSymlinks(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "target.txt", []byte("hello")))
	testenv.Symlink(t, filepath.Join(root, "target.txt"), filepath.Join(root, "link.txt"))
	entries := must(env.ListDir(background, "."))
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	if len(entries) != 2 || entries[0].Name != "link.txt" || entries[0].Kind != harness.FileKindSymlink || entries[1].Name != "target.txt" || entries[1].Kind != harness.FileKindFile {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestNodeEnvStopsReadingTextLinesAtTheRequestedLimit(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("one\ntwo\nthree")))
	if got := must(env.ReadTextLines(background, "file.txt", &harness.ReadTextLinesOptions{MaxLines: new(1)})); strings.Join(got, ",") != "one" {
		t.Fatalf("lines = %q", got)
	}
	if got := must(env.ReadTextLines(background, "file.txt", nil)); strings.Join(got, ",") != "one,two,three" {
		t.Fatalf("all lines = %q", got)
	}
	if got := must(env.ReadTextLines(background, "file.txt", &harness.ReadTextLinesOptions{MaxLines: new(0)})); got == nil || len(got) != 0 {
		t.Fatalf("zero-limit lines = %#v", got)
	}
}

func TestNodeEnvReturnsFileErrorForMissingPaths(t *testing.T) {
	env, root := newTestEnv(t)
	_, err := env.FileInfo(background, "missing.txt")
	var fileErr *harness.FileError
	if !errors.As(err, &fileErr) || fileErr.Code != harness.FileErrorNotFound || fileErr.Path != filepath.Join(root, "missing.txt") {
		t.Fatalf("err = %#v", err)
	}
	if want := "ENOENT: no such file or directory, lstat '" + filepath.Join(root, "missing.txt") + "'"; fileErr.Message != want {
		t.Fatalf("message = %q, want %q", fileErr.Message, want)
	}
	if must(env.Exists(background, "missing.txt")) {
		t.Fatal("missing path exists")
	}
}

func TestNodeEnvReturnsFileErrorForListingNonDirectories(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("hello")))
	_, err := env.ListDir(background, "file.txt")
	if code := fileErrorCode(t, err); code != harness.FileErrorNotDirectory {
		t.Fatalf("code = %s", code)
	}
	if want := "ENOTDIR: not a directory, scandir '" + filepath.Join(root, "file.txt") + "'"; err.Error() != want {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestNodeEnvAppendsToNewFilesAndCreatesParents(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.AppendFile(background, "new/nested/file.txt", []byte("a")))
	mustDo(t, env.AppendFile(background, "new/nested/file.txt", []byte("b")))
	if got := must(env.ReadTextFile(background, "new/nested/file.txt")); got != "ab" {
		t.Fatalf("content = %q", got)
	}
}

func TestNodeEnvAtomicallyRenamesAndReplacesTheDestination(t *testing.T) {
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

func TestNodeEnvRenameFailureReportsTheSourcePath(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "destination.txt", []byte("unchanged")))
	err := env.RenameFile(background, "missing-source.txt", "destination.txt")
	var fileErr *harness.FileError
	if !errors.As(err, &fileErr) || fileErr.Code != harness.FileErrorNotFound || fileErr.Path != filepath.Join(root, "missing-source.txt") {
		t.Fatalf("err = %#v", err)
	}
	want := "ENOENT: no such file or directory, rename '" + filepath.Join(root, "missing-source.txt") + "' -> '" + filepath.Join(root, "destination.txt") + "'"
	if fileErr.Message != want {
		t.Fatalf("message = %q", fileErr.Message)
	}
	if got := must(env.ReadTextFile(background, "destination.txt")); got != "unchanged" {
		t.Fatalf("destination = %q", got)
	}
}

func TestNodeEnvCreatesTemporaryDirectoriesAndFiles(t *testing.T) {
	env, _ := newTestEnv(t)
	t.Setenv("TMPDIR", t.TempDir())
	prefix := "node-env-test-"
	tempDir := must(env.CreateTempDir(background, &prefix))
	if info, err := os.Stat(tempDir); err != nil || !info.IsDir() || !strings.HasPrefix(filepath.Base(tempDir), prefix) || len(filepath.Base(tempDir)) != len(prefix)+6 {
		t.Fatalf("temp dir %q: %v", tempDir, err)
	}
	if !strings.HasPrefix(filepath.Base(must(env.CreateTempDir(background, nil))), "tmp-") {
		t.Fatal("default prefix not tmp-")
	}
	tempFile := must(env.CreateTempFile(background, &harness.CreateTempFileOptions{Prefix: "prefix-", Suffix: ".txt"}))
	if _, err := os.Stat(tempFile); err != nil || !strings.HasSuffix(tempFile, ".txt") || !strings.HasPrefix(filepath.Base(tempFile), "prefix-") {
		t.Fatalf("temp file %q: %v", tempFile, err)
	}
}

func TestNodeEnvHonorsCreateDirAndRemoveOptions(t *testing.T) {
	env, root := newTestEnv(t)
	if code := fileErrorCode(t, env.CreateDir(background, "missing/child", &harness.CreateDirOptions{Recursive: new(false)})); code != harness.FileErrorNotFound {
		t.Fatalf("non-recursive create code = %s", code)
	}
	mustDo(t, env.WriteFile(background, "dir/child/file.txt", []byte("hello")))
	err := env.Remove(background, "dir", &harness.RemoveOptions{})
	if code := fileErrorCode(t, err); code != harness.FileErrorUnknown || err.Error() != "Path is a directory: rm returned EISDIR (is a directory) "+filepath.Join(root, "dir") {
		t.Fatalf("non-recursive remove = %v", err)
	}
	mustDo(t, env.Remove(background, "dir", &harness.RemoveOptions{Recursive: true}))
	if must(env.Exists(background, "dir")) {
		t.Fatal("dir still exists")
	}
	if code := fileErrorCode(t, env.Remove(background, "missing", &harness.RemoveOptions{})); code != harness.FileErrorNotFound {
		t.Fatalf("missing remove code = %s", code)
	}
	mustDo(t, env.Remove(background, "missing", &harness.RemoveOptions{Force: true}))
	mustDo(t, env.CreateDir(background, "exists", nil))
	if err := env.CreateDir(background, "exists", &harness.CreateDirOptions{Recursive: new(false)}); fileErrorCode(t, err) != harness.FileErrorUnknown || !strings.HasPrefix(err.Error(), "EEXIST: file already exists, mkdir '") {
		t.Fatalf("existing create = %v", err)
	}
}

func TestNodeEnvMapsDirectoryReadsAndWritesToIsDirectory(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.CreateDir(background, "d", nil))
	_, err := env.ReadTextFile(background, "d")
	if fileErrorCode(t, err) != harness.FileErrorIsDirectory || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("read dir = %v", err)
	}
	err = env.WriteFile(background, "d", []byte("x"))
	if fileErrorCode(t, err) != harness.FileErrorIsDirectory || err.Error() != "EISDIR: illegal operation on a directory, open '"+filepath.Join(root, "d")+"'" {
		t.Fatalf("write dir = %v", err)
	}
}

func TestNodeEnvReturnsAbortedForPreAbortedFileOperations(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "file.txt", []byte("hello")))
	ctx := abortedContext()
	_, readErr := env.ReadTextFile(ctx, "file.txt")
	_, linesErr := env.ReadTextLines(ctx, "file.txt", nil)
	_, binaryErr := env.ReadBinaryFile(ctx, "file.txt")
	_, listErr := env.ListDir(ctx, ".")
	for index, err := range []error{readErr, linesErr, binaryErr, env.WriteFile(ctx, "other.txt", []byte("hello")), env.RenameFile(ctx, "file.txt", "renamed.txt"), listErr} {
		if code := fileErrorCode(t, err); code != harness.FileErrorAborted {
			t.Errorf("operation %d code = %s", index, code)
		}
	}
	if must(env.Exists(background, "other.txt")) || !must(env.Exists(background, "file.txt")) {
		t.Fatal("aborted operation changed the filesystem")
	}
}

func TestNodeEnvCleanupIsBestEffort(t *testing.T) {
	env, _ := newTestEnv(t)
	env.Cleanup(background)
	env.Cleanup(abortedContext())
}

func TestNodeEnvReadTextFileReplacesInvalidUTF8(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "bad.txt", []byte{'a', 0xe2, 0x82, 'b', 0xff, 0xf0, 0x9f}))
	if got := must(env.ReadTextFile(background, "bad.txt")); got != "a�b��" {
		t.Fatalf("decoded = %q", got)
	}
	if !bytes.Equal(must(env.ReadBinaryFile(background, "bad.txt")), []byte{'a', 0xe2, 0x82, 'b', 0xff, 0xf0, 0x9f}) {
		t.Fatal("binary read changed bytes")
	}
}
