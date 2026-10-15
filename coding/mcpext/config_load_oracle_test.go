package mcpext_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/extensions/mcp/config.ts loadMcpConfig and core/mcp-servers.ts validateMcpServerConfig. Random global and project mcp.json files (valid servers, overrides, every validation failure, bad JSON) load the same servers, scopes, sources, errors and autoEnableCodemode.
func TestLoadMcpConfigMatchesPiOnRandomFiles(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	// mostly returns a valid first option, so that most servers load and the rest probe one failure at a time.
	mostly := func(valid string, others ...string) string {
		if rng.Intn(4) != 0 {
			return valid
		}
		return others[rng.Intn(len(others))]
	}
	names := []string{"fs", "docs", "my-server", "my_server", "a.b", "fs", "x y", "sentry", "internal-tools", "UPPER", "9", "docs", "other", "tools", "é"}
	exposures := []string{`"codemode"`, `"direct"`, `"hidden"`, `"bogus"`, `5`, `null`}
	value := func() string {
		var members []string
		add := func(s string) { members = append(members, s) }
		switch rng.Intn(12) {
		case 0, 1, 2, 3, 9, 10:
			add(`"command":` + mostly(`"npx"`, `"node"`, `5`, `""`, `null`))
			if rng.Intn(2) == 0 {
				add(`"args":` + mostly(`["-y","x"]`, `[]`, `"x"`, `[1]`, `["a",2]`, `null`))
			}
			if rng.Intn(3) == 0 {
				add(`"env":` + mostly(`{"A":"1"}`, `{"A":1}`, `[]`, `"x"`, `{}`, `null`))
			}
			if rng.Intn(4) == 0 {
				add(`"cwd":` + mostly(`"sub"`, `5`, `""`))
			}
		case 4, 5, 6:
			add(`"url":` + mostly(`"https://example.com/mcp"`, `"http://localhost:3000/mcp"`, `"http://example.com"`, `"ftp://x"`, `"not a url"`, `5`, `""`, `"https://[::1]/x"`, `"http://127.0.0.1:1"`))
			if rng.Intn(3) == 0 {
				add(`"headers":` + mostly(`{"Authorization":"Bearer x"}`, `{"A":1}`, `[]`, `{}`))
			}
			if rng.Intn(4) == 0 {
				add(`"auth":` + mostly(`{"provider":"anthropic"}`, `{"provider":""}`, `{}`, `"x"`, `null`))
			}
			if rng.Intn(5) == 0 {
				add(`"oauth":` + mostly(`{"clientId":"c","scope":"a b"}`, `{"clientId":5}`, `{"callbackUrl":"http://127.0.0.1:1/cb"}`, `{"callbackUrl":"https://x/cb"}`, `{"callbackPort":70000}`, `{"callbackPort":8080,"callbackUrl":"http://localhost:9090/cb"}`, `{"callbackPort":8080,"callbackUrl":"http://localhost:8080/cb"}`, `{"clientName":" "}`, `{"clientRegistration":"cimd"}`, `{"clientRegistration":"x"}`, `{"scope":5}`, `{}`, `"x"`, `{"clientSecret":1}`))
			}
		case 7:
			// An override: no command, url or type.
		case 8:
			return mostly(`{"command":"x"}`, `5`, `"x"`, `null`, `[]`, `true`)
		case 11:
			add(`"command":"x"`)
		default:
			add(`"type":` + pick([]string{`"stdio"`, `"http"`, `"streamable-http"`, `"sse"`, `"bogus"`, `5`}))
			add(`"command":"x"`)
			add(`"url":"https://example.com/mcp"`)
		}
		if rng.Intn(3) == 0 {
			add(`"enabled":` + mostly(`false`, `true`, `"yes"`, `1`))
		}
		if rng.Intn(3) == 0 {
			add(`"exposure":` + mostly(`"direct"`, exposures...))
		}
		if rng.Intn(5) == 0 {
			add(`"toolExposure":` + mostly(`{"a":"direct"}`, `{"a*":"hidden","b":"codemode"}`, `{"a":"bogus"}`, `[]`, `"x"`, `{}`))
		}
		if rng.Intn(5) == 0 {
			add(`"timeout":` + mostly(`30`, `0`, `-1`, `"5"`, `1.5`, `null`))
		}
		if rng.Intn(5) == 0 {
			add(`"description":` + mostly(`"d"`, `5`, `""`))
		}
		if rng.Intn(8) == 0 {
			add(`"extraKey":{"x":1}`)
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	file := func() *string {
		switch rng.Intn(12) {
		case 0:
			return nil
		case 1:
			s := pick([]string{"", "{", "[]", "null", "5", `"s"`, `{"mcpServers":[]}`, `{"mcpServers":5}`, `{"mcpServers":null}`, "{}", `{"autoEnableCodemode":true}`, `{"autoEnableCodemode":"no"}`, "\ufeff{}", `{"a":1,}`})
			return &s
		}
		var servers []string
		for range rng.Intn(4) {
			servers = append(servers, `"`+pick(names)+`":`+value())
		}
		text := `{"mcpServers":{` + strings.Join(servers, ",") + `}`
		if rng.Intn(4) == 0 {
			text += `,"autoEnableCodemode":` + pick([]string{"true", "false", `"x"`, "1"})
		}
		text += "}"
		return &text
	}
	type scopes struct {
		Global  *string `json:"global"`
		Project *string `json:"project"`
		Trusted bool    `json:"trusted"`
	}
	var cases []scopes
	for range 1500 {
		cases = append(cases, scopes{file(), file(), rng.Intn(5) != 0})
	}
	in, _ := json.Marshal(cases)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/mcp_config_load.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want []string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	type server struct {
		Name     string
		Scope    string
		Source   string
		Override string
		Config   map[string]any
	}
	type outcome struct {
		Servers            []server
		AutoEnableCodemode *bool
		Errors             []string
		ProjectConfig      *string
	}
	base := t.TempDir()
	var differences []string
	serversSeen := 0
	for i, c := range cases {
		agentDir, cwd := filepath.Join(base, fmt.Sprint(i), "agent"), filepath.Join(base, fmt.Sprint(i), "work")
		_ = os.MkdirAll(agentDir, 0o755)
		_ = os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755)
		if c.Global != nil {
			_ = os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(*c.Global), 0o644)
		}
		if c.Project != nil {
			_ = os.WriteFile(filepath.Join(cwd, ".pi", "mcp.json"), []byte(*c.Project), 0o644)
		}
		loaded := mcpext.LoadMcpConfig(mcpext.LoadOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: c.Trusted, ConfigDirName: ".pi"})
		prefix := filepath.Join(base, fmt.Sprint(i)) + string(filepath.Separator)
		var got, expected outcome
		for _, s := range loaded.Servers {
			encoded, _ := json.Marshal(s.Config)
			var config map[string]any
			_ = json.Unmarshal(encoded, &config)
			got.Servers = append(got.Servers, server{s.Name, s.Scope, strings.TrimPrefix(s.Source, prefix), strings.TrimPrefix(s.Override, prefix), config})
		}
		got.AutoEnableCodemode = loaded.AutoEnableCodemode
		for _, e := range loaded.Errors {
			got.Errors = append(got.Errors, strings.ReplaceAll(e, prefix, ""))
		}
		if loaded.ProjectConfig != "" {
			rel := strings.TrimPrefix(loaded.ProjectConfig, prefix)
			got.ProjectConfig = &rel
		}
		if err := json.Unmarshal([]byte(want[i]), &expected); err != nil {
			t.Fatal(err)
		}
		// Members PiG's typed server config does not model are not compared.
		for si := range expected.Servers {
			if si < len(got.Servers) {
				projected := map[string]any{}
				for key, v := range expected.Servers[si].Config {
					// PiG's typed config omits an empty string or list (omitempty); Pi keeps it.
					if text, isString := v.(string); isString && text == "" {
						continue
					}
					if list, isList := v.([]any); isList && len(list) == 0 {
						continue
					}
					if _, modeled := got.Servers[si].Config[key]; modeled || mcpConfigComparedKeys[key] {
						projected[key] = v
					}
				}
				expected.Servers[si].Config = projected
			}
		}
		serversSeen += len(expected.Servers)
		if len(got.Servers) == 0 {
			got.Servers = []server{}
		}
		if len(got.Errors) == 0 {
			got.Errors = []string{}
		}
		if !reflect.DeepEqual(got, expected) {
			g, _ := json.Marshal(got)
			w, _ := json.Marshal(expected)
			differences = append(differences, fmt.Sprintf("global %q project %q trusted %v\n  go: %s\n  pi: %s", deref(c.Global), deref(c.Project), c.Trusted, g, w))
		}
	}
	if serversSeen < 800 {
		t.Fatalf("Pi loaded only %d servers", serversSeen)
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 8 {
			shown = shown[:8]
		}
		t.Errorf("%d of %d cases differ from Pi:\n%s", len(differences), len(cases), strings.Join(shown, "\n"))
	}
}

// mcpConfigComparedKeys are the members of a server config that PiG models: if Pi holds one, PiG must hold it too.
var mcpConfigComparedKeys = map[string]bool{
	"type": true, "exposure": true, "description": true, "toolExposure": true, "enabled": true, "timeout": true, "command": true, "args": true, "env": true, "cwd": true,
	"url": true, "headers": true, "auth": true, "oauth": true,
}

func deref(s *string) string {
	if s == nil {
		return "<absent>"
	}
	return *s
}
