package env

// Calls each RemoteExecutionEnv file operation directly on the concrete type and reports failures through testing.T.

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// mutation-checked: zeroing the results of RemoteExecutionEnv.CanonicalPath, RemoteExecutionEnv.CreateTempFile, RemoteExecutionEnv.Exists, RemoteExecutionEnv.FileInfo, RemoteExecutionEnv.Id, RemoteExecutionEnv.JoinPath, RemoteExecutionEnv.ListDir, RemoteExecutionEnv.OpenDirReader, RemoteExecutionEnv.OpenTextLineReader, RemoteExecutionEnv.ReadTextFile, RemoteExecutionEnv.ReadTextLines fails it
// Pi: packages/env/src/remote-env.ts:736 (canonicalPath)
// Pi: packages/env/src/remote-env.ts:787 (createTempFile)
// Pi: packages/env/src/remote-env.ts:743 (exists)
// Pi: packages/env/src/remote-env.ts:680 (fileInfo)
// Pi: packages/env/src/remote-env.ts:50 (id)
// Pi: packages/env/src/remote-env.ts:440 (joinPath)
// Pi: packages/env/src/remote-env.ts:688 (listDir)
// Pi: packages/env/src/remote-env.ts:725 (openDirReader)
// Pi: packages/env/src/remote-env.ts:548 (openTextLineReader)
// Pi: packages/env/src/remote-env.ts:561 (readTextLines)
// Calls each RemoteExecutionEnv file operation directly on the concrete type.
// upstream: packages/env/src/remote-env.ts RemoteExecutionEnv: id remote-env.ts:352, joinPath remote-env.ts:440,
// openTextLineReader remote-env.ts:548, readTextLines remote-env.ts:561, truncateFile remote-env.ts:636,
// flushFile remote-env.ts:650, renameFile remote-env.ts:661, fileInfo remote-env.ts:680, listDir remote-env.ts:688,
// openDirReader remote-env.ts:725, canonicalPath remote-env.ts:736, exists remote-env.ts:743,
// createTempFile remote-env.ts:787, cleanup remote-env.ts:932, createDir remote-env.ts:750.
// Pi: packages/env/src/remote-env.ts:736 (canonicalPath).
// Pi: packages/env/src/remote-env.ts:932 (cleanup).
// Pi: packages/env/src/remote-env.ts:787 (createTempFile).
// Pi: packages/env/src/remote-env.ts:353 (cwd).
// Pi: packages/env/src/remote-env.ts:743 (exists).
// Pi: packages/env/src/remote-env.ts:680 (fileInfo).
// Pi: packages/env/src/remote-env.ts:650 (flushFile).
// Pi: packages/env/src/remote-env.ts:352 (id).
// Pi: packages/env/src/remote-env.ts:440 (joinPath).
// Pi: packages/env/src/remote-env.ts:688 (listDir).
// Pi: packages/env/src/remote-env.ts:725 (openDirReader).
// Pi: packages/env/src/remote-env.ts:548 (openTextLineReader).
// Pi: packages/env/src/remote-env.ts:561 (readTextLines).
// Pi: packages/env/src/remote-env.ts:661 (renameFile).
// Pi: packages/env/src/remote-env.ts:636 (truncateFile).
func TestRemoteExecutionEnvDirectOperations(t *testing.T) {
	env, _ := remoteEnvironment(t)

	if got := env.Id(); got != "pi-env:test" {
		t.Fatalf("Id() = %q, want the id the environment was built with", got)
	}
	joined, err := env.JoinPath(background, []string{"a", "b", "..", "c.txt"})
	if err != nil || joined != "a/c.txt" {
		t.Fatalf("JoinPath = %q, %v; want a/c.txt", joined, err)
	}

	if err := env.WriteFile(background, "lines.txt", "one\ntwo\nthree"); err != nil {
		t.Fatal(err)
	}
	exists, err := env.Exists(background, "lines.txt")
	if err != nil || !exists {
		t.Fatalf("Exists(lines.txt) = %v, %v; want true", exists, err)
	}
	exists, err = env.Exists(background, "missing.txt")
	if err != nil || exists {
		t.Fatalf("Exists(missing.txt) = %v, %v; want false without an error", exists, err)
	}
	info, err := env.FileInfo(background, "lines.txt")
	if err != nil || info.Name != "lines.txt" || info.Kind != durableenv.FileKindFile || info.Size != int64(len("one\ntwo\nthree")) {
		t.Fatalf("FileInfo = %+v, %v", info, err)
	}

	lines, err := env.ReadTextLines(background, "lines.txt", nil)
	if err != nil || !slices.Equal(lines, []string{"one", "two", "three"}) {
		t.Fatalf("ReadTextLines = %q, %v", lines, err)
	}
	maxLines := 2
	lines, err = env.ReadTextLines(background, "lines.txt", &durableenv.ReadTextLinesOptions{MaxLines: &maxLines})
	if err != nil || !slices.Equal(lines, []string{"one", "two"}) {
		t.Fatalf("ReadTextLines(max 2) = %q, %v", lines, err)
	}
	reader, err := env.OpenTextLineReader(background, "lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.ReadLine(background)
	if err != nil || first == nil || first.Text != "one" || !first.Terminated {
		t.Fatalf("first line = %+v, %v", first, err)
	}
	if err := reader.Close(background); err != nil {
		t.Fatal(err)
	}

	if err := env.TruncateFile(background, "lines.txt", 3); err != nil {
		t.Fatal(err)
	}
	if err := env.FlushFile(background, "lines.txt"); err != nil {
		t.Fatal(err)
	}
	text, err := env.ReadTextFile(background, "lines.txt")
	if err != nil || text != "one" {
		t.Fatalf("after TruncateFile(3) text = %q, %v", text, err)
	}
	if err := env.RenameFile(background, "lines.txt", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if exists, _ := env.Exists(background, "lines.txt"); exists {
		t.Fatal("RenameFile left the source in place")
	}
	if text, err := env.ReadTextFile(background, "renamed.txt"); err != nil || text != "one" {
		t.Fatalf("renamed text = %q, %v", text, err)
	}

	entries, err := env.ListDir(background, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "renamed.txt" {
		t.Fatalf("ListDir = %+v", entries)
	}
	dir, err := env.OpenDirReader(background, ".")
	if err != nil {
		t.Fatal(err)
	}
	page, err := dir.Next(background, 10)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Name != "renamed.txt" {
		t.Fatalf("DirReader.Next = %+v, %v", page, err)
	}
	if err := dir.Close(background); err != nil {
		t.Fatal(err)
	}

	cwd := env.Cwd()
	testenv.Symlink(t, filepath.Join(cwd, "renamed.txt"), filepath.Join(cwd, "link.txt"))
	canonical, err := env.CanonicalPath(background, "link.txt")
	want, _ := filepath.EvalSymlinks(filepath.Join(cwd, "renamed.txt"))
	if err != nil || canonical != want {
		t.Fatalf("CanonicalPath = %q, %v; want %q", canonical, err, want)
	}

	// remote-env.ts:750 createDir: recursive defaults to true; recursive false refuses a missing parent.
	if err := env.CreateDir(background, "made/deep/er", nil); err != nil {
		t.Fatal(err)
	}
	if exists, err := env.Exists(background, "made/deep/er"); err != nil || !exists {
		t.Fatalf("CreateDir(nil options) did not create the parents: %v", err)
	}
	flat := false
	if err := env.CreateDir(background, "absent/child", &durableenv.CreateDirOptions{Recursive: &flat}); err == nil {
		t.Fatal("CreateDir with Recursive=false created a path below a missing parent")
	}
	if exists, _ := env.Exists(background, "absent"); exists {
		t.Fatal("CreateDir with Recursive=false left a parent behind")
	}

	temp, err := env.CreateTempFile(background, &durableenv.CreateTempFileOptions{Prefix: "p-", Suffix: ".s"})
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(temp); base[:2] != "p-" || filepath.Ext(base) != ".s" {
		t.Fatalf("CreateTempFile name = %q, want prefix p- and suffix .s", base)
	}
	if exists, err := env.Exists(background, temp); err != nil || !exists {
		t.Fatalf("temp file %q does not exist: %v", temp, err)
	}

	// remote-env.ts:760 remove and remote-env.ts:774 createTempDir.
	prefix := "dir-"
	tempDir, err := env.CreateTempDir(background, &prefix)
	if err != nil || !strings.HasPrefix(filepath.Base(tempDir), prefix) {
		t.Fatalf("CreateTempDir = %q, %v", tempDir, err)
	}
	if exists, err := env.Exists(background, tempDir); err != nil || !exists {
		t.Fatalf("temp dir %q does not exist: %v", tempDir, err)
	}
	if err := env.Remove(background, "renamed.txt", nil); err != nil {
		t.Fatal(err)
	}
	if exists, _ := env.Exists(background, "renamed.txt"); exists {
		t.Fatal("Remove left the file in place")
	}
	if err := env.Remove(background, tempDir, &durableenv.RemoveOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	if exists, _ := env.Exists(background, tempDir); exists {
		t.Fatal("a recursive Remove left the directory")
	}

	if err := env.Cleanup(background); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}
