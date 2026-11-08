// Package devtools is p2's dev-tools plugin (apps/p2/plugins/dev-tools): `reload_plugins`, offered only where it can run: a
// dev-mode server and a local session working on this p2 checkout.
package devtools

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/dev-tools/{tool,worker}.ts.

const extensionName = "p2-dev-tools"

const sectionText = "p2 backend saves only mark dirty. After editing p2 plugins, use reload_plugins to apply dirty plugins (or name plugins). Web/UI edits are plugin edits too: reload_plugins rebuilds the web bundle and open pages reload onto it. Runtime edits need an explicit `npm run reload -- --restart` from apps/p2 with this server's P2_DATA_DIR. Only turns the owner started may use reload_plugins."

const toolDescription = "Explicitly reload p2's dirty backend plugins on the server and all running workers; optional plugins selects names. Returns the committed selection or refusal reason. Only owner-initiated root turns in a local p2 checkout. Runtime code needs npm run reload -- --restart instead."

var toolParameters = schema.Obj(schema.Properties{
	schema.Opt("plugins", schema.Arr(schema.Str(schema.MinLength(1), schema.MaxLength(100)), schema.MaxItems(100))),
}, schema.Open())

// NewExtension is the reload extension: reload sends the command; project says whether this session works on a p2
// checkout now.
func NewExtension(reload func(ctx context.Context, plugins []string) (p2runtime.DevResult, error), project func() bool) *p2runtime.P2Extension {
	reply := func(message string, ok bool) durable.ToolExecutionResult {
		return p2runtime.ToolText(message, delta.JsonObjectOf("ok", ok), !ok)
	}
	registration := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: p2runtime.ToolSchemaOf("reload_plugins", toolDescription, toolParameters),
		// Do not replay a code upgrade after a crash; the caller can explicitly request another reload.
		Replay: durable.ReplayUnsafe,
		Execute: func(ctx context.Context, raw any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			args, _ := raw.(map[string]any)
			if !project() {
				return reply("reload_plugins requires a session working on a local p2 checkout", false), nil
			}
			var plugins []string
			if list, ok := args["plugins"].([]any); ok {
				plugins = []string{}
				for _, each := range list {
					name, _ := each.(string)
					plugins = append(plugins, name)
				}
			}
			result, err := reload(ctx, plugins)
			if err != nil {
				if ctx.Err() != nil {
					return durable.ToolExecutionResult{}, err
				}
				return reply(err.Error(), false), nil
			}
			return reply(result.Message, result.OK), nil
		},
	})
	return &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: extensionName, Sections: []*durable.PromptSection{
			harness.Section("p2-development", func(ctx context.Context, input durable.PromptInput) (*string, error) {
				if !project() {
					return nil, nil
				}
				top, err := p2runtime.IsTopLevel(ctx, input.Read, input.ConversationId)
				if err != nil || !top {
					return nil, err
				}
				text := sectionText
				return &text, nil
			}),
		}},
		Tools: []p2runtime.P2Tool{{ToolRegistration: registration, ToolAccess: p2runtime.ToolAccess{Origin: "owner", Locked: "never", Scope: "main"}}},
	}
}

// inside is whether child is parent or below it.
func inside(parent, child string) bool {
	path, err := filepath.Rel(parent, child)
	return err == nil && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func init() {
	worker := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return p2runtime.Contribute(facet, "p2.dev-tools", func(self host.StoreSelf) error {
			workerOf := p2runtime.WorkerOf(self)
			onCheckout := func() bool {
				parts, err := workerOf()
				if err != nil {
					return false
				}
				location := parts.Location.Value()
				if location.Env != nil {
					return false
				}
				cwd, err := filepath.Abs(location.Cwd)
				if err != nil {
					return false
				}
				return inside(parts.Init.App, cwd) || inside(cwd, parts.Init.App)
			}
			extension := NewExtension(func(ctx context.Context, plugins []string) (p2runtime.DevResult, error) {
				parts, err := workerOf()
				if err != nil {
					return p2runtime.DevResult{}, err
				}
				return p2runtime.SendDevCommand(ctx, parts.Init.DataDir, p2runtime.DevCommand{Action: "reload", Plugins: plugins})
			}, onCheckout)
			// Offered only where it can run: a dev-mode server (`npm run dev`, which gives workers P2_DEV=1) and a local
			// session working on this p2 checkout. Owner attribution is per turn, so each call still checks it.
			offered := self.Writable(p2runtime.Keyed[*bool]{p2runtime.Entry(extensionName, boolPointer(false))})
			// Again after every move: the session may move onto or off this checkout.
			self.OnLive(func(ctx host.Ctx) *host.Handled {
				_, _ = workerOf()
				return host.AwaitingCtx(ctx, func() error {
					for ctx.Err() == nil {
						_ = offered.Set(p2runtime.Keyed[*bool]{p2runtime.Entry(extensionName, boolPointer(os.Getenv("P2_DEV") == "1" && onCheckout()))})
						parts, err := workerOf()
						if err != nil {
							return err
						}
						_, _ = parts.Location.Next(ctx)
					}
					return nil
				})
			})
			self.Contribute(p2runtime.ExtensionSwitches.Registry, offered)
			p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extension.Extension.Name, extension))
			return nil
		})
	})
	hosts.RegisterPlugin(hosts.Plugin{Name: "dev-tools", Worker: &worker})
}

func boolPointer(value bool) *bool { return &value }
