// Command rpc-shutdown-go is a Go-SDK extension whose goask command awaits a dialog that no test answers and whose goexec command awaits a child process. RPC_SHUTDOWN_GO_SETTLED_BLOCK makes its agent_settled handler wait until the host cancels it. The runtime has no microtask continuation, so the host counts the command suspended only at a checkpoint.
package main

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func main() {
	ext := sdk.New("rpc-shutdown-go")
	ext.Command("goask", "wait on a select", func(ctx sdk.Context, _ string) error {
		_, _, err := ctx.Select("GoAsk", []string{"yes"})
		return err
	})
	ext.Command("goexec", "wait on a child process", func(ctx sdk.Context, _ string) error {
		_, err := ctx.Exec("sleep", []string{"2"})
		return err
	})
	if os.Getenv("RPC_SHUTDOWN_GO_SETTLED_BLOCK") != "" {
		ext.OnEvent(sdk.EventAgentSettled, func(ctx sdk.Context, _ map[string]any) (any, error) {
			<-ctx.Done()
			return nil, nil
		})
	}
	if err := ext.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "rpc-shutdown-go: %v\n", err)
		os.Exit(1)
	}
}
