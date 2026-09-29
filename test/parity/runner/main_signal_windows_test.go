//go:build parity && windows

package runner

import (
	"os"
	"syscall"
)

// statusControlCExit is STATUS_CONTROL_C_EXIT, the exit status the default
// console control handler gives a process it ends.
const statusControlCExit = 0xC000013A

// exitWithSignal ends the process as Windows' default console control handler
// would. Windows has no signal to re-raise: Go reports Ctrl+C and Ctrl+Break
// as SIGINT and a console close as SIGTERM, and the default handler for each
// exits with STATUS_CONTROL_C_EXIT.
func exitWithSignal(syscall.Signal) {
	os.Exit(statusControlCExit)
}
