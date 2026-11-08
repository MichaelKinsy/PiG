package sync

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// bigCommit is the outbox commit of one row whose pad is pad copies of "é" (two bytes, one UTF-16 unit each).
func bigCommit(t *testing.T, pad int) store.OutboxCommit {
	t.Helper()
	s := sessionStore(t)
	commitAll(t, s, rows{store.Row{"id": "big", "n": int64(1), "pad": strings.Repeat("é", pad)}})
	commits, err := ReadOutbox(background, s, LogStart, 100)
	noError(t, err)
	return commits[len(commits)-1]
}

func lineUnits(t *testing.T, commit store.OutboxCommit) int {
	t.Helper()
	line, err := EncodeLogLine(commit)
	noError(t, err)
	return jsstring.Length(string(line))
}

func isRangeError(t *testing.T, err error) {
	t.Helper()
	var rangeError *RangeError
	if !errors.As(err, &rangeError) || err.Error() != "Invalid string length" {
		t.Fatalf("err = %v, want Pi's RangeError: Invalid string length", err)
	}
}

func setStringLimit(t *testing.T, limit int) {
	t.Helper()
	was := stringLimit
	stringLimit = limit
	t.Cleanup(func() { stringLimit = was })
}

func TestStringLimitIsV8s(t *testing.T) {
	if MaxStringLength != 536870888 || stringLimit != 536870888 {
		t.Fatalf("limit = %d / %d, want V8's 536870888 UTF-16 units", MaxStringLength, stringLimit)
	}
}

// A line is bounded as Pi's `rest += decode(chunk)` is: by V8's string length, in UTF-16 units. Any line an upload writes
// must pull back, so the encoder refuses what the decoder would.
func TestLineLimit(t *testing.T) {
	commit := bigCommit(t, 4000)
	units := lineUnits(t, commit)
	line, err := EncodeLogLine(commit)
	noError(t, err)
	if len(line) <= units {
		t.Fatalf("a line of %d bytes and %d units: the pad is no multi-byte", len(line), units)
	}

	t.Run("decodes a line of exactly the limit in units, though it has more bytes", func(t *testing.T) {
		setStringLimit(t, units)
		commits, err := collect(t, DecodeBatch(gzipped(t, string(line))))
		noError(t, err)
		if len(commits) != 1 || commits[0].Seq != commit.Seq {
			t.Fatalf("commits = %+v", commits)
		}
	})

	t.Run("fails a line one unit past the limit with Pi's RangeError", func(t *testing.T) {
		setStringLimit(t, units-1)
		_, err := collect(t, DecodeBatch(gzipped(t, string(line))))
		isRangeError(t, err)
	})

	t.Run("fails an unfinished line past the limit before its newline arrives", func(t *testing.T) {
		setStringLimit(t, units)
		_, err := collect(t, DecodeBatch(gzipped(t, strings.Repeat("x", units+1))))
		isRangeError(t, err)
	})

	t.Run("the encoder refuses a line past the limit", func(t *testing.T) {
		setStringLimit(t, units-1)
		body := EncodeBatch(SliceCommits([]store.OutboxCommit{commit}))
		_, _, err := body.Next(background)
		isRangeError(t, err)
	})

	t.Run("a line the encoder writes pulls back", func(t *testing.T) {
		setStringLimit(t, units+len(line))
		body := EncodeBatch(SliceCommits([]store.OutboxCommit{commit}))
		commits, err := collect(t, DecodeBatch(body))
		noError(t, err)
		if len(commits) != 1 {
			t.Fatalf("commits = %d, want 1", len(commits))
		}
	})

	t.Run("an upload of a commit past the limit fails and writes nothing", func(t *testing.T) {
		setStringLimit(t, units-1)
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		laptop.commit([]rows{{store.Row{"id": "big", "n": int64(1), "pad": strings.Repeat("é", 4000)}}})
		_, err := UploadSession(background, UploadOptions{SessionID: session, Device: "laptop", Source: laptop.store(), Store: remote, State: laptop.state})
		isRangeError(t, err)
		same(t, "batches", len(listing(t, remote, session).Batches), 0)
	})
}

// Pi counts a batch's size in UTF-16 units: `JSON.stringify(commit.writes).length + 64` to end a batch and to size an
// outbox page, `text.length + 1` for a line a pull applies.
func TestSizesCountUTF16Units(t *testing.T) {
	const pad = 256 * 1024
	commit := bigCommit(t, pad)
	text, err := appendWrites(nil, commit)
	noError(t, err)
	want := jsstring.Length(string(text)) + 64

	t.Run("a commit's encoded size", func(t *testing.T) {
		got, err := encodedSize(commit)
		noError(t, err)
		if got != want || got >= len(text) {
			t.Fatalf("encodedSize = %d, want %d units (%d bytes)", got, want, len(text))
		}
	})

	t.Run("an outbox page's next limit", func(t *testing.T) {
		s := sessionStore(t)
		batches := make([]rows, 4)
		for index := range batches {
			batches[index] = rows{store.Row{"id": string(rune('a' + index)), "n": int64(index), "pad": strings.Repeat("é", pad)}}
		}
		commitAll(t, s, batches...)
		pages := &outboxPages{store: s, after: LogStart, limit: 100}
		page, err := pages.next(background)
		noError(t, err)
		units, bytes := 0, 0
		for _, each := range page {
			size, err := encodedSize(each)
			noError(t, err)
			units += size
			text, err := appendWrites(nil, each)
			noError(t, err)
			bytes += len(text) + 64
		}
		wantLimit := int64(pageBytes * len(page) / units)
		if byteLimit := int64(pageBytes * len(page) / bytes); wantLimit == byteLimit || pages.limit != wantLimit {
			t.Fatalf("limit = %d, want %d by units (%d by bytes)", pages.limit, wantLimit, byteLimit)
		}
	})

	t.Run("a line a pull applies", func(t *testing.T) {
		lines := DecodeBatchLines(EncodeBatch(SliceCommits([]store.OutboxCommit{commit})))
		line, ok, err := lines.Next(background)
		noError(t, err)
		encoded, encodeErr := EncodeLogLine(commit)
		noError(t, encodeErr)
		if !ok || line.Bytes != jsstring.Length(string(encoded)) {
			t.Fatalf("Bytes = %d, want %d units", line.Bytes, jsstring.Length(string(encoded)))
		}
	})

	t.Run("an upload ends its batches as Pi does", func(t *testing.T) {
		remote := newRemote(t)
		laptop := newDevice(t, "laptop", remote)
		batches := make([]rows, 24)
		for index := range batches {
			batches[index] = rows{store.Row{"id": "k" + string(rune('a'+index)), "n": int64(index), "pad": strings.Repeat("é", 512*1024)}}
		}
		laptop.commit(batches)
		laptop.upload()
		if slots := listing(t, remote, session).Batches; len(slots) != 2 {
			t.Fatalf("slots = %+v, want 2 (15 commits of 512 Ki units fill an 8 MiB batch)", slots)
		}
	})
}
