// Command check-model-data validates a hydrated upstream model catalog before generation.
// Ports packages/ai/scripts/check-model-data.ts.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".upstream/current/packages/ai", "upstream AI package root")
	flag.Parse()
	if err := ValidateGeneratedModelData(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "\nModel data is missing or stale. Run `npm run hydrate:model-data` from the repository root.")
		os.Exit(1)
	}
	fmt.Println("Generated model data is valid.")
}
