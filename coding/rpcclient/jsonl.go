// Ports packages/coding-agent/src/modes/rpc/jsonl.ts
package rpcclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// SerializeJsonLine emits JSON.stringify framing, including literal Unicode line and paragraph separators. Escaped backslashes remain escaped.
func SerializeJsonLine(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	input := buffer.Bytes()
	output := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		if input[i] == '\\' && i+1 < len(input) {
			if i+6 <= len(input) && (string(input[i:i+6]) == `\u2028` || string(input[i:i+6]) == `\u2029`) {
				output = append(output, 0xe2, 0x80, 0xa8+(input[i+5]-'8'))
				i += 5
				continue
			}
			output = append(output, input[i], input[i+1])
			i++
			continue
		}
		output = append(output, input[i])
	}
	return output, nil
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
