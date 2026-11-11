package cli

import (
	"io"
	"os"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// rpcTransport is where an RPC session reads its commands, writes its records and ends. The process transport is stdin, the raw stdout writer and os.Exit; a --listen connection's is the connection. pig additive (D232).
type rpcTransport struct {
	in  io.Reader
	out io.Writer
	// exit ends the session with code once the pending records are written: os.Exit for the process; for a connection, closing it.
	exit func(code int)
	// processOwned marks the process transport: it installs the termination hooks, terminates extension processes at exit and writes profiles. A connection registers its controls with the server instead.
	processOwned bool
	// register hands a connection's controls to the server for the signal fan-out and returns the unregister; nil when processOwned.
	register func(rpcSessionControls) (unregister func())
}

// rpcSessionControls is what one rpc process installs through setTerminationPrelude and setTerminationShutdownHook, per session.
type rpcSessionControls struct {
	prelude  func()
	shutdown func(sig syscall.Signal)
}

// processRPCTransport is stdin, the raw stdout writer and the process exit that serveRPCMode's exit path performs.
func processRPCTransport() rpcTransport {
	return rpcTransport{in: os.Stdin, out: codingagent.RawStdoutWriter(), exit: os.Exit, processOwned: true}
}

// transport returns the transport the caller supplied, or the process transport.
func (r rpcModeResources) transport() rpcTransport {
	if r.Transport != nil {
		return *r.Transport
	}
	return processRPCTransport()
}
