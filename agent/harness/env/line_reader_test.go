package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

func openTestReader(t *testing.T, content []byte) (harness.TextLineReader, *NodeExecutionEnv, string) {
	t.Helper()
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "text.txt"), content, 0o600))
	reader := must(env.OpenTextLineReader(context.Background(), "text.txt"))
	t.Cleanup(func() { reader.Close(context.Background()) })
	return reader, env, root
}

func readAllLines(t *testing.T, reader harness.TextLineReader) []harness.TextLine {
	t.Helper()
	lines := []harness.TextLine{}
	for {
		line := must(reader.ReadLine(context.Background()))
		if line == nil {
			return lines
		}
		lines = append(lines, *line)
	}
}

func TestTextLineReaderDecodesUnicodeBlankLinesAndATornFinalLine(t *testing.T) {
	reader, _, _ := openTestReader(t, []byte("hé🙂\n\n\n終\ntorn"))
	want := []harness.TextLine{{Text: "hé🙂", Terminated: true}, {Text: "", Terminated: true}, {Text: "", Terminated: true}, {Text: "終", Terminated: true}, {Text: "torn"}}
	if got := readAllLines(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %+v", got)
	}
	if line := must(reader.ReadLine(context.Background())); line != nil {
		t.Fatalf("read after end = %+v", line)
	}
}

func TestTextLineReaderReadsAnEmptyFile(t *testing.T) {
	reader, _, _ := openTestReader(t, nil)
	if got := readAllLines(t, reader); len(got) != 0 {
		t.Fatalf("lines = %+v", got)
	}
}

func TestTextLineReaderDecodesMultibyteCharactersSplitAcrossChunks(t *testing.T) {
	first := strings.Repeat("a", 64*1024-1) + "🙂" + strings.Repeat("é", 40_000) + "\n"
	reader, _, _ := openTestReader(t, []byte(first+"終"))
	want := []harness.TextLine{{Text: strings.TrimSuffix(first, "\n"), Terminated: true}, {Text: "終"}}
	if got := readAllLines(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines differ: %d lines", len(got))
	}
}

func TestTextLineReaderReplacesMalformedAndIncompleteUTF8(t *testing.T) {
	reader, _, _ := openTestReader(t, []byte{0xff, 0x0a, 0xe2, 0x82})
	want := []harness.TextLine{{Text: "�", Terminated: true}, {Text: "�"}}
	if got := readAllLines(t, reader); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %+v", got)
	}
}

func TestTextLineReaderRejectsAnOpenWithAPreAbortedContext(t *testing.T) {
	env, root := newTestEnv(t)
	mustDo(t, os.WriteFile(filepath.Join(root, "text.txt"), []byte("one\n"), 0o600))
	_, err := env.OpenTextLineReader(abortedContext(), "text.txt")
	if code := fileErrorCode(t, err); code != harness.FileErrorAborted {
		t.Fatalf("code = %s", code)
	}
}

func TestTextLineReaderDoesNotConsumeABufferedLineWhenPreAborted(t *testing.T) {
	reader, _, _ := openTestReader(t, []byte("one\ntwo\n"))
	must(reader.ReadLine(context.Background()))
	_, err := reader.ReadLine(abortedContext())
	if code := fileErrorCode(t, err); code != harness.FileErrorAborted {
		t.Fatalf("code = %s", code)
	}
	if line := must(reader.ReadLine(context.Background())); line == nil || line.Text != "two" {
		t.Fatalf("line = %+v", line)
	}
}

// abortAfterFirstCheck is live for its first Err call and cancelled after, so
// cancellation lands while a read is in flight.
type abortAfterFirstCheck struct {
	context.Context
	checks atomic.Int32
}

func (ctx *abortAfterFirstCheck) Err() error {
	if ctx.checks.Add(1) == 1 {
		return nil
	}
	return context.Canceled
}

func TestTextLineReaderHonorsCancellationDuringAReadAndAllowsRetry(t *testing.T) {
	reader, _, _ := openTestReader(t, []byte(strings.Repeat("é", 70_000)+"\nlast"))
	_, err := reader.ReadLine(&abortAfterFirstCheck{Context: context.Background()})
	if code := fileErrorCode(t, err); code != harness.FileErrorAborted {
		t.Fatalf("code = %s", code)
	}
	var texts []string
	for _, line := range readAllLines(t, reader) {
		texts = append(texts, line.Text)
	}
	if !reflect.DeepEqual(texts, []string{strings.Repeat("é", 70_000), "last"}) {
		t.Fatalf("retried lines = %d", len(texts))
	}
}

func TestTextLineReaderClosesIdempotentlyAndRejectsLaterReads(t *testing.T) {
	reader, _, root := openTestReader(t, []byte("one\ntwo\n"))
	must(reader.ReadLine(context.Background()))
	reader.Close(abortedContext())
	reader.Close(context.Background())
	_, err := reader.ReadLine(context.Background())
	var fileErr *harness.FileError
	if !errors.As(err, &fileErr) || fileErr.Code != harness.FileErrorInvalid || fileErr.Message != "Text line reader is closed" || fileErr.Path != filepath.Join(root, "text.txt") {
		t.Fatalf("err = %#v", err)
	}
}

func TestTextLineReaderReturnsAFileErrorForAMissingFile(t *testing.T) {
	env, root := newTestEnv(t)
	_, err := env.OpenTextLineReader(context.Background(), "missing.txt")
	var fileErr *harness.FileError
	if !errors.As(err, &fileErr) || fileErr.Code != harness.FileErrorNotFound || fileErr.Path != filepath.Join(root, "missing.txt") {
		t.Fatalf("err = %#v", err)
	}
}
