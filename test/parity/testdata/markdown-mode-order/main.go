package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// The Mode's construction seam is private. Drive its real callback pipeline and fused SDK through the package tests, then expose only the complete callback traces to the Pi comparator. A failed assertion or test process remains a failure.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cmd := exec.CommandContext(context.Background(), "go", "test", "-json", "./internal/codingagent", "-run", "^TestMarkdownMode(ReplacementWaitsForCallbackBody|CrossComponentAdmissionOrder)$", "-count=1", "-timeout=1m")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Mode callback probe failed: %w\n%s", err, output)
	}
	traces := make(map[string]string)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var event struct{ Action, Output string }
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if event.Action != "output" {
			continue
		}
		_, trace, found := strings.Cut(event.Output, "MARKDOWN_ORDER ")
		if !found {
			continue
		}
		name, _, _ := strings.Cut(trace, ":")
		if _, duplicate := traces[name]; duplicate {
			return fmt.Errorf("duplicate trace %q", name)
		}
		traces[name] = strings.TrimSpace(trace)
	}
	for _, name := range []string{"replacement", "components"} {
		trace, ok := traces[name]
		if !ok {
			return fmt.Errorf("missing trace %q", name)
		}
		fmt.Println(trace)
	}
	return nil
}
