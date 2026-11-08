package cli

// Ports packages/coding-agent/src/main.ts runAuthCommand: the `auth` command
// dispatch, its output, and its exit codes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const credentialPrintTimeout = 15 * time.Second // upstream: coding-agent/src/main.ts:resolveCredentialForPrint

// authRunEnv is the process state runAuthCommand reads.
type authRunEnv struct {
	stdout, stderr io.Writer
	agentDir       string
	color          bool
}

func authJSONPath(agentDir string) string { return filepath.Join(agentDir, "auth.json") }

// runAuthCommand handles `pig auth ...`. It returns -1 for other commands,
// otherwise the process exit code.
func runAuthCommand(args []string) int {
	// Only `pig auth` needs the agent directory; any other command (`pig --version` included) must not fail on it.
	if len(args) == 0 || args[0] != "auth" {
		return -1
	}
	return runAuthCommandWith(args, authRunEnv{
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		agentDir: agentDirForModel(),
		color:    chalkStdoutColor(),
	})
}

func (env authRunEnv) red(text string) string {
	if !env.color {
		return text
	}
	return codingagent.ChalkApply("\x1b[31m", "\x1b[39m", text)
}

func (env authRunEnv) dim(text string) string {
	if !env.color {
		return text
	}
	return codingagent.ChalkApply("\x1b[2m", "\x1b[22m", text)
}

func (env authRunEnv) printError(message string) {
	writeCLIError(env.stderr, message, env.color)
}

func authErrorMessage(err error, fallback string) string {
	if commandErr, ok := errors.AsType[*AuthCommandError](err); ok {
		return commandErr.Message
	}
	return fallback
}

func runAuthCommandWith(args []string, env authRunEnv) int {
	if IsAuthCommandHelp(args) {
		_, _ = fmt.Fprintln(env.stdout, authCommandHelp)
		return 0
	}
	if len(args) > 1 && args[0] == "auth" && (args[1] == "login" || args[1] == "logout") {
		// main already applied --offline.
		rest := slices.DeleteFunc(slices.Clone(args[2:]), func(arg string) bool { return arg == "--offline" })
		if args[1] == "login" {
			return runAuthLoginCommand(rest, env)
		}
		return runAuthLogoutCommand(rest, env)
	}
	command, err := ParseAuthCommand(args)
	if err != nil {
		env.printError(authErrorMessage(err, "Failed to parse auth command"))
		return 1
	}
	if command == nil {
		return -1
	}
	flags := parseArgs(command.Args)
	if option, ok := firstUnknownFlag(flags); ok {
		_, _ = fmt.Fprintln(env.stderr, env.red(fmt.Sprintf(`Unknown option --%s for "%s".`, option, GetAuthCommandName(command.Kind))))
		_, _ = fmt.Fprintln(env.stderr, env.dim(fmt.Sprintf(`Use "%s --help" or "%s".`, codingagent.AppName, GetAuthCommandUsage(command.Kind))))
		return 1
	}
	code, err := runParsedAuthCommand(command, flags, env)
	if err != nil {
		env.printError(authErrorMessage(err, "Failed to resolve credential"))
		if command.Kind == AuthCommandCheck {
			return 2
		}
		return 1
	}
	return code
}

// runAuthLoginCommand ports main.ts runAuthLoginCommand: it signs in to one provider and returns the exit code. Ctrl+C ends the login.
func runAuthLoginCommand(args []string, env authRunEnv) int {
	cwd, err := processCwd()
	if err != nil {
		env.printError(err.Error())
		return 1
	}
	settingsManager := codingagent.NewSettingsManagerWithOptions(cwd, env.agentDir, codingagent.SettingsManagerCreateOptions{ProjectTrusted: new(false)})
	if err := configureHTTPDispatcherFromSettings(settingsManager); err != nil {
		env.printError(err.Error())
		return 1
	}
	createCtx, cancelCreate := context.WithTimeout(context.Background(), credentialPrintTimeout) //portlint:allow ctxbackground CLI entry point owns the command
	modelRuntime, err := coding.CreateModelRuntime(createCtx, coding.CreateModelRuntimeOptions{AuthPath: authJSONPath(env.agentDir), AllowModelNetwork: false})
	cancelCreate()
	if err != nil {
		env.printError(err.Error())
		return 1
	}
	defer modelRuntime.Close()

	ctx, cancel := context.WithCancel(context.Background()) //portlint:allow ctxbackground CLI entry point owns the command
	defer cancel()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	go func() {
		select {
		case <-interrupts:
			cancel()
		case <-ctx.Done():
		}
	}()
	return RunAuthLogin(ctx, args, AuthLoginOptions{
		ModelRuntime: modelRuntime,
		Terminal:     CreateProcessLoginTerminal(cancel),
		AgentDir:     env.agentDir,
		CWD:          cwd,
		GetDeviceID:  settingsManager.GetOrCreateDeviceID,
	})
}

// runAuthLogoutCommand ports main.ts runAuthLogoutCommand: it removes the stored credentials of one provider and returns the exit code.
func runAuthLogoutCommand(args []string, env authRunEnv) int {
	ctx, cancel := context.WithTimeout(context.Background(), credentialPrintTimeout) //portlint:allow ctxbackground CLI entry point owns the command
	defer cancel()
	modelRuntime, err := coding.CreateModelRuntime(ctx, coding.CreateModelRuntimeOptions{AuthPath: authJSONPath(env.agentDir), AllowModelNetwork: false})
	if err != nil {
		env.printError(err.Error())
		return 1
	}
	defer modelRuntime.Close()
	return RunAuthLogout(ctx, args, AuthLogoutOptions{ModelRuntime: modelRuntime, Terminal: authOutput{stdout: env.stdout, stderr: env.stderr}})
}

// authOutput writes the lines of the terminal authentication commands to stdout and stderr.
type authOutput struct{ stdout, stderr io.Writer }

func (output authOutput) Write(line string) { _, _ = fmt.Fprintln(output.stdout, line) }
func (output authOutput) Error(line string) { _, _ = fmt.Fprintln(output.stderr, line) }

func runParsedAuthCommand(command *AuthCommand, flags Args, env authRunEnv) (int, error) {
	if len(flags.Diagnostics) > 0 {
		messages := make([]string, len(flags.Diagnostics))
		for index, diagnostic := range flags.Diagnostics {
			messages[index] = diagnostic.Message
		}
		return 0, &AuthCommandError{Message: strings.Join(messages, "\n")}
	}
	if command.Kind != AuthCommandCheck {
		ctx, cancel := context.WithTimeout(context.Background(), credentialPrintTimeout)
		defer cancel()
		runtime, err := createCredentialPrintRuntime(ctx, env.agentDir)
		if err != nil {
			return 0, err
		}
		credential, err := ResolveCredentialForPrint(ctx, flags, command.Args, runtime, command.Kind, command.MinExpiryMs)
		if err != nil {
			return 0, err
		}
		_, err = io.WriteString(env.stdout, credential+"\n")
		return 0, err
	}
	target, err := ValidateAuthCommandArgs(flags, command.Args, command.Kind)
	if err != nil {
		return 0, err
	}
	result, credential := runAuthCheck(command, flags, target, env.agentDir)
	output := string(result.Status)
	if command.JSON {
		output, err = authCheckJSON(result, credential)
		if err != nil {
			return 0, err
		}
	} else if credential != "" {
		output = credential
	}
	if _, err := io.WriteString(env.stdout, output+"\n"); err != nil {
		return 0, err
	}
	switch result.Status {
	case AuthCheckReady:
		return 0, nil
	case AuthCheckNotReady:
		return 1, nil
	default:
		return 2, nil
	}
}

// runAuthCheck runs the check and, with --credentials, reads the credential.
// Any failure after argument validation reports the invalid state.
func runAuthCheck(command *AuthCommand, flags Args, target AuthCommandTarget, agentDir string) (AuthCheckResult, string) {
	ctx := context.Background()
	invalid := func() (AuthCheckResult, string) {
		provider := target.Provider
		if provider == "" {
			provider = target.Model
		}
		return AuthCheckResult{Status: AuthCheckInvalid, Provider: provider, Reason: AuthCheckInvalidState}, ""
	}
	var credentials ai.CredentialStore
	if command.NoRefresh {
		credentials = ai.NewReadOnlyAuthStorage(authJSONPath(agentDir))
	} else {
		store, err := ai.NewAuthStorage(authJSONPath(agentDir))
		if err != nil {
			return invalid()
		}
		credentials = store
	}
	runtime, err := CreateAuthCheckModelRuntime(ctx, credentials, agentDir)
	if err != nil {
		return invalid()
	}
	result, err := CheckProviderAuth(ctx, flags, command.Args, runtime, !command.NoRefresh)
	if err != nil {
		return invalid()
	}
	if !command.Credentials || result.Status != AuthCheckReady {
		return result, ""
	}
	credential, err := GetProviderCredential(ctx, result.Provider, runtime, credentials, !command.NoRefresh)
	if err != nil {
		return invalid()
	}
	if credential == "" {
		return AuthCheckResult{Status: AuthCheckNotReady, Provider: result.Provider, Reason: AuthCheckCredentialNotAvailable}, ""
	}
	return result, credential
}

// authCheckJSON mirrors JSON.stringify({...result, credentials}) without
// HTML escaping.
func authCheckJSON(result AuthCheckResult, credential string) (string, error) {
	payload := struct {
		AuthCheckResult
		Credentials string `json:"credentials,omitempty"`
	}{result, credential}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}
