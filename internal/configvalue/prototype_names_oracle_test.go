package configvalue_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the config value row. Pi's resolveEnvConfigValue reads `process.env[name]` (packages/coding-agent/src/core/resolve-config-value.ts:89), and
// process.env inherits the members of Object.prototype, so a reference such as `$constructor` or `$toString` in an API key or a header resolves to the
// text of a native function and `$__proto__` to `[object Object]`, and the value counts as configured. PiG asks the environment for the variable: a
// variable that is not set is not set, whatever its name. PiG's three answers for such a name must be its answers for the unset ordinary name.
func TestAConfigValueNamingAnObjectPrototypeMemberIsAnUnsetVariable(t *testing.T) {
	type answer struct {
		Value      *string           `json:"value"`
		Configured bool              `json:"configured"`
		Headers    map[string]string `json:"headers"`
	}
	const ordinary = "PIG_D645_NO_SUCH_VARIABLE"
	names := []string{"constructor", "toString", "valueOf", "__proto__", ordinary}
	var pi []answer
	pioracle.Run(t, `
const { resolveConfigValueUncached, isConfigValueConfigured, resolveHeaders } = await load("pi-coding-agent/core/resolve-config-value.js");
emit(input.map((name) => ({ value: resolveConfigValueUncached("$" + name) ?? null, configured: isConfigValueConfigured("$" + name), headers: resolveHeaders({ "x-a": "$" + name }) ?? null })));`, names, &pi)
	if len(pi) != len(names) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(names))
	}
	pig := func(name string) (string, bool, map[string]string) {
		return configvalue.ResolveUncached("$"+name, nil), configvalue.IsConfigValueConfigured("$"+name, nil), configvalue.ResolveHeaders(map[string]string{"x-a": "$" + name}, nil)
	}
	wantValue, wantConfigured, wantHeaders := pig(ordinary)
	if last := pi[len(pi)-1]; last.Value != nil || last.Configured || len(last.Headers) != 0 || wantValue != "" || wantConfigured || len(wantHeaders) != 0 {
		t.Errorf("the unset ordinary variable: Pi %+v, PiG %q %v %v, want unset on both sides", last, wantValue, wantConfigured, wantHeaders)
	}
	for i, name := range names[:len(names)-1] {
		value, configured, headers := pig(name)
		piValue := "undefined"
		if pi[i].Value != nil {
			piValue = *pi[i].Value
		}
		t.Logf("$%s: Pi %q configured %v headers %v; PiG %q configured %v headers %v", name, piValue, pi[i].Configured, pi[i].Headers, value, configured, headers)
		native := strings.Contains(piValue, "[native code]")
		if name == "__proto__" {
			native = piValue == "[object Object]"
		}
		if !native || !pi[i].Configured || pi[i].Headers["x-a"] != piValue {
			t.Errorf("$%s: Pi answers %q configured %v headers %v, the record says the inherited member's text, configured, and sent as the header", name, piValue, pi[i].Configured, pi[i].Headers)
		}
		if value != wantValue || configured != wantConfigured || !reflect.DeepEqual(headers, wantHeaders) {
			t.Errorf("$%s: PiG answers %q configured %v headers %v, want what it answers for an unset variable: %q %v %v", name, value, configured, headers, wantValue, wantConfigured, wantHeaders)
		}
	}
}
