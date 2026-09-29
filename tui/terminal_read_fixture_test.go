package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestTerminalReadRetainsDataReturnedWithEOF(t *testing.T) {
	// A stream's final bytes precede its end event. Go readers may return those bytes and io.EOF together.
	data, err := ReadInputChunk(iotest.DataErrReader(strings.NewReader("ab")))
	if string(data) != "ab" || !errors.Is(err, io.EOF) {
		t.Fatalf("final read=(%q,%v), want (ab,EOF)", data, err)
	}
	var got strings.Builder
	err = ReadInputStream(t.Context(), iotest.DataErrReader(strings.NewReader("ab")), func(data []byte) { got.Write(data) })
	if got.String() != "ab" || !errors.Is(err, io.EOF) {
		t.Fatalf("stream=(%q,%v), want (ab,EOF)", got.String(), err)
	}
}

// readTestInput collects the first delivered batch through the production forwarder. It cancels before a subsequent OS read, preserving handoff fixtures.
func readTestInput(source io.Reader) ([]byte, error) {
	return readTestTerminalInput(processTerminal, source)
}

func readTestTerminalInput(terminal *ProcessTerminal, source io.Reader) ([]byte, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var data []byte
	var readErr error
	terminal.forwardInputFrom(ctx, source, func(chunk []byte) {
		data = append(data, chunk...)
		cancel()
	}, func(err error) { readErr = err })
	return data, readErr
}
