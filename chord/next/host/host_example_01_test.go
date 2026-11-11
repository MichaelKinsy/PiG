package host

// pi: packages/chord/test/next/examples/01-objects.ts

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// --- 01-objects.ts -------------------------------------------------------------------------------------------------

func TestExample01Objects(t *testing.T) {
	greeter := exLocal("example.greeter", contracts.Params{exProp("language", exStr())},
		contracts.Mem("language", exStr()),
		contracts.Mem("greet", contracts.NewMethod(exObj(exProp("name", exStr())), exStr())))
	clock := exLocal("example.clock", contracts.Params{}, contracts.Mem("started", exInt()))
	var created exCollector
	host, _ := exOpenHost(t, loadedOptions{}, func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(clock, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			created.add("clock: created at load")
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"started": exConstant(float64(time.Now().UnixMilli()))}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(greeter, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			language := exParam(params, "language")
			created.add("greeter " + language + ": created by its first get")
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"language": exConstant(language)}, map[string]contracts.MethodFunction{
				"greet": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					if language == "de" {
						return "Hallo " + exParam(args, "name"), nil
					}
					return "Hello " + exParam(args, "name"), nil
				}),
			}), nil
		}, ServeOptions{})
	})
	if got := created.all(); !reflect.DeepEqual(got, []string{"clock: created at load"}) {
		t.Fatalf("after load: %v", got)
	}
	german := exGet(t, host, greeter, jsonObject("language", "de"))
	exEqual(t, "language", exValue(t, german, "language"), "de")
	exEqual(t, "greet", exCall(t, german, "greet", jsonObject("name", "Mario")), "Hallo Mario")
	if got := created.all(); !reflect.DeepEqual(got, []string{"clock: created at load", "greeter de: created by its first get"}) {
		t.Fatalf("created: %v", got)
	}
	// Arguments are checked against the contract at the object.
	_, err := german.Call("greet", jsonObject("name", float64(42)), background)
	refused := exCode(t, err, data.CodeInvalid, data.ReasonArgs)
	if refused.Message == "" {
		t.Fatal("refused: empty message")
	}
	// The same params share one object; releasing the last reference ends it.
	again := exGet(t, host, greeter, jsonObject("language", "de"))
	if len(created.all()) != 2 {
		t.Fatalf("the same params made a second object: %v", created.all())
	}
	german.Release()
	again.Release()
	_, err = german.Member("language").Watch(background)
	_ = exCode(t, err, data.CodeReleased, "")
	exCloseHost(t, host)
}
