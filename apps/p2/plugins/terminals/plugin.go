package terminals

import (
	"context"
	_ "embed"
	"os"
	"runtime/debug"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// ghosttyWasm is libghostty-vt (vendor/ghostty), vendored byte for byte from Pi.
//
//go:embed vendor/ghostty/ghostty-vt.wasm
var ghosttyWasm []byte

func init() {
	server, worker := ServerFacet, WorkerFacet
	hosts.RegisterPlugin(hosts.Plugin{Name: "terminals", Server: &server, Worker: &worker})
	hosts.RegisterSubcommand(HostCommand, func(args []string) int {
		if len(args) < 1 {
			_, _ = os.Stderr.WriteString("usage: p2 terminals-host <data directory>\n")
			return 2
		}
		return runHostProcess(args[0])
	})
}

// runHostProcess is the terminal host program (`log-preload.ts` + `main.ts`): its logs are published (bounded and redacted),
// also when it fails at startup; it returns the process's exit code.
func runHostProcess(directory string) (code int) {
	published := p2runtime.CapturePublishedLogs("terminals", directory, false)
	defer func() {
		if recovered := recover(); recovered != nil {
			published.Fatal(recovered, debug.Stack())
			code = 1
		}
	}()
	//portlint:allow ctxbackground the host is a program of its own: nothing above it cancels it
	if err := RunHost(context.Background(), directory); err != nil {
		published.Fatal(err, nil)
		return 1
	}
	_ = published.Stop()
	return 0
}
