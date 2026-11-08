package codemode

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/test/p2/codemode-plugin.test.ts: the worker facet's extension (the tool and its section), the `codemode`
// setting's switch, and the extension following the plugin across a reload and a removal. Scripts across a reload:
// e2e/codemode_test.go.
func TestRegistersItsExtensionFollowsTheSettingAndThePluginAcrossAReloadAndARemoval(t *testing.T) {
	var settings *p2test.Cell
	// No tool call runs here, so the Harness is never used.
	natives := p2test.WorkerFacet(&p2runtime.HarnessParts{}, nil, nil)
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "codemode"))
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{
		Address: &address, Worker: true, Facets: []host.Facet{natives, Facet()},
		Stubs: []p2test.Stub{{Contract: modelscontract.WorkerModels, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			settings = self.Cell(delta.JsonValue(delta.NewJsonObject(0)))
			return p2test.StubMembers{State: map[string]any{"settings": settings}}
		}}},
	})
	// A registry's value is read inside a section of the host's loop (its source does): its contributions change there.
	extension := func() *p2runtime.P2Extension {
		value, err := registryCurrent(fixture.Host, p2runtime.Extensions.Registry)
		if err != nil {
			t.Logf("registry: %v", err)
			return nil
		}
		table, ok := value.(*p2runtime.KeyedTable[*p2runtime.P2Extension])
		if !ok {
			return nil
		}
		found, _ := table.Get("codemode")
		return found
	}
	switchIs := func(want bool) func() (bool, error) {
		return func() (bool, error) {
			value, err := registryCurrent(fixture.Host, p2runtime.ExtensionSwitches.Registry)
			if err != nil {
				return false, nil
			}
			table, ok := value.(*p2runtime.KeyedTable[*bool])
			if !ok {
				return false, nil
			}
			on, _ := table.Get("codemode")
			return on != nil && *on == want, nil
		}
	}
	wait := func(check func() (bool, error), what string) {
		t.Helper()
		if err := p2test.Poll(check, 0, what); err != nil {
			t.Fatal(err)
		}
	}
	wait(func() (bool, error) { return extension() != nil, nil }, "the extension")
	first := extension()
	// The tool, with `models.*` behind internal tools only scripts call, and the section listing callable tools.
	var got []string
	for _, tool := range first.Tools {
		callers := ""
		for _, caller := range tool.Callers {
			callers += string(caller)
		}
		got = append(got, tool.Name+"/"+string(tool.Replay)+"/"+callers)
	}
	if len(got) != 3 || got[0] != "codemode/"+string(durable.ReplaySafe)+"/model" || got[1] != "pi.codemode.models.classify//tools" || got[2] != "pi.codemode.models.generateImages//tools" {
		t.Fatalf("tools = %v", got)
	}
	if len(first.Extension.Sections) != 1 || first.Extension.Sections[0].Key != "codemode" {
		t.Fatalf("sections = %+v", first.Extension.Sections)
	}
	// The `codemode` setting's switch: on without a setting, off with `false`, the registration untouched.
	wait(switchIs(true), "the switch to be on")
	if err := settings.Replace(delta.JsonObjectOf("codemode", false)); err != nil {
		t.Fatal(err)
	}
	wait(switchIs(false), "the switch to follow the setting")
	if extension() != first {
		t.Error("the setting replaced the registration")
	}
	if err := settings.Replace(delta.NewJsonObject(0)); err != nil {
		t.Fatal(err)
	}
	wait(switchIs(true), "the switch to follow the setting")

	// A reload with a new facet value replaces the extension.
	if err := fixture.ReloadFacets([]host.Facet{natives, Facet().Renew()}); err != nil {
		t.Fatal(err)
	}
	wait(func() (bool, error) { e := extension(); return e != nil && e != first, nil }, "the reloaded extension")
	// Removing the plugin removes the extension; loading it again brings it back.
	if err := fixture.ReloadFacets([]host.Facet{natives}); err != nil {
		t.Fatal(err)
	}
	wait(func() (bool, error) { return extension() == nil, nil }, "the extension to go")
	if err := fixture.ReloadFacets([]host.Facet{natives, Facet()}); err != nil {
		t.Fatal(err)
	}
	wait(func() (bool, error) { return extension() != nil, nil }, "the extension again")
}

// registryCurrent is a registry's value now, read as the host's section gives it (the source reads inside one).
func registryCurrent(h host.Host, registry registries.Registry[any, any]) (any, error) {
	source, err := h.Registry(p2test.Background, registry, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	return source.Current()
}
