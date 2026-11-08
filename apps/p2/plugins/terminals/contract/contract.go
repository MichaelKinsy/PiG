// Package contract ports apps/p2/plugins/terminals/contract.ts: the owner's terminal views and the agent's terminal calls.
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var (
	c       = p2runtime.Contract
	str     = schema.Str
	integer = schema.Int
	boolean = schema.Bool
	nul     = schema.Nul
	obj     = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop    = schema.Prop
	opt     = schema.Opt
	arr     = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
	union   = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	method  = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

var (
	empty = obj(schema.Properties{})
	size  = obj(schema.Properties{
		prop("cols", integer(schema.Minimum(1), schema.Maximum(1000))),
		prop("rows", integer(schema.Minimum(1), schema.Maximum(1000))),
	})
	color = integer(schema.Minimum(0), schema.Maximum(0xffffff))
)

// TerminalServiceObject is the server's terminal service (host-internal): open while held; its service is the
// `TerminalServices` native.
var TerminalServiceObject = must(contracts.Local("p2.terminal-service", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{},
}))

// TerminalServices is the open `TerminalServiceObject`'s service (`p2.terminal-services`): its native value is a
// *ServiceParts, nil while the object is not open.
var TerminalServices = p2runtime.NewNativeRegistry("p2.terminal-services")

// Terminals is one owner-only view, scoped to a session. Control is chord2; terminal input/output is a separate byte
// channel. 2: `open` names the conversation whose location the terminal opens at.
var Terminals = must(contracts.Remote("p2.terminals", contracts.RemoteDeclaration{
	Version:     2,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("view", contracts.Str())},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{},
			Methods: []contracts.MethodEntry{
				contracts.Meth("channel", method(empty, obj(schema.Properties{prop("token", str())}))),
				contracts.Meth("list", method(empty)),
				// A new terminal where the session's `conversation` works (a subagent's own location, else the session's).
				contracts.Meth("open", method(obj(append(p2runtime.PropertiesOf(size), prop("conversation", integer(schema.Minimum(1))))))),
				contracts.Meth("attach", method(obj(schema.Properties{prop("terminalId", str(schema.MinLength(1), schema.MaxLength(64)))}))),
				contracts.Meth("close", method(obj(schema.Properties{opt("force", boolean())}))),
				contracts.Meth("resize", method(size)),
				contracts.Meth("theme", method(obj(schema.Properties{
					prop("foreground", color), prop("background", color),
					prop("scheme", union(p2runtime.Literal("light"), p2runtime.Literal("dark"))),
				}))),
				contracts.Meth("ack", method(obj(schema.Properties{prop("bytes", integer(schema.Minimum(1), schema.Maximum(9007199254740991)))}))),
				contracts.Meth("restartHost", method(empty)),
			},
		}),
	},
}))

var (
	target = str(schema.MinLength(1), schema.MaxLength(100))
	// callKey is a key making a call that changes something idempotent: the host does a call with a key it has seen only
	// once.
	callKey = str(schema.MinLength(1), schema.MaxLength(200))
)

// TerminalInfoSchema is a terminal as the host lists it.
var TerminalInfoSchema = obj(schema.Properties{
	prop("id", str()), prop("sessionId", str()), prop("index", integer()), opt("name", str()), opt("agent", boolean()),
	prop("cwd", str()), prop("env", union(str(), nul())), prop("title", str()), prop("createdAt", integer()),
	prop("cols", integer()), prop("rows", integer()),
	opt("exited", obj(schema.Properties{opt("code", integer()), opt("error", str())})),
})

// TerminalWaitSchema is how long a read waits for output, and for what.
var TerminalWaitSchema = obj(schema.Properties{
	prop("until", union(p2runtime.Literal("quiet"), p2runtime.Literal("exit"), p2runtime.Literal("match"))),
	opt("pattern", str(schema.MaxLength(1000))),
	opt("ignoreCase", boolean()),
	opt("quietMs", integer(schema.Minimum(50), schema.Maximum(60_000))),
	prop("timeoutMs", integer(schema.Minimum(0), schema.Maximum(120_000))),
})

// TerminalReadSchema is what a read returns.
var TerminalReadSchema = obj(schema.Properties{
	prop("terminal", TerminalInfoSchema),
	prop("from", integer(schema.Minimum(0))), prop("to", integer(schema.Minimum(0))),
	prop("text", str()), prop("skipped", integer(schema.Minimum(0))), prop("truncated", integer(schema.Minimum(0))),
	opt("screen", str()),
	opt("waited", union(p2runtime.Literal("quiet"), p2runtime.Literal("exit"), p2runtime.Literal("match"), p2runtime.Literal("timeout"), p2runtime.Literal("closed"))),
	opt("foreground", str()),
})

// AgentTerminals are the terminal tool's calls from a session worker (the agent): its session's terminals, the same the
// owner sees in the terminal panel. The session comes from the worker's identity; only its current generation is served,
// never an archived session, and new terminals only on environments the session works on.
var AgentTerminals = must(contracts.Remote("p2.agent-terminals", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("worker", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			// The session's terminals, each with the job in front of its shell while one runs.
			contracts.Meth("list", method(empty, obj(schema.Properties{
				prop("windows", arr(obj(schema.Properties{prop("terminal", TerminalInfoSchema), opt("foreground", str())}))),
			}))),
			// A new terminal at `location`, a shell; `command` is typed into it.
			contracts.Meth("open", method(obj(schema.Properties{
				prop("key", callKey), prop("location", p2runtime.LocationSchema),
				opt("name", str(schema.MinLength(1), schema.MaxLength(60))),
				opt("command", str(schema.MaxLength(64*1024))),
			}), TerminalInfoSchema)),
			// tmux `send-keys`: key names as keys, other arguments as text; all text when `literal`.
			contracts.Meth("send", method(obj(schema.Properties{
				prop("key", callKey), prop("target", target), prop("keys", arr(str(schema.MaxLength(64*1024)))), prop("literal", boolean()),
			}), TerminalInfoSchema)),
			// Where the next read without `from` starts.
			contracts.Meth("cursor", method(obj(schema.Properties{prop("target", target)}),
				obj(schema.Properties{prop("terminal", TerminalInfoSchema), prop("offset", integer(schema.Minimum(0)))}))),
			// Output since `from` (default: the read cursor) as plain text, after an optional wait.
			contracts.Meth("read", method(obj(schema.Properties{
				prop("target", target), opt("from", integer(schema.Minimum(0))), opt("wait", TerminalWaitSchema),
			}), TerminalReadSchema)),
			// tmux `capture-pane`: the screen as plain text, up to `scrollback` lines before it; styled with `escapes`.
			contracts.Meth("capture", method(obj(schema.Properties{
				prop("target", target), prop("scrollback", integer(schema.Minimum(0), schema.Maximum(100_000))), opt("escapes", boolean()),
			}), obj(schema.Properties{prop("terminal", TerminalInfoSchema), prop("text", str())}))),
			contracts.Meth("close", method(obj(schema.Properties{prop("key", callKey), prop("target", target)}),
				obj(schema.Properties{prop("terminalId", str())}))),
		}}),
	},
}))
