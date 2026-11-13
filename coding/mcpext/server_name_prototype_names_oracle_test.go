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
