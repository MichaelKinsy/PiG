// Command rpc-settle-wait-go is a Go-SDK extension whose agent_settled handler and hold command wait on a plain TCP read (the spin command computes instead, on a busy loop the peer ends): they dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. Without RPC_SETTLE_ADDR they return at once. With RPC_SETTLE_DONE the hold command writes that file once its wait returned. The agent_settled handler and hold command dial RPC_SETTLE_ADDR; the input handler dials RPC_SETTLE_INPUT_ADDR and the before_agent_start handler RPC_SETTLE_START_ADDR. The wait is no SDK call, so nothing but the read itself tells the extension runtime from a handler that computes.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func wait(env string) error {
	addr := os.Getenv(env)
	if addr == "" {
		return nil
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, "waiting"); err != nil {
		return err
	}
	_, err = bufio.NewReader(conn).ReadString('\n')
	return err
}

// spin computes without waiting: it announces itself, then busy-loops on a flag until the peer's line arrives. The line is read by a second goroutine, so the command's own goroutine makes no blocking call and no SDK call; only its age tells it from a handler that waits.
func spin(env string) error {
	conn, err := net.Dial("tcp", os.Getenv(env))
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, "waiting"); err != nil {
		return err
	}
	var released atomic.Bool
	go func() {
		_, _ = bufio.NewReader(conn).ReadString('\n')
		released.Store(true)
	}()
	for !released.Load() {
	}
	return nil
}

func main() {
	ext := sdk.New("rpc-settle-wait-go")
	ext.OnEvent(sdk.EventAgentSettled, func(sdk.Context, map[string]any) (any, error) { return nil, wait("RPC_SETTLE_ADDR") })
	ext.OnEvent(sdk.EventInput, func(sdk.Context, map[string]any) (any, error) { return nil, wait("RPC_SETTLE_INPUT_ADDR") })
	ext.OnEvent(sdk.EventBeforeAgentStart, func(sdk.Context, map[string]any) (any, error) { return nil, wait("RPC_SETTLE_START_ADDR") })
	// Handlers of the two events Pi emits without awaiting them: with RPC_NAME_ADDR they wait on the peer, with RPC_NAME_BUSY_MS they compute for that long without waiting, and then they notify.
	for _, event := range []string{sdk.EventSessionInfoChanged, sdk.EventThinkingLevelSelect} {
		ext.OnEvent(event, func(ctx sdk.Context, _ map[string]any) (any, error) {
			busy, _ := strconv.Atoi(os.Getenv("RPC_NAME_BUSY_MS"))
			if os.Getenv("RPC_NAME_ADDR") == "" && busy == 0 {
				return nil, nil
			}
			for end := time.Now().Add(time.Duration(busy) * time.Millisecond); time.Now().Before(end); {
				// computing: nothing waits
			}
			if err := wait("RPC_NAME_ADDR"); err != nil {
				return nil, err
			}
			ctx.Notify("name-handler-done:"+event, "info")
			return nil, nil
		})
	}
	ext.Command("hold", "wait on a socket read", func(sdk.Context, string) error {
		if err := wait("RPC_SETTLE_ADDR"); err != nil {
			return err
		}
		if done := os.Getenv("RPC_SETTLE_DONE"); done != "" {
			return os.WriteFile(done, []byte("done\n"), 0o600)
		}
		return nil
	})
	ext.Command("spin", "compute without waiting until the peer writes a line", func(sdk.Context, string) error { return spin("RPC_SETTLE_ADDR") })
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rpc-settle-wait-go: %v\n", err)
		os.Exit(1)
	}
}
