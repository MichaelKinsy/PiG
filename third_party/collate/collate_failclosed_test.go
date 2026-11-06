package collate

import (
	"bytes"
	"compress/flate"
	"io"
	"fmt"
	"strings"
	"sync"
	"testing"
)

var pristineTables = compressedTables

// useTables swaps tables.bin for data, resets the loader, and puts the real tables back when the test ends.
func useTables(t *testing.T, data string) {
	t.Helper()
	t.Cleanup(func() {
		compressedTables = pristineTables
		tablesLoaded = sync.Once{}
		tablesFailed = nil
		loadTables()
	})
	compressedTables = data
	tablesLoaded = sync.Once{}
	tablesFailed = nil
}

// panicOf returns what f panics with, or nil.
func panicOf(f func()) (v any) {
	defer func() { v = recover() }()
	f()
	return nil
}

func TestCorruptTablesFailClosedOnEveryCall(t *testing.T) {
	good := compressedTables
	cases := []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "corrupt"},
		{"half", good[:len(good)/2], "corrupt: unexpected EOF"},
		{"last byte cut", good[:len(good)-1], "corrupt"},
		{"garbage", strings.Repeat("\xff", 64), "corrupt: flate: corrupt input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTables(t, tc.data)
			c := New()
			first := panicOf(func() { c.CompareString("a", "b") })
			msg, _ := first.(string)
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("first call panicked with %v, want %q", first, tc.want)
			}
			for i, call := range []func(){
				func() { c.CompareString("a", "b") },
				func() { c.AppendKey(nil, "a") },
				func() { c.CompareString("é", "e") },
			} {
				if got := panicOf(call); got != first {
					t.Fatalf("call %d after the failure panicked with %v, want %v", i+2, got, first)
				}
			}
		})
	}
}

func TestTruncatedLastByteIsNotReportedAsExtraData(t *testing.T) {
	useTables(t, compressedTables[:len(compressedTables)-1])
	if msg := fmt.Sprint(panicOf(loadTables)); strings.Contains(msg, "holds more") {
		t.Fatalf("a truncated stream was reported as %q", msg)
	}
}

func TestExtraDataInTablesIsReported(t *testing.T) {
	// A second DEFLATE block cannot follow the final one, so build the extra data inside the stream: decode, append a plane byte, recompress.
	useTables(t, withExtraByte(t, compressedTables))
	if msg := fmt.Sprint(panicOf(loadTables)); !strings.Contains(msg, "holds more than tables.go declares") {
		t.Fatalf("got %q", msg)
	}
}

func TestCorruptByteInTablesPanics(t *testing.T) {
	// Flip every 997th byte of the stream; each damaged stream either decodes to something (DEFLATE has no checksum) or panics, and a panic stays.
	good := compressedTables
	for i := 0; i < len(good); i += 997 {
		b := []byte(good)
		b[i] ^= 0x55
		useTables(t, string(b))
		if first := panicOf(loadTables); first != nil {
			if again := panicOf(loadTables); again != first {
				t.Fatalf("byte %d: first panic %v, later %v", i, first, again)
			}
		}
	}
}

func TestConcurrentFirstUseOfCorruptTablesAllPanic(t *testing.T) {
	useTables(t, compressedTables[:len(compressedTables)/2])
	var wg sync.WaitGroup
	panics := make([]any, 16)
	for i := range panics {
		wg.Go(func() { panics[i] = panicOf(func() { New().CompareString("a", "b") }) })
	}
	wg.Wait()
	for i, p := range panics {
		if p == nil || p != panics[0] {
			t.Fatalf("goroutine %d panicked with %v, goroutine 0 with %v", i, p, panics[0])
		}
	}
}

func TestCallAfterRecoveredPanicStillPanics(t *testing.T) {
	useTables(t, compressedTables[:len(compressedTables)/2])
	c := New()
	// The agent recovers tool panics; the next comparison in the process must not run on half-filled arrays.
	for range 3 {
		if panicOf(func() { c.CompareString("b", "a") }) == nil {
			t.Fatal("comparison returned after the tables failed to load")
		}
	}
}

// withExtraByte inflates data, appends one byte, and deflates it again.
func withExtraByte(t *testing.T, data string) string {
	t.Helper()
	r := flate.NewReader(strings.NewReader(data))
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w, _ := flate.NewWriter(&out, flate.BestCompression)
	w.Write(raw)
	w.Write([]byte{0})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
