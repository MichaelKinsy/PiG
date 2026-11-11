package terminals

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/plugins/terminals/worker.ts: the terminals plugin's worker facet, the agent's `terminal` tool, calling the
// server's `AgentTerminals` as this session's worker, at the calling conversation's location. It follows the code mode
// switch as MCP tools do: for scripts while code mode is on, loaded with `tool_search` while it is off.
//
// Forced Go difference: a contribution to `Extensions` is a value, not Pi's Writable, so the extension is made from the
// switches as they compose; the factory runs again when they change (the registry is a declared dependency).

// codemodeExtension is the code mode plugin's extension: its switch is on while the `codemode` setting is, absent without the
// plugin.
const codemodeExtension = "codemode"

// agentBackend is the tool's backend over the server's `AgentTerminals`.
type agentBackend struct {
	worker func() (*p2runtime.WorkerParts, error)
}

// call is one call of the server's object, as this worker, cancelled with ctx.
func (b agentBackend) call(ctx context.Context, method string, args *delta.JsonObject) (delta.JsonValue, error) {
	parts, err := b.worker()
	if err != nil {
		return nil, err
	}
	reply, err := parts.Root(func(root host.Ctx) (any, error) {
		terminals, err := root.Get(contract.AgentTerminals, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return terminals.Call(method, args, root)
	}, ctx)
	if err != nil {
		return nil, err
	}
	return reply, nil
}

func (b agentBackend) List(ctx context.Context) ([]ListedWindow, error) {
	reply, err := b.call(ctx, "list", delta.NewJsonObject(0))
	if err != nil {
		return nil, err
	}
	windows, _ := jsonObject(reply).Value("windows").([]any)
	listed := make([]ListedWindow, len(windows))
	for index, each := range windows {
		window := jsonObject(each)
		info, err := ParseInfo(window.Value("terminal"))
		if err != nil {
			return nil, err
		}
		listed[index] = ListedWindow{Terminal: info, Foreground: stringOf(window.Value("foreground"))}
	}
	return listed, nil
}

func (b agentBackend) Open(ctx context.Context, key string, location p2runtime.Location, name, command string) (TerminalInfo, error) {
	var env any
	if location.Env != nil {
		env = *location.Env
	}
	args := delta.JsonObjectOf("key", key, "location", delta.JsonObjectOf("cwd", location.Cwd, "env", env))
	if name != "" {
		args.Set("name", name)
	}
	if command != "" {
		args.Set("command", command)
	}
	reply, err := b.call(ctx, "open", args)
	if err != nil {
		return TerminalInfo{}, err
	}
	return ParseInfo(reply)
}

func (b agentBackend) Send(ctx context.Context, key, target string, keys []string, literal bool) (TerminalInfo, error) {
	list := make([]any, len(keys))
	for index, each := range keys {
		list[index] = each
	}
	reply, err := b.call(ctx, "send", delta.JsonObjectOf("key", key, "target", target, "keys", list, "literal", literal))
	if err != nil {
		return TerminalInfo{}, err
	}
	return ParseInfo(reply)
}

func (b agentBackend) Cursor(ctx context.Context, target string) (TerminalInfo, int64, error) {
	reply, err := b.call(ctx, "cursor", delta.JsonObjectOf("target", target))
	if err != nil {
		return TerminalInfo{}, 0, err
	}
	info, err := ParseInfo(jsonObject(reply).Value("terminal"))
	offset, _ := jsonObject(reply).Value("offset").(float64)
	return info, int64(offset), err
}

func (b agentBackend) Read(ctx context.Context, target string, from *int64, wait *TerminalWait) (TerminalRead, error) {
	args := delta.JsonObjectOf("target", target)
	if from != nil {
		args.Set("from", float64(*from))
	}
	if wait != nil {
		args.Set("wait", wait.JSON())
	}
	reply, err := b.call(ctx, "read", args)
	if err != nil {
		return TerminalRead{}, err
	}
	return ParseRead(reply)
}

func (b agentBackend) Capture(ctx context.Context, target string, scrollback int, escapes bool) (TerminalInfo, string, error) {
	args := delta.JsonObjectOf("target", target, "scrollback", float64(scrollback))
	if escapes {
		args.Set("escapes", true)
	}
	reply, err := b.call(ctx, "capture", args)
	if err != nil {
		return TerminalInfo{}, "", err
	}
	info, err := ParseInfo(jsonObject(reply).Value("terminal"))
	return info, stringOf(jsonObject(reply).Value("text")), err
}

func (b agentBackend) Close(ctx context.Context, key, target string) (string, error) {
	reply, err := b.call(ctx, "close", delta.JsonObjectOf("key", key, "target", target))
	if err != nil {
		return "", err
	}
	return stringOf(jsonObject(reply).Value("terminalId")), nil
}

// WorkerFacet is the plugin's worker facet.
var WorkerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.terminal-tools", func(self host.StoreSelf) error {
		harness := p2runtime.HarnessOf(self)
		worker := p2runtime.WorkerOf(self)
		switches := self.Registry(p2runtime.ExtensionSwitches.Registry)
		caller := func(ctx context.Context, api durable.ToolExecutionApi) (p2runtime.Location, string, error) {
			parts, err := harness()
			if err != nil {
				return p2runtime.Location{}, "", err
			}
			environments, err := parts.Environments()
			if err != nil {
				return p2runtime.Location{}, "", err
			}
			here, err := environments.Locate(ctx, api.ConversationId(), api)
			if err != nil {
				return p2runtime.Location{}, "", err
			}
			sandbox, err := environments.Sandbox(ctx, here.Env)
			return here, sandbox, err
		}
		// Code mode switched off or not loaded: scripts cannot reach the tool, so `tool_search` loads it.
		reach := ReachToolSearch
		if table, err := p2runtime.ExtensionSwitches.Table(switches); err == nil {
			if on, ok := table.Get(codemodeExtension); ok && on != nil && *on {
				reach = ReachCodemode
			}
		}
		extension, err := CreateTerminalExtension(reach, agentBackend{worker: worker}, caller)
		if err != nil {
			return err
		}
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(TerminalExtension, extension))
		return nil
	})
})
