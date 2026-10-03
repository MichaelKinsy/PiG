//go:build pig_experimental

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental"
)

// Ports packages/coding-agent/src/experimental/cli.ts

func main() {
	args := os.Args[1:]
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
	runStableCLI()
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
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		agentDir := codingagent.AgentDir()
		settings := codingagent.NewSettingsManager(cwd, agentDir)
		// client-tui.ts:731-741 loads a DefaultResourceLoader with only themes enabled.
		themes := collectThemePaths(cwd, agentDir, settings, CLIFlags{}, settings.IsProjectTrusted())
		return experimental.RunClientTui(ctx, command, experimental.RunClientTuiOptions{ThemePaths: themes})
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
