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
		switch {
		case event.Type == "start" && event.Message != nil:
			// upstream: package-manager-cli.ts progress callback process.stdout.write(chalk.dim(`${event.message}\n`)).
			_, _ = fmt.Fprint(writer, chalkDim(*event.Message+"\n"))
		case event.Type == "progress" && event.Message != nil && *event.Message != "":
			// upstream (0d23d534): `else if (event.type === "progress" && event.message)` prints chalk.yellow(`${event.message}\n`).
			_, _ = fmt.Fprint(writer, chalkYellow(*event.Message+"\n"))
		}
	}
}

// updateSummary collects what `pig update` reports about the packages it updated: the changed packages from "result" events and the
// number of warnings the update printed (package-manager-cli.ts updatedPackages, warningCount).
type updateSummary struct {
	updated      []string
	warningCount int
}

// progress is packageProgressPrinter that also collects the update summary.
func (u *updateSummary) progress(writer io.Writer) packagemanager.ProgressCallback {
	print := packageProgressPrinter(writer)
	return func(event packagemanager.ProgressEvent) {
		switch {
		case event.Type == "result" && event.Action == "update" && event.Message != nil && *event.Message != "":
			u.updated = append(u.updated, *event.Message)
		default:
			if event.Type == "progress" && event.Message != nil && *event.Message != "" {
				// Progress messages are warnings, for example npm reinstalled the old version from stale registry metadata.
				u.warningCount++
			}
			print(event)
		}
	}
}

// print reports the result of an update of source ("" for every package). It claims "up to date" only when the update checked the
// packages and nothing warned.
func (u *updateSummary) print(source string) {
	switch {
	case packagemanager.IsOfflineModeEnabled():
		fmt.Println(chalkYellow("Skipped package updates (offline mode)"))
	case len(u.updated) == 0 && u.warningCount > 0:
		if source != "" {
			fmt.Println(chalkYellow(source + " was not updated, see the warnings above"))
		} else {
			fmt.Println(chalkYellow("No packages were updated, see the warnings above"))
		}
	case len(u.updated) == 0:
		if source != "" {
			fmt.Println(chalkGreen(source + " is already up to date"))
		} else {
			fmt.Println(chalkGreen("Packages are already up to date"))
		}
	default:
		plural := "s"
		if len(u.updated) == 1 {
			plural = ""
		}
		fmt.Println(chalkGreen(fmt.Sprintf("Updated %d package%s:", len(u.updated), plural)))
		for _, message := range u.updated {
			fmt.Println("  " + message)
		}
	}
}
