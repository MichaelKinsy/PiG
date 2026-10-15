// Ports packages/coding-agent/src/modes/rpc/jsonl.ts
package rpcclient

import (
	"bufio"
	"bytes"
	"errors"
	"io"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// SerializeJsonLine is JSON.stringify(value) followed by the LF that frames a record (jsonl.ts serializeJsonLine).
func SerializeJsonLine(value any) ([]byte, error) {
	encoded, err := jsstring.MarshalJSON(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// ReadJSONLLines reads strict JSONL records from r. It splits only on LF,
// removes one trailing CR, delivers blank records, and delivers a final
// unterminated record. Returning false from onLine stops the read.
func ReadJSONLLines(r io.Reader, onLine func([]byte) bool) error {
	return ReadJSONLBatches(r, func(lines [][]byte) bool {
		for _, line := range lines {
			if !onLine(line) {
				return false
			}
		}
		return true
	})
}

// ReadJSONLBatches preserves the complete records already available in one buffered read. A caller can admit that input callback's commands before running their awaited continuations, as attachJsonlLineReader does in Node's onData callback. Partial and final unterminated records retain ReadJSONLLines framing.
func ReadJSONLBatches(r io.Reader, onBatch func([][]byte) bool) error {
	reader := bufio.NewReader(r)
	trim := func(line []byte) []byte {
		return bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
	}
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			batch := [][]byte{trim(line)}
			for reader.Buffered() > 0 {
				buffered, _ := reader.Peek(reader.Buffered())
				if !bytes.Contains(buffered, []byte("\n")) {
					break
				}
				next, _ := reader.ReadBytes('\n')
				batch = append(batch, trim(next))
			}
			if !onBatch(batch) {
				return nil
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
