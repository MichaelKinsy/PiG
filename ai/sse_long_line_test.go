package ai

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Pi's SSE readers (the SDKs' and anthropic-messages.ts iterateSseMessages) buffer a line of any length; they have no cap.
// A single `data:` line larger than 32 MiB (a base64 image chunk) used to end the stream with bufio.ErrTooLong (comb-audit row 4).
func TestSSEDecoderAcceptsALineLargerThan32MiB(t *testing.T) {
	payload := strings.Repeat("a", 33<<20)
	decoder := newSSEDecoder(strings.NewReader("data: " + payload + "\n\ndata: next\n\n"))
	if !decoder.Next() || decoder.Event().Data != payload {
		t.Fatalf("first event lost: err=%v", decoder.Err())
	}
	if !decoder.Next() || decoder.Event().Data != "next" {
		t.Fatalf("second event = %q err=%v", decoder.Event().Data, decoder.Err())
	}
	if decoder.Next() || decoder.Err() != nil {
		t.Fatalf("extra event or error: %v", decoder.Err())
	}
}

// pig divergence (D134): sseLineLimit is the longest JavaScript string Node can hold, which is where every Pi SSE reader fails.
func TestSSELineLimitIsNodesMaxStringLength(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "node", "-e", "process.stdout.write(String(require('node:buffer').constants.MAX_STRING_LENGTH))").Output()
	if err != nil {
		t.Fatal(err)
	}
	if want, err := strconv.Atoi(strings.TrimSpace(string(output))); err != nil || sseLineLimit != want {
		t.Fatalf("sseLineLimit = %d, Node's MAX_STRING_LENGTH = %s (%v)", sseLineLimit, output, err)
	}
}

// pig divergence (D134): a line that does not end within the bound stops the stream there instead of growing without limit. The test line is 1 MiB, finite, so that a regression fails instead of exhausting memory.
func TestSSEDecoderBoundsALine(t *testing.T) {
	previous := sseLineLimit
	sseLineLimit = 1 << 10
	t.Cleanup(func() { sseLineLimit = previous })

	decoder := newSSEDecoder(strings.NewReader("data: " + strings.Repeat("a", 900) + "\n\n"))
	if !decoder.Next() || len(decoder.Event().Data) != 900 {
		t.Fatalf("a line under the bound was lost: err=%v", decoder.Err())
	}
	decoder = newSSEDecoder(io.MultiReader(strings.NewReader("data: ok\n\n"), io.LimitReader(endlessReader{}, 1<<20)))
	if !decoder.Next() || decoder.Event().Data != "ok" {
		t.Fatalf("first event lost: err=%v", decoder.Err())
	}
	if decoder.Next() || !errors.Is(decoder.Err(), bufio.ErrTooLong) {
		t.Fatalf("an endless line did not stop at the bound: err=%v", decoder.Err())
	}
}

// endlessReader is a line that never ends; the test limits it.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}
