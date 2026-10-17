package node

// pi: packages/durable/src/env/node.ts

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// NodeExecutionEnv's file system calls, one by one, against packages/durable/src/env/node.ts: id and cwd (node.ts:606), absolutePath resolves against the cwd (node.ts:644), joinPath is Node's path.join (node.ts:648), openTextLineReader/readTextFile/readTextLines/readBinaryFile read what writeFile, appendFile and truncateFile left (node.ts:896-1059), flushFile and renameFile (node.ts:1059-1079), listDir reports kind and size (node.ts:1103), openDirReader pages entries (node.ts:1128), canonicalPath resolves links (node.ts:1145), exists (node.ts:1156), and createTempDir and createTempFile (node.ts:1195-1220).
func TestNodeExecutionEnvFileSystemCallsMirrorPi(t *testing.T) {
	env, root := newTestEnv(t)
	if env.Id() != "node:local" || env.Cwd() != root {
		t.Fatalf("id %q cwd %q", env.Id(), env.Cwd())
	}
	if got := must(env.AbsolutePath(background, "a/b.txt")); got != filepath.Join(root, "a", "b.txt") {
		t.Fatalf("absolutePath = %q", got)
	}
	absolute := filepath.Join(os.TempDir(), "abs-x")
	if got := must(env.AbsolutePath(background, absolute)); got != absolute {
		t.Fatalf("absolutePath of an absolute path = %q", got)
	}
	if got := must(env.JoinPath(background, []string{"a", "../b", "c.txt"})); got != filepath.Join("b", "c.txt") {
		t.Fatalf("joinPath = %q", got)
	}
	if got := must(env.JoinPath(background, nil)); got != "." {
		t.Fatalf("joinPath of nothing = %q", got)
	}

	mustDo(t, env.WriteFile(background, "dir/file.txt", "one\ntwo\nthree"))
	mustDo(t, env.AppendFile(background, "dir/file.txt", "\nfour"))
	if got := must(env.ReadTextFile(background, "dir/file.txt")); got != "one\ntwo\nthree\nfour" {
		t.Fatalf("readTextFile = %q", got)
	}
	two := 2
	if got := must(env.ReadTextLines(background, "dir/file.txt", &durableenv.ReadTextLinesOptions{MaxLines: &two})); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("readTextLines = %q", got)
	}
	if got := must(env.ReadBinaryFile(background, "dir/file.txt")); string(got) != "one\ntwo\nthree\nfour" {
		t.Fatalf("readBinaryFile = %q", got)
	}
	reader := must(env.OpenTextLineReader(background, "dir/file.txt"))
	first := must(reader.ReadLine(background))
	if first == nil || first.Text != "one" || !first.Terminated {
		t.Fatalf("first line = %+v", first)
	}
	mustDo(t, reader.Close(background))
	// packages/durable/src/env/index.ts:96-98: BinaryReader.info is the metadata of the opened file.
	binary := must(env.OpenBinaryReader(background, "dir/file.txt", nil))
	info := must(binary.Info(background))
	if info.Name != "file.txt" || info.Kind != durableenv.FileKindFile || info.Size != int64(len("one\ntwo\nthree\nfour")) {
		t.Fatalf("binary reader info = %+v", info)
	}
	mustDo(t, binary.Close(background))
	mustDo(t, env.TruncateFile(background, "dir/file.txt", 3))
	if got := must(env.ReadTextFile(background, "dir/file.txt")); got != "one" {
		t.Fatalf("after truncate = %q", got)
	}
	mustDo(t, env.FlushFile(background, "dir/file.txt"))
	var missing *durableenv.FileError
	if err := env.FlushFile(background, "dir/absent.txt"); !errors.As(err, &missing) || missing.Code != durableenv.FileErrorNotFound {
		t.Fatalf("flushFile of a missing file = %v, want a not_found FileError", err)
	}
	mustDo(t, env.RenameFile(background, "dir/file.txt", "dir/renamed.txt"))
	if exists := must(env.Exists(background, "dir/file.txt")); exists {
		t.Fatal("the source of a rename must be gone")
	}
	if exists := must(env.Exists(background, "dir/renamed.txt")); !exists {
		t.Fatal("the destination of a rename must exist")
	}

	testenv.RequireDirectoryLink(t, filepath.Join(root, "dir"), filepath.Join(root, "link"))
	entries := must(env.ListDir(background, "dir"))
	if len(entries) != 1 || entries[0].Name != "renamed.txt" || entries[0].Kind != durableenv.FileKindFile || entries[0].Size != 3 {
		t.Fatalf("listDir = %+v", entries)
	}
	dirReader := must(env.OpenDirReader(background, "dir"))
	page := must(dirReader.Next(background, 10))
	if len(page.Entries) != 1 || page.Entries[0].Name != "renamed.txt" {
		t.Fatalf("dir reader page = %+v", page)
	}
	mustDo(t, dirReader.Close(background))
	canonical := must(env.CanonicalPath(background, "link/renamed.txt"))
	want := must(filepath.EvalSymlinks(filepath.Join(root, "dir", "renamed.txt")))
	if canonical != want {
		t.Fatalf("canonicalPath = %q, want %q", canonical, want)
	}

	prefix := "pi-test-"
	tempDir := must(env.CreateTempDir(background, &prefix))
	if !strings.HasPrefix(filepath.Base(tempDir), prefix) {
		t.Fatalf("temp dir %q lacks the prefix", tempDir)
	}
	if info, err := os.Stat(tempDir); err != nil || !info.IsDir() {
		t.Fatalf("temp dir %q: %v", tempDir, err)
	}
	tempFile := must(env.CreateTempFile(background, &durableenv.CreateTempFileOptions{Prefix: "p-", Suffix: ".s"}))
	base := filepath.Base(tempFile)
	if !strings.HasPrefix(base, "p-") || !strings.HasSuffix(base, ".s") {
		t.Fatalf("temp file %q", tempFile)
	}
	if data, err := os.ReadFile(tempFile); err != nil || len(data) != 0 {
		t.Fatalf("a new temp file is empty: %q %v", data, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir); _ = os.RemoveAll(filepath.Dir(tempFile)) })
}

// packages/durable/src/env/index.ts:171-307 declares FileSystem; through the interface the node environment answers id, absolutePath, joinPath, createTempDir and createTempFile as the file system contract does,.
func TestFileSystemInterfaceContractThroughTheInterface(t *testing.T) {
	local, root := newTestEnv(t)
	var fsys durableenv.FileSystem = local
	if fsys.Id() != "node:local" {
		t.Fatalf("id = %q", fsys.Id())
	}
	if fsys.Cwd() != root {
		t.Fatalf("cwd = %q, want the directory relative paths resolve against (env/index.ts:173-177)", fsys.Cwd())
	}
	if got := must(fsys.AbsolutePath(background, "x/y")); got != filepath.Join(root, "x", "y") {
		t.Fatalf("absolutePath = %q", got)
	}
	if got := must(fsys.JoinPath(background, []string{"a", "b"})); got != filepath.Join("a", "b") {
		t.Fatalf("joinPath = %q", got)
	}
	dir := must(fsys.CreateTempDir(background, nil))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if !strings.HasPrefix(filepath.Base(dir), "tmp-") {
		t.Fatalf("temp dir %q has no default prefix", dir)
	}
	file := must(fsys.CreateTempFile(background, nil))
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(file)) })
	if info, err := os.Stat(file); err != nil || info.IsDir() {
		t.Fatalf("temp file %q: %v", file, err)
	}
	changes := make(chan durableenv.WatchChange, 8)
	watcher := must(fsys.Watch(background, []durableenv.WatchTarget{{Path: "."}}, func(change durableenv.WatchChange) { changes <- change }))
	mustDo(t, local.WriteFile(background, "watched.txt", "x"))
	select {
	case <-changes:
	case <-time.After(5 * time.Second):
		t.Fatal("watch reported no change")
	}
	mustDo(t, watcher.Close(background))
}

// packages/durable/src/env/index.ts:116: WatchTarget.exclude keeps names out of what a watch reports; packages/durable/src/env/index.ts:252: ShellExecOptions.cwd is the directory the command runs in, resolved against the environment's cwd.
func TestWatchTargetExcludeAndShellCwdMirrorPi(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.CreateDir(background, "sub", nil))
	result, output, err := collectShellOutput(env, "pwd", &durableenv.ShellExecOptions{Cwd: "sub"}, background)
	mustDo(t, err)
	want := must(filepath.EvalSymlinks(filepath.Join(root, "sub")))
	if result.ExitCode != 0 || strings.TrimSpace(output) != want {
		t.Fatalf("pwd in cwd sub = %q (exit %d), want %q", output, result.ExitCode, want)
	}

	changes := make(chan durableenv.WatchChange, 64)
	watcher := must(env.Watch(background, []durableenv.WatchTarget{{Path: ".", Recursive: true, Exclude: &durableenv.WatchExclude{Names: []string{"skipped"}, Hidden: true}}}, func(change durableenv.WatchChange) { changes <- change }))
	defer func() { _ = watcher.Close(background) }()
	mustDo(t, env.WriteFile(background, "skipped", "x"))
	mustDo(t, env.WriteFile(background, ".hidden", "x"))
	mustDo(t, env.WriteFile(background, "kept", "x"))
	var reported []string
	deadline := time.After(5 * time.Second)
	for !slices.ContainsFunc(reported, func(path string) bool { return strings.HasSuffix(path, "kept") }) {
		select {
		case change := <-changes:
			if paths, ok := change.(durableenv.WatchChangePaths); ok {
				reported = append(reported, paths.Paths...)
			}
		case <-deadline:
			t.Fatalf("the kept file was never reported: %v", reported)
		}
	}
	for _, path := range reported {
		if strings.HasSuffix(path, "skipped") || strings.HasSuffix(path, ".hidden") {
			t.Fatalf("an excluded name was reported: %v", reported)
		}
	}
}
