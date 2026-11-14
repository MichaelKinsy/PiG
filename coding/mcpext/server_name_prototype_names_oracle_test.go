package mcpext_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type protoNameRun struct {
	Code   int      `json:"code"`
	Output []string `json:"output"`
	File   string   `json:"file"`
}

// D645, the MCP server name row and the `mcp` option row. Pi's addMcpServerConfig and removeMcpServerConfig test `target[name] !== undefined` on the plain
// object parsed from mcp.json (packages/coding-agent/src/extensions/mcp/config.ts:195), and its option parser reads `known[name]` on a plain object
// (extensions/mcp/cli.ts:204). A server named like a member of Object.prototype is therefore "Replaced" the first time it is added and "Removed" when
// nothing is there, a server named __proto__ is never written, and `--constructor v` is taken for a known option with a value. PiG treats such a name
// as any other name: each probe runs the same five commands for the name and for the ordinary name `plain`, and PiG's lines for the name must be its
// lines for `plain` with the name in place. Pi's answers are the record's and the pinned Pi must give them in this run; `plain` must be equal on both sides.
func TestAnMcpServerOrOptionNamedLikeAnObjectPrototypeMemberIsTreatedAsAnyOtherName(t *testing.T) {
	names := []string{"constructor", "toString", "hasOwnProperty", "__proto__", "plain"}
	commands := func(name string) [][]string {
		return [][]string{
			{"add", name, "--", "echo", "hi"},
			{"add", name, "--", "echo", "hi"},
			{"remove", name},
			{"remove", name},
			{"add", "srv", "--" + name, "v", "--", "echo", "hi"},
		}
	}
	probes := make([][][]string, 0, len(names))
	for _, name := range names {
		probes = append(probes, commands(name))
	}
	var pi [][]protoNameRun
	pioracle.Run(t, `
const { runMcpCommand } = await load("pi-coding-agent/extensions/mcp/cli.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const commands of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "mcpname-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(cwd);
	process.env.PIG_CODING_AGENT_DIR = agentDir; process.env.PI_CODING_AGENT_DIR = agentDir;
	const runs = [];
	for (const args of commands) {
		const output = [];
		const code = await runMcpCommand(args, { cwd, agentDir, log: (l) => output.push(String(l)), error: (l) => output.push(String(l)) });
		const file = path.join(agentDir, "mcp.json");
		runs.push({ code, output: output.map((l) => l.split(root).join("<ROOT>")), file: fs.existsSync(file) ? fs.readFileSync(file, "utf8").replace(/\s+/g, "") : "" });
	}
	out.push(runs);
}
emit(out);`, probes, &pi)
	if len(pi) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(probes))
	}
	pig := make([][]protoNameRun, 0, len(names))
	for _, name := range names {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		for _, dir := range []string{agentDir, cwd} {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, key := range []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME", "HOME"} {
			t.Setenv(key, agentDir)
		}
		var runs []protoNameRun
		for _, args := range commands(name) {
			var output []string
			record := func(line string) { output = append(output, strings.ReplaceAll(line, root, "<ROOT>")) }
			code := mcpext.RunMcpCommand(t.Context(), args, mcpext.McpCommandOptions{Cwd: cwd, AgentDir: agentDir, AppName: "pig", ConfigDirName: ".pig", Log: record, Error: record})
			file, _ := os.ReadFile(filepath.Join(agentDir, "mcp.json"))
			runs = append(runs, protoNameRun{Code: code, Output: output, File: strings.Join(strings.Fields(string(file)), "")})
		}
		pig = append(pig, runs)
	}
	plain := len(names) - 1
	if !reflect.DeepEqual(pi[plain], pig[plain]) {
		t.Errorf("the ordinary name differs:\nPi  %+v\nPiG %+v", pi[plain], pig[plain])
	}
	for i, name := range names[:plain] {
		for j, args := range commands(name) {
			t.Logf("mcp %s: Pi exit %d %q file %s; PiG exit %d %q file %s", strings.Join(args, " "), pi[i][j].Code, pi[i][j].Output, pi[i][j].File, pig[i][j].Code, pig[i][j].Output, pig[i][j].File)
			want := protoNameRun{Code: pig[plain][j].Code, File: strings.ReplaceAll(pig[plain][j].File, "plain", name)}
			for _, line := range pig[plain][j].Output {
				want.Output = append(want.Output, strings.ReplaceAll(line, "plain", name))
			}
			if !reflect.DeepEqual(pig[i][j], want) {
				t.Errorf("mcp %s: PiG answers exit %d %q file %s, want what it answers for the name plain: exit %d %q file %s", strings.Join(args, " "), pig[i][j].Code, pig[i][j].Output, pig[i][j].File, want.Code, want.Output, want.File)
			}
		}
		first, again, option := pi[i][0], pi[i][3], pi[i][4]
		if len(first.Output) == 0 || !strings.HasPrefix(first.Output[0], `Replaced global MCP server "`+name+`"`) {
			t.Errorf("mcp add %s on an empty file: Pi answers %q, the record says Replaced", name, first.Output)
		}
		if name == "__proto__" && first.File != `{"mcpServers":{}}` {
			t.Errorf("mcp add __proto__: Pi's file is %s, the record says it holds no server", first.File)
		}
		if len(again.Output) == 0 || !strings.HasPrefix(again.Output[0], `Removed global MCP server "`+name+`"`) {
			t.Errorf("the second mcp remove %s: Pi answers %q, the record says Removed", name, again.Output)
		}
		if option.Code != 0 || len(option.Output) == 0 || !strings.HasPrefix(option.Output[0], `Added global MCP server "srv"`) {
			t.Errorf("mcp add srv --%s v: Pi answers exit %d %q, the record says the server is added", name, option.Code, option.Output)
		}
	}
}

// D645, the listed tool row. Pi's `mcp list` reads `report.toolExposure?.[tool]` on the server's plain overrides object
// (packages/coding-agent/src/extensions/mcp/cli.ts:588), so a tool named like a member of Object.prototype is listed with the inherited member's text
// in the brackets where an overridden exposure is shown. PiG's overrides are own entries: the tool is listed like any tool without an override. PiG's
// lines must be Pi's without that bracket.
func TestAnMcpToolNamedLikeAnObjectPrototypeMemberIsListedWithoutAnExposure(t *testing.T) {
	server := filepath.Join(t.TempDir(), "server.js")
	if err := os.WriteFile(server, []byte(`const readline = require("node:readline");
const send = (message) => process.stdout.write(JSON.stringify(message) + "\n");
const schema = { type: "object", properties: { query: { type: "string" } }, required: ["query"] };
readline.createInterface({ input: process.stdin }).on("line", (line) => {
  const message = JSON.parse(line);
  if (message.id === undefined) return;
  let result = {};
  if (message.method === "initialize") result = { protocolVersion: message.params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "docs", version: "1.0.0" } };
  else if (message.method === "tools/list") result = { tools: [{ name: "constructor", description: "A tool named constructor.", inputSchema: schema }, { name: "search", description: "Search the docs.", inputSchema: schema }] };
  send({ jsonrpc: "2.0", id: message.id, result });
});
`), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{{"add", "docs", "--", "node", server}, {"list"}}
	var pi []protoNameRun
	pioracle.Run(t, `
const { runMcpCommand } = await load("pi-coding-agent/extensions/mcp/cli.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const root = fs.mkdtempSync(path.join(os.tmpdir(), "mcplist-"));
const agentDir = path.join(root, "agent");
const cwd = path.join(root, "proj");
fs.mkdirSync(agentDir); fs.mkdirSync(cwd);
process.env.PIG_CODING_AGENT_DIR = agentDir; process.env.PI_CODING_AGENT_DIR = agentDir;
const runs = [];
for (const args of input) {
	const output = [];
	const code = await runMcpCommand(args, { cwd, agentDir, log: (l) => output.push(String(l)), error: (l) => output.push(String(l)) });
	runs.push({ code, output: output.map((l) => l.split(root).join("<ROOT>")), file: "" });
}
emit(runs);`, commands, &pi)
	root := t.TempDir()
	agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME", "HOME"} {
		t.Setenv(key, agentDir)
	}
	var pig []protoNameRun
	for _, args := range commands {
		var output []string
		record := func(line string) { output = append(output, strings.ReplaceAll(line, root, "<ROOT>")) }
		code := mcpext.RunMcpCommand(t.Context(), args, mcpext.McpCommandOptions{Cwd: cwd, AgentDir: agentDir, AppName: "pig", ConfigDirName: ".pig", Log: record, Error: record})
		pig = append(pig, protoNameRun{Code: code, Output: output})
	}
	if len(pi) != 2 || len(pig) != 2 {
		t.Fatalf("Pi answered %d and PiG %d of 2 commands", len(pi), len(pig))
	}
	const bracket = " [function Object() { [native code] }]"
	piList, pigList := strings.Join(pi[1].Output, "\n"), strings.Join(pig[1].Output, "\n")
	t.Logf("mcp list: Pi exit %d %q; PiG exit %d %q", pi[1].Code, pi[1].Output, pig[1].Code, pig[1].Output)
	if !strings.Contains(piList, "constructor"+bracket) {
		t.Errorf("mcp list: Pi lists %q, the record says the tool constructor is listed with%s", pi[1].Output, bracket)
	}
	if want := strings.ReplaceAll(piList, bracket, ""); pigList != want || pig[1].Code != pi[1].Code || !strings.Contains(pigList, "constructor") {
		t.Errorf("mcp list: PiG lists %q (exit %d), want Pi's lines without the bracket: %q (exit %d)", pig[1].Output, pig[1].Code, want, pi[1].Code)
	}
}
