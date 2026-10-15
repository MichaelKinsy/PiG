package env

import "testing"

// packages/durable/src/env/decode.ts:13 startsWithBom: exactly the bytes EF BB BF drop as a byte-order mark; one differing byte in any of the three positions does not.
func TestStartsWithBomChecksEachOfTheThreeBytes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []byte
		want  bool
	}{
		{"the mark", []byte{0xef, 0xbb, 0xbf, 'a'}, true},
		{"the mark alone", []byte{0xef, 0xbb, 0xbf}, true},
		{"first byte differs", []byte{0xee, 0xbb, 0xbf}, false},
		{"second byte differs", []byte{0xef, 0xba, 0xbf}, false},
		{"third byte differs", []byte{0xef, 0xbb, 0xbe}, false},
		{"zero bytes", []byte{0, 0, 0}, false},
		{"too short", []byte{0xef, 0xbb}, false},
		{"empty", nil, false},
	} {
		if got := StartsWithBom(tc.input); got != tc.want {
			t.Errorf("%s: StartsWithBom(% x) = %v, want %v", tc.name, tc.input, got, tc.want)
		}
	}
}

// line-scan.ts:85-97 finish(): a selection that starts past the last line is empty at the end of the file, and its last line starts there too.
func TestLineScannerSelectionPastTheLastLineIsEmptyAtTheEnd(t *testing.T) {
	scanner, err := NewLineScanner(5, nil)
	if _, ok := any(scanner).(*LineScanner); err != nil || !ok {
		t.Fatal(err)
	}
	scanner.Push([]byte("a\nb\n"))
	want := LineScan{Newlines: 2, Start: 4, End: 4, FirstLineEnd: 4, LastLineStart: 4}
	if got := scanner.Finish(); got != want {
		t.Fatalf("Finish() = %+v, want %+v", got, want)
	}
}

// decode.ts StreamDecoder: only a leading U+FEFF at the start of the stream is a byte-order mark; the same decoder keeps one that follows the start.
func TestStreamDecoderTrimsOnlyTheMarkAtTheStart(t *testing.T) {
	decoder := NewStreamDecoder()
	if _, ok := any(decoder).(*StreamDecoder); !ok {
		t.Fatal("NewStreamDecoder is not a *StreamDecoder")
	}
	if got := decoder.Decode([]byte("\xef\xbb\xbfab")); got != "ab" {
		t.Fatalf("first chunk = %q, want the mark dropped", got)
	}
	if got := decoder.Decode([]byte("\xef\xbb\xbfcd")); got != "\ufeffcd" {
		t.Fatalf("second chunk = %q, want its U+FEFF kept", got)
	}
}
