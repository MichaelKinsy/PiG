package rpcclient

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// ReadJSONLLines reads strict JSONL records from r. It splits only on LF,
// removes one trailing CR, delivers blank records, and delivers a final
// unterminated record. Returning false from onLine stops the read.
func ReadJSONLLines(r io.Reader, onLine func([]byte) bool) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSuffix(line, []byte("\n"))
			line = bytes.TrimSuffix(line, []byte("\r"))
			if !onLine(line) {
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
