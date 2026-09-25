package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type projectTrustResolutionOptions struct {
	CWD              string
	Store            *codingagent.ProjectTrustStore
	Override         *bool
	Default          string
	Runner           *inproc.Runner
	UI               extension.UIContext
	OnExtensionError func(string)
}

func resolveProjectTrusted(ctx context.Context, opts projectTrustResolutionOptions) (bool, error) {
	if opts.Override != nil {
		return *opts.Override, nil
	}
	hasTrustResources := codingagent.HasTrustRequiringProjectResources(opts.CWD)
	if !hasTrustResources {
		return true, nil
	}
	if opts.Runner != nil {
		result, handlerErrors, err := inproc.EmitProjectTrust(opts.Runner, ctx, extension.ProjectTrustEvent{
			Type: "project_trust",
			Cwd:  opts.CWD,
		})
		if err != nil {
			return false, err
		}
		for _, handlerErr := range handlerErrors {
			if opts.OnExtensionError != nil {
				opts.OnExtensionError(fmt.Sprintf("Extension %q project_trust error: %s", handlerErr.ExtensionPath, handlerErr.Error))
			}
		}
		if result != nil {
			trusted := result.Trusted == extension.ProjectTrustYes
			if result.Remember != nil && *result.Remember {
				if err := opts.Store.Set(opts.CWD, new(trusted)); err != nil {
					return false, err
				}
			}
			return trusted, nil
		}
	}

	decision, err := opts.Store.Get(opts.CWD)
	if err != nil {
		return false, err
	}
	if decision != nil {
		return *decision, nil
	}
	switch opts.Default {
	case "always":
		return true, nil
	case "never":
		return false, nil
	}
	if opts.UI == nil || opts.UI == extension.NoopUIContext {
		return false, nil
	}

	options := codingagent.GetProjectTrustOptions(opts.CWD, true)
	labels := make([]string, 0, len(options))
	for _, option := range options {
		labels = append(labels, option.Label)
	}
	selected, err := opts.UI.Select(ctx, formatProjectTrustPrompt(opts.CWD), labels, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, nil
		}
		return false, err
	}
	for _, option := range options {
		if option.Label != selected {
			continue
		}
		if len(option.Updates) > 0 {
			if err := opts.Store.SetMany(option.Updates); err != nil {
				return false, err
			}
		}
		return option.Trusted, nil
	}
	return false, nil
}

func formatProjectTrustPrompt(cwd string) string {
	return fmt.Sprintf("Trust project folder?\n%s\n\nThis allows pig to load project settings, resources, and instructions, install missing project packages, and execute project extensions.", cwd)
}

type startupTrustUI struct {
	extension.UIContext
	opts codingagent.StartupUIOptions
}

func newStartupTrustUI(opts codingagent.StartupUIOptions) extension.UIContext {
	return &startupTrustUI{UIContext: extension.NoopUIContext, opts: opts}
}

func (ui *startupTrustUI) Select(ctx context.Context, title string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	selected, ok, err := codingagent.ShowStartupSelector(title, options, ui.opts)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", context.Canceled
	}
	return options[selected], nil
}

func (ui *startupTrustUI) Confirm(ctx context.Context, title, message string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	selected, err := ui.Select(ctx, title+"\n"+message, []string{"Yes", "No"}, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, nil
		}
		return false, err
	}
	return selected == "Yes", nil
}

func (ui *startupTrustUI) Input(ctx context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value, ok, err := codingagent.ShowStartupInput(title, placeholder, ui.opts)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", context.Canceled
	}
	return value, nil
}

func (ui *startupTrustUI) Notify(message, kind string) {
	if kind == "error" || kind == "warning" {
		fmt.Fprintln(os.Stderr, message)
	}
}
