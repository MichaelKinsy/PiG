// Command headless embeds PiG as a library: a coding session with MCP servers and one provider (Anthropic), and no
// terminal. It reads a prompt from its arguments, runs it, and prints the last assistant message.
//
//	ANTHROPIC_API_KEY=... go run ./examples/headless "list the files in the current directory"
//
// MCP servers come from mcp.json in the agent directory and in the project's .pig directory, as for the pig command.
//
// The size of this program is what an embedder pays for MCP, the session API and the provider code. Build it with the
// strip tags that drop the features it never uses (see docs/site/docs/embedding.md and TestHeadlessEmbedBinarySizeGate).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// model is the one provider and model this program uses: provider id, then model id.
const model = "anthropic/claude-sonnet-4-5"

// configDirName is the name of the per-project configuration directory (.pig/mcp.json, .pig/settings.json).
const configDirName = ".pig"

func main() {
	if err := runUntilInterrupted(strings.Join(os.Args[1:], " ")); err != nil {
		fmt.Fprintln(os.Stderr, "headless:", err)
		os.Exit(1)
	}
}

func runUntilInterrupted(prompt string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return run(ctx, prompt, os.Stdout)
}

func run(ctx context.Context, prompt string, stdout io.Writer) (err error) {
	if prompt == "" {
		return errors.New("usage: headless <prompt>")
	}
	agentDir := coding.DefaultAgentDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{
		AgentDir: agentDir,
		ResourceLoaderOptions: &coding.DefaultResourceLoaderOptions{
			// No extension files: the embedder's own factory is the only extension. Options.Terminal stays nil, so the
			// MCP extension has no /mcp manager view and no tool renderers.
			NoExtensions: true,
			ExtensionFactories: []extension.InlineExtension{extension.NamedInlineExtension{
				Name:    "mcp",
				Factory: mcpext.CreateMcpExtension(mcpext.Options{AgentDir: agentDir, ConfigDirName: configDirName}),
			}},
		},
	})
	if err != nil {
		return err
	}
	defer services.Close()

	chosen, err := coding.BuildModel(model, services)
	if err != nil {
		return err
	}
	created, err := coding.CreateAgentSessionFromServices(coding.CreateAgentSessionFromServicesOptions{Services: services, Model: chosen})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, created.Session.Close()) }()

	// Binding starts the extensions: the MCP extension connects its servers on session_start.
	if err := created.Session.BindExtensions(ctx, coding.ExtensionBindings{}); err != nil {
		return err
	}
	if _, err := created.Session.Send(ctx, prompt); err != nil {
		return err
	}
	if text := created.Session.LastAssistantText(); text != nil {
		_, err = fmt.Fprintln(stdout, *text)
	}
	return err
}
