package plugins

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Not upstream's (docs/plan/chord-plugins-node-evaluator.md 10.3, 10.4): the JSON form of a value on the control channel. Bytes
// are `{"$bytes": base64}` and no plain datum can be mistaken for them: a key that starts with `$` gets one more `$`.

func TestWireValuesRoundTripBytesAndDollarKeys(t *testing.T) {
	value := delta.JsonObjectOf(
		"blob", []byte{0, 1, 255},
		"$bytes", "not bytes",
		"$$twice", []any{[]byte{}, delta.JsonObjectOf("$bytes", "x", "$", 1.0)},
		"plain", "text",
	)
	encoded := wireEncode(value).(*delta.JsonObject)
	if got := encoded.Keys(); !reflect.DeepEqual(got, []string{"blob", "$$bytes", "$$$twice", "plain"}) {
		t.Fatalf("encoded keys = %v", got)
	}
	raw, err := marshalWire(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := unmarshalWire(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, value) {
		t.Fatalf("round trip = %#v, want %#v", decoded, value)
	}
}

// Bytes and `$` keys cross both ways. Go to Node: a keyed member's call and a state member's value and watch carry bytes, a
// value with `$` keys comes back as it went; Node to Go is the same results read here (the Node host encodes them).
func TestNodeGetBytesAndDollarKeysCrossBothWays(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	if got := rfRead(t, ref, "bin"); !reflect.DeepEqual(got, []byte{1, 2, 3}) {
		t.Fatalf("bin = %#v, want the bytes 1 2 3", got)
	}
	if got, err := ref.Call("echoBytes", []byte{9, 8, 7, 0}, background); err != nil || !reflect.DeepEqual(got, []byte{9, 8, 7, 0}) {
		t.Fatalf("echoBytes = %#v, %v", got, err)
	}
	plain := delta.JsonObjectOf("$bytes", "just text", "$k", []any{[]byte{5}, delta.JsonObjectOf("$bytes", "x", "$", 2.0)})
	if got, err := ref.Call("echoAny", plain, background); err != nil || !reflect.DeepEqual(got, plain) {
		t.Fatalf("echoAny = %#v, %v, want %#v", got, err, plain)
	}
	watch, err := ref.Member("bin").Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	if got := watch.Value(); !reflect.DeepEqual(got, []byte{1, 2, 3}) {
		t.Fatalf("the watch starts at %#v", got)
	}
}

// A keyed member's key is pinned on the Node host: absent documents give nil, a present one a pin whose Get reads it and
// whose release is idempotent.
func TestNodeGetPinsAKeyOfAKeyedMember(t *testing.T) {
	ref := rfGet(t, rfNode(t))
	notes := ref.Keyed("notes")
	if pin, err := notes.Pin(background, "a"); err != nil || pin != nil {
		t.Fatalf("a pin of an absent note = %v, %v", pin, err)
	}
	if _, err := ref.Call("setNote", delta.JsonObjectOf("key", "a", "title", "one"), background); err != nil {
		t.Fatal(err)
	}
	pin, err := notes.Pin(background, "a")
	if err != nil || pin == nil {
		t.Fatalf("pin = %v, %v", pin, err)
	}
	if value, found, err := pin.Get(background); err != nil || !found || !reflect.DeepEqual(value, delta.JsonObjectOf("title", "one")) {
		t.Fatalf("pinned note = %v, %v, %v", value, found, err)
	}
	pin.Release()
	pin.Release()
	if _, err := ref.Member("count").Value(); err != nil {
		t.Fatal(err)
	}
	_ = host.GetOptions{}
}

// Pi's BuiltFacet: a failed build has its `errors` array (there when it is empty too), a script has none.
func TestWireABuiltFacetHasErrorsOnlyWhenItFailedAndThenAlways(t *testing.T) {
	for _, each := range []struct {
		name  string
		built model.BuiltFacet
		want  string
	}{
		{"failed without errors", model.BuiltFacet{Kind: model.BuiltFacetFailed, Plugin: "@a/b", Facet: "worker"}, `{"kind":"failed","plugin":"@a/b","facet":"worker","errors":[]}`},
		{"failed", model.BuiltFacet{Kind: model.BuiltFacetFailed, Plugin: "@a/b", Facet: "worker", Errors: []string{"x"}}, `{"kind":"failed","plugin":"@a/b","facet":"worker","errors":["x"]}`},
		{"script", model.BuiltFacet{Kind: model.BuiltFacetScript, Plugin: "@a/b", Facet: "worker", Source: "s", Digest: "d"}, `{"kind":"script","plugin":"@a/b","facet":"worker","source":"s","digest":"d"}`},
	} {
		got, err := json.Marshal(scriptsWire([]PluginScript{{Built: each.built}})[0].Built)
		if err != nil || string(got) != each.want {
			t.Errorf("%s: %s, %v; want %s", each.name, got, err, each.want)
		}
	}
}

// The proxy script is JavaScript source: a facet named with <, > or & is written as JSON.stringify writes it, not as \u003c.
func TestBridgedProxyScriptWritesHTMLCharactersAsJSONStringifyDoes(t *testing.T) {
	if got := quoteJS("a<b>&c"); got != `"a<b>&c"` {
		t.Fatalf("quoteJS = %s", got)
	}
	scripts, err := bridgedScripts("worker", []BridgedFacet{{Name: "core<&>"}})
	if err != nil || len(scripts) != 1 {
		t.Fatalf("bridgedScripts = %v, %v", scripts, err)
	}
	if source := scripts[0].Built.Source; !strings.Contains(source, `"name":"core<&>"`) || strings.Contains(source, `\u003c`) {
		t.Fatalf("source = %s", source)
	}
}
