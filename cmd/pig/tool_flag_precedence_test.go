package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Pi's sdk.ts:258-265 gives an explicit --tools list precedence over --no-tools and --no-builtin-tools: allowedToolNames = tools ?? (noTools === "all" ? [] : undefined) and initialActiveToolNames = tools ?? (noTools ? [] : defaults), then the denylist is subtracted. Measured against the exact Pi 0.87.1 oracle in --mode json with the faux provider (bash, read and write prompts).
func TestToolsAllowlistOverridesNoToolsFlagsInJSONMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	prompts := map[string]string{"bash": "Run: expr 20 + 22", "read": "Run: read parity-read-target.txt"}
	cases := []struct {
		name string
		args []string
		runs map[string]bool
	}{
		{"no-tools with tools", []string{"--no-tools", "--tools", "bash"}, map[string]bool{"bash": true, "read": false}},
		{"no-builtin-tools with tools", []string{"--no-builtin-tools", "--tools", "read"}, map[string]bool{"bash": false, "read": true}},
		{"tools minus excluded", []string{"--tools", "bash,read", "--exclude-tools", "bash"}, map[string]bool{"bash": false, "read": true}},
	}
	for _, tc := range cases {
		for tool, prompt := range prompts {
			t.Run(tc.name+"/"+tool, func(t *testing.T) {
				args := append([]string{"--mode", "json"}, tc.args...)
				stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil, append(args, prompt)...)
				if code != 0 {
					t.Fatalf("exit %d, stderr %s", code, stderr)
				}
				end := toolExecutionEnd(t, jsonModeLines(t, stdout), tool)
				text := fmt.Sprint(end["result"])
				if ran := !strings.Contains(text, "Tool "+tool+" not found"); ran != tc.runs[tool] {
					t.Fatalf("%s ran=%v, want %v: %v", tool, ran, tc.runs[tool], end)
				}
			})
		}
	}
}
