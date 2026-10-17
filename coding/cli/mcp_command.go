//go:build !pig_strip_mcp

package cli

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// runMcpCommand runs `pig mcp <command>` and returns its exit code, or -1 when args are not an `mcp` command. Upstream routes it after the package and config commands and before parsing arguments (main.ts:608-612).
//
// This file and the `coding/mcpext` import are the boundary a Piglet strips with the pig_strip_mcp build tag (docs/design/builtin-mcp.md).
func runMcpCommand(args []string) int {
	if len(args) == 0 || args[0] != "mcp" {
		return -1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pig mcp:", err)
		return 1
	}
	agentDir := codingagent.AgentDir()
	// main.ts:591-593 applies the global proxy setting before it routes the command.
	bootstrap := codingagent.NewSettingsManagerWithOptions(cwd, agentDir, codingagent.SettingsManagerCreateOptions{ProjectTrusted: new(false)})
	if err := ai.ApplyHTTPProxySettings(bootstrap.GetGlobalSettings().HTTPProxy); err != nil {
		fmt.Fprintln(os.Stderr, "pig mcp:", err)
		return 1
	}
	trust := codingagent.NewProjectTrustStore(agentDir)
	return mcpext.RunMcpCommand(context.Background(), args[1:], mcpext.McpCommandOptions{
		Cwd:           cwd,
		AgentDir:      agentDir,
		AppName:       codingagent.AppName,
		ConfigDirName: codingagent.ConfigDirName(),
		OpenURL:       codingagent.OpenBrowser,
		IsProjectTrusted: func(cwd string) (bool, error) {
			decision, err := trust.Get(cwd)
			return err == nil && decision != nil && *decision, err
		},
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Color:       chalkColorLevel(environMap(os.Environ()), args, term.IsTerminal(int(os.Stdout.Fd()))) > 0,
	})
}
