// Command program is the `go run` probe of TestRunScenarioBuildsGoProbesBeforeTheTimedRun.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println("PROGRAM", strings.Join(os.Args[1:], " "))
}
