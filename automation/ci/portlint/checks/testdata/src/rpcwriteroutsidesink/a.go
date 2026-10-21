package rpcwriteroutsidesink

import (
	"fmt"
	"io"
	"net"
	"os"
)

// Conn stands in for the subprocess Conn.
type Conn struct{ sock net.Conn }

func (c *Conn) writeInline(frame []byte) (bool, error) {
	_, err := c.sock.Write(frame) // want `a net.Conn write puts a frame on the socket`
	return err == nil, err
}

func (c *Conn) send(frame []byte) {
	_, _ = c.writeInline(frame) // want `\(\*Conn\).writeInline writes a frame on the caller's goroutine`
}

func writeJSONLine(w io.Writer, v string) { _, _ = io.WriteString(w, v) }

// Bad: os.Stdout reached from an extension-output path.
func badStdout(text string) {
	fmt.Fprintln(os.Stdout, text) // want `os.Stdout is written outside the RPC writer`
}

// Bad: a record written through the line writer from a second path.
func badLine(w io.Writer) {
	writeJSONLine(w, "{}") // want `writeJSONLine writes a record to its writer`
}

// Bad: a concrete connection written directly.
func badUnix(c *net.UnixConn) {
	_, _ = c.Write([]byte("x")) // want `a net.Conn write puts a frame on the socket`
}

// Good: stderr is diagnostics, not an RPC stream.
func goodStderr(text string) {
	fmt.Fprintln(os.Stderr, text)
}

// Good: an io.Writer the caller owns is not a socket or stdout.
func goodWriter(w io.Writer, b []byte) {
	_, _ = w.Write(b)
}

// Good: the owner of the stream carries a marker with a class.
func ownerWrite(w net.Conn, frame []byte) {
	//portlint:allow rpcwriteroutsidesink executor: the connection's writer goroutine owns the socket
	_, _ = w.Write(frame)
}

// Good: terminal output that is not an RPC stream says so.
func terminal(text string) {
	//portlint:allow rpcwriteroutsidesink lifecycle: extension console output in TUI mode, not RPC
	fmt.Fprintln(os.Stdout, text)
}
