package main

import (
	"os"

	_ "github.com/MichaelKinsy/PiG/ai/all"
	"github.com/MichaelKinsy/PiG/coding/cli"
)

// Ports packages/coding-agent/src/cli.ts

func main() {
	cli.Main(os.Args[1:], nil)
}
