package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi's reload() discovers the extensions again (resource-loader.ts DefaultResourceLoader.reload), in RPC mode as in the others: a command that removes, adds or edits an extension file and awaits ctx.reload() leaves the extensions that are on disk loaded, as they now are. RPC mode once reloaded the set it started with.

// membershipFixture is an agent directory whose extensions folder holds the caller, whose command act-and-reload changes the sibling's file and reloads, and, for "remove" and "change", the sibling. The caller's command goes on to write the marker once the reload has returned.
type membershipFixture struct {
	home, marker string
}

func (f membershipFixture) env() []string {
	return []string{
		"HOME=" + f.home, "USERPROFILE=" + f.home, "PIG_HOME=" + f.home, "PIG_CODING_AGENT_DIR=" + filepath.Join(f.home, "agent"), "PI_CODING_AGENT_DIR=",
		"PIG_TEST_FAUX=1", "PIG_OFFLINE=1",
	}
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// nodeSiblingSource is a Node extension with one command.
func nodeSiblingSource(command string) string {
	return fmt.Sprintf("export default function (pi) {\n  pi.registerCommand(%q, {description: \"sibling\", handler: async () => {}});\n}\n", command)
}

// nodeMembershipFixture writes Node extensions: the caller, and the sibling unless action is "add". The action is what the caller's command does to the sibling's file before it reloads: "remove" it, "add" it (the command the new extension registers is sibling-hello) or "change" it (sibling-hello becomes sibling-changed).
func nodeMembershipFixture(t *testing.T, action string) membershipFixture {
	t.Helper()
	f := membershipFixture{home: t.TempDir()}
	f.marker = filepath.Join(f.home, "resolved")
	extensions := filepath.Join(f.home, "agent", "extensions")
	sibling := filepath.Join(extensions, "sibling.js")
	var act string
	switch action {
	case "remove":
		act = fmt.Sprintf("rmSync(%q, {force: true, recursive: true});", sibling)
	case "add":
		act = fmt.Sprintf("writeFileSync(%q, %q);", sibling, nodeSiblingSource("sibling-hello"))
	case "change":
		act = fmt.Sprintf("writeFileSync(%q, %q);", sibling, nodeSiblingSource("sibling-changed"))
	default:
		t.Fatalf("unknown action %q", action)
	}
	writeFixtureFile(t, filepath.Join(extensions, "caller.js"), fmt.Sprintf(`import {rmSync, writeFileSync} from "node:fs";
export default function (pi) {
  pi.registerCommand("act-and-reload", {description: "change the sibling and reload", handler: async (_args, ctx) => {
    %s
    await ctx.reload();
    writeFileSync(%q, "resolved");
  }});
  pi.registerCommand("ping", {description: "ping", handler: async (_args, ctx) => { ctx.ui.notify("ping", "info"); }});
}
`, act, f.marker))
	if action != "add" {
		writeFixtureFile(t, sibling, nodeSiblingSource("sibling-hello"))
	}
	return f
}

// goMembershipFixture writes two Go extensions the host packs into one cell: the caller, whose command removes the sibling and reloads, and the sibling.
func goMembershipFixture(t *testing.T) membershipFixture {
	t.Helper()
	f := membershipFixture{home: t.TempDir()}
	f.marker = filepath.Join(f.home, "resolved")
	extensions := filepath.Join(f.home, "agent", "extensions")
	sibling := filepath.Join(extensions, "sibling")
	module := func(name string) string {
		return fmt.Sprintf("module example.com/%s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.3.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", name, filepath.Join(fixtureSourceRoot, "extensions", "sdk"))
	}
	writeFixtureFile(t, filepath.Join(extensions, "caller", "go.mod"), module("caller"))
	writeFixtureFile(t, filepath.Join(extensions, "caller", "extension.go"), fmt.Sprintf(`package caller

import (
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New("caller")
	e.Command("act-and-reload", "remove the sibling and reload", func(ctx sdk.Context, _ string) error {
		if err := os.RemoveAll(%q); err != nil {
			return err
		}
		if err := ctx.Reload(); err != nil {
			return err
		}
		return os.WriteFile(%q, []byte("resolved"), 0o600)
	})
	e.Command("ping", "ping", func(ctx sdk.Context, _ string) error {
		ctx.Notify("ping", "info")
		return nil
	})
	return e
}
`, sibling, f.marker))
	writeFixtureFile(t, filepath.Join(sibling, "go.mod"), module("sibling"))
	writeFixtureFile(t, filepath.Join(sibling, "extension.go"), `package sibling

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New("sibling")
	e.Command("sibling-hello", "hello", func(sdk.Context, string) error { return nil })
	return e
}
`)
	return f
}

func (f membershipFixture) start(t *testing.T) *rpcProcess {
	t.Helper()
	return startRPCProcessAt(t, t.TempDir(), f.env(), "--no-session", "--model", "test-faux/faux-1")
}

// fixtureCommands asks get_commands and returns the sorted names of the commands whose source lies in the fixture's agent directory: not the built-in extensions'.
func (f membershipFixture) commands(p *rpcProcess, id string) []string {
	p.t.Helper()
	p.send(fmt.Sprintf(`{"id":%q,"type":"get_commands"}`, id))
	var names []string
	p.await("the get_commands response "+id, func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != id {
			return false
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		for _, entry := range commands {
			command, _ := entry.(map[string]any)
			info, _ := command["sourceInfo"].(map[string]any)
			if path, _ := info["path"].(string); strings.HasPrefix(path, f.home) {
				names = append(names, fmt.Sprint(command["name"]))
			}
		}
		return true
	})
	slices.Sort(names)
	return names
}

// reload runs the caller's command and waits for its response.
func (f membershipFixture) reload(p *rpcProcess) {
	p.t.Helper()
	p.send(`{"id":"act","type":"prompt","message":"/act-and-reload"}`)
	p.await("the response of the command that awaited ctx.reload()", func(record rpcRecord) bool {
		return record["type"] == "response" && record["id"] == "act"
	})
}

// ping runs the caller's ping command and requires its notification.
func ping(p *rpcProcess) {
	p.t.Helper()
	p.send(`{"id":"ping","type":"prompt","message":"/ping"}`)
	var notified bool
	p.await("the reloaded caller's command", func(record rpcRecord) bool {
		if record["type"] == "extension_ui_request" && record["method"] == "notify" && record["message"] == "ping" {
			notified = true
		}
		return record["type"] == "response" && record["id"] == "ping"
	})
	if !notified {
		p.t.Fatal("the reloaded caller's command did not run")
	}
}

func requireCommands(t *testing.T, f membershipFixture, p *rpcProcess, id string, want ...string) {
	t.Helper()
	if got := f.commands(p, id); !slices.Equal(got, want) {
		t.Fatalf("extension commands %s = %q, want %q\n%s", id, got, want, p.stderr.String())
	}
}

// A reload loads the extensions that are on disk. RPC mode loaded the set it started with, so the removed sibling stayed configured and, in a Node cell, failed the load of the caller beside it.
func TestRPCReloadDropsAnExtensionRemovedFromDisk(t *testing.T) {
	for name, fixture := range map[string]func(*testing.T) membershipFixture{
		"node": func(t *testing.T) membershipFixture { return nodeMembershipFixture(t, "remove") },
		"go":   goMembershipFixture,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := fixture(t)
			p := f.start(t)
			requireCommands(t, f, p, "before", "act-and-reload", "ping", "sibling-hello")
			f.reload(p)
			requireCommands(t, f, p, "after", "act-and-reload", "ping")
			ping(p)
			p.closeAndWait("the reload settled")
		})
	}
}

// The other way: an extension file that appears before the reload is loaded by it.
func TestRPCReloadLoadsAnExtensionAddedToDisk(t *testing.T) {
	f := nodeMembershipFixture(t, "add")
	p := f.start(t)
	requireCommands(t, f, p, "before", "act-and-reload", "ping")
	f.reload(p)
	requireCommands(t, f, p, "after", "act-and-reload", "ping", "sibling-hello")
	p.closeAndWait("the reload settled")
}

// An extension file that changes before the reload is loaded as it now is.
func TestRPCReloadLoadsAnExtensionChangedOnDisk(t *testing.T) {
	f := nodeMembershipFixture(t, "change")
	p := f.start(t)
	requireCommands(t, f, p, "before", "act-and-reload", "ping", "sibling-hello")
	f.reload(p)
	requireCommands(t, f, p, "after", "act-and-reload", "ping", "sibling-changed")
	p.closeAndWait("the reload settled")
}
