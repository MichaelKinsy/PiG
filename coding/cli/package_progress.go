package cli

import (
	"fmt"
	"io"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Ports packages/coding-agent/src/core/package-manager.ts (ProgressEvent, ProgressCallback, withProgress).

// Package CLI output prints starts; the command caller owns success and failure diagnostics.
func packageProgressPrinter(writer io.Writer) packagemanager.ProgressCallback {
	return func(event packagemanager.ProgressEvent) {
		if event.Type == "start" && event.Message != nil {
			// upstream: package-manager-cli.ts progress callback process.stdout.write(chalk.dim(`${event.message}\n`)).
			_, _ = fmt.Fprint(writer, chalkDim(*event.Message+"\n"))
		}
	}
}
