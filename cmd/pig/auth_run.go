package main

// Mirrors runAuthCommand in upstream packages/coding-agent/src/main.ts: the
// `auth` command dispatch, its output, and its exit codes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/ai"
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
	return runAuthCommandWith(args, authRunEnv{
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		agentDir: agentDirForModel(),
		color:    term.IsTerminal(int(os.Stderr.Fd())),
	})
}

func (env authRunEnv) red(text string) string {
	if !env.color {
		return text
	}
	return "\x1b[31m" + text + "\x1b[39m"
}

func (env authRunEnv) dim(text string) string {
	if !env.color {
		return text
	}
	return "\x1b[2m" + text + "\x1b[22m"
}

func (env authRunEnv) printError(message string) {
	_, _ = fmt.Fprintln(env.stderr, env.red("Error: "+message)) // Best effort: stderr has no fallback channel.
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
	command, err := ParseAuthCommand(args)
	if err != nil {
		env.printError(authErrorMessage(err, "Failed to parse auth command"))
		return 1
	}
	if command == nil {
		return -1
	}
	flags := parseFlags(command.Args)
	if option, ok := firstUnknownFlag(flags, command.Args); ok {
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

func runParsedAuthCommand(command *AuthCommand, flags CLIFlags, env authRunEnv) (int, error) {
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
func runAuthCheck(command *AuthCommand, flags CLIFlags, target AuthCommandTarget, agentDir string) (AuthCheckResult, string) {
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
