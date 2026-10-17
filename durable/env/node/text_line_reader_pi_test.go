package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Ports the abort gate at the top of NodeTextLineReader.readLine (packages/durable/src/env/node.ts:403-404): `abortResult(context.abortSignal, path)`
// runs before any read, so a call with an already aborted context fails with the "aborted" FileError for the file's path (abortResult, :144-145),
// reads nothing, and leaves every line to be read later.
func TestTextLineReaderRejectsAnAlreadyAbortedReadWithoutReading(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "lines.txt", []byte("one\ntwo\n")))
	original := readFileAt
	t.Cleanup(func() { readFileAt = original })
	reads := 0
	readFileAt = func(file *os.File, chunk []byte, offset int64) (int, error) {
		reads++
		return original(file, chunk, offset)
	}
	reader := must(env.OpenTextLineReader(background, "lines.txt"))
	defer func() { _ = reader.Close(background) }()

	ctx, cancel := context.WithCancel(background)
	cancel()
	_, err := reader.ReadLine(ctx)
	expectFileError(t, err, durableenv.FileErrorAborted, filepath.Join(root, "lines.txt"))
	if message := fileError(t, err).Message; message != "aborted" {
		t.Fatalf("aborted ReadLine message = %q, want %q", message, "aborted")
	}
	if reads != 0 {
		t.Fatalf("an aborted ReadLine read the file %d time(s), want none", reads)
	}
	for _, want := range []string{"one", "two"} {
		if line := must(reader.ReadLine(background)); line == nil || *line != (durableenv.TextLine{Text: want, Terminated: true}) {
			t.Fatalf("line after the aborted call = %+v, want %q", line, want)
		}
	}
}

// Ports the order of the two gates at the top of NodeTextLineReader.readLine (node.ts:403-405): the abort check runs before the closed check, so an
// aborted call on a closed reader fails with "aborted" for the file's path, not with the closed reader's "invalid" error. A Node probe of the pinned
// NodeExecutionEnv returns {code: "aborted", message: "aborted"} for it, and {code: "invalid"} only for a closed reader with a live signal.
func TestTextLineReaderReportsAnAbortBeforeTheClosedReader(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "lines.txt", []byte("one\n")))
	reader := must(env.OpenTextLineReader(background, "lines.txt"))
	mustDo(t, reader.Close(background))

	ctx, cancel := context.WithCancel(background)
	cancel()
	_, err := reader.ReadLine(ctx)
	expectFileError(t, err, durableenv.FileErrorAborted, filepath.Join(root, "lines.txt"))
	if message := fileError(t, err).Message; message != "aborted" {
		t.Fatalf("aborted ReadLine on a closed reader: message = %q, want %q", message, "aborted")
	}
	_, err = reader.ReadLine(background)
	expectFileError(t, err, durableenv.FileErrorInvalid, filepath.Join(root, "lines.txt"))
}

// Ports NodeTextLineReader.close (node.ts:439-449): `await this.file.close()` releases the opened file, and a second close does not touch it again.
func TestTextLineReaderCloseReleasesTheOpenedFile(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "lines.txt", []byte("one\n")))
	original := readFileAt
	t.Cleanup(func() { readFileAt = original })
	var opened *os.File
	readFileAt = func(file *os.File, chunk []byte, offset int64) (int, error) {
		opened = file
		return original(file, chunk, offset)
	}
	reader := must(env.OpenTextLineReader(background, "lines.txt"))
	must(reader.ReadLine(background))
	if opened == nil {
		t.Fatal("the reader never read its file")
	}
	if _, err := opened.Stat(); err != nil {
		t.Fatalf("the file was unusable before Close: %v", err)
	}
	mustDo(t, reader.Close(background))
	if _, err := opened.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("after Close the file's Stat = %v, want os.ErrClosed", err)
	}
	mustDo(t, reader.Close(background))
}

// Ports the catch of NodeTextLineReader.readLine (node.ts:434-436): a failing read is returned as the FileError for the file's path (toFileError,
// :118-142: EIO has no mapped code, so "unknown"), and since the position only advances after a successful read (:423-426), a later call reads
// the same bytes again.
func TestTextLineReaderReturnsAFailedReadAsAFileErrorAndRetriesItsBytes(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "lines.txt", []byte("one\ntwo\n")))
	original := readFileAt
	t.Cleanup(func() { readFileAt = original })
	fail := true
	readFileAt = func(file *os.File, chunk []byte, offset int64) (int, error) {
		if fail {
			return 0, &os.PathError{Op: "read", Path: file.Name(), Err: syscall.EIO}
		}
		return original(file, chunk, offset)
	}
	reader := must(env.OpenTextLineReader(background, "lines.txt"))
	defer func() { _ = reader.Close(background) }()

	line, err := reader.ReadLine(background)
	if line != nil {
		t.Fatalf("a failed read returned the line %+v", line)
	}
	expectFileError(t, err, durableenv.FileErrorUnknown, filepath.Join(root, "lines.txt"))

	fail = false
	for _, want := range []string{"one", "two"} {
		if line := must(reader.ReadLine(background)); line == nil || *line != (durableenv.TextLine{Text: want, Terminated: true}) {
			t.Fatalf("line after the failed read = %+v, want %q", line, want)
		}
	}
}
