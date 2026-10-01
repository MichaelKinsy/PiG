package extensionconformance

import "testing"

// What the real Pi 0.99.1 McpServerRegistry.list() returns, as JSON.stringify writes it, for configs registered with these members in this order (probe: validateMcpServerConfig then McpServerRegistry.register and list; mcp-servers.ts is unchanged in 0.99.2 except the additions the registry's copy semantics do not touch). validateMcpServerConfig returns the object it was given (mcp-servers.ts:151-222 in 0.99.1) and list() structuredClones it, so getMcpServers hands back every member, known or not, in the order the extension wrote (integer-like keys first, as a JavaScript object orders them).
const mcpServerOrderWant = `[["ordered",{"timeout":5,"url":"https://mcp.example/x","toolExposure":{"2":"direct","b*":"direct","a":"hidden"},"headers":{"X-Z":"1","A":"2"},"enabled":true,"exposure":"direct","foo":{"zz":1,"aa":[{"y":1,"b":2}]},"oauth":{"scope":"s","clientId":"c"}}],["stdio_one",{"args":["--x"],"env":{"Z":"1","A":"2"},"cwd":".","command":"mcp-bin","type":"stdio"}]]`

const nodeMcpOrderFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerMcpServer("ordered", { timeout: 5, url: "https://mcp.example/x", toolExposure: { "b*": "direct", a: "hidden", 2: "direct" }, headers: { "X-Z": "1", A: "2" }, enabled: true, exposure: "direct", foo: { zz: 1, aa: [{ y: 1, b: 2 }] }, oauth: { scope: "s", clientId: "c" } });
  pi.registerMcpServer("stdio_one", { args: ["--x"], env: { Z: "1", A: "2" }, cwd: ".", command: "mcp-bin", type: "stdio" });
  pi.registerCommand("order", { handler: async (_args, ctx) => {
    lines(ctx, "order", JSON.stringify(pi.getMcpServers().map((s) => [s.name, s.config])));
  } });
}
`

const pyMcpOrderFixture = pyAPIPrelude + `
def new_extension():
    e = pig_sdk.Extension("pyapi-mcp-order")
    e.register_mcp_server("ordered", {"timeout": 5, "url": "https://mcp.example/x", "toolExposure": {"b*": "direct", "a": "hidden", "2": "direct"}, "headers": {"X-Z": "1", "A": "2"}, "enabled": True, "exposure": "direct", "foo": {"zz": 1, "aa": [{"y": 1, "b": 2}]}, "oauth": {"scope": "s", "clientId": "c"}})
    e.register_mcp_server("stdio_one", {"args": ["--x"], "env": {"Z": "1", "A": "2"}, "cwd": ".", "command": "mcp-bin", "type": "stdio"})

    def order(ctx, args):
        lines(ctx, "order", json.dumps([[s["name"], s["config"]] for s in ctx.get_mcp_servers()], separators=(",", ":")))

    e.command("order", "", order)
    return e
`

// loader.ts:456-478 and mcp-servers.ts:151-222, 283: an extension that reads getMcpServers sees each config as registered, in the order it wrote the members, with the members the host does not know. The host answers from the state it replicates, so a config the host re-encoded from a Go struct (fixed member order, unknown members dropped) fails here. The Go SDK decodes configs into its typed McpServerConfig, which has no member order or unknown members (a Go language mechanic); the Rust SDK's pass-through config is the same state the host replicates and is covered by the host-level registry test.
func TestNodeGetMcpServersKeepsRegisteredMemberOrder(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-mcp-order", nodeMcpOrderFixture})
		rig.command("nodeapi-mcp-order", "order", "")
		rig.waitNotification("order|" + mcpServerOrderWant)
	})
}

func TestPythonGetMcpServersKeepsRegisteredMemberOrder(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-mcp-order", pyMcpOrderFixture})
		rig.command("pyapi-mcp-order", "order", "")
		rig.waitNotification("order|" + mcpServerOrderWant)
	})
}

// What real Pi 0.99.2 getMcpServers returns for a server registered with the `codemode-deferred` alias: validateMcpServerConfig returns a copy with the aliases of `exposure` and of every `toolExposure` entry resolved in place, and registerMcpServer stores structuredClone of it (mcp-servers.ts:151-166,192-196; loader.ts:479 in 0.99.2). Member order is the one written.
const mcpServerAliasWant = `[["aliased",{"command":"mcp-bin","exposure":"codemode","toolExposure":{"b*":"codemode","a":"direct"},"enabled":false}]]`

const nodeMcpAliasFixture = nodeAPIPrelude + `
export default function (pi) {
  pi.registerMcpServer("aliased", { command: "mcp-bin", exposure: "codemode-deferred", toolExposure: { "b*": "codemode-deferred", a: "direct" }, enabled: false });
  pi.registerCommand("alias", { handler: async (_args, ctx) => {
    lines(ctx, "alias", JSON.stringify(pi.getMcpServers().map((s) => [s.name, s.config])));
  } });
}
`

const pyMcpAliasFixture = pyAPIPrelude + `
def new_extension():
    e = pig_sdk.Extension("pyapi-mcp-alias")
    e.register_mcp_server("aliased", {"command": "mcp-bin", "exposure": "codemode-deferred", "toolExposure": {"b*": "codemode-deferred", "a": "direct"}, "enabled": False})

    def alias(ctx, args):
        lines(ctx, "alias", json.dumps([[s["name"], s["config"]] for s in ctx.get_mcp_servers()], separators=(",", ":")))

    e.command("alias", "", alias)
    return e
`

// mcp-servers.ts:151-166,192-196 and loader.ts:479 (0.99.2): an extension that reads getMcpServers sees the exposure an alias stands for, never the alias. An SDK that never asks the host, or a host that replicates the raw registration, fails here.
func TestNodeGetMcpServersReportsResolvedExposureAliases(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newNodeAPIRig(t, isolation, nil, nodeAPIFixture{"nodeapi-mcp-alias", nodeMcpAliasFixture})
		rig.command("nodeapi-mcp-alias", "alias", "")
		rig.waitNotification("alias|" + mcpServerAliasWant)
	})
}

func TestPythonGetMcpServersReportsResolvedExposureAliases(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-mcp-alias", pyMcpAliasFixture})
		rig.command("pyapi-mcp-alias", "alias", "")
		rig.waitNotification("alias|" + mcpServerAliasWant)
	})
}
