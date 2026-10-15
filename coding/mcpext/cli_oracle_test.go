package mcpext_test

import (
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type mcpCLIProbe struct {
	Seed     string     `json:"seed"`
	Project  string     `json:"project"`
	Commands [][]string `json:"commands"`
}

type mcpCLIOutcome struct {
	Runs []struct {
		Code   int      `json:"code"`
		Output []string `json:"output"`
	} `json:"runs"`
	Global  string `json:"global"`
	Project string `json:"project"`
}

// The empty-list message names the project file ".pi/mcp.json" in Pi and ".pig/mcp.json" in PiG (D2 host identity). Servers named like Object.prototype members ("__proto__", "constructor", "toString" ...) are in the corpus: Pi's plain-object lookups find them inherited and report "Replaced" or "Removed".
// `pi mcp` (mcp/cli.js) against Pi's runMcpCommand: help, unknown commands and options, add (stdio and HTTP; every option, misplaced options, bad KEY=VALUE pairs, `--` and positional pass-through, invalid exposure and port values, replacing,
// project scope and the trust note), remove (global, project, defined in the other scope), login/logout error paths, and an empty or invalid listing. Output lines, exit codes and the bytes of both mcp.json files are compared.
func TestMcpCommandMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	seeds := []string{
		"", "", "",
		`{"mcpServers":{"old":{"command":"x"},"parked":{"command":"y","enabled":false}}}`,
		`{"mcpServers":{"web":{"url":"https://x.test/mcp"},"parked":{"command":"y","enabled":false}},"other":1}`,
		"{\n    \"mcpServers\": {\n        \"a\": {\"command\": \"x\", \"enabled\": false}\n    },\n    \"keep\": [1, 2]\n}\n",
		`not json`,
		`{"mcpServers":{"bad":{"args":["no command"]}}}`,
		`{"mcpServers":[]}`,
		`[]`,
	}
	name := func() string {
		return pick("srv", "old", "web", "parked", "a b", "a.b", "日本", "x-y_z", "", "__proto__", "constructor", "toString", "hasOwnProperty", "valueOf", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__")
	}
	envPair := func() string { return pick("K=V", "A=b=c", "=x", "noequals", "K=", "日本=語") }
	add := func() []string {
		args := []string{"add"}
		if rng.Intn(12) != 0 {
			args = append(args, name())
		}
		options := []string{}
		httpMode := rng.Intn(2) == 0
		if httpMode {
			options = append(options, "--url", pick("https://x.test/mcp", "http://localhost:3000", "not a url", "ftp://x", "", "https://x.test/mcp?a=1#h"))
			for range rng.Intn(3) {
				switch rng.Intn(6) {
				case 0:
					options = append(options, "--header", pick("X-A=1", "Authorization=Bearer x", "authorization=x", "bad", "X-B=a=b"))
				case 1:
					options = append(options, "--bearer-token-env-var", pick("TOKEN", "", "A B"))
				case 2:
					options = append(options, "--oauth-client-id", pick("cid", ""))
				case 3:
					options = append(options, "--oauth-client-secret", pick("${SECRET}", "!cmd", "plain"))
				case 4:
					options = append(options, "--oauth-callback-port", pick("8080", "abc", "0", "-1", "1e3", "", "65536", "80.5", " 90 ", "0x10"))
				default:
					options = append(options, "--oauth-client-name", pick("Pi", "日本"))
				}
			}
			if rng.Intn(8) == 0 {
				options = append(options, pick("--env", "--cwd"), pick("K=V", "/tmp"))
			}
		} else if rng.Intn(8) == 0 {
			options = append(options, "--header", "X=1")
		}
		for range rng.Intn(3) {
			switch rng.Intn(5) {
			case 0:
				options = append(options, "--exposure", pick("codemode", "deferred", "direct", "hidden", "bogus", "", "Direct"))
			case 1:
				options = append(options, "--description", pick("d", "multi\nline", "", "  padded  "))
			case 2:
				options = append(options, "--local")
			case 3:
				options = append(options, "-l")
			default:
				if !httpMode {
					options = append(options, "--env", envPair())
				}
			}
		}
		if !httpMode && rng.Intn(3) == 0 {
			options = append(options, "--cwd", pick("/tmp", "rel/dir", ""))
		}
		if rng.Intn(15) == 0 {
			options = append(options, pick("--bogus", "--env", "--url"))
		}
		args = append(args, options...)
		if !httpMode {
			if rng.Intn(10) != 0 {
				args = append(args, "--")
			}
			for range rng.Intn(4) {
				args = append(args, pick("node", "server.js", "--flag", "-x", "--env", "a b", "--", "日本"))
			}
		} else if rng.Intn(10) == 0 {
			args = append(args, "--", "cmd")
		}
		return args
	}
	command := func() []string {
		switch rng.Intn(14) {
		case 0, 1, 2, 3, 4:
			return add()
		case 5, 6:
			args := []string{"remove"}
			if rng.Intn(10) != 0 {
				args = append(args, name())
			}
			if rng.Intn(3) == 0 {
				args = append(args, pick("-l", "--local"))
			}
			if rng.Intn(10) == 0 {
				args = append(args, "extra")
			}
			return args
		case 7:
			args := []string{pick("login", "logout")}
			if rng.Intn(8) != 0 {
				args = append(args, name())
			}
			if rng.Intn(4) == 0 {
				args = append(args, "--timeout", pick("0", "-1", "abc", "10", "", "1e400", " 5 ", "0x10"))
			}
			if rng.Intn(10) == 0 {
				args = append(args, "extra")
			}
			return args
		case 8:
			return []string{pick("help", "bogus", "")}
		case 9:
			return []string{"add", "--help"}
		default:
			return []string{pick("remove", "login", "logout", "add")}
		}
	}
	var probes []mcpCLIProbe
	for range 600 {
		p := mcpCLIProbe{Seed: pick(seeds...)}
		if rng.Intn(4) == 0 {
			p.Project = pick(`{"mcpServers":{"old":{"command":"p"}}}`, `{"mcpServers":{"srv":{"command":"p","enabled":false}}}`)
		}
		for range 1 + rng.Intn(3) {
			p.Commands = append(p.Commands, command())
		}
		probes = append(probes, p)
	}
	// list needs no connection when every server is disabled or invalid: run it on its own.
	for _, seed := range []string{"", seeds[3], seeds[5], seeds[6], seeds[7], seeds[8], seeds[9]} {
		for _, args := range [][]string{{"list"}, {"list", "--json"}, {"list", "extra"}, {"list", "--bogus"}} {
			probes = append(probes, mcpCLIProbe{Seed: seed, Commands: [][]string{args}})
		}
	}
	// A connection that fails before any response is Node's fetch rejection, TypeError("fetch failed"), whatever the cause: a refused port, a name that does not resolve, or a scheme-less proxy answer.
	for _, url := range []string{"http://127.0.0.1:1/mcp", "https://x.test/mcp", "http://[::1]:1/mcp"} {
		seed := `{"mcpServers":{"web":{"url":"` + url + `"}}}`
		for _, args := range [][]string{{"login", "web"}, {"list"}, {"list", "--json"}} {
			probes = append(probes, mcpCLIProbe{Seed: seed, Commands: [][]string{args}})
		}
	}
	var want []mcpCLIOutcome
	pioracle.Run(t, `
const { runMcpCommand } = await load("pi-coding-agent/extensions/mcp/cli.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const p of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "mcpcli-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(cwd);
	// Pi's default credential store is <getAgentDir()>/mcp-auth.json, whatever options.agentDir says: keep it in the probe's directory, as PiG's default is, so login and logout never reach the agent directory of the process running the test.
	process.env.PIG_CODING_AGENT_DIR = agentDir; process.env.PI_CODING_AGENT_DIR = agentDir;
	if (p.seed !== "") fs.writeFileSync(path.join(agentDir, "mcp.json"), p.seed);
	if (p.project !== "") { fs.mkdirSync(path.join(cwd, ".pig")); fs.writeFileSync(path.join(cwd, ".pig", "mcp.json"), p.project); }
	const runs = [];
	for (const args of p.commands) {
		const output = [];
		const code = await runMcpCommand(args, { cwd, agentDir, log: (l) => output.push(String(l)), error: (l) => output.push(String(l)) });
		runs.push({ code, output: output.map((l) => l.split(root).join("<ROOT>").split(JSON.stringify(root).slice(1, -1)).join("<ROOT>").replace(" or .pi/mcp.json.", " or .pig/mcp.json.")) });
	}
	const read = (f) => (fs.existsSync(f) ? fs.readFileSync(f, "utf8") : "<absent>");
	out.push({ runs, global: read(path.join(agentDir, "mcp.json")), project: read(path.join(cwd, ".pig", "mcp.json")) });
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);
`, probes, &want, "NO_COLOR=1")
	bad := 0
	for i, p := range probes {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		_ = os.Mkdir(agentDir, 0o755)
		_ = os.Mkdir(cwd, 0o755)
		if p.Seed != "" {
			_ = os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(p.Seed), 0o600)
		}
		if p.Project != "" {
			_ = os.Mkdir(filepath.Join(cwd, ".pig"), 0o755)
			_ = os.WriteFile(filepath.Join(cwd, ".pig", "mcp.json"), []byte(p.Project), 0o600)
		}
		var problem string
		for j, args := range p.Commands {
			var output []string
			// --json prints a path with its backslashes escaped.
			jsonRoot := strings.ReplaceAll(root, `\`, `\\`)
			record := func(line string) {
				output = append(output, strings.ReplaceAll(strings.ReplaceAll(line, root, "<ROOT>"), jsonRoot, "<ROOT>"))
			}
			code := mcpext.RunMcpCommand(t.Context(), args, mcpext.McpCommandOptions{Cwd: cwd, AgentDir: agentDir, AppName: "pig", ConfigDirName: ".pig", Log: record, Error: record})
			w := want[i].Runs[j]
			if code != w.Code || strings.Join(output, "\n") != strings.Join(w.Output, "\n") {
				problem = "command " + strings.Join(args, " ") + ":\n got " + strconv.Itoa(code) + " " + strings.Join(output, "\n    ") + "\nwant " + strconv.Itoa(w.Code) + " " + strings.Join(w.Output, "\n    ")
				break
			}
		}
		read := func(path string) string {
			data, err := os.ReadFile(path)
			if err != nil {
				return "<absent>"
			}
			return string(data)
		}
		if problem == "" {
			if g := read(filepath.Join(agentDir, "mcp.json")); g != want[i].Global {
				problem = "global mcp.json:\n got " + g + "\nwant " + want[i].Global
			} else if g := read(filepath.Join(cwd, ".pig", "mcp.json")); g != want[i].Project {
				problem = "project mcp.json:\n got " + g + "\nwant " + want[i].Project
			}
		}
		if problem != "" {
			if bad++; bad <= 5 {
				t.Errorf("probe %d seed=%q project=%q commands=%q: %s", i, p.Seed, p.Project, p.Commands, problem)
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
