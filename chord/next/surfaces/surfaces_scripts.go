package surfaces

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/src/surfaces/scripts.ts. The current plugin scripts of a client kind (slice 11 §1, decision "Slice
// 11, P5"): what the server's surface serves as a remote object every page and terminal holds. Its state is the set: the
// base bundle's hash, the combined file of every plugin script, and each plugin's script (and stylesheet); a rollout
// shipping the kind publishes a new set, and each client fetches what changed.

// ScriptEntry is one plugin's script of a set: its name, its script's digest and URL, its stylesheet's URL when it has one.
type ScriptEntry struct {
	Name   string
	Digest string
	URL    string
	// CSS is the stylesheet's URL (nil: the plugin has none).
	CSS *string
}

// ScriptSet is a client kind's current scripts: Base (the base bundle's hash: a change reloads the client), Combined (the
// URL of one file holding every plugin's script, what a fresh client loads), Plugins (each one's script, in name order).
type ScriptSet struct {
	Base     string
	Combined string
	Plugins  []ScriptEntry
}

var scriptEntrySchema = contracts.Obj(
	contracts.Prop("name", contracts.Str()),
	contracts.Prop("digest", contracts.Str()),
	contracts.Prop("url", contracts.Str()),
	contracts.Prop("css", contracts.Opt(contracts.Str())),
)

// ScriptSetSchema is the schema of a ScriptSet: the state of a scripts object.
var ScriptSetSchema = contracts.Obj(
	contracts.Prop("base", contracts.Str()),
	contracts.Prop("combined", contracts.Str()),
	contracts.Prop("plugins", contracts.Arr(scriptEntrySchema)),
)

// JSON is the set as its schema's value.
func (s ScriptSet) JSON() delta.JsonValue {
	plugins := make([]any, len(s.Plugins))
	for i, entry := range s.Plugins {
		object := delta.JsonObjectOf("name", entry.Name, "digest", entry.Digest, "url", entry.URL)
		if entry.CSS != nil {
			object.Set("css", *entry.CSS)
		}
		plugins[i] = object
	}
	return delta.JsonObjectOf("base", s.Base, "combined", s.Combined, "plugins", plugins)
}

// ScriptSetOf reads a set from its schema's value (a value the scripts object served: it is checked already).
func ScriptSetOf(value delta.JsonValue) ScriptSet {
	object := value.(*delta.JsonObject)
	set := ScriptSet{Base: object.Value("base").(string), Combined: object.Value("combined").(string)}
	for _, each := range object.Value("plugins").([]any) {
		entry := each.(*delta.JsonObject)
		next := ScriptEntry{Name: entry.Value("name").(string), Digest: entry.Value("digest").(string), URL: entry.Value("url").(string)}
		if css, ok := entry.Value("css").(string); ok {
			next.CSS = &css
		}
		set.Plugins = append(set.Plugins, next)
	}
	return set
}

// WebScripts is the current web scripts (ScriptSet), served by the server's web surface to any principal that may load the
// app.
var WebScripts = scriptsContract("chord.web.scripts")

// TuiScripts is the current terminal scripts (ScriptSet), served by the server's terminal surface as WebScripts is.
var TuiScripts = scriptsContract("chord.tui.scripts")

// ScriptsContract is the contract of a client kind's scripts, as clients hold it (WebScripts, TuiScripts): Pi's
// `typeof WebScripts | typeof TuiScripts`; both are a remote contract, whose Go type is *contracts.Contract.
type ScriptsContract = *contracts.Contract

// scriptsContract is the contract of a client kind's current scripts, named name: a remote object on the server, its state
// the set.
func scriptsContract(name string) *contracts.Contract {
	contract, err := contracts.Remote(name, contracts.RemoteDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) {
			return serverKind.Address(nil), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State: []contracts.Property{contracts.Prop("set", ScriptSetSchema)},
		})},
	})
	if err != nil {
		panic(err)
	}
	return contract
}
