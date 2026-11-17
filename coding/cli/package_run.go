package cli

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// packageRun is what one process fixes for the package installs of every load (main.ts packageCommandOutput, announcePackageInstalls, onPackageProgress).
// Interactive mode installs and refreshes packages again on /reload, /new, /resume and /fork while the TUI draws on the terminal, where git and npm
// output would corrupt its screen (Pi #11264). Their output is captured, and until the TUI starts each install is announced on stderr instead.
// A nil *packageRun is every other mode: the output is inherited and nothing is announced.
// Ports packages/coding-agent/src/main.ts (packageCommandOutput, announcePackageInstalls, onPackageProgress).
type packageRun struct {
	output   packagemanager.CommandOutput
	announce atomic.Bool
}

// newPackageRun is the run of a process in mode: capture and announce in interactive mode, inherit otherwise (main.ts:861-870).
func newPackageRun(mode appMode) *packageRun {
	if mode != appModeInteractive {
		return nil
	}
	run := &packageRun{output: packagemanager.CommandOutputCapture}
	run.announce.Store(true)
	return run
}

// stopAnnouncing ends the stderr announcements: the TUI draws from now on (main.ts `announcePackageInstalls = false`).
func (r *packageRun) stopAnnouncing() {
	if r != nil {
		r.announce.Store(false)
	}
}

// progress is main.ts onPackageProgress: the message of each started operation on stderr, dimmed, while announcing.
func (r *packageRun) progress() packagemanager.ProgressCallback {
	if r == nil {
		return nil
	}
	return func(event packagemanager.ProgressEvent) {
		if r.announce.Load() && event.Type == "start" && event.Message != nil && *event.Message != "" {
			_, _ = fmt.Fprint(os.Stderr, chalkDim(*event.Message)+"\n")
		}
	}
}

// manager is the run's package manager over cwd and the settings (the resource loader's, with packageCommandOutput and onPackageProgress).
func (r *packageRun) manager(cwd, agentDir string, sm *codingagent.SettingsManager) *packagemanager.PackageManager {
	options := packagemanager.PackageManagerOptions{CWD: cwd, SettingsManager: sm}
	if r != nil {
		options.CommandOutput = r.output
	}
	manager := packagemanager.NewPackageManager(options)
	manager.AgentDir = agentDir
	manager.SetProgressCallback(r.progress())
	return manager
}
