package views

import (
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the `checks an agent's arrangement against the session's views` case of test/p2/views-layout.test.ts (Pi main da57c166),
// with its inputs; the other cases are the browser's layout.ts, run unchanged by web/node_tests_test.go.
func TestArrangementErrorChecksAnAgentsArrangementAgainstTheSessionsViews(t *testing.T) {
	known := map[string]bool{contract.Transcript: true, "plan": true, "glyph": true, "notes": true}
	parse := func(text string) any {
		value, err := chordjson.Decode([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := ArrangementError(parse(`{"row":["transcript",{"column":["plan",{"tabs":["glyph","notes"],"active":"notes"}]}],"sizes":[1,2]}`), known); got != "" {
		t.Errorf("the good arrangement: %q", got)
	}
	for _, check := range []struct{ arrangement, want string }{
		{`{"row":["transcript","missing"]}`, `no view "missing"`},
		{`{"row":["plan",{"tabs":["plan"]}]}`, `twice`},
		{`{"row":["transcript","plan"],"sizes":[1]}`, `sizes`},
		{`{"tabs":["plan"],"active":"glyph"}`, `active`},
	} {
		if got := ArrangementError(parse(check.arrangement), known); !regexp.MustCompile(check.want).MatchString(got) {
			t.Errorf("%s: %q does not match %s", check.arrangement, got, check.want)
		}
	}
}
