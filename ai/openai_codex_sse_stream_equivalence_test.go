package ai

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// parseSSE's buffer is a byte buffer searched from where the last search stopped, and well-formed text skips the per-rune decoder. The reference is the framing it replaced: the decoded text concatenated onto a string, searched from its start after every read.
type referenceCodexFrames struct {
	decoder codexTextDecoder
	buffer  string
}

func (frames *referenceCodexFrames) add(data []byte, done bool) []string {
	if done {
		frames.buffer += frames.decoder.decode(nil, false)
	} else {
		frames.buffer += frames.decoder.decode(data, true)
	}
	if done && trimJSWhitespace(frames.buffer) != "" {
		frames.buffer += "\n\n"
	}
	var taken []string
	for {
		index := strings.Index(frames.buffer, "\n\n")
		if index < 0 {
			return taken
		}
		taken = append(taken, frames.buffer[:index])
		frames.buffer = frames.buffer[index+2:]
	}
}

var codexFrameAlphabet = []string{"\n", "\n\n", "\r\n", " ", "\u00a0", "data:", "data: {\"type\":\"x\"}", ": c", "\ufeff", "é", "\xff", "\xe4\xbd", "\xa0", strings.Repeat("z", 200)}

func TestCodexSSEFramesMatchReference(t *testing.T) {
	random := rand.New(rand.NewPCG(15, 16))
	for range 4000 {
		var body []byte
		for range random.IntN(24) {
			body = append(body, codexFrameAlphabet[random.IntN(len(codexFrameAlphabet))]...)
		}
		chunks := randomSplits(random, body, 1+random.IntN(40))
		reference := &referenceCodexFrames{}
		var frames codexSSEFrames
		var decoder codexTextDecoder
		var got, want []string
		for i := range len(chunks) + 1 {
			var data []byte
			done := i == len(chunks)
			if !done {
				data = append([]byte(nil), chunks[i]...)
			}
			want = append(want, reference.add(data, done)...)
			frames.add(&decoder, data, done)
			// The frames must not keep the read: the body reuses its buffer.
			for j := range data {
				data[j] = '#'
			}
			for {
				frame, ok := frames.next()
				if !ok {
					break
				}
				got = append(got, frame)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("body %q in %d chunks:\n got %q\nwant %q", body, len(chunks), got, want)
		}
	}
}
