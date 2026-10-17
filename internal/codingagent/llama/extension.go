package llama

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// disposableView gives the manager view the Dispose member of a component the UI mounts; the view holds nothing to release.
type disposableView struct{ CustomComponent }

func (disposableView) Dispose() {}

// CommandHandler is the /llama handler llamaExtension registers: it runs index.ts's command with the invocation's ctx.ui and
// ctx.modelRegistry. A runtime that exposes no model registry fails the command.
// upstream: packages/coding-agent/src/extensions/llama/index.ts:registerCommand
func CommandHandler(controller *LlamaProviderController) func(ctx context.Context, args string) error {
	return func(ctx context.Context, _ string) error {
		registry := registryOf(ctx)
		if registry == nil {
			return errors.New("llama.cpp needs the model registry of its session")
		}
		return RunCommand(adaptCommandContext(ctx), registry, controller)
	}
}

func registryOf(ctx context.Context) ModelRegistry {
	c := extension.CommandContextFromContext(ctx)
	if c == nil {
		return nil
	}
	registry, err := c.ModelRegistry()
	if err != nil || registry == nil {
		return nil
	}
	return registry
}

// adaptCommandContext adapts the extension command context to the members /llama uses.
func adaptCommandContext(ctx context.Context) CommandContext {
	out := CommandContext{Ctx: ctx, Mode: "print", Notify: func(string, string) {}}
	c := extension.CommandContextFromContext(ctx)
	if c == nil {
		return out
	}
	if mode, err := c.Mode(); err == nil {
		out.Mode = string(mode)
	}
	ui, err := c.UI()
	if err != nil {
		return out
	}
	out.Notify = func(message, notifyType string) { ui.Notify(message, notifyType) }
	out.Custom = func(factory func(requestRender, done func()) CustomComponent) {
		_, _ = ui.Custom(ctx, extension.CustomFactory(func(host extension.TUI, _ *tui.Theme, _ extension.KeybindingsManager, done func(any)) (extension.DisposableComponent, error) {
			return disposableView{factory(func() { host.RequestRender() }, func() { done(nil) })}, nil
		}), nil)
	}
	return out
}
