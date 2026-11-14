package ai

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// A tool argument named like a member of Object.prototype is validated as any other name: Pi's validator tests own properties (pi-schema uses Object.hasOwn),
// and so does this one. The messages are the ones pinned Pi 1.5.2 gives for the same schemas and arguments.
func TestAToolArgumentNamedLikeAnObjectPrototypeMemberIsValidatedAsAnOwnProperty(t *testing.T) {
	for _, name := range []string{"constructor", "toString", "__proto__", "hasOwnProperty", "valueOf", "nosuchname"} {
		quoted := strconv.Quote(name)
		required := json.RawMessage(`{"type":"object","properties":{` + quoted + `:{"type":"string"}},"required":[` + quoted + `]}`)
		optional := json.RawMessage(`{"type":"object","properties":{` + quoted + `:{"type":"string"}}}`)
		closed := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
		supplied := json.RawMessage(`{` + quoted + `:"v"}`)

		if _, err := ValidateToolArgumentsJSON("t", required, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "\n  - "+name+": missing\n") {
			t.Errorf("required %s omitted: error %v, want the property reported missing", name, err)
		}
		if out, err := ValidateToolArgumentsJSON("t", required, supplied); err != nil || string(out) != string(supplied) {
			t.Errorf("required %s supplied: %s, error %v; want %s", name, out, err, supplied)
		}
		if out, err := ValidateToolArgumentsJSON("t", optional, json.RawMessage(`{}`)); err != nil || string(out) != `{}` {
			t.Errorf("optional %s omitted: %s, error %v; want {}", name, out, err)
		}
		if _, err := ValidateToolArgumentsJSON("t", closed, supplied); err == nil || !strings.Contains(err.Error(), "\n  - "+name+": unknown field\n") {
			t.Errorf("unknown %s supplied to a closed object: error %v, want an unknown field", name, err)
		}
	}
}
