package protocol

import "testing"

// protocol/src/framing.ts:12-17: FrameError(message) keeps its message; a decoder that has failed or ended reports it.
func TestNewFrameErrorKeepsItsMessageAndTheDecoderRaisesIt(t *testing.T) {
	if err := NewFrameError("bad frame"); err.Message != "bad frame" || err.Error() != "bad frame" {
		t.Fatalf("NewFrameError = %+v", err)
	}
	decoder, err := NewFrameDecoder(FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = decoder.fail("boom")
	if _, err = decoder.Push(nil); err == nil || err.Error() != "Frame decoder has failed" {
		t.Fatalf("Push after failure = %v", err)
	}
}
