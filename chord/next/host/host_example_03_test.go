package host

// pi: packages/chord/test/next/examples/03-derives.ts

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// --- 03-derives.ts -------------------------------------------------------------------------------------------------

func TestExample03Derives(t *testing.T) {
	notes := exLocal("example.notes", contracts.Params{},
		contracts.Mem("titles", exArr(exStr())), contracts.Mem("count", exInt()),
		contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr())))))
	badge := exLocal("example.badge", contracts.Params{exProp("user", exStr())}, contracts.Mem("label", exStr()))
	var computed exCollector
	host, _ := exOpenHost(t, loadedOptions{}, func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, exNotesDoc)
			// mapShared calls its function once per note object.
			title := state.MapShared(func(note any) any {
				text := note.(*delta.JsonObject).Value("text").(string)
				computed.add("title computed for: " + text)
				if len(text) > 12 {
					text = text[:12]
				}
				return text
			})
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"titles": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					list := values[0].(*delta.JsonObject).Value("list").([]any)
					titles := title(list)
					out := make([]any, len(titles))
					copy(out, titles)
					return out, nil
				}),
				"count": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
					return float64(len(values[0].(*delta.JsonObject).Value("list").([]any))), nil
				}),
			}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { exPush(draft, jsonObject("text", args.Value("text"))) }, ctx)
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		// A derive over another object's member, through a reference declared with self.get.
		return host.Serve(badge, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			user := exParam(params, "user")
			count := self.Get(notes, jsonObject())
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"label": self.Derive([]any{count.Member("count")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("%s: %d notes", user, int(values[0].(float64))), nil
				}),
			}, nil), nil
		}, ServeOptions{})
	})
	notesRef := exGet(t, host, notes, jsonObject())
	badgeRef := exGet(t, host, badge, jsonObject("user", "mario"))
	exCall(t, notesRef, "add", jsonObject("text", "buy milk and bread"))
	exCall(t, notesRef, "add", jsonObject("text", "call Anna"))
	// Nobody watches `titles`, so the commits only marked it; reading computes it, each title once.
	exEqual(t, "titles", exValue(t, notesRef, "titles"), exJSON(t, `["buy milk and","call Anna"]`))
	exCall(t, notesRef, "add", jsonObject("text", "water plants"))
	exEqual(t, "titles", exValue(t, notesRef, "titles"), exJSON(t, `["buy milk and","call Anna","water plants"]`))
	exEqual(t, "badge", exValue(t, badgeRef, "label"), "mario: 3 notes")
	want := []string{"title computed for: buy milk and bread", "title computed for: call Anna", "title computed for: water plants"}
	if got := computed.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("title computations = %v, want %v", got, want)
	}
	exCloseHost(t, host)
}
