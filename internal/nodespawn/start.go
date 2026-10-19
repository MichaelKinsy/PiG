package nodespawn

import (
	"errors"
	"os"
	"os/exec"
	"slices"
)

// Start starts cmd, which this package's functions prepared, as Node's spawn
// starts it, and closes the parent's copies of the files it gave the child.
// Wait for the child with cmd.Wait.
//
// os/exec drops an entry of cmd.Env that shares the text before its first "="
// with a later entry (sharesEnvKey), where Node's spawn passes both. Outside
// Windows SetProgram has already arranged such a start (useTrampoline). On
// Windows, Start first puts cmd.Env in the order in which os/exec writes the
// block, keeping libuv's order among the entries os/exec compares equal
// (SetEnvProperties). It then starts cmd.Path itself with os.StartProcess and
// that block, or calls cmd.Start when os/exec would drop no entry.
//
// So that both ways start cmd alike, Start takes only a command whose Stdin,
// Stdout, and Stderr are each nil, which gives the child the NUL device, or an
// *os.File, and that has no Cancel and no WaitDelay; it returns an error for
// any other. Whether the start succeeds or not, Start then closes each of
// those files other than PiG's own standard files (standardFiles), as Node's
// spawn closes the child's ends of the pipes it creates and cmd.Start closes
// those of its Pipe methods.
func Start(cmd *exec.Cmd) error {
	defer closeChildFiles(cmd)
	for _, stdio := range childStdio(cmd) {
		if _, ok := stdio.(*os.File); stdio != nil && !ok {
			return errors.New("nodespawn: Start takes only nil or an *os.File as Stdin, Stdout, and Stderr")
		}
	}
	if cmd.Cancel != nil || cmd.WaitDelay != 0 {
		return errors.New("nodespawn: Start takes no command with a Cancel or a WaitDelay")
	}
	return start(cmd)
}

// childStdio is cmd's Stdin, Stdout, and Stderr.
func childStdio(cmd *exec.Cmd) [3]any {
	return [3]any{cmd.Stdin, cmd.Stdout, cmd.Stderr}
}

// standardFiles are PiG's own standard files: the files os.Stdin, os.Stdout,
// and os.Stderr hold when the program starts, the ones Node's "inherit" gives
// a child. They are captured once because the variables are not stable: the
// output guard reassigns os.Stdout while other goroutines run, so reading it
// in Start would race with the guard's restore (a bash tool's spawn can overlap
// the exit path's), and during a takeover it names the guard's pipe, not the
// process's own stdout.
var standardFiles = [...]*os.File{os.Stdin, os.Stdout, os.Stderr}

// closeChildFiles closes each file of cmd's Stdin, Stdout, and Stderr other
// than PiG's own standard files.
func closeChildFiles(cmd *exec.Cmd) {
	for _, stdio := range childStdio(cmd) {
		if file, ok := stdio.(*os.File); ok && file != nil && !slices.Contains(standardFiles[:], file) {
			// A file given twice, as stdout and stderr, is already closed the
			// second time.
			_ = file.Close()
		}
	}
}
