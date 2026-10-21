package cli

import (
	"fmt"
	"os"
)

// Other files of coding/cli are out of scope.
func printHelp(text string) {
	fmt.Fprintln(os.Stdout, text)
}
