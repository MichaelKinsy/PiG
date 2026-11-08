// Command baseline is the stand-in for `p2 server` (apps/p2/cmd/p2 does not exist yet) that the desktop app is measured
// against: the same hosts.StartServer, the same plugins, no window. A browser tab opened on its URL is the other half of
// the comparison. It prints its port to stdout and runs until SIGINT or SIGTERM.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == hosts.WorkerArg {
		hosts.RunWorker()
		return
	}
	zero := 0
	server, err := hosts.StartServer(hosts.ServerOptions{Port: &zero})
	if err != nil {
		fmt.Fprintln(os.Stderr, "baseline:", err)
		os.Exit(1)
	}
	fmt.Println(server.Port)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	_ = server.Close(context.Background())
}
