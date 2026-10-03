package codingagent

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

// handleLoginCommand mirrors Pi's handleLoginCommand (interactive-mode.ts:5775-5795).
func handleLoginCommand(sc *SlashContext) error {
	ref := strings.ToLower(strings.TrimSpace(sc.Args))
	if ref == "" {
		return showLoginAuthTypeSelector(sc, nil)
	}
	matches := slices.DeleteFunc(slices.Clone(sc.LoginProviders()), func(p tui.OAuthProvider) bool {
		return strings.ToLower(p.ID) != ref && strings.ToLower(p.Name) != ref
	})
	if len(matches) == 1 {
		return startProviderLogin(sc, matches[0], nil)
	}
	if len(matches) > 1 && !slices.ContainsFunc(matches, func(p tui.OAuthProvider) bool { return p.ID != matches[0].ID }) {
		return showLoginAuthTypeSelector(sc, matches)
	}
	return showLoginProviderSelector(sc, "", sc.Args)
}

// startProviderLogin mirrors Pi's startProviderLogin: onBack reopens the selector the login was started from when the
// user cancels it (interactive-mode.ts:5798-5807, :6157-6162, :6284-6286).
func startProviderLogin(sc *SlashContext, provider tui.OAuthProvider, onBack func() error) error {
	err := sc.StartProviderLogin(provider)
	if errors.Is(err, errLoginCancelled) {
		if onBack != nil {
			return onBack()
		}
		return nil
	}
	return err
}

// showLoginAuthTypeSelector mirrors Pi's showLoginAuthTypeSelector (interactive-mode.ts:5809-5883). The top-level
// menu offers Radius directly, as its last option.
func showLoginAuthTypeSelector(sc *SlashContext, providers []tui.OAuthProvider) error {
	choice, ok := sc.SelectAuthMethod(providers)
	if !ok {
		return nil
	}
	if providers == nil && choice != "oauth" && choice != "api_key" {
		options := sc.LoginProviders()
		index := slices.IndexFunc(options, func(p tui.OAuthProvider) bool { return p.ID == choice && p.AuthType == "oauth" })
		if index < 0 {
			return nil
		}
		return startProviderLogin(sc, options[index], func() error { return showLoginAuthTypeSelector(sc, nil) })
	}
	if providers != nil {
		for _, p := range providers {
			if p.AuthType == choice {
				return startProviderLogin(sc, p, func() error { return showLoginAuthTypeSelector(sc, providers) })
			}
		}
		return nil
	}
	return showLoginProviderSelector(sc, choice, "")
}

// showLoginProviderSelector mirrors Pi's showLoginProviderSelector (interactive-mode.ts:5885-5925).
func showLoginProviderSelector(sc *SlashContext, authType, initialSearch string) error {
	providers := sc.LoginProviders()
	if authType != "" {
		providers = slices.DeleteFunc(slices.Clone(providers), func(p tui.OAuthProvider) bool { return p.AuthType != authType })
	}
	if len(providers) == 0 {
		message := "No login providers available."
		switch authType {
		case "oauth":
			message = "No account providers available."
		case "api_key":
			message = "No API key providers available."
		}
		showStatusOrAppend(sc, message)
		return nil
	}
	picked, ok := sc.SelectAuthProvider("login", providers, initialSearch)
	if !ok {
		if authType != "" {
			return showLoginAuthTypeSelector(sc, nil)
		}
		return nil
	}
	return startProviderLogin(sc, picked, func() error { return showLoginProviderSelector(sc, authType, initialSearch) })
}

func handleLogoutCommand(sc *SlashContext) error {
	providers, err := sc.LogoutProviders()
	if err != nil {
		return fmt.Errorf("Could not read stored credentials: %w", err)
	}
	if len(providers) == 0 {
		showStatusOrAppend(sc, "No stored credentials to remove. /logout only removes credentials saved by /login; environment variables and models.json config are unchanged.")
		return nil
	}
	provider, ok := sc.SelectAuthProvider("logout", providers, "")
	if !ok {
		return nil
	}
	if err := sc.Logout(provider.ID); err != nil {
		return fmt.Errorf("Logout failed: %w", err)
	}
	message := "Logged out of " + provider.Name
	if provider.AuthType == "api_key" {
		message = "Removed stored API key for " + provider.Name + ". Environment variables and models.json config are unchanged."
	}
	showStatusOrAppend(sc, message)
	return nil
}
