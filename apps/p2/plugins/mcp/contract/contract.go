// Package contract ports apps/p2/plugins/mcp/contract.ts: MCP servers' contracts: the server's manager (host-internal:
// every MCP connection and process, by instance and lease, and the OAuth sign-ins) and its view per principal (`Mcp`: a
// session worker sets its locations and calls tools; the owner sees and restarts instances and signs in and out of HTTP
// servers).
package contract

import (
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var (
	c      = p2runtime.Contract
	str    = schema.Str
	obj    = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop   = schema.Prop
	opt    = schema.Opt
	arr    = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
	union  = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	oneOf  = func(values ...any) schema.Schema { return schema.OneOf(values) }
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
	literal = func(value string) schema.Schema { return p2runtime.Literal(value) }
	with    = func(a, b schema.Properties) schema.Properties { return append(append(schema.Properties{}, a...), b...) }
)

// ToolSchema is an MCP tool as its server lists it (`tools/list`): name, title, description, schemas and annotations.
var ToolSchema = obj(schema.Properties{
	prop("name", str()),
	opt("title", str()),
	opt("description", str()),
	prop("inputSchema", schema.Unknown()),
	opt("outputSchema", schema.Unknown()),
	opt("annotations", schema.Unknown()),
})

// RunsOnSchema is where an instance runs: `"host"` (the p2 host) or `{ env }` (an environment of `envs.json`).
var RunsOnSchema = union(literal("host"), obj(schema.Properties{prop("env", str())}))

// InstanceSchema is one running (or failed) server process or connection.
var InstanceSchema = obj(schema.Properties{
	prop("id", str()),
	prop("server", str()),
	prop("source", oneOf("owner", "project")),
	// The location it serves; null: shared by every session, or an HTTP server.
	prop("location", union(p2runtime.LocationSchema, schema.Nul())),
	prop("runsOn", RunsOnSchema),
	// `oauth`: an HTTP server that signs in with OAuth (no `Authorization` header configured).
	opt("auth", literal("oauth")),
	// `sign-in`: the HTTP server asked for authorization.
	prop("status", oneOf("starting", "connected", "sign-in", "failed")),
	opt("error", str()),
	// Kept while it reconnects.
	prop("tools", arr(ToolSchema)),
})

// SignInSchema is a sign-in to an HTTP server under way (`waiting`: for the browser's redirect back or a pasted redirect
// URL) or failed, one per server: returning to a loopback callback on the p2 host, or to p2's `/mcp/callback` route. No
// URL, state or code.
var SignInSchema = obj(schema.Properties{
	prop("id", str()),
	prop("server", str()),
	prop("via", oneOf("loopback", "route")),
	prop("status", oneOf("waiting", "failed")),
	opt("error", str()),
})

// SignInStartSchema is a begun sign-in: signed in at once (stored tokens refreshed), or the authorization URL to open.
var SignInStartSchema = union(
	obj(schema.Properties{prop("kind", literal("authorized"))}),
	obj(schema.Properties{prop("kind", literal("redirect")), prop("url", str())}),
)

// PasteSchema completes sign-in `id` with the final redirect URL the owner copied from the browser's address bar.
var PasteSchema = obj(schema.Properties{prop("id", str()), prop("redirectUrl", str(schema.MaxLength(8192)))})

// LeaseViewSchema is the instances one session's lease at a location uses, and why entries of its project were refused.
var LeaseViewSchema = obj(schema.Properties{
	prop("session", str()),
	prop("location", p2runtime.LocationSchema),
	prop("instances", arr(str())),
	prop("errors", arr(str())),
})

// ProjectFileMaxChars is the largest project `mcp.json` a worker sends.
const ProjectFileMaxChars = 256 * 1024

// ProjectFileSchema is a project's `<cwd>/.pi/mcp.json` at one of the locations, as the worker read it there: its text
// (checked by the manager: a trust boundary), or why it could not be read.
var ProjectFileSchema = union(
	obj(schema.Properties{prop("location", p2runtime.LocationSchema), prop("text", str(schema.MaxLength(ProjectFileMaxChars)))}),
	obj(schema.Properties{prop("location", p2runtime.LocationSchema), prop("error", str(schema.MaxLength(2000)))}),
)

// locations is where a session's conversations work, and the project files found there (absent: none).
func locations() schema.Properties {
	return schema.Properties{prop("locations", arr(p2runtime.LocationSchema)), opt("projects", arr(ProjectFileSchema))}
}

// callResult is `tools/call`'s result as the server sent it.
var callResult schema.Schema = chordjson.ObjectOf("type", "object")

// args are tool arguments: any JSON object (a record of any values); the MCP server checks them.
var args = p2runtime.Record(schema.Unknown())

// caller is what every worker request names: the session's worker it comes from (filled in by the view).
func caller() schema.Properties {
	return schema.Properties{prop("session", str()), prop("generation", schema.Int()), prop("incarnation", str())}
}

// McpManager is the MCP manager (`p2.mcp.manager`, version 2; server, host lifetime): the owner configuration, the
// instances and their leases. Every MCP process and connection is here; session workers reach it only through `Mcp`.
//
// Authority: a lease places servers only at a location on an environment the session's conversations work on as the
// server records them (the catalog's session location, the subagent store's own locations); the cwd there is the worker's
// to choose, as its agent can run commands anywhere on that machine. A project's servers are code the worker names (it
// reads the file), so this fences stale or confused workers to their machines, not a compromised worker process: it runs
// unsandboxed on the p2 host, its subagent store entries are its own reports, and its location tool moves the session to
// any configured environment.
var McpManager = must(contracts.Local("p2.mcp.manager", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("instances", c(arr(InstanceSchema))),
		contracts.Mem("signIns", c(arr(SignInSchema))),
		// The leases of every session and location, with the instances each uses.
		contracts.Mem("leases", c(arr(LeaseViewSchema))),
		// Why entries of `mcp.json` were skipped (the leases' projects' errors are in `leases`).
		contracts.Mem("errors", c(arr(str()))),
		// Leases the configured servers (and the projects') at exactly `locations` for the caller's session (its leases
		// elsewhere end); a newer generation's leases refuse an older generation.
		contracts.Mem("setLocations", method(obj(with(caller(), locations())))),
		// Ends the leases of one worker generation (its view ended).
		contracts.Mem("releaseLeases", method(obj(schema.Properties{prop("session", str()), prop("generation", schema.Int())}))),
		// Calls `tool` of `server` at `location`, through the instance the lease there uses.
		contracts.Mem("call", method(obj(with(caller(), schema.Properties{
			prop("location", p2runtime.LocationSchema), prop("server", str()), prop("tool", str()), prop("args", args),
		})), callResult)),
		contracts.Mem("restart", method(obj(schema.Properties{prop("instance", str())}))),
		// Begins a sign-in to HTTP server `server`, replacing its current one: the browser returns to a loopback callback
		// on the p2 host, or to p2's callback route at `url`.
		contracts.Mem("beginSignIn", method(obj(schema.Properties{
			prop("server", str()),
			prop("redirect", union(
				obj(schema.Properties{prop("kind", literal("loopback"))}),
				obj(schema.Properties{prop("kind", literal("route")), prop("url", str())}),
			)),
		}), SignInStartSchema)),
		// Completes the route sign-in whose `state` the authorization response on the callback route carries.
		contracts.Mem("completeSignIn", method(obj(schema.Properties{
			prop("state", str()), opt("code", str()), opt("iss", str()), opt("error", str()),
		}), obj(schema.Properties{prop("server", str())}))),
		contracts.Mem("pasteSignIn", method(PasteSchema)),
		contracts.Mem("cancelSignIn", method(obj(schema.Properties{prop("id", str())}))),
		// Forgets the server's OAuth credentials; its instances reconnect without them.
		contracts.Mem("signOut", method(obj(schema.Properties{prop("server", str())}))),
		// A loopback probe URL for a sign-in page on `origin`: only a browser on the p2 host reaches it (see
		// `LoopbackProbe`).
		contracts.Mem("probe", method(obj(schema.Properties{prop("origin", str())}), obj(schema.Properties{prop("url", str())}))),
	},
}))

// Mcp is MCP servers as a principal sees them (`p2.mcp`, version 2): a session worker sets its locations and calls tools
// (its session, generation and incarnation from the link), the owner sees every instance with its errors, restarts them
// and signs in and out of HTTP servers. Nobody else gets one.
var Mcp = must(contracts.Remote("p2.mcp", contracts.RemoteDeclaration{
	Version:     2,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("worker", contracts.VariantDeclaration{
			State: []contracts.Property{
				// The instances the session's leases use, shared ones included; `error` is the owner's only.
				p2runtime.StateProp("servers", arr(InstanceSchema)),
				// Why the session's leases refused entries of its projects' `mcp.json` (or a location).
				p2runtime.StateProp("errors", arr(str())),
			},
			Methods: []contracts.MethodEntry{
				// Where the session's conversations work now, with the project files there: its servers are leased there,
				// and only there.
				contracts.Meth("setLocations", method(obj(locations()))),
				contracts.Meth("call", method(obj(schema.Properties{
					prop("location", p2runtime.LocationSchema), prop("server", str()), prop("tool", str()), prop("args", args),
				}), callResult)),
			},
		}),
		contracts.Var("owner", contracts.VariantDeclaration{
			// `errors`: the owner's `mcp.json`'s, then every lease's (by session).
			State: []contracts.Property{
				p2runtime.StateProp("instances", arr(InstanceSchema)),
				p2runtime.StateProp("errors", arr(str())),
				p2runtime.StateProp("signIns", arr(SignInSchema)),
			},
			Methods: []contracts.MethodEntry{
				contracts.Meth("restart", method(obj(schema.Properties{prop("instance", str())}))),
				// Begins a sign-in returning to a loopback callback on the p2 host (pasting its final URL works from
				// anywhere). Browsers open `/mcp/sign-in/<server>` instead, which also finds out where the browser runs.
				contracts.Meth("signIn", method(obj(schema.Properties{prop("server", str())}), SignInStartSchema)),
				contracts.Meth("paste", method(PasteSchema)),
				contracts.Meth("cancelSignIn", method(obj(schema.Properties{prop("id", str())}))),
				contracts.Meth("signOut", method(obj(schema.Properties{prop("server", str())}))),
			},
		}),
	},
}))

// SignInLink is the p2 page that signs the owner in to HTTP server `server` (relative: it opens on the origin the viewer
// uses).
func SignInLink(server string) string { return "/mcp/sign-in/" + encodeURIComponent(server) }

// encodeURIComponent is JavaScript's: every UTF-8 byte but letters, digits and `-_.!~*'()` as `%XX`.
func encodeURIComponent(text string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := range len(text) {
		b := text[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("-_.!~*'()", b) >= 0 {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&15])
	}
	return out.String()
}

// McpExtension is the worker's Durable extension of MCP tools.
const McpExtension = "mcp"
