//go:build !pig_experimental

package main

import (
	"os"

	"github.com/MichaelKinsy/PiG/coding/cli"
)

// Ports packages/coding-agent/src/cli.ts

func main() {
	cli.Main(os.Args[1:], nil)
}
