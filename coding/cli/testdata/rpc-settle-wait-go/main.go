// Command rpc-settle-wait-go is a Go-SDK extension whose agent_settled handler and hold command wait on a plain TCP read: they dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. Without RPC_SETTLE_ADDR they return at once. The agent_settled handler and hold command dial RPC_SETTLE_ADDR; the input handler dials RPC_SETTLE_INPUT_ADDR. The wait is no SDK call, so nothing but the read itself tells the extension runtime from a handler that computes.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"

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

func main() {
	ext := sdk.New("rpc-settle-wait-go")
	ext.OnEvent(sdk.EventAgentSettled, func(sdk.Context, map[string]any) (any, error) { return nil, wait("RPC_SETTLE_ADDR") })
	ext.OnEvent(sdk.EventInput, func(sdk.Context, map[string]any) (any, error) { return nil, wait("RPC_SETTLE_INPUT_ADDR") })
	ext.Command("hold", "wait on a socket read", func(sdk.Context, string) error { return wait("RPC_SETTLE_ADDR") })
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rpc-settle-wait-go: %v\n", err)
		os.Exit(1)
	}
}
