// Command p2 is p2's executable: `p2 server` (the default; Pi's `npm start`, src/hosts/server.ts) starts the server, and
// `p2 worker` is a session worker, which the server starts itself (the same executable, D473).
//
// pig divergence (D470): p2 is its own Go module and binary under apps/p2, outside Stock and Piglets.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/brand/sprite"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"

	// The plugins this executable ships (every directory of plugins/ with a plugin.go: plugins_test.go checks the list).
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/agent"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/codemode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/coding-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/conversations"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/dev-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/discord"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/file-index"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/history"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/locations"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/login"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/logs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/projects"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/proxy-marker"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/quiet-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/radius"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/search"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/voice"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace"
)

// closeDeadline is how long a signalled server may take to close before it exits anyway (below scripts/dev.mjs's 10 s
// SIGKILL upstream).
const closeDeadline = 8 * time.Second

func main() {
	command := "server"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case hosts.WorkerArg:
		hosts.RunWorker()
	case "server":
		os.Exit(runServer())
	case hosts.TUISubcommand:
		// The terminal client of a running server (Pi's `npm run tui`).
		os.Exit(hosts.RunTUI(os.Args[2:]))
	case "owner-link":
		// A private invitation, emitted to the invoking user's terminal only.
		link, err := p2runtime.OwnerLink(context.Background(), os.Args[2:], os.Getenv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(link)
	default:
		fmt.Fprintf(os.Stderr, "usage: p2 [server|worker|tui [url] [options]|owner-link [base-url]]\n")
		os.Exit(2)
	}
}

func runServer() int {
	// Provider logins and server plugins fetch in this process too.
	p2runtime.ConfigureFetch()
	options := hosts.ServerOptions{}
	// The branded executable draws the sprite the user chose with PiG's `/sprite` (brand/sprite); a failure leaves the mark's
	// default pig.
	if home, err := sprite.DefaultHome(); err == nil {
		if source, err := sprite.Open(home); err == nil {
			options.Sprite = source
		} else {
			fmt.Fprintln(os.Stderr, "p2: sprite selection unavailable:", err)
		}
	}
	server, err := hosts.StartServer(options)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("p2: http://127.0.0.1:%d (data %s)\n", server.Port, server.DataDir)
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	// Every way out goes through the returned code: a signal's default action would skip the close (the lock files, the
	// MCP servers' process groups).
	<-signals
	closed := make(chan struct{})
	go func() {
		_ = server.Close(context.Background())
		close(closed)
	}()
	deadline := time.After(closeDeadline)
	for {
		select {
		case received := <-signals:
			fmt.Fprintf(os.Stderr, "p2: %v again, exiting without finishing the close\n", received)
			return 1
		case <-closed:
			return 0
		case <-deadline:
			fmt.Fprintf(os.Stderr, "p2: the close did not finish within %d s, exiting\n", int(closeDeadline/time.Second))
			return 1
		}
	}
}
