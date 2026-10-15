package codingagent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// parallelIndexFixture is a session file with every kind of line the index handles: messages of each role with tool calls, custom messages,
// bash executions with long commands, labels, names, model changes and compactions, and the lines the load skips or keeps without an entry
// (malformed JSON, falsy values, records that are not entries, a second header), with CRLF and blank lines and lines longer than a batch.
func parallelIndexFixture(t *testing.T) []byte {
	t.Helper()
	path := compactedSessionFile(t, 60, 6)
	session, err := NewSessionManagerWithDir(filepath.Dir(path), filepath.Dir(path)).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := session.AppendMessage(mkUserMsg("look at " + strings.Repeat("the long file ", 4000)))
	if err != nil {
		t.Fatal(err)
	}
	label := "kept"
	for _, appendEntry := range []func() error{
		func() error {
			_, err := session.AppendCustomMessageEntry("note", strings.Repeat("custom text ", 300), true, nil)
			return err
		},
		func() error {
			_, err := session.AppendBashExecution(BashExecutionMessage{Command: "echo " + strings.Repeat("x", 3000), Output: "x", Timestamp: 1767225601000})
			return err
		},
		func() error { _, err := session.AppendLabelChange(userID, &label); return err },
		func() error { _, err := session.AppendSessionInfo("parallel"); return err },
		func() error { _, err := session.AppendModelChange("p", "m2"); return err },
	} {
		if err := appendEntry(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	leaf := *session.GetLeafID()
	extra := []string{
		fmt.Sprintf(`{"type":"message","id":"tc000001","parentId":%q,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"text","text":"calling"},{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"a"}}],"api":"openai-completions","provider":"p","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":1767225601000}}`, leaf),
		`{"type":"message","id":"tr000001","parentId":"tc000001","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":1767225602000}}` + "\r",
		"",
		`{"type":"message","type":"message","id":"dup00001","parentId":"tr000001","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"user","content":"twice typed","timestamp":1}}`,
		`{"type":"message","id":"odd00001","parentId":"dup00001","timestamp":"2026-01-01T00:00:04Z","message":{"role":"user","content":"odd timestamp","timestamp":1}}`,
		`{"type":"message","id":"num00001","parentId":"odd00001","timestamp":5,"message":{"role":"user","content":"numeric timestamp","timestamp":1}}`,
		`{"type":"message","id":"cut"`,
		`[1]`,
		`true`,
		`"text"`,
		`0`,
		`null`,
		`{"type":"custom","customType":"x","id":7,"parentId":"num00001"}`,
		`{"type":"session","version":3,"id":"second","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`,
		"\u00a0{\"type\":\"label\"}",
		`{"type":"message","id":"last0001","parentId":"num00001","timestamp":"2026-01-01T00:00:05.000Z","message":{"role":"user","content":"last","timestamp":1}}`,
	}
	return append(data, strings.Join(extra, "\n")+"\n"...)
}

// The index a parallel load builds is the serial load's: the same slots with the same cold parts, stats, labels, names and leaf, and after
// the first /tree search the same search texts in the same arena places, for any batch size and number of workers.
func TestParallelIndexMatchesTheSerialIndex(t *testing.T) {
	data := parallelIndexFixture(t)
	path := filepath.Join(t.TempDir(), "s.jsonl")
	load := func(content []byte, batching lineBatching) (*Session, error) {
		t.Helper()
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return streamSessionFileBatched(path, batching)
	}
	for _, content := range [][]byte{data, data[:len(data)-1], []byte(strings.ReplaceAll(string(data), "\n", "\r\n"))} {
		serial, err := load(content, lineBatching{workers: 1})
		if err != nil {
			t.Fatal(err)
		}
		filled, err := load(content, lineBatching{workers: 1})
		if err != nil {
			t.Fatal(err)
		}
		filled.indexSearchTexts()
		if len(serial.slots) < 100 || filled.searchTexts.size == 0 || len(serial.labels) == 0 || serial.name == "" {
			t.Fatalf("the fixture indexes %d slots, %d search bytes, %d labels, name %q", len(serial.slots), filled.searchTexts.size, len(serial.labels), serial.name)
		}
		// The load copies the lines into the snapshot as it reads them, so the snapshot holds the file the load read.
		if serial.snapshot == nil {
			t.Fatal("the serial load has no snapshot")
		}
		if file, err := os.ReadFile(path); err != nil {
			t.Fatal(err)
		} else if !bytes.Equal(snapshotBytes(t, serial.snapshot), file) {
			t.Fatal("the serial load's snapshot differs from the session file")
		}
		for _, batching := range []lineBatching{
			{batchBytes: 1, depth: 1, workers: 2},
			{batchBytes: 700, depth: 3, workers: 3},
			{batchBytes: 64 << 10, depth: 4, workers: 8},
			defaultLineBatching(),
		} {
			parallel, err := load(content, batching)
			if err != nil {
				t.Fatalf("%+v: %v", batching, err)
			}
			if !sameIndex(t, parallel, serial) {
				for i := range min(len(parallel.slots), len(serial.slots)) {
					if !reflect.DeepEqual(parallel.slots[i], serial.slots[i]) {
						t.Fatalf("%+v: slot %d: %+v, serial %+v", batching, i, parallel.slots[i], serial.slots[i])
					}
				}
				t.Fatalf("%+v: the parallel index differs from the serial one (%d and %d slots)", batching, len(parallel.slots), len(serial.slots))
			}
			parallel.indexSearchTexts()
			if !sameIndex(t, parallel, filled) {
				t.Fatalf("%+v: the parallel index's search texts differ from the serial one's", batching)
			}
		}
	}
}

// sameIndex reports whether two loads hold the same index. A time value is compared by its bits, since an unparsable timestamp's NaN is unequal
// to itself. Each load has a snapshot of its own, so the snapshots are compared by their bytes.
func sameIndex(t *testing.T, a, b *Session) bool {
	t.Helper()
	if len(a.slots) != len(b.slots) {
		return false
	}
	if snapA, snapB := snapshotBytes(t, a.snapshot), snapshotBytes(t, b.snapshot); !bytes.Equal(snapA, snapB) {
		t.Errorf("snapshot of %d bytes, want %d bytes", len(snapA), len(snapB))
		return false
	}
	snapA, snapB := a.snapshot, b.snapshot
	a.snapshot, b.snapshot = nil, nil
	defer func() { a.snapshot, b.snapshot = snapA, snapB }()
	// Each id index hashes with a seed of its own, so two indexes of the same ids lay out differently: they are compared by what they find.
	if a.byID.n != b.byID.n {
		t.Errorf("the id index holds %d ids, want %d", a.byID.n, b.byID.n)
		return false
	}
	for i := range a.slots {
		id := a.slots[i].id
		if gotSlot, got := a.slotOf(id); !got || gotSlot != b.slotOrZero(id) {
			t.Errorf("id %q finds slot %d (%v), want %d", id, gotSlot, got, b.slotOrZero(id))
			return false
		}
	}
	byIDA, byIDB := a.byID, b.byID
	a.byID, b.byID = idIndex{}, idIndex{}
	defer func() { a.byID, b.byID = byIDA, byIDB }()
	for i := range a.slots {
		if math.Float64bits(a.slots[i].ts) != math.Float64bits(b.slots[i].ts) {
			t.Errorf("slot %d: time value %v, want %v", i, a.slots[i].ts, b.slots[i].ts)
			return false
		}
	}
	tsA, tsB := make([]float64, len(a.slots)), make([]float64, len(b.slots))
	for i := range a.slots {
		tsA[i], tsB[i] = a.slots[i].ts, b.slots[i].ts
		a.slots[i].ts, b.slots[i].ts = 0, 0
	}
	same := reflect.DeepEqual(a, b)
	for i := range a.slots {
		a.slots[i].ts, b.slots[i].ts = tsA[i], tsB[i]
	}
	return same
}

// snapshotBytes is the content of a load's snapshot, or nil when it has none.
func snapshotBytes(t *testing.T, snapshot *sessionSnapshot) []byte {
	t.Helper()
	if snapshot == nil {
		return nil
	}
	out := make([]byte, snapshot.size)
	if _, err := snapshot.ReadAt(out, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	return out
}

// A parallel load ends as the serial one does when the file has no header or needs migration, and leaves no goroutine behind.
func TestParallelIndexStopsAsTheSerialLoadDoes(t *testing.T) {
	data := parallelIndexFixture(t)
	header, rest, _ := strings.Cut(string(data), "\n")
	for name, content := range map[string]string{
		"no header":    rest,
		"old version":  strings.Replace(header, `"version":3`, `"version":2`, 1) + "\n" + rest,
		"empty":        "",
		"blank lines":  "\n\n",
		"header alone": header + "\n",
	} {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		results := make([]string, 0, 2)
		for _, batching := range []lineBatching{{workers: 1}, {batchBytes: 1, depth: 1, workers: 4}} {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			session, err := streamSessionFileBatched(path, batching)
			results = append(results, fmt.Sprintf("%v %v", session != nil, err))
		}
		if results[0] != results[1] {
			t.Errorf("%s: serial load %s, parallel load %s", name, results[0], results[1])
		}
	}
	waitForGoroutines(t)
}

// errAfterReader returns data and then err.
type errAfterReader struct {
	data []byte
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// forEachPreparedLine visits the lines forEachJSONLLineOffset yields, in order, with their preparations, and returns the first error in line
// order: the visit's, or then the read's.
func TestForEachPreparedLineVisitsInOrderAndReturnsTheFirstError(t *testing.T) {
	data := parallelIndexFixture(t)
	readErr := errors.New("read failed")
	visitErr := errors.New("visit failed")
	type visited struct {
		line string
		off  int64
	}
	run := func(batching lineBatching, failAt int) ([]visited, error) {
		var lines []visited
		err := forEachPreparedLine(&errAfterReader{data: data, err: readErr}, batching, func(line []byte, off int64, prepared *preparedLine) error {
			if batching.workers > 1 {
				if want := prepareIndexLine(line); !reflect.DeepEqual(*prepared, want) {
					t.Fatalf("line at %d: preparation %+v, want %+v", off, *prepared, want)
				}
			} else if prepared != nil {
				t.Fatal("a serial visit received a preparation")
			}
			if len(lines) == failAt {
				return visitErr
			}
			lines = append(lines, visited{string(line), off})
			return nil
		})
		return lines, err
	}
	want, err := run(lineBatching{workers: 1}, -1)
	if !errors.Is(err, readErr) || len(want) < 100 {
		t.Fatalf("serial: %d lines, error %v", len(want), err)
	}
	for _, batching := range []lineBatching{{batchBytes: 1, depth: 1, workers: 2}, {batchBytes: 900, depth: 2, workers: 5}} {
		got, err := run(batching, -1)
		if !errors.Is(err, readErr) || !slices.Equal(got, want) {
			t.Fatalf("%+v: %d lines, error %v; want %d lines and the read error", batching, len(got), err, len(want))
		}
		for _, failAt := range []int{0, 1, len(want) / 2, len(want) - 1} {
			got, err := run(batching, failAt)
			if !errors.Is(err, visitErr) || !slices.Equal(got, want[:failAt]) {
				t.Fatalf("%+v: a visit failing at line %d gave %d lines and %v", batching, failAt, len(got), err)
			}
		}
	}
	waitForGoroutines(t)
}

// gatedReader returns first, then blocks until gate closes, and then ends; readDone is set when that second read returns.
type gatedReader struct {
	first    []byte
	gate     chan struct{}
	readDone atomic.Bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if len(r.first) > 0 {
		n := copy(p, r.first)
		r.first = r.first[n:]
		return n, nil
	}
	<-r.gate
	r.readDone.Store(true)
	return 0, io.EOF
}

// forEachPreparedLine returns only after its reader has returned, even when the visit ends the walk while a read is outstanding.
func TestForEachPreparedLineJoinsTheReadBeforeReturning(t *testing.T) {
	reader := &gatedReader{first: []byte("{\"a\":1}\n{\"b\":2}\n"), gate: make(chan struct{})}
	stopped := errors.New("stopped")
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(reader.gate)
	}()
	err := forEachPreparedLine(reader, lineBatching{batchBytes: 1, depth: 1, workers: 2}, func([]byte, int64, *preparedLine) error { return stopped })
	if !errors.Is(err, stopped) {
		t.Fatalf("error %v, want the visit's", err)
	}
	if !reader.readDone.Load() {
		t.Fatal("forEachPreparedLine returned while its read was still running")
	}
}

// waitForGoroutines fails when goroutines the loads started are still running.
func waitForGoroutines(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		buf := make([]byte, 1<<20)
		stacks := string(buf[:runtime.Stack(buf, true)])
		if !strings.Contains(stacks, "forEachPreparedLine") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines of forEachPreparedLine are still running:\n%s", stacks)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// countingReader reads data and counts the bytes it has handed out.
type countingReader struct {
	data []byte
	read atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	r.read.Add(int64(n))
	return n, nil
}

// Once the visit fails (a file with no header, or one that needs migration), forEachPreparedLine stops reading: the read-ahead stays within its
// bound instead of running to the end of the file.
func TestForEachPreparedLineStopsReadingWhenTheVisitFails(t *testing.T) {
	line := []byte(`{"pad":"` + strings.Repeat("x", 1000) + "\"}\n")
	reader := &countingReader{data: bytes.Repeat(line, 16<<10)}
	total := len(reader.data)
	stopped := errors.New("stopped")
	batching := lineBatching{batchBytes: 4 << 10, depth: 2, workers: 2}
	err := forEachPreparedLine(reader, batching, func([]byte, int64, *preparedLine) error { return stopped })
	if !errors.Is(err, stopped) {
		t.Fatalf("error %v, want the visit's", err)
	}
	// The line reader buffers 64 KiB; the read-ahead holds at most depth+2 batches of batchBytes.
	if read, bound := reader.read.Load(), int64(64<<10+(batching.depth+3)*batching.batchBytes+len(line)); read > bound {
		t.Fatalf("read %d of %d bytes after the visit failed, want at most %d", read, total, bound)
	}
}
