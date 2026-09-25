// Command sdk-fixture runs the canonical Go SDK conformance extension over a
// subprocess socket.
package main

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/tests/extension-conformance/testfixture"
)

func main() {
	if err := testfixture.Extension().Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "sdk-fixture: %v\n", err)
		os.Exit(1)
	}
}
