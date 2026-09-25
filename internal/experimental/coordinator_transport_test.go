package experimental

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadControlLinesFraming(t *testing.T) {
	input := io.MultiReader(strings.NewReader("{\"text\":\""), strings.NewReader("\xc3"), strings.NewReader("\xa9\xf0"), strings.NewReader("\x9f\x98\x80\"}\r\nnull\n{\"unfinished\":"))
	var lines []string
	err := readControlLines(input, func(line json.RawMessage) error { lines = append(lines, string(line)); return nil })
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "{\"text\":\"é😀\"}\r" || lines[1] != "null" {
		t.Fatalf("lines: %q", lines)
	}
	for _, invalid := range []string{"\n", "not JSON\n", "{\n"} {
		if err := readControlLines(strings.NewReader(invalid), func(json.RawMessage) error { t.Fatal("invalid frame delivered"); return nil }); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
}

type spaceReader struct{}

func (spaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestReadControlLinesByteLimit(t *testing.T) {
	for _, extra := range []int{0, 1} {
		input := io.MultiReader(strings.NewReader("0"), io.LimitReader(spaceReader{}, int64(MaxControlLineBytes-2+extra)), strings.NewReader("\n"))
		delivered := false
		err := readControlLines(input, func(json.RawMessage) error { delivered = true; return io.EOF })
		if extra == 0 && (!delivered || !errors.Is(err, io.EOF)) {
			t.Fatalf("boundary not delivered: %v", err)
		}
		if extra != 0 && (delivered || err == nil || err.Error() != "Coordinator message is too large") {
			t.Fatalf("oversized frame: delivered=%t err=%v", delivered, err)
		}
	}
}

func TestReadControlLinesHandlerPanicClosesReader(t *testing.T) {
	var err error
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		err = readControlLines(strings.NewReader("{}\n"), func(json.RawMessage) error { panic("listener failed") })
	}()
	if panicked {
		t.Fatal("listener panic escaped the JSON-line callback boundary")
	}
	if err == nil || err.Error() != "Coordinator sent invalid JSON" {
		t.Fatalf("handler error: %v", err)
	}
}
