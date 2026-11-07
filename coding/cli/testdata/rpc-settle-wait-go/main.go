// Command rpc-settle-wait-go is a Go-SDK extension whose agent_settled handler and hold command wait on a plain TCP read: they dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. Without RPC_SETTLE_ADDR they return at once. The wait is no SDK call, so nothing but the read itself tells the extension runtime from a handler that computes.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func wait() error {
	if os.Getenv("RPC_SETTLE_ADDR") == "" {
		return nil
	}
	conn, err := net.Dial("tcp", os.Getenv("RPC_SETTLE_ADDR"))
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
	ext.OnEvent(sdk.EventAgentSettled, func(sdk.Context, map[string]any) (any, error) { return nil, wait() })
	ext.Command("hold", "wait on a socket read", func(sdk.Context, string) error { return wait() })
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rpc-settle-wait-go: %v\n", err)
		os.Exit(1)
	}
}
