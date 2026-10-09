// Command print-loop-turn-helper runs the session_shutdown part of Pig's print-mode disposal over Node extensions that each run in their own runtime process.
//
// Pig's CLI packs every Node factory into one runtime process, which shares one event loop as Pi's process does. Separate Node processes arise from crash recovery, which quarantines a culprit in its own process (node_recovery.go). This helper drives the same Host path with each extension isolated: the Host declares print mode, the extensions' session_shutdown handlers run in configured order, each awaited, as Pi's runner awaits them (runner.ts:1089-1116), then the Host invalidates the runner and stops the processes (agent-session-runtime.ts:404-410, agent-session.ts:1393-1406).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(entries []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	configs := make([]subprocess.ExtConfig, len(entries))
	for i, entry := range entries {
		source, err := filepath.Abs(entry)
		if err != nil {
			return err
		}
		configs[i] = subprocess.ExtConfig{Name: strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)), Source: source, Enabled: true, Isolation: "isolated"}
	}
	host := subprocess.NewHost(cwd)
	host.SetMode("print")
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
	defer host.Shutdown("print disposal done")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	loaded, errs := host.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		return fmt.Errorf("loaded %d of %d extensions: %v", len(loaded), len(configs), errs)
	}
	for _, ext := range loaded {
		for _, handler := range ext.EventHandlers("session_shutdown") {
			if _, err := handler(map[string]any{"type": "session_shutdown", "reason": "quit"}); err != nil {
				return fmt.Errorf("%s session_shutdown: %w", ext.Name, err)
			}
		}
	}
	host.Invalidate("")
	return nil
}
