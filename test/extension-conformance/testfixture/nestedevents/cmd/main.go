// Command nested-events runs the nested-event conformance row's Go extension as a standalone executable, so the host gives it
// its own isolated process.
package main

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/nestedevents"
)

func main() {
	if err := nestedevents.Extension("nested-go-isolated").Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "nested-events: %v\n", err)
		os.Exit(1)
	}
}
