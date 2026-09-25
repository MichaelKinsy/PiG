package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type loginCommand string

const (
	authLogin  loginCommand = "login"
	authLogout loginCommand = "logout"
)

type loginCLIOptions struct {
	command  loginCommand
	provider string
	help     bool
	list     bool
	json     bool
	noInput  bool
	invalid  string
}

func parseLoginCommand(args []string) (*loginCLIOptions, bool) {
	if len(args) == 0 {
		return nil, false
	}
	switch args[0] {
	case "login", "logout":
	default:
		return nil, false
	}
	opts := &loginCLIOptions{command: loginCommand(args[0])}
	for _, arg := range args[1:] {
		switch arg {
		case "-h", "--help":
			opts.help = true
		case "--list":
			opts.list = true
		case "--json":
			opts.json = true
		case "--no-input":
			opts.noInput = true
		default:
			if strings.HasPrefix(arg, "-") || opts.provider != "" {
				opts.invalid = arg
				continue
			}
			opts.provider = arg
		}
	}
	return opts, true
}

func runLoginCommand(args []string) int {
	opts, ok := parseLoginCommand(args)
	if !ok {
		return -1
	}
	if opts.invalid != "" || (opts.list && opts.command != authLogin) || (opts.list && opts.provider != "") {
		fmt.Fprintf(os.Stderr, "Usage: pig %s [provider] [--list] [--json] [--no-input]\n", opts.command)
		return 2
	}
	if opts.help {
		printLoginCommandHelp(opts.command)
		return 0
	}
	var contributions *authContributionRegistry
	defer func() { closeAuthContributionRegistry(contributions) }()
	if opts.command == authLogin && opts.list {
		var err error
		contributions, err = discoverAuthContributions()
		if err != nil {
			fmt.Fprintln(os.Stderr, "pig login --list:", err)
			return 1
		}
		return listAuthTargets(opts.json, contributions)
	}
	if opts.noInput && opts.provider == "" {
		fmt.Fprintf(os.Stderr, "pig %s: --no-input requires an explicit provider; use `pig login --list --json` to discover targets\n", opts.command)
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	agentDir := os.Getenv("PIG_CODING_AGENT_DIR")
	if agentDir == "" {
		agentDir = codingagent.DefaultAgentDir()
	}
	authPath := filepath.Join(agentDir, "auth.json")
	store, err := ai.NewAuthStorage(authPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	_ = cwd

	switch opts.command {
	case authLogin:
		provider, err := resolveLoginProvider(opts.provider, &contributions)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		cred, err := runOAuthProviderLogin(provider, opts.noInput)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		credentialPath := "auth.json"
		if credentialStore, ok := provider.(ai.OAuthCredentialStore); ok {
			credentialPath, err = credentialStore.StoreOAuthCredentials(cred)
		} else {
			err = store.Set(provider.ID(), ai.Credential{Type: ai.CredentialOAuth, Refresh: cred.Refresh, Access: cred.Access, Expires: cred.Expires, ProjectID: cred.ProjectID, Scope: cred.Scope})
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		fmt.Printf("\nCredentials saved to %s\n", credentialPath)
		return 0
	case authLogout:
		if opts.provider == "" {
			contributions, err = discoverAuthContributions()
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 1
			}
			if err := contributions.loadAll(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 1
			}
		} else if err := loadDeclaredAuthProvider(opts.provider, &contributions); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		providerID, err := resolveLogoutProvider(store, opts.provider)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		removedFromExtension := false
		if provider, ok := ai.GetOAuthProvider(providerID); ok {
			if credentialStore, ok := provider.(ai.OAuthCredentialStore); ok {
				removedFromExtension, err = credentialStore.DeleteOAuthCredentials()
				if err != nil {
					fmt.Fprintln(os.Stderr, "Error:", err)
					return 1
				}
			}
		}
		if err := store.Delete(providerID); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		if removedFromExtension {
			fmt.Printf("Logged out from %s. Credentials removed from extension store\n", providerID)
		} else {
			fmt.Printf("Logged out from %s. Credentials removed from auth.json\n", providerID)
		}
		return 0
	default:
		return -1
	}
}

type authTargetListOutput struct {
	Targets     []authTargetItem           `json:"targets"`
	Diagnostics []authInspectionDiagnostic `json:"diagnostics,omitempty"`
}

type authTargetItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	CallbackServer bool   `json:"callbackServer"`
}

// pig additive (D40): Pig auth targets are discoverable through a generic,
// current pre-session inventory shared with contributed providers.
func listAuthTargets(jsonMode bool, registry *authContributionRegistry) int {
	targets := registeredAuthTargetItems(registry)
	if jsonMode {
		output := authTargetListOutput{Targets: targets}
		if registry != nil {
			output.Diagnostics = slices.Clone(registry.diagnostics)
		}
		data, err := json.Marshal(output)
		if err != nil {
			fmt.Fprintln(os.Stderr, "pig login --list: encode JSON:", err)
			return 1
		}
		fmt.Println(string(data))
		return 0
	}
	if registry != nil {
		for _, diagnostic := range registry.diagnostics {
			fmt.Fprintf(os.Stderr, "pig login --list: extension %q inspection failed: %s\n", diagnostic.Extension, diagnostic.Error)
		}
	}
	if len(targets) == 0 {
		fmt.Println("No authentication targets registered.")
		return 0
	}
	fmt.Println("Authentication targets:")
	for _, target := range targets {
		fmt.Printf("  %s\t%s\n", target.ID, target.Name)
	}
	return 0
}

func printLoginCommandHelp(cmd loginCommand) {
	switch cmd {
	case authLogin:
		fmt.Print("Usage:\n  pig login [provider] [--no-input]\n  pig login --list [--json]\n\nLogin to an OAuth provider or list registered auth targets.\n\nExamples:\n  pig login\n  pig login github-copilot\n  pig login --list --json\n")
	case authLogout:
		fmt.Print("Usage:\n  pig logout [provider] [--no-input]\n\nRemove stored OAuth credentials from auth.json.\n\nExamples:\n  pig logout\n  pig logout github-copilot\n")
	}
}

func resolveLoginProvider(input string, registry **authContributionRegistry) (ai.OAuthProviderInterface, error) {
	if input != "" {
		return authProviderFromContribution(input, registry)
	}
	if *registry == nil {
		discovered, err := discoverAuthContributions()
		if err != nil {
			return nil, err
		}
		*registry = discovered
	}
	targets := registeredAuthTargetItems(*registry)
	fmt.Println("Select a provider:")
	fmt.Println()
	for i, target := range targets {
		fmt.Printf("  %d. %s\n", i+1, target.Name)
	}
	fmt.Println()
	fmt.Printf("Enter number (1-%d): ", len(targets))
	line, err := readLine(os.Stdin)
	if err != nil {
		return nil, err
	}
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 1 || idx > len(targets) {
		return nil, fmt.Errorf("Invalid selection")
	}
	return authProviderFromContribution(targets[idx-1].ID, registry)
}

func resolveLogoutProvider(store *ai.AuthStorage, input string) (string, error) {
	if input != "" {
		return input, nil
	}
	creds, err := store.Load()
	if err != nil {
		return "", err
	}
	var providers []string
	providersByID := make(map[string]struct{})
	for id, cred := range creds {
		if cred.Type == ai.CredentialOAuth {
			providers = append(providers, id)
			providersByID[id] = struct{}{}
		}
	}
	for _, provider := range ai.GetOAuthProviders() {
		if _, exists := providersByID[provider.ID()]; exists {
			continue
		}
		credentialStore, ok := provider.(ai.OAuthCredentialStore)
		if !ok {
			continue
		}
		if _, present := credentialStore.OAuthCredentialStatus(); present {
			providers = append(providers, provider.ID())
			providersByID[provider.ID()] = struct{}{}
		}
	}
	slices.Sort(providers)
	if len(providers) == 0 {
		return "", fmt.Errorf("No OAuth providers logged in.")
	}
	if len(providers) == 1 {
		return providers[0], nil
	}
	fmt.Println("Select a provider to logout:")
	fmt.Println()
	for i, provider := range providers {
		fmt.Printf("  %d. %s\n", i+1, provider)
	}
	fmt.Println()
	fmt.Printf("Enter number (1-%d): ", len(providers))
	line, err := readLine(os.Stdin)
	if err != nil {
		return "", err
	}
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 1 || idx > len(providers) {
		return "", fmt.Errorf("Invalid selection")
	}
	return providers[idx-1], nil
}

func runOAuthProviderLogin(provider ai.OAuthProviderInterface, noInput bool) (ai.OAuthCredentials, error) {
	callbacks := ai.OAuthLoginCallbacks{
		OnAuth: func(info ai.OAuthAuthInfo) {
			fmt.Printf("\nOpen this URL in your browser:\n%s\n", info.URL)
			if info.Instructions != "" {
				fmt.Println(info.Instructions)
			}
			fmt.Println()
		},
		OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
			if noInput {
				return "", fmt.Errorf("provider %s requires interactive input; rerun without --no-input", provider.ID())
			}
			question := prompt.Message
			if prompt.AllowEmpty && provider.ID() == "github-copilot" {
				question += " (blank for github.com)"
			}
			if prompt.Placeholder != "" {
				question += fmt.Sprintf(" (%s)", prompt.Placeholder)
			}
			question += ": "
			fmt.Print(question)
			return readLine(os.Stdin)
		},
		OnManualCodeInput: func() (string, error) {
			if noInput {
				return "", fmt.Errorf("provider %s requires a callback code; rerun without --no-input", provider.ID())
			}
			fmt.Print("Enter code or callback URL: ")
			return readLine(os.Stdin)
		},
		OnProgress: func(message string) {
			fmt.Println(message)
		},
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			fmt.Printf("\nOpen this URL in your browser:\n%s\n", info.VerificationURI)
			fmt.Printf("Enter code: %s\n", info.UserCode)
		},
		OnSelect: func(prompt ai.OAuthSelectPrompt) (string, error) {
			if noInput {
				return "", fmt.Errorf("provider %s requires a selection; rerun without --no-input", provider.ID())
			}
			fmt.Println(prompt.Message)
			for i, opt := range prompt.Options {
				fmt.Printf("  %d) %s\n", i+1, opt.Label)
			}
			fmt.Print("Choice [1]: ")
			line, err := readLine(os.Stdin)
			if err != nil {
				return "", err
			}
			line = strings.TrimSpace(line)
			if line == "" {
				return prompt.Options[0].ID, nil
			}
			n, convErr := strconv.Atoi(line)
			if convErr != nil || n < 1 || n > len(prompt.Options) {
				return "", fmt.Errorf("invalid choice %q", line)
			}
			return prompt.Options[n-1].ID, nil
		},
	}
	if parityHarnessEnabled() {
		return runParityHarnessOAuthLogin(provider, callbacks)
	}
	return provider.Login(callbacks)
}

func sortedOAuthProviders() []ai.OAuthProviderInterface {
	providers := ai.GetOAuthProviders()
	order := map[string]int{
		"anthropic":      0,
		"github-copilot": 1,
		"openai-codex":   2,
	}
	slices.SortFunc(providers, func(a, b ai.OAuthProviderInterface) int {
		aiOrder, aok := order[a.ID()]
		biOrder, bok := order[b.ID()]
		switch {
		case aok && bok && aiOrder != biOrder:
			return aiOrder - biOrder
		case aok && !bok:
			return -1
		case !aok && bok:
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})
	return providers
}

func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func runParityHarnessOAuthLogin(provider ai.OAuthProviderInterface, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	switch provider.ID() {
	case "github-copilot":
		var polls int
		return withAuthMockClient(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.URL.Host == "github.com" && req.URL.Path == "/login/device/code":
				return authJSONResponse(200, `{"device_code":"device-123","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":0,"expires_in":60}`), nil
			case req.URL.Host == "github.com" && req.URL.Path == "/login/oauth/access_token":
				polls++
				if polls == 1 {
					return authJSONResponse(200, `{"error":"authorization_pending"}`), nil
				}
				return authJSONResponse(200, `{"access_token":"ghu_refresh"}`), nil
			case req.URL.Host == "api.github.com" && req.URL.Path == "/copilot_internal/v2/token":
				return authJSONResponse(200, `{"token":"tid=x;proxy-ep=proxy.individual.githubcopilot.com;other=y","expires_at":4102444800}`), nil
			case req.URL.Host == "api.individual.githubcopilot.com" && req.URL.Path == "/models":
				return authJSONResponse(200, `{"data":[{"id":"gpt-4o","model_picker_enabled":true,"policy":{"state":"enabled"},"capabilities":{"supports":{"tool_calls":true}}}]}`), nil
			case req.URL.Host == "api.individual.githubcopilot.com" && strings.HasPrefix(req.URL.Path, "/models/") && strings.HasSuffix(req.URL.Path, "/policy"):
				return authJSONResponse(200, `{"ok":true}`), nil
			default:
				return nil, fmt.Errorf("unexpected oauth request: %s %s", req.Method, req.URL.String())
			}
		}, func() (ai.OAuthCredentials, error) {
			return provider.Login(callbacks)
		})
	default:
		return provider.Login(callbacks)
	}
}

type authRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f authRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func withAuthMockClient(handler func(*http.Request) (*http.Response, error), fn func() (ai.OAuthCredentials, error)) (ai.OAuthCredentials, error) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: authRoundTripperFunc(handler)}
	defer func() { http.DefaultClient = oldClient }()
	return fn()
}

func authJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func parityHarnessEnabled() bool {
	return os.Getenv("PIG_PARITY_HARNESS") == "1"
}
