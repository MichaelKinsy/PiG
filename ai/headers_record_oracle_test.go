package ai

import (
	"math/rand"
	"net/http"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// packages/ai/src/utils/headers.ts:3-9 headersToRecord against Pi's own function fed a WHATWG Headers built with the same appends:
// header names in mixed case, repeated names joined with ", ", several set-cookie entries (the record keeps the last), empty and
// punctuated values, and names that differ only in case. Values carry no leading or trailing space: Headers.append trims them and so does the transport before a response header reaches Go.
func TestHeadersToRecordMatchesPiOracle(t *testing.T) {
	random := rand.New(rand.NewSource(17))
	names := []string{"X-Foo", "x-foo", "X-FOO", "Set-Cookie", "set-cookie", "SET-COOKIE", "Content-Type", "content-type", "Empty", "retry-after", "X-Request-Id", "x_underscore", "X.Dot", "ETag", "a", "Accept"}
	values := []string{"", "a", "b", "text/plain", "a, b", "1", "x=1; Path=/", "y=2; Expires=Wed, 21 Oct 2015 07:28:00 GMT", "é", "a b", "\"q\"", ",", "0"}
	type pair struct{ Name, Value string }
	var inputs [][]pair
	for range 400 {
		pairs := []pair{}
		for range random.Intn(9) {
			pairs = append(pairs, pair{names[random.Intn(len(names))], values[random.Intn(len(values))]})
		}
		inputs = append(inputs, pairs)
	}
	var want []map[string]string
	pioracle.Run(t, `
const { headersToRecord } = await load("pi-ai/utils/headers.js");
emit(input.map((pairs) => {
	const headers = new Headers();
	for (const p of pairs) headers.append(p.Name, p.Value);
	return headersToRecord(headers);
}));`, inputs, &want)
	for i, pairs := range inputs {
		header := http.Header{}
		for _, p := range pairs {
			header.Add(p.Name, p.Value)
		}
		got := headersToRecord(header)
		if len(got) != len(want[i]) {
			t.Fatalf("%v: got %v, Pi %v", pairs, got, want[i])
		}
		for key, value := range want[i] {
			if have, ok := got[key]; !ok || have != value {
				t.Fatalf("%v: %s = %q (present %v), Pi %q", pairs, key, have, ok, value)
			}
		}
	}
}
