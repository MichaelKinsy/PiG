package extension

import (
	"encoding/json"
	"testing"
)

// mcp-servers.ts:151-166 resolveExposureAliases: `{ ...value }` then in-place assignment keeps every member, known or not, in the order written; only the aliases change.
func TestResolveMcpExposureAliasesKeepsMemberOrderAndUnknownMembers(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"exposure and tool entries", `{"zz":1,"toolExposure":{"b":"codemode-deferred","a":"direct"},"foo":{"y":1,"x":2},"exposure":"codemode-deferred","command":"c"}`, `{"zz":1,"toolExposure":{"b":"codemode","a":"direct"},"foo":{"y":1,"x":2},"exposure":"codemode","command":"c"}`},
		{"tool entry only", `{"url":"u","toolExposure":{"*":"codemode-deferred"}}`, `{"url":"u","toolExposure":{"*":"codemode"}}`},
		{"no alias", `{"url":"u","exposure":"direct","toolExposure":{"*":"hidden"}}`, `{"url":"u","exposure":"direct","toolExposure":{"*":"hidden"}}`},
		{"not an object", `["codemode-deferred"]`, `["codemode-deferred"]`},
		{"invalid exposure left for validation", `{"exposure":7,"toolExposure":{"a":null}}`, `{"exposure":7,"toolExposure":{"a":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(resolveMcpExposureAliases(json.RawMessage(tc.in))); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
