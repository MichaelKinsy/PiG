package workers

import (
	"os"
	"sync"
)

// Go has no `process.once("exit")`: a worker exits through exitProcess, which runs what was registered with atExit first
// (a socket file lock's removal), as Node's exit listeners run before the process ends.

var (
	exitMu    sync.Mutex
	exitHooks []*exitHook
)

type exitHook struct{ run func() }

// atExit registers run for exitProcess and returns the hook's removal.
func atExit(run func()) (remove func()) {
	hook := &exitHook{run: run}
	exitMu.Lock()
	exitHooks = append(exitHooks, hook)
	exitMu.Unlock()
	return func() {
		exitMu.Lock()
		defer exitMu.Unlock()
		for index, each := range exitHooks {
			if each == hook {
				exitHooks = append(exitHooks[:index:index], exitHooks[index+1:]...)
				return
			}
		}
	}
}

// exitProcess is `process.exit(code)`: the registered hooks run, then the process ends.
func exitProcess(code int) {
	exitMu.Lock()
	hooks := exitHooks
	exitHooks = nil
	exitMu.Unlock()
	for _, hook := range hooks {
		hook.run()
	}
	os.Exit(code)
}
