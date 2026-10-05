// Command check-model-data validates a hydrated upstream model catalog before generation. With -hydrate, it first
// hydrates the package's provider data from a published typed catalog.
// Ports packages/ai/scripts/check-model-data.ts and the command of packages/ai/scripts/hydrate-model-catalog.ts.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".upstream/current/packages/ai", "upstream AI package root")
	hydrate := flag.String("hydrate", "", "published typed model catalog (models.all.json) to hydrate the provider data from")
	flag.Parse()
	if *hydrate != "" {
		if err := HydrateModelCatalog(*root, *hydrate, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := ValidateGeneratedModelData(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "\nModel data is missing or stale. Run `npm run hydrate:model-data` from the repository root.")
		os.Exit(1)
	}
	fmt.Println("Generated model data is valid.")
}
