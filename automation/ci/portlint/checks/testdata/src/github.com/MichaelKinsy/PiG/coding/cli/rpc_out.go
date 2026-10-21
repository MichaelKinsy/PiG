package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// rpc*.go files of coding/cli are in scope.
func rpcBad(text string) {
	fmt.Fprintln(os.Stdout, text) // want `os.Stdout is written outside the RPC writer`
}

// A second holder of the raw stdout writer.
func rpcRaw() io.Writer {
	return codingagent.RawStdoutWriter() // want `codingagent.RawStdoutWriter hands out the raw stdout writer`
}
