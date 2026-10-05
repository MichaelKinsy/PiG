// Command probe records its own executable at startup and reports what Detect says when the test asks.
package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/installchange"
)

func main() {
	installchange.Record()
	for _, path := range os.Args[1:] {
		installchange.TrackFile(path)
	}
	fmt.Println("ready")
	input := bufio.NewReader(os.Stdin)
	for {
		if _, err := input.ReadString('\n'); err != nil {
			return
		}
		change := installchange.Detect()
		if change == nil {
			fmt.Println("none")
			continue
		}
		fmt.Printf("%s %s\n", change.Kind, change.Path)
	}
}
