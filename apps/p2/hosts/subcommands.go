package hosts

import (
	"fmt"
	"os"
	"sync"
)

// Go-only: the p2 executable is also the programs Pi starts by path (D473): the session worker (`p2 worker`, RunWorker) and
// the terminal host (`p2 terminals-host <data directory>`). A plugin that is its own program registers a subcommand; the
// executable's main (and the tests' TestMain, p2test.Main) hands its arguments to RunSubcommand first.

var (
	subcommandsMu sync.Mutex
	subcommands   = map[string]func(args []string) int{}
)

// RegisterSubcommand registers run as the program `p2 <name> <args...>`; it returns the process's exit code. It panics for a
// name registered twice (a program error).
func RegisterSubcommand(name string, run func(args []string) int) {
	subcommandsMu.Lock()
	defer subcommandsMu.Unlock()
	if _, ok := subcommands[name]; ok {
		panic(fmt.Sprintf("p2 subcommand %q is registered twice", name))
	}
	subcommands[name] = run
}

// RunSubcommand runs the registered subcommand named by args[1] (args is os.Args) and exits with its code; it returns when
// args name none.
func RunSubcommand(args []string) {
	if len(args) < 2 {
		return
	}
	subcommandsMu.Lock()
	run := subcommands[args[1]]
	subcommandsMu.Unlock()
	if run != nil {
		os.Exit(run(args[2:]))
	}
}
