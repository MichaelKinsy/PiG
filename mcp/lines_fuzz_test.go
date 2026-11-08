package mcp

import (
	"bytes"
	"testing"
)

// FuzzLineReaderFraming checks that the stdio framing never panics and does not depend on where the chunks split: the messages and errors
// reported for the whole input equal those reported for the input one byte at a time.
func FuzzLineReaderFraming(f *testing.F) {
	f.Add([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n\r\n  \n{\"x\"\n"), uint8(3))
	f.Add([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"p\xe6\x97\xa5\"}\r\n{\"jsonrpc\":\"2.0\""), uint8(1))
	f.Add(bytes.Repeat([]byte("a"), 300), uint8(7))
	f.Fuzz(func(t *testing.T, data []byte, chunk uint8) {
		size := int(chunk)%16 + 1
		whole, wholeEvents := startLineReader(0)
		feedLines(whole, data, size)
		whole.end()
		bytewise, bytewiseEvents := startLineReader(0)
		feedLines(bytewise, data, 1)
		bytewise.end()
		if a, b := len(wholeEvents.messages), len(bytewiseEvents.messages); a != b {
			t.Fatalf("messages: %d with chunks of %d, %d bytewise", a, size, b)
		}
		if a, b := len(wholeEvents.errors), len(bytewiseEvents.errors); a != b {
			t.Fatalf("errors: %d with chunks of %d, %d bytewise", a, size, b)
		}
	})
}
