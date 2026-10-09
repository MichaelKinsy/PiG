package experimental

// pi: packages/coding-agent/src/cli/experimental/cli.ts

// pi: packages/coding-agent/src/cli/experimental/commands/server.ts

// pi: packages/coding-agent/src/cli/experimental/command.ts

// pi: packages/coding-agent/src/cli/experimental/command-options.ts

// pi: packages/coding-agent/src/cli/experimental/commands/client.ts

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestExperimentalCLICommandComposition(t *testing.T) {
	t.Parallel()

	// upstream: packages/coding-agent/test/experimental-cli-resolution.test.ts:5
	t.Run("requires an experimental subcommand", func(t *testing.T) {
		got, err := Cli.Parse(nil)
		want := CommandParseResult{Errors: []string{"Expected experimental command: server or client"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
		}
	})

	// upstream: packages/coding-agent/test/experimental-cli-resolution.test.ts:12
	t.Run("passes server options to the command action", func(t *testing.T) {
		var calls []ServerCommand
		got, err := Cli.Execute(t.Context(), []string{"server", "--server-id", "00000000-0000-4000-8000-000000000001", "--session-dir", "./sessions", "--provider", "anthropic", "--model", "claude-sonnet-4-5"}, CliContext{
			RunServer: func(_ context.Context, command ServerCommand) error {
				calls = append(calls, command)
				return nil
			},
			RunClient: func(context.Context, ClientCommand) error { return nil },
		})
		command := ServerCommand{Command: "server", ServerId: new("00000000-0000-4000-8000-000000000001"), SessionDir: new("./sessions"), Provider: new("anthropic"), Model: new("claude-sonnet-4-5")}
		want := CommandExecutionResult{Ok: true, Command: command}
		if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(calls, []ServerCommand{command}) {
			t.Fatalf("execute = %#v, %v; calls %#v; want %#v and one call with %#v", got, err, calls, want, command)
		}
	})

	// upstream: packages/coding-agent/test/experimental-cli-resolution.test.ts:40 (server and client)
	for _, name := range []string{"server", "client"} {
		t.Run("executes the parsed "+name+" command", func(t *testing.T) {
			serverCalls, clientCalls := 0, 0
			got, err := Cli.Execute(t.Context(), []string{name}, CliContext{
				RunServer: func(context.Context, ServerCommand) error { serverCalls++; return nil },
				RunClient: func(context.Context, ClientCommand) error { clientCalls++; return nil },
			})
			var command NamedCommandInvocation = ClientCommand{Command: name}
			wantServerCalls, wantClientCalls := 0, 1
			if name == "server" {
				command = ServerCommand{Command: name}
				wantServerCalls, wantClientCalls = 1, 0
			}
			want := CommandExecutionResult{Ok: true, Command: command}
			if err != nil || !reflect.DeepEqual(got, want) || serverCalls != wantServerCalls || clientCalls != wantClientCalls {
				t.Fatalf("execute = %#v, %v; calls server=%d client=%d; want %#v, server=%d client=%d", got, err, serverCalls, clientCalls, want, wantServerCalls, wantClientCalls)
			}
		})
	}
}

// Source-derived guard: command.ts awaits the action; action rejection is not a parser diagnostic.
// upstream: packages/coding-agent/src/cli/experimental/command.ts:131-142
func TestCommandExecutionWaitsAndPropagatesActionCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	started, finished := make(chan struct{}), make(chan struct{})
	var result CommandExecutionResult
	var executionErr error
	go func() {
		defer close(finished)
		result, executionErr = Cli.Execute(ctx, []string{"client"}, CliContext{
			RunClient: func(actionContext context.Context, _ ClientCommand) error {
				close(started)
				<-actionContext.Done()
				return actionContext.Err()
			},
		})
	}()
	defer func() { cancel(); <-finished }()
	<-started
	select {
	case <-finished:
		t.Fatal("Execute returned before the action completed")
	default:
	}
	cancel()
	<-finished
	if !errors.Is(executionErr, context.Canceled) || !reflect.DeepEqual(result, CommandExecutionResult{}) {
		t.Fatalf("execute = %#v, %v; want zero result and context cancellation", result, executionErr)
	}
}

// upstream: packages/coding-agent/src/cli/experimental/command.ts:137-140
func TestCommandExecutionErrorsAndInvalidInput(t *testing.T) {
	t.Parallel()
	failure := errors.New("action rejected")
	got, err := Cli.Execute(t.Context(), []string{"server"}, CliContext{
		RunServer: func(context.Context, ServerCommand) error { return failure },
	})
	if !errors.Is(err, failure) || err.Error() != failure.Error() || errors.Unwrap(err) != nil || !reflect.DeepEqual(got, CommandExecutionResult{}) {
		t.Fatalf("execute = %#v, %v; want unchanged action error", got, err)
	}
	calls := 0
	got, err = Cli.Execute(t.Context(), []string{"server", "--provider", "anthropic"}, CliContext{
		RunServer: func(context.Context, ServerCommand) error { calls++; return nil },
	})
	want := CommandExecutionResult{Errors: []string{"--provider requires --model"}}
	if err != nil || calls != 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("execute invalid = %#v, %v; calls %d; want %#v and no calls", got, err, calls, want)
	}
}

// upstream: packages/coding-agent/src/cli/experimental/command.ts:88-123,139-163
func TestCommandCompositionErrors(t *testing.T) {
	t.Parallel()
	command := NewCommand("client")
	option := StringOption("--model", false)
	if err := command.Option(option); err != nil {
		t.Fatal(err)
	}
	if err := command.Option(option); err == nil || err.Error() != "Option --model is already registered for client" {
		t.Fatalf("duplicate option = %v", err)
	}
	group := NewCommand("experimental")
	if err := group.Command(command); err != nil {
		t.Fatal(err)
	}
	if err := group.Command(command); err == nil || err.Error() != "Command client is already registered" {
		t.Fatalf("duplicate subcommand = %v", err)
	}
	if _, err := command.Parse(nil); err == nil || err.Error() != "Command client does not define a builder" {
		t.Fatalf("missing builder = %v", err)
	}
	command.Build(func(ParsedCommandInput) CommandParseResult { return CommandParseResult{} })
	if _, err := command.Parse(nil); err == nil || err.Error() != "Command client failed without an error" {
		t.Fatalf("failed builder = %v", err)
	}
	command.Build(func(ParsedCommandInput) CommandParseResult {
		return CommandParseResult{Ok: true, Command: ClientCommand{Command: "client"}}
	})
	if _, err := command.Execute(t.Context(), nil, CliContext{}); err == nil || err.Error() != "Command client does not define an action" {
		t.Fatalf("missing action = %v", err)
	}
}
