package ai

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
)

const codexEncoderBubbleProbeEnv = "PIG_CODEX_ENCODER_BUBBLE_PROBE"

// The Codex request encoder is one process-wide value built on first use, and the zstd library creates the channel that hands its encoder out at that
// first EncodeAll. A channel made inside a testing/synctest bubble belongs to that bubble: the next EncodeAll from outside it, or from another bubble,
// is the Go runtime fatal "receive on synctest channel from outside bubble", which kills the whole test process. Which test reaches the encoder first
// depends on the order the process runs them (a 21-shard run of this package lost 63 tests to it), so TestMain builds the encoder before any test and
// this probe, run in a fresh process where it is the first and only user, proves that the encoder is already there.
func TestCodexRequestEncoderServesSynctestBubblesAndTheRestOfTheProcess(t *testing.T) {
	if os.Getenv(codexEncoderBubbleProbeEnv) == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCodexRequestEncoderServesSynctestBubblesAndTheRestOfTheProcess$", "-test.count=1")
		cmd.Env = append(os.Environ(), codexEncoderBubbleProbeEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("a fresh process that compresses in two bubbles and then outside them failed: %v\n%s", err, output)
		}
		return
	}
	body := []byte(strings.Repeat(`{"model":"gpt-5.1-codex","input":[{"type":"message","role":"user","content":"hello"}]}`, 8))
	compress := func(where string) {
		decoded, err := decodeZstdFrameForTest(compressCodexRequestBody(body))
		if err != nil || !bytes.Equal(decoded, body) {
			t.Errorf("%s: frame does not decode to its body (%v)", where, err)
		}
	}
	for _, where := range []string{"first bubble", "second bubble"} {
		synctest.Test(t, func(t *testing.T) { compress(where) })
	}
	compress("outside a bubble")
}
