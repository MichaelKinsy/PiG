package packagemanager

// InstallOption adjusts how one managed install runs its package manager. With no option an install behaves as Pi's does.
type InstallOption func(*installOptions)

type installOptions struct {
	ignoreScripts bool
	progress      ProgressCallback
	output        CommandOutput
}

// Ports packages/coding-agent/src/core/package-manager.ts (PackageCommandOutput, PackageManagerOptions.commandOutput).
// CommandOutput is where git and npm output goes while packages are installed, updated or refreshed (package-manager.ts PackageCommandOutput).
type CommandOutput string

const (
	// CommandOutputInherit writes the output to the terminal, live (`pig install`, `pig update`). It is the default.
	CommandOutputInherit CommandOutput = "inherit"
	// CommandOutputCapture writes nothing to the terminal and keeps git and ssh from prompting, for hosts that draw on it (the interactive TUI). A failure carries the end of the output.
	CommandOutputCapture CommandOutput = "capture"
)

// WithCommandOutput chooses where the install's git and npm output goes (package-manager.ts PackageManagerOptions.commandOutput). Without it the output is inherited.
func WithCommandOutput(output CommandOutput) InstallOption {
	return func(options *installOptions) { options.output = output }
}

// WithIgnoreScripts runs the package manager with `--ignore-scripts`, so no lifecycle script of the installed Package or of its dependencies runs during the install. Npm, pnpm and bun accept the same flag.
//
// pig additive (D194): Piglet resolution installs Packages without running lifecycle scripts unless the user opts in.
func WithIgnoreScripts() InstallOption {
	return func(options *installOptions) { options.ignoreScripts = true }
}

// WithProgressReporter receives the progress events a managed Git install raises while it runs, such as the warning for a failed `git clean`. Pi's package manager emits them through the callback set with setProgressCallback.
func WithProgressReporter(progress ProgressCallback) InstallOption {
	return func(options *installOptions) { options.progress = progress }
}

func resolveInstallOptions(opts []InstallOption) installOptions {
	var options installOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// ignoreScriptsArgs is the argument that every supported package manager reads as "run no lifecycle scripts".
func (options installOptions) ignoreScriptsArgs() []string {
	if options.ignoreScripts {
		return []string{"--ignore-scripts"}
	}
	return nil
}
