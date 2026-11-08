package mcp

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Ports test/p2/mcp-project.test.ts: a project's `.pi/mcp.json` checked against the owner's servers: what it may add (stdio
// servers at its location), what it may change (`enabled` of the owner's servers), and each rule that refuses the rest.

func mustOwner(t *testing.T, text string) OwnerConfig {
	t.Helper()
	config, err := ParseOwnerConfig(text)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func ownerForProjects(t *testing.T) OwnerConfig {
	return mustOwner(t, `{"mcpServers":{"files":{"command":"files-server"},"off":{"command":"off-server","enabled":false},"my-docs":{"url":"https://example.com/mcp"}}}`)
}

func project(owner OwnerConfig, servers string) ProjectConfig {
	return ParseProjectConfig(`{"mcpServers":`+servers+`}`, owner)
}

func TestAProjectAddsStdioServersAndSetsEnabledOfTheOwnersHostVariablesStayAsWritten(t *testing.T) {
	config := project(ownerForProjects(t), `{
		"lint": { "command": "lint-server", "args": ["--fix"], "env": { "TOKEN": "${SECRET}" } },
		"later": { "command": "later-server", "enabled": false },
		"files": { "enabled": false },
		"off": { "enabled": true },
		"my-docs": {}
	}`)
	if len(config.Errors) != 0 {
		t.Fatalf("errors %v", config.Errors)
	}
	if len(config.Servers) != 1 {
		t.Fatalf("servers %v", config.Servers)
	}
	lint := config.Servers[0]
	token, _ := lint.Config.Env.Get("TOKEN")
	if lint.Name != "lint" || lint.Config.Command != "lint-server" || !reflect.DeepEqual(lint.Config.Args, []string{"--fix"}) || token != "${SECRET}" {
		t.Fatalf("lint %+v", lint)
	}
	// An entry without `enabled` is an override that changes nothing.
	if !reflect.DeepEqual(config.Enabled.Names(), []string{"files", "off"}) {
		t.Fatalf("overrides %v", config.Enabled.Names())
	}
	if enabled, _ := config.Enabled.Get("files"); enabled {
		t.Fatal("files must be off")
	}
	if enabled, _ := config.Enabled.Get("off"); !enabled {
		t.Fatal("off must be on")
	}
}

func TestEachRuleRefusesWhatItShouldNamingTheServerAndWhyTheOtherEntriesStillCount(t *testing.T) {
	config := project(ownerForProjects(t), `{
		"remote": { "url": "https://example.com/mcp" },
		"placed": { "command": "x", "runOn": "host" },
		"everywhere": { "command": "x", "shared": true },
		"signed": { "command": "x", "oauth": { "clientId": "c" } },
		"provided": { "command": "x", "auth": { "provider": "radius" } },
		"files": { "command": "other-files" },
		"off": { "enabled": true, "args": ["x"] },
		"my-docs": { "enabled": "yes" },
		"ghost": { "enabled": true },
		"my_docs": { "command": "x" },
		"bad name!": { "command": "x" },
		"kept": { "command": "kept-server" }
	}`)
	if len(config.Servers) != 1 || config.Servers[0].Name != "kept" {
		t.Fatalf("servers %v", config.Servers)
	}
	errors := strings.Join(config.Errors, "\n")
	if len(config.Errors) != 11 {
		t.Fatalf("%d errors:\n%s", len(config.Errors), errors)
	}
	for _, each := range config.Errors {
		if !strings.HasPrefix(each, ".pi/mcp.json: ") {
			t.Errorf("error %q has no prefix", each)
		}
	}
	for _, pattern := range []string{
		`server "remote": HTTP servers are for the owner's mcp\.json`,
		`server "placed": "runOn" is for the owner's mcp\.json`,
		`server "everywhere": "shared" is for the owner's mcp\.json`,
		`server "signed": "oauth" is for the owner's mcp\.json`,
		`server "provided": "auth" is for the owner's mcp\.json`,
		`server "files": the owner's mcp\.json defines it`,
		`server "off": an override can only set "enabled"`,
		`server "my-docs": enabled must be a boolean`,
		`server "ghost": needs "command", or a server of the owner's mcp\.json to override`,
		`server "my_docs": its tools would be named like those of "my-docs"`,
		`invalid server name`,
	} {
		if !regexp.MustCompile(pattern).MatchString(errors) {
			t.Errorf("no error matching %s in:\n%s", pattern, errors)
		}
	}
	if len(config.Enabled.Names()) != 0 {
		t.Fatalf("overrides %v", config.Enabled.Names())
	}
}

func TestAFileThatIsNoConfigurationIsOneErrorAndNothingElse(t *testing.T) {
	owner := ownerForProjects(t)
	for _, each := range []struct{ text, why string }{
		{"{", `Invalid \.pi/mcp\.json syntax`},
		{"[]", `\.pi/mcp\.json must be an object`},
		{`{ "mcpServers": [] }`, `"mcpServers" must be an object`},
	} {
		config := ParseProjectConfig(each.text, owner)
		if len(config.Servers) != 0 || len(config.Enabled.Names()) != 0 || len(config.Errors) != 1 {
			t.Fatalf("%q: %+v", each.text, config)
		}
		if !regexp.MustCompile(each.why).MatchString(config.Errors[0]) {
			t.Errorf("%q: error %q", each.text, config.Errors[0])
		}
	}
	// Comments are allowed, as in the owner's file.
	if errors := ParseProjectConfig("{ // none\n \"mcpServers\": {} }", owner).Errors; len(errors) != 0 {
		t.Fatalf("errors %v", errors)
	}
}
