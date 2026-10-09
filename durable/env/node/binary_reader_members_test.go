package node

import (
	"errors"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// upstream: packages/durable/src/testing/env-conformance.ts "binary reader reads byte ranges of the opened file": Info is the opened file's metadata, Read returns up to length bytes at offset and fewer only at the end, a bad range is invalid, Close is idempotent and every later call is invalid.
// Pi source: packages/durable/src/testing/env-conformance.ts:125-142 (info, read, close).
// mutation-checked: negating the condition at binary_reader.go:49, :68, :81, :133, :168 or :179 fails it.
func TestBinaryReaderInfoReadAndCloseFollowTheOpenedFile(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "data.txt", []byte("hello world")))
	reader := must(env.OpenBinaryReader(background, "data.txt", nil))
	info := must(reader.Info(background))
	if info.Name != "data.txt" || info.Kind != durableenv.FileKindFile || info.Size != 11 {
		t.Fatalf("Info = %+v, want data.txt, file, 11 bytes", info)
	}
	for _, c := range []struct {
		offset, length int64
		want           string
	}{{0, 5, "hello"}, {6, 100, "world"}, {11, 4, ""}, {50, 1, ""}, {3, 0, ""}} {
		if got := string(must(reader.Read(background, c.offset, c.length))); got != c.want {
			t.Errorf("Read(%d, %d) = %q, want %q", c.offset, c.length, got, c.want)
		}
	}
	for _, c := range [][2]int64{{-1, 1}, {0, -1}, {9007199254740992, 1}} {
		if _, err := reader.Read(background, c[0], c[1]); !isFileError(err, durableenv.FileErrorInvalid) {
			t.Errorf("Read(%d, %d) error = %v, want an invalid FileError", c[0], c[1], err)
		}
	}
	mustDo(t, reader.Close(background))
	mustDo(t, reader.Close(background))
	if _, err := reader.Read(background, 0, 1); !isFileError(err, durableenv.FileErrorInvalid) {
		t.Errorf("Read after Close error = %v, want an invalid FileError", err)
	}
	if _, err := reader.Info(background); !isFileError(err, durableenv.FileErrorInvalid) {
		t.Errorf("Info after Close error = %v, want an invalid FileError", err)
	}
}

// upstream: packages/durable/src/testing/env-conformance.ts "binary reader keeps reading the file it opened after a rename": Info and Read describe the opened file, not whatever its path names now.
// Pi source: packages/durable/src/testing/env-conformance.ts:185-196 (info after rename).
// mutation-checked: negating the condition at binary_reader.go:81 fails it.
func TestBinaryReaderInfoDescribesTheOpenedFileAfterARename(t *testing.T) {
	env, _ := newTestEnv(t)
	mustDo(t, env.WriteFile(background, "a.txt", []byte("one")))
	reader := must(env.OpenBinaryReader(background, "a.txt", nil))
	defer func() { _ = reader.Close(background) }()
	mustDo(t, env.RenameFile(background, "a.txt", "b.txt"))
	mustDo(t, env.WriteFile(background, "a.txt", []byte("replacement")))
	if info := must(reader.Info(background)); info.Size != 3 {
		t.Fatalf("Info.Size = %d, want the opened file's 3 bytes", info.Size)
	}
	if got := string(must(reader.Read(background, 0, 10))); got != "one" {
		t.Fatalf("Read = %q, want %q", got, "one")
	}
}

// upstream: packages/durable/src/testing/env-conformance.ts "binary reader scans lines like decoding the whole file": a byte-order mark, an invalid sequence before a newline, an empty line, a later U+FEFF and no final newline. Expected values are those of TextDecoder over `bytes` (a leading BOM is dropped and not counted; a truncated sequence is one U+FFFD; a later U+FEFF stays).
// Pi source: packages/durable/src/testing/env-conformance.ts:144-183 (scanLines).
// mutation-checked: negating the condition at binary_reader.go:116 fails it.
func TestBinaryReaderScanLinesMatchesDecodingTheWholeFile(t *testing.T) {
	env, _ := newTestEnv(t)
	bytes := []byte{0xef, 0xbb, 0xbf, 0x61, 0x0a, 0xe2, 0x82, 0x0a, 0x0a, 0xef, 0xbb, 0xbf, 0x62, 0x0a, 0xc3, 0xa9}
	mustDo(t, env.WriteFile(background, "lines.txt", bytes))
	reader := must(env.OpenBinaryReader(background, "lines.txt", nil))
	defer func() { _ = reader.Close(background) }()
	end := func(v int64) *int64 { return &v }
	for _, c := range []struct {
		name       string
		start      int64
		end        *int64
		want       durableenv.LineScan
		wantFields string
	}{
		{"whole file", 0, nil, durableenv.LineScan{Newlines: 4, Start: 0, End: 16, FirstLineEnd: 4, LastLineStart: 14, SelectedBytes: 14, FirstLineBytes: 1}, ""},
		{"first line", 0, end(1), durableenv.LineScan{Newlines: 4, Start: 0, End: 4, FirstLineEnd: 4, LastLineStart: 0, SelectedBytes: 1, FirstLineBytes: 1}, ""},
		{"invalid sequence and empty line", 1, end(3), durableenv.LineScan{Newlines: 4, Start: 5, End: 8, FirstLineEnd: 7, LastLineStart: 8, SelectedBytes: 4, FirstLineBytes: 3}, ""},
		{"empty line", 2, end(3), durableenv.LineScan{Newlines: 4, Start: 8, End: 8, FirstLineEnd: 8, LastLineStart: 8, SelectedBytes: 0, FirstLineBytes: 0}, ""},
		{"later BOM kept", 3, nil, durableenv.LineScan{Newlines: 4, Start: 9, End: 16, FirstLineEnd: 13, LastLineStart: 14, SelectedBytes: 7, FirstLineBytes: 4}, ""},
		{"bounded past the end", 4, end(9), durableenv.LineScan{Newlines: 4, Start: 14, End: 16, FirstLineEnd: 16, LastLineStart: 14, SelectedBytes: 2, FirstLineBytes: 2}, ""},
	} {
		got, err := reader.ScanLines(background, durableenv.ScanLinesOptions{StartLine: c.start, EndLine: c.end})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: ScanLines = %+v, want %+v", c.name, got, c.want)
		}
	}
	past := must(reader.ScanLines(background, durableenv.ScanLinesOptions{StartLine: 9}))
	if past.Start != 16 || past.End != 16 || past.SelectedBytes != 0 {
		t.Errorf("a selection past the last line = %+v, want empty at the end of the file", past)
	}
	if _, err := reader.ScanLines(background, durableenv.ScanLinesOptions{StartLine: 2, EndLine: end(2)}); !isFileError(err, durableenv.FileErrorInvalid) {
		t.Errorf("an empty line range error = %v, want an invalid FileError", err)
	}
}

func isFileError(err error, code durableenv.FileErrorCode) bool {
	fileErr, ok := errors.AsType[*durableenv.FileError](err)
	return ok && fileErr.Code == code
}
