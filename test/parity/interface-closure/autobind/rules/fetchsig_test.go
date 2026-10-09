package rules

import (
	"go/types"
	"testing"
)

const fetchSigSrc = `
import (
	"context"
	"net/http"
)

type Fetch func(*http.Request) (*http.Response, error)
type FetchCtx func(context.Context, *http.Request) (*http.Response, error)
type TwoArgs func(url string, req *http.Request) (*http.Response, error)
type ValueRequest func(http.Request) (*http.Response, error)
type NoError func(*http.Request) *http.Response
type OtherResult func(*http.Request) (*http.Request, error)
type ExtraParam func(*http.Request, int) (*http.Response, error)
type NotAnError func(*http.Request) (*http.Response, string)
`

// TestFetchSignature: DS2s accepts a fetch call as one *http.Request returning (*http.Response, error), refutes a fetch call with any
// other Go shape, and stays silent on a call that is not fetch-shaped.
func TestFetchSignature(t *testing.T) {
	l := ouLoad(t, fetchSigSrc)
	sig := func(name string) *types.Signature { return l.typ(t, name).Underlying().(*types.Signature) }
	fetch := func(input string, init bool) CallShape {
		c := CallShape{Parameters: []Param{{Name: "input", Type: input}}, Returns: "Promise<Response>"}
		if init {
			c.Parameters = append(c.Parameters, Param{Name: "init", Type: "RequestInit", Optional: true})
		}
		return c
	}
	cases := []struct {
		name string
		up   CallShape
		goT  string
		want Tri
		ok   bool
	}{
		{"input and init", fetch("RequestInfo | URL", true), "Fetch", Yes, true},
		{"string, Request or URL", fetch("string | Request | URL", true), "Fetch", Yes, true},
		{"a leading context", fetch("string | URL", true), "FetchCtx", Yes, true},
		{"input only", fetch("string", false), "Fetch", Yes, true},
		{"Go takes two parameters", fetch("string | URL", true), "TwoArgs", No, true},
		{"Go takes a request value", fetch("string | URL", true), "ValueRequest", No, true},
		{"Go returns no error", fetch("string | URL", true), "NoError", No, true},
		{"Go returns another type", fetch("string | URL", true), "OtherResult", No, true},
		{"Go takes an extra parameter", fetch("string | URL", true), "ExtraParam", No, true},
		{"Go's second result is not an error", fetch("string | URL", true), "NotAnError", No, true},
		{"input of another type", fetch("number", true), "Fetch", Unknown, false},
		{"init of another type", CallShape{Parameters: []Param{{Name: "input", Type: "string"}, {Name: "init", Type: "Options", Optional: true}}, Returns: "Promise<Response>"}, "Fetch", Unknown, false},
		{"required init", CallShape{Parameters: []Param{{Name: "input", Type: "string"}, {Name: "init", Type: "RequestInit"}}, Returns: "Promise<Response>"}, "Fetch", Unknown, false},
		{"another result", CallShape{Parameters: []Param{{Name: "input", Type: "string"}}, Returns: "Promise<string>"}, "Fetch", Unknown, false},
	}
	for _, c := range cases {
		v, ok := fetchSignature{}.Signature(nil, c.up, sig(c.goT))
		if ok != c.ok || ok && v.OK != c.want {
			t.Errorf("%s: got %v %v (%s), want %v %v", c.name, ok, v.OK, v.Why, c.ok, c.want)
		}
	}
}
