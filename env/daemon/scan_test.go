package daemon

// Ports packages/env/daemon/src/scan.rs tests. The daemon scans with Durable's LineScanner.

import (
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func scan(t *testing.T, file []byte, start int64, end *int64, chunk int) durableenv.LineScan {
	t.Helper()
	scanner, err := durableenv.NewLineScanner(start, end)
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(file); offset += max(chunk, 1) {
		scanner.Push(file[offset:min(len(file), offset+max(chunk, 1))])
	}
	return scanner.Finish()
}

func TestScanLocatesAndMeasuresLinesLikeWholeFileDecoding(t *testing.T) {
	// BOM, "a", invalid sequence, empty line, a later U+FEFF, and no final newline.
	file := []byte{0xef, 0xbb, 0xbf, 0x61, 0x0a, 0xe2, 0x82, 0x0a, 0x0a, 0xef, 0xbb, 0xbf, 0x62, 0x0a, 0xc3, 0xa9}
	for chunk := 1; chunk < 6; chunk++ {
		all := scan(t, file, 0, nil, chunk)
		// "a\n\ufffd\n\n\ufeffb\né" is 1+1+3+1+1+3+1+1+2 = 14 bytes.
		if all.Newlines != 4 || all.SelectedBytes != 14 || all.FirstLineBytes != 1 {
			t.Fatalf("chunk %d: %+v", chunk, all)
		}
		three := int64(3)
		middle := scan(t, file, 1, &three, chunk)
		if middle.Start != 5 || middle.End != 8 || middle.SelectedBytes != 4 || middle.LastLineStart != 8 {
			t.Fatalf("chunk %d: middle %+v", chunk, middle)
		}
		if past := scan(t, file, 9, nil, chunk); past.Start != 16 {
			t.Fatalf("chunk %d: past %+v", chunk, past)
		}
	}
}
