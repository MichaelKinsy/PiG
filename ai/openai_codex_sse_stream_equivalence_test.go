package ai

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// referenceCodexSSE mirrors Pi's shared SseDecoder (packages/ai/src/utils/sse.ts, Pi da7e26cc8):
// line endings LF, CRLF (including split across chunks), and lone CR; blank line flushes;
// data: strips at most one leading space and joins with "\n".
func referenceCodexSSE(body []byte) []string {
	var dec codexTextDecoder
	text := dec.decode(body, false)
	var events []string
	var data []string
	hasEvent := false
	flush := func() {
		if !hasEvent && len(data) == 0 {
			return
		}
		if len(data) > 0 {
			events = append(events, strings.Join(data, "\n"))
		}
		hasEvent = false
		data = data[:0]
	}
	processLine := func(line string) {
		if line == "" {
			flush()
			return
		}
		if strings.HasPrefix(line, ":") {
			return
		}
		field, val, found := strings.Cut(line, ":")
		if !found {
			val = ""
		}
		val = strings.TrimPrefix(val, " ")
		switch field {
		case "event":
			hasEvent = val != ""
		case "data":
			data = append(data, val)
		}
	}
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\r' && text[i] != '\n' {
			continue
		}
		processLine(text[start:i])
		if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(text) {
		processLine(text[start:])
	}
	flush()
	return events
}

var codexFrameAlphabet = []string{"\n", "\n\n", "\r\n", "\r", " ", "\u00a0", "data:", "data: {\"type\":\"x\"}", ": c", "\ufeff", "é", "\xff", "\xe4\xbd", "\xa0", strings.Repeat("z", 200)}

func TestCodexSSEFramesMatchReference(t *testing.T) {
	random := rand.New(rand.NewPCG(15, 16))
	for range 4000 {
		var body []byte
		for range random.IntN(24) {
			body = append(body, codexFrameAlphabet[random.IntN(len(codexFrameAlphabet))]...)
		}
		chunks := randomSplits(random, body, 1+random.IntN(40))
		want := referenceCodexSSE(body)
		var frames codexSSEFrames
		var decoder codexTextDecoder
		var got []string
		for i := range len(chunks) + 1 {
			var data []byte
			done := i == len(chunks)
			if !done {
				data = append([]byte(nil), chunks[i]...)
			}
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
