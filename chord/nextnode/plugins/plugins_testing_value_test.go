package plugins

// pi: packages/chord/src/node/plugins/testing.ts

import (
	"strings"
	"testing"
)

// D503: a contribution is data across the process boundary (Pi's `contributions` returns the live values, functions included).
// What `Contributions` hands the caller for a value that is not data is whatever JSON carries of it between the Node subprocess and
// Go: JSON.stringify drops a function member of an object and writes null for one in an array, and a function that is the whole value
// leaves the entry without `value`.
func TestTestPluginContributionsOfAValueThatIsNotData(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "fn", "@acme/fn", map[string]string{"worker.ts": strings.Join([]string{
		`import { defineFacet, defineRegistry } from "@acme/app/v3";`,
		`const Whole = defineRegistry("acme.whole", { version: 1 });`,
		`const Member = defineRegistry("acme.member", { version: 1 });`,
		`const Item = defineRegistry("acme.item", { version: 1 });`,
		"export default defineFacet((_host, self) => {",
		"	self.contribute(Whole, () => 1);",
		`	self.contribute(Member, { title: "x", render: () => 1 });`,
		`	self.contribute(Item, ["a", () => 1]);`,
		"});",
	}, "\n")}, nil)
	tested := tpPlugin(t, dir, tpOpts(t, t.TempDir()))
	// A function that is a member of an object is dropped; the rest of the object arrives.
	member, err := tested.Contributions("acme.member", background)
	if err != nil || len(member) != 1 || member[0].Plugin != "@acme/fn" || tpJSON(t, member[0].Value) != `{"title":"x"}` {
		t.Fatalf("acme.member = %+v, %v, want one entry holding {\"title\":\"x\"}", member, err)
	}
	// A function in an array is null.
	item, err := tested.Contributions("acme.item", background)
	if err != nil || len(item) != 1 || tpJSON(t, item[0].Value) != `["a",null]` {
		t.Fatalf("acme.item = %+v, %v, want one entry holding [\"a\",null]", item, err)
	}
	// A function that is the whole value leaves its entry without a value: the call fails, with no table, and with the message of
	// the JSON decoder (a registry that holds such an entry cannot be read through Contributions at all).
	whole, err := tested.Contributions("acme.whole", background)
	if err == nil || whole != nil || err.Error() != "unexpected end of JSON input" {
		t.Fatalf("acme.whole = %+v, %v, want no table and the error %q", whole, err, "unexpected end of JSON input")
	}
}
