package ai

import (
	"maps"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

var oracleHeaderNames = []string{"X-Foo", "x-foo", "X-FOO", "Set-Cookie", "set-cookie", "SET-COOKIE", "Content-Type", "content-type", "Authorization", "authorization", "Empty", "x-api-key", "X-Api-Key", "Accept", "ACCEPT"}

var oracleHeaderValues = []string{"", "a", "b", "text/plain", "a, b", "Bearer t", "x y", "c1=1; Path=/", "c2=2", "é", "1", "0"}

type headersOracleCase struct {
	Appends [][2]string `json:"appends"`
}

type providerHeadersOracleCase struct {
	Sources [][][2]*string `json:"sources"`
}

// utils/headers.ts headersToRecord is `for (const [key, value] of headers.entries()) result[key] = value`: Headers.entries lists lowercase names in sorted order, joins repeated values with ", " and
// lists every set-cookie value separately, so the record keeps the last one. PiG's header map comes from a response, so its names are canonical and its values have no outer whitespace
// (Headers strips it on append), which is the same input domain the oracle draws from.
func TestHeadersToRecordMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 29))
	cases := make([]headersOracleCase, 0, 800)
	cases = append(cases, headersOracleCase{})
	for len(cases) < cap(cases) {
		var c headersOracleCase
		for range rng.IntN(9) {
			c.Appends = append(c.Appends, [2]string{oracleHeaderNames[rng.IntN(len(oracleHeaderNames))], oracleHeaderValues[rng.IntN(len(oracleHeaderValues))]})
		}
		cases = append(cases, c)
	}
	var want []map[string]string
	pioracle.Run(t, `
const { headersToRecord } = await load("pi-ai/utils/headers.js");
emit(input.map((c) => {
	const headers = new Headers();
	for (const [name, value] of c.appends ?? []) headers.append(name, value);
	return headersToRecord(headers);
}));`, cases, &want)
	for i, c := range cases {
		header := http.Header{}
		for _, a := range c.Appends {
			header.Add(a[0], a[1])
		}
		if got := headersToRecord(header); !maps.Equal(got, want[i]) {
			t.Fatalf("case %d %q\n  PiG %v\n  Pi  %v", i, c.Appends, got, want[i])
		}
	}
}

// utils/headers.ts providerHeadersToRecord: later sources replace earlier ones case-insensitively and keep the later spelling, a null value deletes the name, and no header left is undefined.
// Within one source PiG's map has no insertion order, so a source never holds two spellings of one name here (Pi lets the later key win, PiG cannot say which is later).
func TestProviderHeadersToRecordMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 31))
	cases := make([]providerHeadersOracleCase, 0, 1500)
	cases = append(cases, providerHeadersOracleCase{}, providerHeadersOracleCase{Sources: [][][2]*string{{}, {}}})
	for len(cases) < cap(cases) {
		var c providerHeadersOracleCase
		for range 1 + rng.IntN(4) {
			var source [][2]*string
			seen := map[string]bool{}
			for range rng.IntN(5) {
				name := oracleHeaderNames[rng.IntN(len(oracleHeaderNames))]
				if seen[strings.ToLower(name)] {
					continue
				}
				seen[strings.ToLower(name)] = true
				entry := [2]*string{&name, nil}
				if rng.IntN(4) != 0 {
					value := oracleHeaderValues[rng.IntN(len(oracleHeaderValues))]
					entry[1] = &value
				}
				source = append(source, entry)
			}
			c.Sources = append(c.Sources, source)
		}
		cases = append(cases, c)
	}
	var want []map[string]string
	pioracle.Run(t, `
const { providerHeadersToRecord } = await load("pi-ai/utils/headers.js");
emit(input.map((c) => providerHeadersToRecord(...(c.sources ?? []).map((entries) => Object.fromEntries(entries ?? []))) ?? null));`, cases, &want)
	for i, c := range cases {
		sources := make([]ProviderHeaders, 0, len(c.Sources))
		for _, entries := range c.Sources {
			source := ProviderHeaders{}
			for _, entry := range entries {
				source[*entry[0]] = entry[1]
			}
			sources = append(sources, source)
		}
		got := providerHeadersToRecord(sources...)
		if (got == nil) != (want[i] == nil) || !maps.Equal(got, want[i]) {
			t.Fatalf("case %d sources %v\n  PiG %v\n  Pi  %v", i, c.Sources, got, want[i])
		}
	}
}
