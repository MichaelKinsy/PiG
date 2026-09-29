package main

import (
	"fmt"
	"io"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Ports packages/coding-agent/src/core/package-manager.ts (ProgressEvent, ProgressCallback, withProgress).

// Package CLI output prints starts; the command caller owns success and failure diagnostics.
func packageProgressPrinter(writer io.Writer) packagemanager.ProgressCallback {
	return func(event packagemanager.ProgressEvent) {
		if event.Type == "start" && event.Message != nil {
			_, _ = fmt.Fprintln(writer, *event.Message)
		}
	}
}
