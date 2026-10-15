package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// abortAfter is a context whose Err reports cancellation from its n-th call on, so a test can abort an operation at each of its abort checks in turn.
type abortAfter struct {
	context.Context
	calls atomic.Int64
	from  int64
}

func (ctx *abortAfter) Err() error {
	if ctx.calls.Add(1) >= ctx.from {
		return context.Canceled
	}
	return nil
}

func isAborted(err error) bool {
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	return ok && fileErr.Code == durableenv.FileErrorAborted
}

// Pi packages/durable/src/env/node.ts abortResult checks (:1007-1185): every file operation tests its signal where Pi does (before it starts, and after each step that Pi
// follows with an abortResult), and an abort at any one of them is an "aborted" FileError. `checks` is the number of tests of the signal
// in one run of the operation.
func TestNodeExecutionEnvFileOperationsCheckTheirSignalWherePiDoes(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("one\ntwo\n"), 0o600))
	mustDo(t, os.MkdirAll(filepath.Join(root, "dir", "sub"), 0o700))
	mustDo(t, os.WriteFile(filepath.Join(root, "dir", "a.txt"), nil, 0o600))
	cases := []struct {
		name   string
		checks int64
		run    func(ctx context.Context) error
	}{
		{"ReadBinaryFile", 2, func(ctx context.Context) error { _, err := env.ReadBinaryFile(ctx, "file.txt"); return err }},
		{"ReadTextFile", 2, func(ctx context.Context) error { _, err := env.ReadTextFile(ctx, "file.txt"); return err }},
		{"WriteFile", 2, func(ctx context.Context) error { return env.WriteFile(ctx, "out/new.txt", "x") }},
		{"AppendFile", 3, func(ctx context.Context) error { return env.AppendFile(ctx, "out/log.txt", "x") }},
		{"TruncateFile", 2, func(ctx context.Context) error { return env.TruncateFile(ctx, "file.txt", 3) }},
		{"FlushFile", 2, func(ctx context.Context) error { return env.FlushFile(ctx, "file.txt") }},
		{"RenameFile", 1, func(ctx context.Context) error { return env.RenameFile(ctx, "file.txt", "renamed.txt") }},
		{"FileInfo", 1, func(ctx context.Context) error { _, err := env.FileInfo(ctx, "file.txt"); return err }},
		{"ListDir", 3, func(ctx context.Context) error { _, err := env.ListDir(ctx, "dir"); return err }},
		{"CanonicalPath", 1, func(ctx context.Context) error { _, err := env.CanonicalPath(ctx, "file.txt"); return err }},
		{"CreateDir", 1, func(ctx context.Context) error { return env.CreateDir(ctx, "made/deep", nil) }},
		{"Remove", 1, func(ctx context.Context) error { return env.Remove(ctx, "dir/a.txt", nil) }},
		{"CreateTempDir", 1, func(ctx context.Context) error {
			path, err := env.CreateTempDir(ctx, new("abort-"))
			if err == nil {
				_ = os.Remove(path)
			}
			return err
		}},
	}
	reset := func() {
		mustDo(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("one\ntwo\n"), 0o600))
		_ = os.Remove(filepath.Join(root, "renamed.txt"))
		mustDo(t, os.RemoveAll(filepath.Join(root, "out")))
		mustDo(t, os.RemoveAll(filepath.Join(root, "made")))
		mustDo(t, os.WriteFile(filepath.Join(root, "dir", "a.txt"), nil, 0o600))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for from := int64(1); from <= tc.checks; from++ {
				reset()
				if err := tc.run(&abortAfter{Context: context.Background(), from: from}); !isAborted(err) {
					t.Fatalf("aborted at check %d: %v, want an aborted FileError", from, err)
				}
			}
			reset()
			ctx := &abortAfter{Context: context.Background(), from: tc.checks + 1}
			if err := tc.run(ctx); err != nil {
				t.Fatalf("with %d checks passing: %v, want success", tc.checks, err)
			}
			if got := ctx.calls.Load(); got != tc.checks {
				t.Fatalf("the operation tested its signal %d times, want %d", got, tc.checks)
			}
		})
	}
}

// A file system wrapper whose reads are replaced: the embedded environment's Self dispatches to the outer type, as a subclass override does in Pi (node.ts class methods call `this.`).
type overridingEnv struct {
	*NodeExecutionEnv
	openReader func(ctx context.Context, path string) (durableenv.TextLineReader, error)
	fileInfo   func(ctx context.Context, path string) (durableenv.FileInfo, error)
}

func (env *overridingEnv) OpenTextLineReader(ctx context.Context, path string) (durableenv.TextLineReader, error) {
	return env.openReader(ctx, path)
}

func (env *overridingEnv) FileInfo(ctx context.Context, path string) (durableenv.FileInfo, error) {
	return env.fileInfo(ctx, path)
}

type failingLineReader struct{ err error }

func (reader failingLineReader) ReadLine(context.Context) (*durableenv.TextLine, error) {
	return nil, reader.err
}
func (failingLineReader) Close(context.Context) error { return nil }

func TestNodeExecutionEnvSelfDispatchesReadTextLinesAndExistsToOverrides(t *testing.T) {
	env, _ := newTestEnv(t)
	boom := errors.New("reader failed")
	outer := &overridingEnv{NodeExecutionEnv: env}
	env.Self = outer
	outer.openReader = func(context.Context, string) (durableenv.TextLineReader, error) {
		return failingLineReader{err: boom}, nil
	}
	if _, err := env.ReadTextLines(background, "any.txt", nil); !errors.Is(err, boom) {
		t.Fatalf("ReadTextLines through an overriding reader = %v, want the reader's failure", err)
	}
	outer.fileInfo = func(context.Context, string) (durableenv.FileInfo, error) {
		return durableenv.FileInfo{}, durableenv.NewFileError(durableenv.FileErrorNotFound, "gone", "x", nil)
	}
	if exists, err := env.Exists(background, "x"); err != nil || exists {
		t.Fatalf("Exists with an overriding FileInfo = %v, %v; want false", exists, err)
	}
	outer.fileInfo = func(context.Context, string) (durableenv.FileInfo, error) {
		return durableenv.FileInfo{}, durableenv.NewFileError(durableenv.FileErrorPermissionDenied, "denied", "x", nil)
	}
	if exists, err := env.Exists(background, "x"); err == nil || exists {
		t.Fatalf("Exists with a failing FileInfo = %v, %v; want the failure", exists, err)
	}
}

// node.ts:606-650: setCwd moves the base of every relative path; joinPath keeps the trailing separator of the last non-empty part.
func TestNodeExecutionEnvSetCwdAndJoinPath(t *testing.T) {
	env, root := newTestEnv(t)
	other := t.TempDir()
	env.SetCwd(other)
	if env.Cwd() != other {
		t.Fatalf("Cwd = %q after SetCwd(%q)", env.Cwd(), other)
	}
	if abs := must(env.AbsolutePath(background, "x/y")); abs != filepath.Join(other, "x", "y") {
		t.Fatalf("AbsolutePath = %q, want it under the new cwd (not %q)", abs, root)
	}
	for _, tc := range []struct {
		parts []string
		want  string
	}{
		{[]string{"a", "b/"}, "a/b/"},
		{[]string{"a/", ""}, "a/"},
		{[]string{"a", "b", ""}, "a/b"},
		{[]string{"a", ""}, "a"},
		{[]string{"", ""}, "."},
		{nil, "."},
	} {
		if got := must(env.JoinPath(background, tc.parts)); got != tc.want {
			t.Errorf("JoinPath(%q) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}

// node.ts:915-960 readTextFile decodes UTF-8 like TextDecoder (U+FFFD for bad bytes); readBinaryFile of a missing file fails with not_found naming the path; writeFile truncates and rejects non-text content.
func TestNodeExecutionEnvReadWriteSemantics(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "bad.txt"), []byte{'a', 0xff, 'b'}, 0o600))
	if text := must(env.ReadTextFile(background, "bad.txt")); text != "a\ufffdb" {
		t.Fatalf("ReadTextFile = %q, want the invalid byte decoded as U+FFFD", text)
	}
	_, err := env.ReadBinaryFile(background, "missing.txt")
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	if !ok || fileErr.Code != durableenv.FileErrorNotFound || fileErr.Path != filepath.Join(root, "missing.txt") || !strings.Contains(fileErr.Message, "open") {
		t.Fatalf("ReadBinaryFile of a missing file = %v, want not_found for the resolved path from open", err)
	}
	mustDo(t, env.WriteFile(background, "w.txt", "longer text"))
	mustDo(t, env.WriteFile(background, "w.txt", "short"))
	if got := must(env.ReadTextFile(background, "w.txt")); got != "short" {
		t.Fatalf("a rewrite left %q, want the file truncated to its new content", got)
	}
	err = env.WriteFile(background, "w.txt", 5)
	fileErr, ok = errors.AsType[*durableenv.FileError](err)
	if !ok || fileErr.Code != durableenv.FileErrorInvalid || !strings.Contains(fileErr.Message, "string or []byte") || fileErr.Path != filepath.Join(root, "w.txt") {
		t.Fatalf("WriteFile of an int = %v, want an invalid FileError", err)
	}
	if got := must(env.ReadTextFile(background, "w.txt")); got != "short" {
		t.Fatalf("a rejected write changed the file to %q", got)
	}
	// A parent that cannot be created reports the parent (mkdir), not the file.
	mustDo(t, os.WriteFile(filepath.Join(root, "plain"), nil, 0o600))
	err = env.WriteFile(background, "plain/child.txt", "x")
	fileErr, ok = errors.AsType[*durableenv.FileError](err)
	if !ok || !strings.Contains(fileErr.Message, "mkdir") || fileErr.Path != filepath.Join(root, "plain") {
		t.Fatalf("WriteFile below a regular file = %v, want a mkdir failure naming the parent", err)
	}
	if got := must(env.ReadTextLines(background, "missing-lines.txt", &durableenv.ReadTextLinesOptions{MaxLines: new(0)})); len(got) != 0 {
		t.Fatalf("ReadTextLines with MaxLines 0 = %v, want no lines without opening the file", got)
	}
}

// node.ts:1040-1055 truncateFile: a size that is negative or above Number.MAX_SAFE_INTEGER is invalid; it never creates the file.
func TestNodeExecutionEnvTruncateFileSizeBounds(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "t.txt"), []byte("12345"), 0o600))
	const maxSafe = 1<<53 - 1
	for _, size := range []int64{-1, maxSafe + 1} {
		err := env.TruncateFile(background, "t.txt", size)
		if fileErr, ok := errors.AsType[*durableenv.FileError](err); !ok || fileErr.Code != durableenv.FileErrorInvalid || fileErr.Message != "File size must be a non-negative safe integer" || fileErr.Path != filepath.Join(root, "t.txt") {
			t.Errorf("TruncateFile(%d) = %v, want an invalid FileError", size, err)
		}
	}
	// The largest safe size is not rejected as invalid (the host may refuse so large a file for another reason).
	if err := env.TruncateFile(background, "t.txt", maxSafe); err != nil {
		if fileErr, ok := errors.AsType[*durableenv.FileError](err); ok && fileErr.Code == durableenv.FileErrorInvalid {
			t.Errorf("TruncateFile(MAX_SAFE_INTEGER) = %v, want it not rejected as invalid", err)
		}
	}
}
