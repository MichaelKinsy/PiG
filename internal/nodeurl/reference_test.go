package nodeurl

import (
	"net/url"
	"reflect"
	"testing"
)

// Expected values are Node 24 `new URL(input, base)` origin, pathname and `[...searchParams]`; "" origin means it throws.
func TestResolveHTTPURLMatchesNodeURL(t *testing.T) {
	const local, redirect = "http://localhost", "http://localhost:1455/auth/callback"
	for _, tc := range []struct {
		input, base, origin, pathname string
		params                        [][2]string
	}{
		{"/callback?code=x", local, "http://localhost", "/callback", [][2]string{{"code", "x"}}},
		{"/%63allback?code=x", local, "http://localhost", "/%63allback", [][2]string{{"code", "x"}}},
		{"/other/../callback?code=x", local, "http://localhost", "/callback", [][2]string{{"code", "x"}}},
		{"/x/%2e%2e/callback?code=x", local, "http://localhost", "/callback", [][2]string{{"code", "x"}}},
		{"/x/%2E./callback", local, "http://localhost", "/callback", nil},
		{`/a\..\callback`, local, "http://localhost", "/callback", nil},
		{`\callback`, local, "http://localhost", "/callback", nil},
		{"//evil/callback?code=x", local, "http://evil", "/callback", [][2]string{{"code", "x"}}},
		{`/\evil/callback`, local, "http://evil", "/callback", nil},
		{"http://evil/callback?code=x", local, "http://evil", "/callback", [][2]string{{"code", "x"}}},
		{"HTTP://evil:80/x/../callback", local, "http://evil", "/callback", nil},
		{"http:/callback", local, "http://localhost", "/callback", nil},
		{"http:callback", local, "http://localhost", "/callback", nil},
		{"*", local, "http://localhost", "/*", nil},
		{"/callback?code=a;b", local, "http://localhost", "/callback", [][2]string{{"code", "a;b"}}},
		{"/callback?code=%zz&code=2", local, "http://localhost", "/callback", [][2]string{{"code", "%zz"}, {"code", "2"}}},
		{"/callback?code=a+b%20c", local, "http://localhost", "/callback", [][2]string{{"code", "a b c"}}},
		{"/callback?=x&code&code=y", local, "http://localhost", "/callback", [][2]string{{"", "x"}, {"code", ""}, {"code", "y"}}},
		{"/callback?code=%E2%82", local, "http://localhost", "/callback", [][2]string{{"code", "\ufffd"}}},
		{"/callback?code=%EF%BB%BFx", local, "http://localhost", "/callback", [][2]string{{"code", "\ufeffx"}}},
		{"/call back", local, "http://localhost", "/call%20back", nil},
		{"/callback#frag?code=x", local, "http://localhost", "/callback", nil},
		{"/callback?code=x#y", local, "http://localhost", "/callback", [][2]string{{"code", "x"}}},
		{"/callback/", local, "http://localhost", "/callback/", nil},
		{"/./callback", local, "http://localhost", "/callback", nil},
		{"/callback/%2e", local, "http://localhost", "/callback/", nil},
		{"/callback?", local, "http://localhost", "/callback", nil},
		{"*", redirect, "http://localhost:1455", "/auth/*", nil},
		{"x/../callback", redirect, "http://localhost:1455", "/auth/callback", nil},
		{"/auth/./callback", redirect, "http://localhost:1455", "/auth/callback", nil},
		{"/auth/%63allback", redirect, "http://localhost:1455", "/auth/%63allback", nil},
		{"HTTP://LOCALHOST:1455/auth/callback?code=c", "http://x", "http://localhost:1455", "/auth/callback", [][2]string{{"code", "c"}}},
		{`http://localhost:1455\auth\callback?code=c`, "http://x", "http://localhost:1455", "/auth/callback", [][2]string{{"code", "c"}}},
		{"http://localhost:01455/auth/callback?code=c", "http://x", "http://localhost:1455", "/auth/callback", [][2]string{{"code", "c"}}},
		{"http://user@localhost:1455/auth/callback?code=c;d", "http://x", "http://localhost:1455", "/auth/callback", [][2]string{{"code", "c;d"}}},
		{"http://localhost:99999/auth/callback", "http://x", "", "", nil},
	} {
		got, err := ResolveHTTPURL(tc.input, tc.base)
		if tc.origin == "" {
			if err == nil {
				t.Errorf("ResolveHTTPURL(%q, %q) = %+v, want an error", tc.input, tc.base, got)
			}
			continue
		}
		if err != nil || got.Origin() != tc.origin || got.Pathname != tc.pathname {
			t.Errorf("ResolveHTTPURL(%q, %q) = %q %q (%v), want %q %q", tc.input, tc.base, got.Origin(), got.Pathname, err, tc.origin, tc.pathname)
			continue
		}
		want := url.Values{}
		for _, pair := range tc.params {
			want[pair[0]] = append(want[pair[0]], pair[1])
		}
		search := got.Search
		if search != "" {
			search = search[1:]
		}
		if params := SearchParams(search); !reflect.DeepEqual(params, want) {
			t.Errorf("SearchParams of %q = %v, want %v", tc.input, params, want)
		}
	}
}
