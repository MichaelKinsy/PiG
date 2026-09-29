package experimental

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// Ports packages/coding-agent/src/cli/experimental/commands/client.ts
// Ports packages/coding-agent/src/cli/experimental/commands/server.ts
// Ports packages/coding-agent/src/cli/experimental/cli.ts

var (
	sessionIdOption     = StringOption("--session-id", false)
	continueOption      = FlagOption("--continue")
	continueShortOption = FlagOption("-c")
	resumeOption        = FlagOption("--resume")
	resumeShortOption   = FlagOption("-r")
	providerOption      = StringOption("--provider", false)
	modelOption         = StringOption("--model", false)
	pluginPackageOption = StringOption("-e", true)
	serverIdOption      = ValueOption("--server-id", func(value string) (any, error) {
		if !protocol.IsServerId(value) {
			return nil, fmt.Errorf("Invalid --server-id \"%s\"; expected a lowercase UUIDv4", value)
		}
		return value, nil
	}, false)
	sessionDirOption = StringOption("--session-dir", false)
)

// Cli parses the development-only server/client group without consulting the stable CLI or environment.
var Cli = createCli()

func createCli() *Command {
	client := NewCommand("client").Build(buildClientCommand).Action(func(ctx context.Context, invocation NamedCommandInvocation, commandContext CliContext) error {
		return commandContext.RunClient(ctx, invocation.(ClientCommand))
	})
	registerCommandOptions(client, connectOption, sessionIdOption, continueOption, continueShortOption, resumeOption, resumeShortOption, providerOption, modelOption, pluginPackageOption)
	server := NewCommand("server").Build(buildServerCommand).Action(func(ctx context.Context, invocation NamedCommandInvocation, commandContext CliContext) error {
		return commandContext.RunServer(ctx, invocation.(ServerCommand))
	})
	registerCommandOptions(server, serverIdOption, sessionDirOption, providerOption, modelOption, pluginPackageOption)
	group := NewCommand("experimental").Build(func(ParsedCommandInput) CommandParseResult {
		return CommandParseResult{Errors: []string{"Expected experimental command: server or client"}}
	})
	for _, subcommand := range []*Command{server, client} {
		if err := group.Command(subcommand); err != nil {
			panic(err)
		}
	}
	return group
}

func registerCommandOptions(command *Command, options ...CommandOption) {
	for _, option := range options {
		if err := command.Option(option); err != nil {
			panic(err)
		}
	}
}

func buildClientCommand(input ParsedCommandInput) CommandParseResult {
	var diagnostics []string
	connect, _ := input.Value(connectOption).(*TransportAddress)
	command := ClientCommand{
		Command: "client", Connect: connect,
		SessionId: commandString(input, sessionIdOption),
		Continue:  commandFlag(input, continueOption, continueShortOption),
		Resume:    commandFlag(input, resumeOption, resumeShortOption),
		Provider:  commandString(input, providerOption), Model: commandString(input, modelOption),
		PluginPackages: commandStrings(input, pluginPackageOption),
	}
	promptArgs := input.RemainingArgs
	separator := len(promptArgs) > 0 && promptArgs[0] == "--"
	if separator {
		promptArgs = promptArgs[1:]
	}
	if len(promptArgs) == 1 && (separator || !strings.HasPrefix(promptArgs[0], "-")) && promptArgs[0] != "" {
		command.Prompt = new(promptArgs[0])
	}
	if command.Provider != nil && command.Model == nil {
		diagnostics = append(diagnostics, "--provider requires --model")
	}
	selected := 0
	for _, present := range []bool{command.SessionId != nil, command.Continue != nil, command.Resume != nil} {
		if present {
			selected++
		}
	}
	if selected > 1 {
		diagnostics = append(diagnostics, "--session-id, --continue, and --resume are mutually exclusive")
	}
	if command.Prompt == nil {
		diagnostics = append(diagnostics, unsupportedOptions("client", input)...)
	}
	if len(diagnostics) > 0 {
		return CommandParseResult{Errors: diagnostics}
	}
	return CommandParseResult{Ok: true, Command: command}
}

func buildServerCommand(input ParsedCommandInput) CommandParseResult {
	var diagnostics []string
	command := ServerCommand{
		Command:  "server",
		ServerId: commandString(input, serverIdOption), SessionDir: commandString(input, sessionDirOption),
		Provider: commandString(input, providerOption), Model: commandString(input, modelOption),
		PluginPackages: commandStrings(input, pluginPackageOption),
	}
	if command.Provider != nil && command.Model == nil {
		diagnostics = append(diagnostics, "--provider requires --model")
	}
	diagnostics = append(diagnostics, unsupportedOptions("server", input)...)
	if len(diagnostics) > 0 {
		return CommandParseResult{Errors: diagnostics}
	}
	return CommandParseResult{Ok: true, Command: command}
}
