// Package rlfix is the Go extension of the RPC reload tests (rpc_reload_pipelined_test.go), the counterpart of rpc-reload.mjs: every wait is on a file in RL_DIR that the test creates. The generation counts the processes: a reload gives g2.
package rlfix

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var dir = os.Getenv("RL_DIR")

func exists(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
func touch(name string)       { _ = os.WriteFile(filepath.Join(dir, name), nil, 0o600) }

// until yields to the host and polls for the gate, as a handler that awaits a timer does.
func until(ctx sdk.Context, name string) {
	for !exists(name) {
		_ = ctx.Sleep(2 * time.Millisecond)
	}
}

func Extension() *sdk.Extension {
	e := sdk.New("rlfix")
	gen := 1
	if data, err := os.ReadFile(filepath.Join(dir, "gen")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			gen = n + 1
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "gen"), []byte(strconv.Itoa(gen)), 0o600)
	g := "g" + strconv.Itoa(gen)
	// The reloaded process does not register until the test lets it go.
	if gen > 1 && os.Getenv("RL_LOAD") != "" {
		touch("loading")
		for !exists("load-gate") {
			time.Sleep(2 * time.Millisecond)
		}
	}
	e.Command("rl", "reload", func(ctx sdk.Context, _ string) error { return ctx.Reload() })
	e.Command("ping", "ping", func(ctx sdk.Context, _ string) error {
		ctx.Notify("ping "+g, "info")
		return nil
	})
	e.Command("held", "held", func(ctx sdk.Context, _ string) error {
		ctx.Notify("held running "+g, "info")
		until(ctx, "held-gate")
		ctx.Notify("held done "+g, "info")
		return nil
	})
	e.Command("ask", "ask", func(ctx sdk.Context, _ string) error {
		// The answer is recorded without the host: the handler's ctx is stale once a reload has run.
		value, _, _ := ctx.Input("gate", "")
		_ = os.WriteFile(filepath.Join(dir, "asked"), fmt.Appendf(nil, "%v", value), 0o600)
		ctx.Notify("ask done "+g, "info")
		return nil
	})
	e.OnSessionStart(func(ctx sdk.Context, event map[string]any) (any, error) {
		if event["reason"] != "reload" {
			return nil, nil
		}
		ctx.Notify("started "+g, "info")
		switch os.Getenv("RL_START") {
		case "await":
			until(ctx, "start-gate")
			ctx.Notify("start done "+g, "info")
		case "ask":
			value, _, _ := ctx.Input("start-ask", "")
			ctx.Notify(fmt.Sprintf("start answered %v %s", value, g), "info")
		}
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, event map[string]any) (any, error) {
		mode := os.Getenv("RL_SHUTDOWN")
		if event["reason"] != "reload" || mode == "" {
			return nil, nil
		}
		ctx.Notify("shutting down "+g, "info")
		switch mode {
		case "await":
			until(ctx, "shutdown-gate")
		case "ask":
			_, _, _ = ctx.Input("shutdown-ask", "")
		}
		ctx.Notify("shutdown done "+g, "info")
		return nil, nil
	})
	return e
}
