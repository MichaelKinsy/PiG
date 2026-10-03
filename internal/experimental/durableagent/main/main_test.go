package main

import (
	"context"
	"testing"
)

// main.ts:5-11: an unknown argument stops the program before it opens a session.
func TestRunRejectsAnUnknownArgument(t *testing.T) {
	err := run(context.Background(), []string{"--bogus"})
	if err == nil || err.Error() != "Unknown argument: --bogus" {
		t.Fatalf("run = %v", err)
	}
}
