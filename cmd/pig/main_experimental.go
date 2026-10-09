//go:build pig_experimental

package main

import (
	"os"

	"github.com/MichaelKinsy/PiG/coding/cli"
)

// Ports packages/coding-agent/src/experimental/cli.ts

func main() {
	cli.ExperimentalMain(os.Args[1:])
}
