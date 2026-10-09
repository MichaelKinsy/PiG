//go:build pig_experimental

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// Ports packages/coding-agent/src/experimental/cli.ts

// ExperimentalMain is the entry of the pig_experimental executable (experimental/cli.ts): an internal process role, else an experimental command, else Main.
func ExperimentalMain(args []string) {
	ctx := context.Background()
	// The selected internal entry owns validation and consumption; this lookup only chooses that entry.
	if selected, internal := os.LookupEnv(experimental.InternalProcessEnv); internal {
		role := experimental.InternalProcessRole(selected)
		if err := runInternalProcess(ctx, role, args); err != nil {
			if role != "session-worker" {
				fmt.Fprintln(os.Stderr, err)
			}
			os.Exit(1)
		}
		return
	}
	if handled, code := runExperimentalCommand(ctx, args); handled {
		if args[0] == "client" || code != 0 {
			os.Exit(code)
		}
		return
	}
	Main(args, nil)
}

func runInternalProcess(ctx context.Context, role experimental.InternalProcessRole, args []string) error {
	if role == "coordinator" {
		return experimental.RunCoordinatorEntry(ctx, args)
	}
	consumed, err := experimental.ConsumeInternalProcessRole()
	if err != nil {
		return err
	}
	switch consumed {
	case "server":
		return experimental.RunServerProcess(ctx, args)
	case "session-worker":
		return experimental.RunSessionWorkerProcess(ctx, args)
	default:
		return fmt.Errorf("Unsupported internal process role: %s", role)
	}
}

// Ports packages/coding-agent/src/experimental/commands.ts

func runExperimentalCommand(ctx context.Context, args []string) (bool, int) {
	if !codingagent.AreExperimentalFeaturesEnabled() || len(args) == 0 || (args[0] != "server" && args[0] != "client") {
		return false, 0
	}
	// pig additive (D92): a Piglet that strips the experimental server leaves `server` and `client` to the stable CLI.
	// --piglet selects it as in the stable CLI, ahead of PIG_PIGLET_PATH and PIG_PIGLET_NAME; the experimental parser
	// never sees the flag, which it would reject as an existing CLI option. The entry reads the Piglet's strip list and
	// records none of it: the stable CLI records the whole list when it takes the command, and the experimental server
	// applies no Piglet, so a partial strip state would hide built-ins its sessions still run.
	pigletFlag, args := takePigletFlag(args)
	if p, _ := resolvePiglet(pigletFlag); p != nil && p.Strip.HasFeature(pigstrip.ExperimentalServer) {
		return false, 0
	}
	// The selected entry initializes process markers once; stable fallback retains its own unchanged startup sequence.
	setupCli()
	red := func(text string) string {
		if chalkColorLevel(environMap(os.Environ()), args, term.IsTerminal(int(os.Stdout.Fd()))) > 0 {
			return "\x1b[31m" + text + "\x1b[39m"
		}
		return text
	}
	result, err := experimental.Cli.Execute(ctx, args, experimental.CliContext{
		RunServer: runServerCommand,
		RunClient: runClientCommand,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, red("Error: "+err.Error()))
		return true, 1
	}
	if !result.Ok {
		for _, diagnostic := range result.Errors {
			fmt.Fprintln(os.Stderr, red("Error: "+diagnostic))
		}
		return true, 1
	}
	return true, 0
}

// takePigletFlag removes every --piglet <value> and --piglet=<value> before a "--" from args, and returns the last
// value keyed as parseFlags keys it for resolvePiglet. A --piglet with no value maps to true, which resolvePiglet
// reports.
func takePigletFlag(args []string) (map[string]any, []string) {
	flags := map[string]any{}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--":
			return flags, append(rest, args[i:]...)
		case strings.HasPrefix(arg, "--piglet="):
			flags["piglet"] = strings.TrimPrefix(arg, "--piglet=")
		case arg == "--piglet" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			i++
			flags["piglet"] = args[i]
		case arg == "--piglet":
			flags["piglet"] = true
		default:
			rest = append(rest, arg)
		}
	}
	return flags, rest
}

func runServerCommand(ctx context.Context, command experimental.ServerCommand) (err error) {
	pluginPackages := command.PluginPackages
	if pluginPackages == nil {
		pluginPackages = []string{}
	}
	// pig divergence (D64): experimental Radius is designed out, so the foreground server opens no relay and reports no Radius status.
	runtime, err := experimental.StartForegroundServer(ctx, experimental.StartServerOptions{
		ServerId: command.ServerId, SessionDir: command.SessionDir,
		Provider: command.Provider, Model: command.Model,
		PluginPackages: pluginPackages,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Server: %s\n", runtime.ServerId)
	fmt.Printf("Socket: %s\n", runtime.SocketPath)
	defer func() {
		if closeErr := runtime.Close(); closeErr != nil {
			err = closeErr
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-signals:
		return nil
	case <-runtime.Closed():
		return runtime.ClosedError()
	}
}

func runClientCommand(ctx context.Context, command experimental.ClientCommand) error {
	if command.Prompt == nil && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		return experimental.RunClientTui(ctx, command, experimental.RunClientTuiOptions{})
	}
	result, err := experimental.RunClient(ctx, command, experimental.RunClientOptions{})
	if err != nil {
		return err
	}
	return writeClientResult(os.Stdout, result)
}

func writeClientResult(output io.Writer, result experimental.ClientResult) error {
	switch result := result.(type) {
	case experimental.ClientAttachedResult:
		_, err := fmt.Fprintf(output, "%s\t%s\tattached\n", result.ServerId, result.SessionId)
		return err
	case experimental.ClientPromptedResult:
		_, err := fmt.Fprintln(output, result.Text)
		return err
	case experimental.ClientListResult:
		for _, session := range result.Sessions {
			if _, err := fmt.Fprintf(output, "%s\t%s\n", session.ServerId, session.SessionId); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("Unexpected client result: %T", result)
	}
}
