package cli

// Ports packages/coding-agent/src/cli/auth-login.ts

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/weburl"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// errLoginCancelled is the "Login cancelled" error a prompt gives when its login ends.
var errLoginCancelled = errors.New("Login cancelled")

// AuthLoginRuntime is the part of the model runtime `pig auth login` uses.
type AuthLoginRuntime interface {
	GetProviders() []*ai.ModelsProvider
	Login(ctx context.Context, id string, kind ai.AuthType, interaction ai.AuthInteraction, options ...ai.LoginOptions) (ai.Credential, error)
	Refresh(ctx context.Context, options ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult
}

// AuthLoginOptions are the dependencies of RunAuthLogin, injected so tests can script the terminal.
type AuthLoginOptions struct {
	ModelRuntime AuthLoginRuntime
	Terminal     LoginTerminal
	AgentDir     string
	CWD          string
	GetDeviceID  func() string
}

var authLoginUsage = codingagent.AppName + " auth login <provider> [--api-key] [--no-browser]"

// loginRequest is the parsed `pig auth login` arguments.
type loginRequest struct {
	providerRef string
	// apiKey logs in with an API key instead of an account.
	apiKey bool
	// browser opens the browser and takes the browser sign-in method without asking.
	browser bool
}

const catalogRefreshTimeout = 15 * time.Second // upstream: cli/auth-login.ts:refreshProviderCatalog

// RunAuthLogin runs `pig auth login <provider>` in the terminal and returns the process exit code. ctx ends the login, e.g. on Ctrl+C.
func RunAuthLogin(ctx context.Context, args []string, options AuthLoginOptions) int {
	modelRuntime, terminal := options.ModelRuntime, options.Terminal
	request, message := parseLoginRequest(args)
	if message != "" {
		terminal.Error(message)
		return 1
	}

	var provider *ai.ModelsProvider
	if matches := codingagent.FindMatchingProviders(modelRuntime.GetProviders(), request.providerRef, providerIDName); len(matches) > 0 {
		provider = matches[0]
	}
	if provider == nil {
		var loginProviders []string
		for _, candidate := range modelRuntime.GetProviders() {
			if candidate.Auth.OAuth != nil || (candidate.Auth.APIKey != nil && candidate.Auth.APIKey.Login != nil) {
				loginProviders = append(loginProviders, candidate.ID)
			}
		}
		slices.SortFunc(loginProviders, func(a, b string) int { return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b)) })
		terminal.Error(`Unknown provider "` + widthx.ReplaceControlCharacters(request.providerRef) + `".`)
		terminal.Error("Providers: " + strings.Join(loginProviders, ", "))
		return 1
	}
	if !terminal.Interactive() {
		terminal.Error(`"` + authLoginUsage + `" requires an interactive terminal`)
		return 1
	}

	authType, message := selectAuthType(provider, request.apiKey)
	if message != "" {
		terminal.Error(message)
		return 1
	}
	_, err := modelRuntime.Login(ctx, provider.ID, authType, createLoginInteraction(provider.ID, authType, request, terminal),
		ai.LoginOptions{GetDeviceID: options.GetDeviceID})
	if err != nil {
		message := err.Error()
		if _, ok := errors.AsType[*codingagent.CredentialSynchronizationError](err); ok {
			terminal.Error("Logged in to " + provider.Name + ", but local model state could not be synchronized: " + message)
		} else if ctx.Err() != nil || message == errLoginCancelled.Error() { //portlint:allow erroridentity Pi compares the message (auth-login.ts:78 `message === "Login cancelled"`): providers, extension subprocesses and the OAuth callback server each build their own "Login cancelled" error, so there is no shared identity to match
			terminal.Error("Login cancelled.")
		} else {
			terminal.Error("Failed to log in to " + provider.Name + ": " + message)
		}
		return 1
	}

	if authType == ai.CredentialOAuth {
		terminal.Write("Logged in to " + provider.Name + ". Credentials saved to " + authJSONPath(options.AgentDir))
	} else {
		terminal.Write("Saved API key for " + provider.Name + ". Credentials saved to " + authJSONPath(options.AgentDir))
	}
	refreshProviderCatalog(ctx, provider, modelRuntime, terminal)
	if provider.ID == ai.RadiusProviderID {
		configureRadiusMcpServer(provider, options)
	}
	return 0
}

// parseLoginRequest parses the arguments of `pig auth login`, or returns the message that ends the command.
func parseLoginRequest(args []string) (loginRequest, string) {
	usage := "Usage: " + authLoginUsage
	request := loginRequest{browser: true}
	for _, arg := range args {
		switch {
		case arg == "--api-key":
			request.apiKey = true
		case arg == "--no-browser":
			request.browser = false
		case strings.HasPrefix(arg, "-") || request.providerRef != "":
			return loginRequest{}, usage
		default:
			request.providerRef = arg
		}
	}
	if request.providerRef == "" {
		return loginRequest{}, usage
	}
	if request.apiKey && !request.browser {
		return loginRequest{}, "--no-browser only applies to account sign-in; omit it with --api-key."
	}
	return request, ""
}

// selectAuthType is account login unless apiKey asks for an API key. Providers without account login use their API key login, since that is the only way
// in. A non-empty message says why neither is available.
func selectAuthType(provider *ai.ModelsProvider, apiKey bool) (ai.AuthType, string) {
	hasAPIKeyLogin := provider.Auth.APIKey != nil && provider.Auth.APIKey.Login != nil
	if !apiKey && provider.Auth.OAuth != nil {
		return ai.CredentialOAuth, ""
	}
	if hasAPIKeyLogin {
		return ai.CredentialAPIKey, ""
	}
	if apiKey && provider.Auth.OAuth != nil {
		return "", provider.Name + " has no API key login. Omit --api-key to sign in."
	}
	method := "ambient credentials"
	if provider.Auth.APIKey != nil && provider.Auth.APIKey.Name != "" {
		method = provider.Auth.APIKey.Name
	}
	return "", provider.Name + " is configured outside " + codingagent.AppName + " (" + method + ")."
}

// createLoginInteraction adapts provider prompts and events to the command's selected sign-in method.
func createLoginInteraction(providerID string, authType ai.AuthType, request loginRequest, terminal LoginTerminal) ai.AuthInteraction {
	// Account logins list the browser sign-in first, as /login; API key setup asks every selection.
	methodChoice := "other"
	if authType == ai.CredentialAPIKey {
		methodChoice = "ask"
	} else if request.browser {
		methodChoice = "browser"
	}
	var (
		mu             sync.Mutex
		skippedMethods []string
	)

	selectMethod := func(ctx context.Context, prompt ai.AuthSelectPrompt) (string, error) {
		if methodChoice == "ask" || len(prompt.Options) == 0 {
			return selectInTerminal(ctx, prompt.Message, prompt.Options, terminal)
		}
		first, others := prompt.Options[0], prompt.Options[1:]
		if methodChoice == "browser" {
			mu.Lock()
			skippedMethods = make([]string, len(others))
			for index, option := range others {
				skippedMethods[index] = option.Label
			}
			mu.Unlock()
			return first.ID, nil
		}
		// A provider whose only method is the browser sign-in still gets it, without opening the browser.
		if len(others) == 0 {
			return first.ID, nil
		}
		if len(others) > 1 {
			return selectInTerminal(ctx, prompt.Message, others, terminal)
		}
		terminal.Write(prompt.Message + " " + others[0].Label)
		return others[0].ID, nil
	}

	return ai.AuthInteraction{
		Prompt: func(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
			if selection, ok := prompt.(ai.AuthSelectPrompt); ok {
				return selectMethod(ctx, selection)
			}
			var message, placeholder string
			secret := false
			switch typed := prompt.(type) {
			case ai.AuthTextPrompt:
				message, placeholder = typed.Message, typed.Placeholder
			case ai.AuthManualCodePrompt:
				message, placeholder = typed.Message, typed.Placeholder
			case ai.AuthSecretPrompt:
				message, placeholder, secret = typed.Message, typed.Placeholder, true
			default:
				return "", fmt.Errorf("unsupported auth prompt %q", prompt.Type())
			}
			if placeholder != "" {
				placeholder = " (" + placeholder + ")"
			}
			question := message + placeholder + ": "
			if secret {
				return terminal.Secret(ctx, question)
			}
			return terminal.Question(ctx, question)
		},
		Notify: func(event ai.AuthEvent) {
			switch event := event.(type) {
			case ai.AuthURLEvent:
				terminal.Write("Open this URL to sign in:\n  " + weburl.DisplayURL(event.URL))
				if event.Instructions != "" {
					terminal.Write(event.Instructions)
				}
				if target, ok := weburl.WebURL(event.URL); ok && request.browser {
					terminal.OpenURL(target)
				}
				// Providers without a method choice have nothing else to offer, so they get no hint.
				rerun := `"` + codingagent.AppName + " auth login " + providerID + ` --no-browser"`
				mu.Lock()
				skipped := slices.Clone(skippedMethods)
				mu.Unlock()
				if len(skipped) == 1 {
					terminal.Write(`To use "` + skipped[0] + `" instead, run ` + rerun)
				} else if len(skipped) > 1 {
					labels := make([]string, len(skipped))
					for index, label := range skipped {
						labels[index] = `"` + label + `"`
					}
					terminal.Write("Other sign-in methods: " + strings.Join(labels, ", ") + ". Run " + rerun + " to choose one.")
				}
			case ai.AuthDeviceCodeEvent:
				terminal.Write("Open " + weburl.DisplayURL(event.VerificationURI) + " and enter code: " + event.UserCode)
				if target, ok := weburl.WebURL(event.VerificationURI); ok && request.browser && (event.OpenBrowserOnConfirm == nil || *event.OpenBrowserOnConfirm) {
					terminal.OpenURL(target)
				}
				terminal.Write("Waiting for authentication...")
			case ai.AuthInfoEvent:
				terminal.Write(event.Message)
				for _, link := range event.Links {
					label := ""
					if link.Label != "" {
						label = link.Label + ": "
					}
					terminal.Write("  " + label + weburl.DisplayURL(link.URL))
				}
			case ai.AuthProgressEvent:
				terminal.Write(event.Message)
			}
		},
	}
}

// selectInTerminal lists options by number and asks until the answer is one of them.
func selectInTerminal(ctx context.Context, message string, options []ai.AuthSelectOption, terminal LoginTerminal) (string, error) {
	terminal.Write(message)
	for index, option := range options {
		description := ""
		if option.Description != "" {
			description = " - " + option.Description
		}
		terminal.Write("  " + strconv.Itoa(index+1) + ". " + option.Label + description)
	}
	count := strconv.Itoa(len(options))
	prompt, invalid := "Select 1-"+count+": ", "Enter a number from 1 to "+count+"."
	for {
		answer, err := terminal.Question(ctx, prompt)
		if err != nil {
			return "", err
		}
		if number, ok := parseSelection(widthx.JSTrim(answer)); ok && number >= 1 && number <= len(options) {
			return options[number-1].ID, nil
		}
		terminal.Write(invalid)
	}
}

// parseSelection is /^\d+$/u followed by Number(): the digits of answer as a number, false for anything else.
func parseSelection(answer string) (int, bool) {
	if answer == "" {
		return 0, false
	}
	number := 0
	for _, char := range answer {
		if char < '0' || char > '9' {
			return 0, false
		}
		number = number*10 + int(char-'0')
		if number > 1<<20 {
			return 1 << 20, true // Beyond any option list.
		}
	}
	return number, true
}

// refreshProviderCatalog fetches the provider's model catalog now, since dynamic catalogs stay empty until the first authenticated refresh.
func refreshProviderCatalog(ctx context.Context, provider *ai.ModelsProvider, modelRuntime AuthLoginRuntime, terminal LoginTerminal) {
	// The login is saved by now, so Ctrl+C only skips the refresh.
	cancelled := func() {
		terminal.Write("Skipped the " + provider.Name + " model catalog refresh; " + codingagent.AppName + " refreshes it on next start.")
	}
	refreshCtx, cancel := context.WithTimeout(ctx, catalogRefreshTimeout)
	defer cancel()
	result := modelRuntime.Refresh(refreshCtx, ai.ModelsRefreshOptions{Providers: []string{provider.ID}})
	switch {
	case ctx.Err() != nil:
		cancelled()
	case result.Aborted:
		terminal.Write("The " + provider.Name + " model catalog refresh timed out; " + codingagent.AppName + " refreshes it on next start.")
	case len(result.Errors) > 0:
		terminal.Write("The " + provider.Name + " model catalog could not be refreshed; " + codingagent.AppName + " retries on next start.")
	}
}

// configureRadiusMcpServer points the Radius MCP server in the global mcp.json at the Radius login without asking, since the login is what the server
// needs; /login asks instead. Nothing changes when a global server already uses this login.
func configureRadiusMcpServer(provider *ai.ModelsProvider, options AuthLoginOptions) {
	terminal := options.Terminal
	plan, err := codingagent.PlanRadiusMcpServer(provider.ID, options.AgentDir, options.CWD)
	if err != nil {
		terminal.Error("Could not update " + filepath.Join(options.AgentDir, "mcp.json") + ": " + err.Error())
		return
	}
	if plan == nil {
		return
	}
	replaced, err := plan.Apply()
	if err != nil {
		terminal.Error("Could not update " + plan.McpPath + ": " + err.Error())
		return
	}
	verb := "Added"
	if replaced {
		verb = "Updated"
	}
	terminal.Write(verb + ` MCP server "` + plan.Name + `" in ` + plan.McpPath + " to use the " + provider.Name + " login.")
}
