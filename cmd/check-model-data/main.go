// Command check-model-data validates a hydrated upstream model catalog before generation. With -hydrate, it first
// hydrates the package's provider data from a published typed catalog.
// Ports packages/ai/scripts/check-model-data.ts and the command of packages/ai/scripts/hydrate-model-catalog.ts.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is check-model-data.ts: it prints the success line, or the validation failure with the hydration hint, and
// returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check-model-data", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".upstream/current/packages/ai", "upstream AI package root")
	hydrate := flags.String("hydrate", "", "published typed model catalog (models.all.json) to hydrate the provider data from")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *hydrate != "" {
		if err := HydrateModelCatalog(*root, *hydrate, false); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := ValidateGeneratedModelData(*root); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		_, _ = fmt.Fprintln(stderr, "\nModel data is missing or stale. Run `npm run hydrate:model-data` from the repository root.")
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "Generated model data is valid.")
	return 0
}
