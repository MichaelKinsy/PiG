// Command early-dispatch runs the pre-ready dispatch row's Go extension over a subprocess socket.
package main

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/earlydispatch"
)

func main() {
	if err := earlydispatch.Extension().Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "early-dispatch: %v\n", err)
		os.Exit(1)
	}
}
