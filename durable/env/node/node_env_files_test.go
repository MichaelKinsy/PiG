//go:build unix

package node

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// node.ts:1079-1100 renameFile / fileInfo / listDir: a failing rename names both paths, fileInfo reports modification time in milliseconds, and a file type Pi does not
// model (a FIFO) is "Unsupported file type" for fileInfo and omitted by listDir; a failing scandir names its syscall.
func TestNodeExecutionEnvRenameInfoAndListDirReports(t *testing.T) {
	env, root := newTestEnv(t)
	err := env.RenameFile(background, "missing.txt", "target.txt")
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	source, destination := filepath.Join(root, "missing.txt"), filepath.Join(root, "target.txt")
	if !ok || fileErr.Code != durableenv.FileErrorNotFound || !strings.Contains(fileErr.Message, "rename '"+source+"' -> '"+destination+"'") {
		t.Fatalf("RenameFile of a missing source = %v, want not_found naming %s -> %s", err, source, destination)
	}

	mustDo(t, os.WriteFile(filepath.Join(root, "f.txt"), []byte("abc"), 0o600))
	stamp := time.Date(2020, 5, 6, 7, 8, 9, 250_000_000, time.UTC)
	mustDo(t, os.Chtimes(filepath.Join(root, "f.txt"), stamp, stamp))
	info := must(env.FileInfo(background, "f.txt"))
	if math.Abs(info.MtimeMs-float64(stamp.UnixMilli())) > 0.01 || info.Size != 3 || info.Name != "f.txt" || info.Kind != durableenv.FileKindFile {
		t.Fatalf("FileInfo = %+v, want mtime %d ms, size 3, name f.txt", info, stamp.UnixMilli())
	}

	mustDo(t, syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600))
	_, err = env.FileInfo(background, "pipe")
	fileErr, ok = errors.AsType[*durableenv.FileError](err)
	if !ok || fileErr.Code != durableenv.FileErrorInvalid || fileErr.Message != "Unsupported file type" || fileErr.Path != filepath.Join(root, "pipe") {
		t.Fatalf("FileInfo of a FIFO = %v, want the invalid Unsupported file type error", err)
	}
	entries := must(env.ListDir(background, "."))
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if len(entries) != 1 || names[0] != "f.txt" {
		t.Fatalf("ListDir = %v, want the FIFO omitted", names)
	}
	_, err = env.ListDir(background, "no-such-dir")
	if fileErr, ok = errors.AsType[*durableenv.FileError](err); !ok || fileErr.Code != durableenv.FileErrorNotFound || !strings.Contains(fileErr.Message, "scandir '"+filepath.Join(root, "no-such-dir")+"'") {
		t.Fatalf("ListDir of a missing directory = %v, want a scandir failure", err)
	}
}

// node.ts:1169-1220 remove and the temporary names: removing a directory without recursive reports Node's rm EISDIR text; a temporary directory is private (0700) and a failing
// mkdtemp names the template; a temporary file is prefix + a version 4 UUID + suffix.
func TestNodeExecutionEnvRemoveAndTemporaryNames(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.MkdirAll(filepath.Join(root, "d"), 0o700))
	err := env.Remove(background, "d", nil)
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	want := "Path is a directory: rm returned EISDIR (is a directory) " + filepath.Join(root, "d")
	if !ok || fileErr.Code != durableenv.FileErrorUnknown || fileErr.Message != want {
		t.Fatalf("Remove of a directory = %v, want %q", err, want)
	}

	dir := must(env.CreateTempDir(background, new("sem-")))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	info, statErr := os.Stat(dir)
	mustDo(t, statErr)
	if info.Mode().Perm() != 0o700 || !regexp.MustCompile(`/sem-[A-Za-z0-9]{6}$`).MatchString(dir) {
		t.Fatalf("CreateTempDir = %s (%v), want a private sem-XXXXXX directory", dir, info.Mode().Perm())
	}
	bad := filepath.Join(root, "no-such-parent", "x-")
	_, err = env.CreateTempDir(background, &bad)
	if fileErr, ok = errors.AsType[*durableenv.FileError](err); !ok || !strings.Contains(fileErr.Message, "mkdtemp") || fileErr.Path != filepath.Join(os.TempDir(), bad)+"XXXXXX" {
		t.Fatalf("CreateTempDir under a missing parent = %v, want a mkdtemp failure naming the XXXXXX template", err)
	}

	file := must(env.CreateTempFile(background, &durableenv.CreateTempFileOptions{Prefix: "pre-", Suffix: ".sfx"}))
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(file)) })
	uuid := regexp.MustCompile(`^pre-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.sfx$`)
	if !uuid.MatchString(filepath.Base(file)) {
		t.Fatalf("CreateTempFile = %s, want prefix + version 4 UUID + suffix", filepath.Base(file))
	}
	bare := must(env.CreateTempFile(background, nil))
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(bare)) })
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(filepath.Base(bare)) {
		t.Fatalf("CreateTempFile(nil) = %s, want a bare UUID name", filepath.Base(bare))
	}
}

// node.ts:1222-1240 cleanup: it kills every running command's process tree and forgets them; a command that finishes is forgotten too.
func TestNodeExecutionEnvTracksAndCleansUpChildren(t *testing.T) {
	env, _ := newTestEnv(t)
	if _, ok := any(env).(*NodeExecutionEnv); !ok {
		t.Fatalf("newTestEnv returned %T, want *NodeExecutionEnv", env)
	}
	cmd := exec.Command("sleep", "60")
	mustDo(t, env.startChild(cmd))
	if len(env.activeChildPids) != 1 {
		t.Fatalf("tracked children = %d after start, want 1", len(env.activeChildPids))
	}
	mustDo(t, env.Cleanup(background))
	if len(env.activeChildPids) != 0 {
		t.Fatalf("tracked children = %d after Cleanup, want 0", len(env.activeChildPids))
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("Cleanup did not terminate the running command")
	}

	if _, _, err := collectShellOutput(env, "true", nil, background); err != nil {
		t.Fatal(err)
	}
	if len(env.activeChildPids) != 0 {
		t.Fatalf("tracked children = %d after a command finished, want 0", len(env.activeChildPids))
	}
}

// node.ts:1007-1016 writeFile: a failing write reports the write failure for the file (ENOSPC on /dev/full).
func TestNodeExecutionEnvWriteFailureIsReported(t *testing.T) {
	env, _ := newTestEnv(t)
	err := env.WriteFile(background, "/dev/full", "data")
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	if !ok || fileErr.Path != "/dev/full" || !strings.Contains(fileErr.Message, "write") {
		t.Fatalf("WriteFile to /dev/full = %v, want a write failure for /dev/full", err)
	}
}
