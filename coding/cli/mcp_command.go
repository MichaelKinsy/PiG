//go:build !pig_strip_mcp

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
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
	// pig additive (D202): a Piglet Binary built with build.closed starts only the servers its closure lists, so it adds none.
	if message := pigletBinaryMcpRefusal(currentPigletBinary(), args); message != "" {
		fmt.Fprintln(os.Stderr, message)
		return 1
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
	providerToken, closeProviderTokens := newMcpProviderTokenReader(agentDir)
	defer closeProviderTokens()
	return mcpext.RunMcpCommand(context.Background(), args[1:], mcpext.McpCommandOptions{
		Cwd:           cwd,
		AgentDir:      agentDir,
		AppName:       codingagent.AppName,
		ConfigDirName: codingagent.ConfigDirName(),
		ProviderToken: providerToken,
		OpenURL:       codingagent.OpenBrowser,
		IsProjectTrusted: func(cwd string) (bool, error) {
			decision, err := trust.Get(cwd)
			return err == nil && decision != nil && *decision, err
		},
		Interactive: term.IsTerminal(int(os.Stdin.Fd())),
		Color:       chalkColorLevel(environMap(os.Environ()), args, term.IsTerminal(int(os.Stdout.Fd()))) > 0,
	})
}

// newMcpProviderTokenReader reads provider tokens from the agent directory's credentials, refreshing an expired OAuth token as a session does
// (Pi 4d72d05bc createProviderTokenReader). The model runtime is created on the first request, so a server without `auth.provider` never loads it. The
// returned function releases it. A missing or unrefreshable credential gives "": the server's 401 then reports that sign-in is needed.
func newMcpProviderTokenReader(agentDir string) (read func(ctx context.Context, provider string) string, closeRuntime func()) {
	var (
		once    sync.Once
		runtime *coding.ModelRuntime
	)
	read = func(ctx context.Context, provider string) string {
		// `list` connects to the servers concurrently.
		once.Do(func() {
			runtime, _ = coding.CreateModelRuntime(ctx, coding.CreateModelRuntimeOptions{
				AuthPath:        filepath.Join(agentDir, "auth.json"),
				RefreshOnCreate: new(false),
			})
		})
		if runtime == nil {
			return ""
		}
		auth, err := runtime.GetAuth(ctx, provider)
		if err != nil || auth == nil {
			return ""
		}
		return auth.Auth.APIKey
	}
	return read, func() {
		if runtime != nil {
			runtime.Close()
		}
	}
}
