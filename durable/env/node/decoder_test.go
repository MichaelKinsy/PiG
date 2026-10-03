package node

import "testing"

// Not upstream cases: the decoder is Go's stand-in for TextDecoder in streaming
// mode, which the shell cases rely on.
func TestStreamDecoderHoldsAnIncompleteCharacterUntilItsRemainingBytesArrive(t *testing.T) {
	var decoder utf8StreamDecoder
	for _, step := range []struct {
		chunk []byte
		want  string
	}{
		{[]byte("a\xf0"), "a"},
		{[]byte("\x9f"), ""},
		{[]byte("\x98\x80b"), "😀b"},
		{[]byte("\x80"), "\uFFFD"}, // a stray continuation byte
		{[]byte("\xe4\xb8"), ""},
	} {
		if got := decoder.decode(step.chunk); got != step.want {
			t.Fatalf("decode(%x) = %q, want %q", step.chunk, got, step.want)
		}
	}
	if got := decoder.flush(); got != "\uFFFD" {
		t.Fatalf("flush = %q, want one replacement character", got)
	}
	if got := decoder.flush(); got != "" {
		t.Fatalf("second flush = %q", got)
	}
}

func TestStreamDecoderDropsAByteOrderMarkOnlyAtTheStartOfTheStream(t *testing.T) {
	var split utf8StreamDecoder
	for _, step := range []struct {
		chunk []byte
		want  string
	}{
		{[]byte("\xef\xbb"), ""},
		{[]byte("\xbfa"), "a"},
		{[]byte("\xef\xbb\xbfb"), "\ufeffb"},
	} {
		if got := split.decode(step.chunk); got != step.want {
			t.Fatalf("decode(%x) = %q, want %q", step.chunk, got, step.want)
		}
	}
}
