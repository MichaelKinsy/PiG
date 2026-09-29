package experimental

import "context"

// Ports packages/coding-agent/src/cli/experimental/command-options.ts

// TransportAddress is a parsed Unix address. Experimental Radius addresses are designed out (D64).
type TransportAddress struct {
	Transport string
	Path      string
}

// Ports packages/coding-agent/src/cli/experimental/commands/client.ts

// ClientCommand preserves absent selections as nil, including absent versus explicitly empty PluginPackages.
type ClientCommand struct {
	Command        string
	Connect        *TransportAddress
	SessionId      *string
	Continue       *bool
	Resume         *bool
	Provider       *string
	Model          *string
	PluginPackages []string
	Prompt         *string
}

func (ClientCommand) commandInvocation() {}

// Ports packages/coding-agent/src/cli/experimental/commands/server.ts

// ServerCommand contains the server options before runtime defaults are applied.
type ServerCommand struct {
	Command        string
	Provider       *string
	Model          *string
	PluginPackages []string
	ServerId       *string
	SessionDir     *string
}

func (ServerCommand) commandInvocation() {}

// Ports packages/coding-agent/src/cli/experimental/command.ts

// NamedCommandInvocation is the closed union of experimental CLI invocations.
type NamedCommandInvocation interface {
	commandInvocation()
}

// CommandParseResult distinguishes CLI diagnostics from successful invocations. Go errors carry upstream exceptions, not option diagnostics.
type CommandParseResult struct {
	Ok      bool
	Command NamedCommandInvocation
	Errors  []string
}

// CommandExecutionResult has the same shape as a parse result; Execute returns only after the selected action completes.
type CommandExecutionResult = CommandParseResult

// CliContext supplies the two awaited experimental command actions. Each action owns its work until it returns.
type CliContext struct {
	RunServer func(context.Context, ServerCommand) error
	RunClient func(context.Context, ClientCommand) error
}
