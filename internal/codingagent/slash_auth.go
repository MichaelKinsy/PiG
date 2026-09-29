package codingagent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

func handleLoginCommand(sc *SlashContext) error {
	providers := sc.LoginProviders()
	ref := strings.ToLower(strings.TrimSpace(sc.Args))
	if ref != "" {
		matches := slices.DeleteFunc(slices.Clone(providers), func(p tui.OAuthProvider) bool {
			return strings.ToLower(p.ID) != ref && strings.ToLower(p.Name) != ref
		})
		if len(matches) == 1 {
			return sc.StartProviderLogin(matches[0])
		}
		if len(matches) > 1 && !slices.ContainsFunc(matches, func(p tui.OAuthProvider) bool { return p.ID != matches[0].ID }) {
			kind, ok := sc.SelectAuthMethod(matches)
			if !ok {
				return nil
			}
			for _, p := range matches {
				if p.AuthType == kind {
					return sc.StartProviderLogin(p)
				}
			}
			return nil
		}
		picked, ok := sc.SelectAuthProvider("login", providers, sc.Args)
		if !ok {
			return nil
		}
		return sc.StartProviderLogin(picked)
	}
	for {
		kind, ok := sc.SelectAuthMethod(nil)
		if !ok {
			return nil
		}
		filtered := slices.DeleteFunc(slices.Clone(providers), func(p tui.OAuthProvider) bool { return p.AuthType != kind })
		if len(filtered) == 0 {
			label := "subscription"
			if kind == "api_key" {
				label = "API key"
			}
			showStatusOrAppend(sc, "No "+label+" providers available.")
			return nil
		}
		picked, ok := sc.SelectAuthProvider("login", filtered, "")
		if !ok {
			continue
		}
		return sc.StartProviderLogin(picked)
	}
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
