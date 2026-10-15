package codingagent

import "time"

// terminalReads is a terminalInputSink that hands an interactiveTerminalReader's raw reads to a test, without framing.
type terminalReads struct {
	data   chan []byte
	errors chan error
}

func newTerminalReads() *terminalReads {
	return &terminalReads{data: make(chan []byte), errors: make(chan error, 1)}
}

func (r *terminalReads) begin()              {}
func (r *terminalReads) process(data []byte) { r.data <- data }
func (r *terminalReads) deadline() time.Time { return time.Time{} }
func (r *terminalReads) flush()              {}
func (r *terminalReads) fail(err error)      { r.errors <- err }
