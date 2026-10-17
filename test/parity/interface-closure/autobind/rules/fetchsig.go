package rules

import (
	"go/types"
	"strings"
)

// DS2s, a data-shapes signature rule: a WHATWG fetch call `(input: string | URL | Request | RequestInfo, init?: RequestInit) =>
// Promise<Response>` is a Go call that takes one *net/http.Request (after an optional context.Context) and returns
// (*net/http.Response, error). The request carries both upstream arguments: its URL is the input, and its method, headers and body are
// the init members net/http models; its context is init.signal. The Promise is the error result. The rule decides only that exact
// shape; any other parameter or result is left to the other rules.
type fetchSignature struct{}

func (fetchSignature) Name() string { return "DS2s" }

// fetchInputTypes are the WHATWG types a fetch input may be: a URL string, a URL object, a Request, or their RequestInfo union.
var fetchInputTypes = map[string]bool{"string": true, "URL": true, "Request": true, "RequestInfo": true}

func (fetchSignature) Signature(_ Env, up CallShape, sig *types.Signature) (Verdict, bool) {
	if !fetchCall(up) {
		return Verdict{}, false
	}
	params := sig.Params()
	start := 0
	if params.Len() > 0 && isNamed(params.At(0).Type(), "context", "Context") {
		start = 1
	}
	if params.Len()-start != 1 || sig.Variadic() {
		return Refute("DS2s: a fetch call is one *http.Request in Go, got %d parameters", params.Len()-start), true
	}
	if p, ok := params.At(start).Type().(*types.Pointer); !ok || !isNamed(p.Elem(), "net/http", "Request") {
		return Refute("DS2s: a fetch call takes a *http.Request in Go, got %s", params.At(start).Type()), true
	}
	res := sig.Results()
	if res.Len() != 2 {
		return Refute("DS2s: a fetch call returns (*http.Response, error) in Go"), true
	}
	if p, ok := res.At(0).Type().(*types.Pointer); !ok || !isNamed(p.Elem(), "net/http", "Response") || res.At(1).Type().String() != "error" {
		return Refute("DS2s: a fetch call returns (*http.Response, error) in Go, got %s", res), true
	}
	return Accept("DS2s: the fetch input and init are the *http.Request (URL, method, headers, body, context) and the Promise<Response> is (*http.Response, error)"), true
}

// fetchCall reports whether an upstream call is the fetch shape: an input of WHATWG request types, an optional RequestInit, and
// Promise<Response>.
func fetchCall(up CallShape) bool {
	if strings.TrimSpace(up.Returns) != "Promise<Response>" || len(up.Parameters) == 0 || len(up.Parameters) > 2 {
		return false
	}
	in := up.Parameters[0]
	if in.Optional || in.Rest {
		return false
	}
	for part := range strings.SplitSeq(in.Type, "|") {
		if !fetchInputTypes[strings.TrimSpace(part)] {
			return false
		}
	}
	if len(up.Parameters) == 2 {
		init := up.Parameters[1]
		return init.Optional && !init.Rest && strings.TrimSpace(init.Type) == "RequestInit"
	}
	return true
}

func init() { RegisterSignature(DataShapes, fetchSignature{}) }
