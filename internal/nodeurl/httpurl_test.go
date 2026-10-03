package nodeurl

import "testing"

// Expected values are Node 24 `URL.canParse(input)` and `new URL(input)` protocol, hostname, port, search and hash.
func TestParseHTTPURLMatchesNodeURL(t *testing.T) {
	ok := func(protocol, hostname, port, search, hash string) *HTTPURL {
		return &HTTPURL{Protocol: protocol, Hostname: hostname, Port: port, Search: search, Hash: hash}
	}
	for _, tc := range []struct {
		input string
		want  *HTTPURL // nil: URL.canParse is false or the protocol is not http: or https:
	}{
		{"http://%6cocalhost/", ok("http:", "localhost", "", "", "")},
		{"http://%6cocalhost:8080/cb", ok("http:", "localhost", "8080", "", "")},
		{"HTTP://LocalHost/", ok("http:", "localhost", "", "", "")},
		{"http:example.com", ok("http:", "example.com", "", "", "")},
		{"http:/example.com", ok("http:", "example.com", "", "", "")},
		{`http:\\example.com\x`, ok("http:", "example.com", "", "", "")},
		{"http:///example.com", ok("http:", "example.com", "", "", "")},
		{"https://", nil},
		{"http://", nil},
		{"http://:80", nil},
		{"http://host:99999", nil},
		{"http://host:80", ok("http:", "host", "", "", "")},
		{"https://host:443", ok("https:", "host", "", "", "")},
		{"https://host:80", ok("https:", "host", "80", "", "")},
		{"http://host:8080", ok("http:", "host", "8080", "", "")},
		{"http://host:", ok("http:", "host", "", "", "")},
		{"http://host:abc", nil},
		{"http://user:pw@127.1/", ok("http:", "127.0.0.1", "", "", "")},
		{"http://@host/", ok("http:", "host", "", "", "")},
		{"http://u@/", nil},
		{"http://[::1]:1/", ok("http:", "[::1]", "1", "", "")},
		{"http://[0:0::1]/", ok("http:", "[::1]", "", "", "")},
		{"http://[::1/", nil},
		{"http://[::1]x/", nil},
		{"http://exa mple.com", nil},
		{"http://exa%20mple.com", nil},
		{"http://a%2fb/", nil},
		{"http://%7f/", nil},
		{"http://0x7f.1/", ok("http:", "127.0.0.1", "", "", "")},
		{"http://127.0.0.1./", ok("http:", "127.0.0.1", "", "", "")},
		{"http://256.1.1.1/", nil},
		{"http://1.2.3/", ok("http:", "1.2.0.3", "", "", "")},
		{"http://１２７.0.0.1/", ok("http:", "127.0.0.1", "", "", "")},
		{"  http://host/\t\n", ok("http:", "host", "", "", "")},
		{"ftp://host", nil},
		{"mailto:x@y", nil},
		{"foo", nil},
		{"http://host/?", ok("http:", "host", "", "", "")},
		{"http://host/#", ok("http:", "host", "", "", "")},
		{"http://host/?a", ok("http:", "host", "", "?a", "")},
		{"http://host/#b", ok("http:", "host", "", "", "#b")},
		{"http://host?a", ok("http:", "host", "", "?a", "")},
		{"http://host#b", ok("http:", "host", "", "", "#b")},
		{"http://host/p?q#f", ok("http:", "host", "", "?q", "#f")},
		{"http://xn--/", nil},
		{"http://ex%41mple.com/", ok("http:", "example.com", "", "", "")},
		{"http://%/", nil},
		{"http://a..b/", ok("http:", "a..b", "", "", "")},
		{"http://host:0/", ok("http:", "host", "0", "", "")},
		{"http://host:00080/", ok("http:", "host", "", "", "")},
		{"http://host:8080:9/", nil},
		{"http://a@b@c/", ok("http:", "c", "", "", "")},
		{"http://%40/", nil},
		// A hex or octal last part past 64 bits still ends the host in a number, so the IPv4 parser rejects it.
		{"http://0x7f1e142949672961e1/", nil},
		{"http://a.0x10000000000000000/", nil},
		{"http://a.0777777777777777777777777/", nil},
		{"http://0x100000000/", nil},
		{"http://0xffffffff/", ok("http:", "255.255.255.255", "", "", "")},
		// A part with a non-digit is no number however long its digit run, so the host is a domain.
		{"http://42949672964294967296b/", ok("http:", "42949672964294967296b", "", "", "")},
		{"http://0x7f1e142949672961e1g/", ok("http:", "0x7f1e142949672961e1g", "", "", "")},
	} {
		got, err := ParseHTTPURL(tc.input)
		switch {
		case tc.want == nil && err == nil:
			t.Errorf("ParseHTTPURL(%q) = %+v, want an error", tc.input, got)
		case tc.want != nil && err != nil:
			t.Errorf("ParseHTTPURL(%q) error = %v, want %+v", tc.input, err, *tc.want)
		case tc.want != nil && got != *tc.want:
			t.Errorf("ParseHTTPURL(%q) = %+v, want %+v", tc.input, got, *tc.want)
		}
	}
}
