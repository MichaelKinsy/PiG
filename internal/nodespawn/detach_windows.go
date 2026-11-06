//go:build windows

package nodespawn

import "os/exec"

// DetachFromTerminal gives cmd a console of its own without a window
// (CREATE_NO_WINDOW and a hidden window, as HideWindow does), the Windows
// counterpart of a new session: the child does not share PiG's console. A
// console program that a shim starts opens no visible window either, as it
// would under DETACHED_PROCESS. cmd's stdin must be the null device and its
// stdout and stderr pipes.
func DetachFromTerminal(cmd *exec.Cmd) {
	HideWindow(cmd, Ignore, Pipe, Pipe)
}
