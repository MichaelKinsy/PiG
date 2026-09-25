package main

import (
	"fmt"
	"os"
)

func runExtensionCommand(args []string) int {
	if len(args) == 0 || args[0] != "extension" {
		return -1
	}
	if len(args) == 1 || args[1] == "-h" || args[1] == "--help" {
		printExtensionCommandHelp()
		return 0
	}
	switch args[1] {
	case "init":
		return runExtensionInit(args[2:])
	case "preview-login":
		return runExtensionLoginPreview(args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown extension command %q.\n", args[1])
		printExtensionCommandHelp()
		return 1
	}
}

func printExtensionCommandHelp() {
	fmt.Print("Usage:\n  pig extension init <path> [--name <name>] [--lang go|python|rust] [--isolated] [--force] [--json]\n  pig extension preview-login <path>\n")
}
