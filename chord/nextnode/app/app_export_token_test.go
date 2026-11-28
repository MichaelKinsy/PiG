package app

// pi: packages/chord/src/node/app/data.ts

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// D460 claim 5, the record's one security claim: a plugin object can see the bridged `chord.app.data.export` of its worker but cannot
// call it without the start's token (in Pi a plugin object reaches `hostDataFacet` and is refused by its principal; PiG's token is the
// guard of the Go local behind the JavaScript shim). The probe plugin declares the same local contract as `host_data.mjs` and calls
// `exportTo` with the token it is told to send: the wrong one, none, and (the control) the one the worker wrote into its module.
var snoopPlugin = map[string]string{
	"worker.ts": `import { defineFacet, hostKind, local, method, obj, remote, str } from "@test/app/v1";
const Project = hostKind("project", { id: str() });
const Snoop = remote("test.snoop", { version: 1, params: { id: str() }, hostAddress: ({ id }) => Project({ id }), methods: { probe: method(obj({ mode: str(), token: str(), path: str(), manifest: str(), server: str() }), str()) } });
const HostDataExport = local("chord.app.data.export", { version: 1, params: {}, members: { exportTo: method(obj({ token: str(), path: str(), manifest: str(), server: str() })) } });
export default defineFacet((host) => host.serve(Snoop, (_params, _principal, self) => {
	const exporter = self.get(HostDataExport, {});
	return {
		probe: async ({ mode, token, path, manifest, server }, ctx) => {
			const args = { path, manifest, server };
			if (mode === "token") args.token = token;
			await exporter.exportTo(args, ctx);
			return "exported";
		},
	};
}));`,
}

var snoop = mustRemote("test.snoop", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
		return project.Address(delta.JsonObjectOf("id", params.Value("id"))), nil
	},
	Methods: []contracts.RemoteMethodEntry{contracts.Meth("probe", contracts.NewMethod(
		contracts.Obj(contracts.Prop("mode", contracts.Str()), contracts.Prop("token", contracts.Str()), contracts.Prop("path", contracts.Str()), contracts.Prop("manifest", contracts.Str()), contracts.Prop("server", contracts.Str())),
		contracts.Str(),
	))},
})

func TestAWorkersDataExportLocalRefusesAPluginCallWithAWrongTokenOrNoTokenAndExportsNothing(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin, "snoop": snoopPlugin}, projectOptions(t))
	p1 := holdNotes(t, s, "p1")
	defer p1.Release()
	probe, err := s.app.Host.Get(background, snoop, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Release()
	server := filepath.Join(t.TempDir(), "server.archive")
	if err := os.WriteFile(server, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"format":1,"app":"test","api":"test","address":{"kind":"project","key":"project|p1"}}`
	call := func(mode, token, path string) (delta.JsonValue, error) {
		return probe.Call("probe", delta.JsonObjectOf("mode", mode, "token", token, "path", path, "manifest", manifest, "server", server), background)
	}
	out := t.TempDir()

	// The worker's own token, read from the module it wrote for this start: the control, so that the refusals below are the token's.
	module, err := os.ReadFile(filepath.Join(s.dataDir, "workers", "project", "project%7Cp1", ".host-data.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	found := regexp.MustCompile(`(?m)^const TOKEN = "([0-9a-f]+)";$`).FindSubmatch(module)
	if found == nil {
		t.Fatalf("no token in the module:\n%s", module)
	}
	good := filepath.Join(out, "good.tar")
	if got, err := call("token", string(found[1]), good); err != nil || got != "exported" {
		t.Fatalf("the call with the worker's token = %v, %v", got, err)
	}
	if _, err := os.Stat(good); err != nil {
		t.Fatalf("the call with the worker's token wrote no archive: %v", err)
	}

	// A wrong token (of the right length, and an empty one) is forbidden, and nothing is written.
	for name, token := range map[string]string{"wrong": string(found[1][:len(found[1])-1]) + "x", "empty": ""} {
		path := filepath.Join(out, name+".tar")
		if got, err := call("token", token, path); err == nil {
			t.Fatalf("%s token: exported (%v)", name, got)
		} else if code := refused(t, err).Code; code != data.CodeForbidden {
			t.Fatalf("%s token: code = %s (%v), want forbidden", name, code, err)
		}
		assertNothingExported(t, out, name)
	}

	// No token at all: the local's schema requires one, so the call is refused (the plugin's own host or the Go local, whichever sees it
	// first) and nothing is written.
	if got, err := call("none", "", filepath.Join(out, "none.tar")); err == nil {
		t.Fatalf("no token: exported (%v)", got)
	}
	assertNothingExported(t, out, "none")
}

// assertNothingExported fails the test if out holds an archive of name or a staging directory of a refused export.
func assertNothingExported(t *testing.T, out, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(out, name+".tar")); !os.IsNotExist(err) {
		t.Fatalf("%s: an archive was written (%v)", name, err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "good.tar" {
			t.Fatalf("%s: %s was left in the export directory", name, entry.Name())
		}
	}
}
